package cmd

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/typeform/tfr-static/internal/git"
	"github.com/typeform/tfr-static/internal/module"
)

var tagCmd = &cobra.Command{
	Use:   "tag [module-path]",
	Short: "Create a version tag for a module",
	Long: `Interactively create a semver git tag for a module. If no module path
is provided, an interactive selector is shown.

Ensures the repository is on the main branch and up to date before tagging.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTag,
}

func init() {
	rootCmd.AddCommand(tagCmd)
}

func runTag(cmd *cobra.Command, args []string) error {
	gitRunner := git.NewRunner(cfg.RepoPath)

	// Resolve the actual repository root for module discovery.
	// Git finds .git by walking up, but filepath.Walk needs the real root.
	repoRoot, err := gitRunner.TopLevel()
	if err != nil {
		return fmt.Errorf("resolving repository root: %w", err)
	}

	// Verify we're on the expected branch
	branch, err := gitRunner.CurrentBranch()
	if err != nil {
		return fmt.Errorf("getting current branch: %w", err)
	}
	if branch != cfg.MainBranch {
		return fmt.Errorf("must be on %q branch (currently on %q)", cfg.MainBranch, branch)
	}

	// Fetch and check if up to date
	fmt.Println("Fetching from remote...")
	if err := gitRunner.Fetch(); err != nil {
		return fmt.Errorf("fetching: %w", err)
	}

	upToDate, err := gitRunner.IsUpToDate(cfg.MainBranch)
	if err != nil {
		return fmt.Errorf("checking if up to date: %w", err)
	}
	if !upToDate {
		return fmt.Errorf("local branch %q is not up to date with remote; please pull first", cfg.MainBranch)
	}

	// Determine module path
	var modulePath string
	if len(args) > 0 {
		modulePath = args[0]
	} else {
		modulePath, err = selectModule(repoRoot)
		if err != nil {
			return err
		}
	}

	// Validate the module path exists in the current tree
	modules, err := module.DiscoverModules(repoRoot)
	if err != nil {
		return fmt.Errorf("discovering modules: %w", err)
	}
	found := false
	for _, m := range modules {
		if m.Path == modulePath {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("module %q not found in repository (no .tf files in that directory)", modulePath)
	}

	// Find the latest version for this module
	tags, err := gitRunner.ListTags()
	if err != nil {
		return fmt.Errorf("listing tags: %w", err)
	}
	allParsed := module.ParseAllTags(tags)
	moduleTags := module.FilterTagsForModule(allParsed, modulePath)

	var currentVersion *semver.Version
	if latest := module.LatestVersion(moduleTags); latest != nil {
		currentVersion = latest.Version
		fmt.Printf("Module: %s (current version: %s)\n", modulePath, currentVersion)
	} else {
		currentVersion = semver.MustParse("0.0.0")
		fmt.Printf("Module: %s (no existing versions)\n", modulePath)
	}

	// Compute bump options
	patchVersion, _ := module.NextVersion(currentVersion, "patch")
	minorVersion, _ := module.NextVersion(currentVersion, "minor")
	majorVersion, _ := module.NextVersion(currentVersion, "major")

	// Ask the user to pick a bump type
	var bumpChoice string
	form := huh.NewSelect[string]().
		Title("Select release type").
		Options(
			huh.NewOption(
				fmt.Sprintf("Patch release => %s  (small fixes, no resource or variables changes)", patchVersion),
				"patch",
			),
			huh.NewOption(
				fmt.Sprintf("Minor release => %s  (variables changes, added or removed resources)", minorVersion),
				"minor",
			),
			huh.NewOption(
				fmt.Sprintf("Major release => %s  (breaking changes, state modification required)", majorVersion),
				"major",
			),
		).
		Value(&bumpChoice)

	if err := form.Run(); err != nil {
		return fmt.Errorf("selecting release type: %w", err)
	}

	var newVersion *semver.Version
	switch bumpChoice {
	case "patch":
		newVersion = patchVersion
	case "minor":
		newVersion = minorVersion
	case "major":
		newVersion = majorVersion
	}

	newTag := module.FormatTag(modulePath, newVersion)

	// Check tag doesn't already exist
	exists, err := gitRunner.TagExists(newTag)
	if err != nil {
		return fmt.Errorf("checking tag existence: %w", err)
	}
	if exists {
		return fmt.Errorf("tag %q already exists", newTag)
	}

	// Create the tag
	message := fmt.Sprintf("Release %s version %s", modulePath, newVersion)
	if err := gitRunner.CreateTag(newTag, message); err != nil {
		return fmt.Errorf("creating tag: %w", err)
	}
	fmt.Printf("\nCreated tag: %s\n", newTag)

	// Ask to push
	var shouldPush bool
	pushForm := huh.NewConfirm().
		Title(fmt.Sprintf("Push tag %s to origin?", newTag)).
		Value(&shouldPush)

	if err := pushForm.Run(); err != nil {
		return fmt.Errorf("confirming push: %w", err)
	}

	if shouldPush {
		fmt.Printf("Pushing tag %s...\n", newTag)
		if err := gitRunner.PushTag(newTag); err != nil {
			return fmt.Errorf("pushing tag: %w", err)
		}
		fmt.Println("Tag pushed successfully.")
	} else {
		fmt.Printf("Tag created locally. Push it later with: git push origin %s\n", newTag)
	}

	return nil
}

func selectModule(repoPath string) (string, error) {
	modules, err := module.DiscoverModules(repoPath)
	if err != nil {
		return "", fmt.Errorf("discovering modules: %w", err)
	}
	if len(modules) == 0 {
		return "", fmt.Errorf("no modules found in repository")
	}

	options := make([]huh.Option[string], len(modules))
	for i, m := range modules {
		options[i] = huh.NewOption(m.Path, m.Path)
	}

	var selected string
	form := huh.NewSelect[string]().
		Title("Select module to tag").
		Options(options...).
		Filtering(true).
		Value(&selected)

	if err := form.Run(); err != nil {
		return "", fmt.Errorf("selecting module: %w", err)
	}

	return selected, nil
}
