package cmd

import (
	"fmt"
	"os"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
	"github.com/typeform/tfr-static/internal/git"
	"github.com/typeform/tfr-static/internal/module"
	"github.com/typeform/tfr-static/internal/registry"
)

var (
	publishTag    string
	publishModule string
	publishAll    bool
	dryRun        bool
)

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish module versions to the static registry",
	Long: `Generate static registry files (archive, download HTML, versions.json)
for one or more module versions. By default publishes a single tag.

Examples:
  # Publish a specific tag
  tfr-static publish --tag hetzner/server-1.0.0

  # Regenerate all versions of a module
  tfr-static publish --module hetzner/server

  # Regenerate everything
  tfr-static publish --all`,
	RunE: runPublish,
}

func init() {
	publishCmd.Flags().StringVar(&publishTag, "tag", os.Getenv("TFR_TAG"), "specific tag to publish (also reads TFR_TAG env var)")
	publishCmd.Flags().StringVar(&publishModule, "module", "", "regenerate all versions of this module")
	publishCmd.Flags().BoolVar(&publishAll, "all", false, "regenerate all versions of all modules")
	publishCmd.Flags().BoolVar(&dryRun, "dry-run", false, "only show what would be done (including invalidations)")
	rootCmd.AddCommand(publishCmd)
}

func runPublish(cmd *cobra.Command, args []string) error {
	if cfg.BaseURL == "" {
		return fmt.Errorf("--base-url is required (or set TFR_BASE_URL)")
	}

	flagCount := 0
	if publishTag != "" {
		flagCount++
	}
	if publishModule != "" {
		flagCount++
	}
	if publishAll {
		flagCount++
	}
	if flagCount == 0 {
		return fmt.Errorf("one of --tag, --module, or --all is required")
	}
	if flagCount > 1 {
		return fmt.Errorf("only one of --tag, --module, or --all may be specified")
	}

	gitRunner := git.NewRunner(cfg.RepoPath)
	publisher := registry.NewPublisher(gitRunner, cfg.OutputDir, cfg.BaseURL, cfg.ModulesPath)

	var err error
	switch {
	case publishTag != "":
		err = publishSingleTag(publisher, gitRunner, publishTag)
	case publishModule != "":
		err = publishModuleVersions(publisher, gitRunner, publishModule)
	case publishAll:
		err = publishAllModules(publisher, gitRunner)
	}
	if err != nil {
		return err
	}

	if !dryRun {
		if err := publisher.GenerateServiceDiscovery(); err != nil {
			return fmt.Errorf("generating service discovery: %w", err)
		}
	}

	return nil
}

func publishSingleTag(pub *registry.Publisher, gitRunner *git.Runner, tag string) error {
	info, err := module.ParseTag(tag)
	if err != nil {
		return fmt.Errorf("invalid tag %q: %w", tag, err)
	}

	// Verify the module path exists at this tag
	exists, err := gitRunner.PathExistsAtTag(info.Tag, info.ModulePath)
	if err != nil {
		return fmt.Errorf("checking module path: %w", err)
	}
	if !exists {
		return fmt.Errorf("module path %q does not exist at tag %q", info.ModulePath, info.Tag)
	}

	if dryRun {
		fmt.Printf("[dry-run] Would publish %s version %s\n", info.ModulePath, info.Version)
		for _, p := range registry.InvalidationPaths(info.ModulePath, info.Version) {
			fmt.Printf("[dry-run] Invalidation: %s\n", p)
		}
		return nil
	}

	fmt.Printf("Publishing %s version %s...\n", info.ModulePath, info.Version)
	if err := pub.PublishVersion(*info); err != nil {
		return err
	}

	// Generate versions.json with all known versions for this module
	versions, err := collectModuleVersions(gitRunner, info.ModulePath)
	if err != nil {
		return fmt.Errorf("collecting versions: %w", err)
	}
	if err := pub.GenerateVersionsJSON(info.ModulePath, versions); err != nil {
		return err
	}

	fmt.Printf("Published %s version %s\n", info.ModulePath, info.Version)
	fmt.Println("\nInvalidation paths:")
	for _, p := range registry.InvalidationPaths(info.ModulePath, info.Version) {
		fmt.Printf("  %s\n", p)
	}

	return nil
}

func publishModuleVersions(pub *registry.Publisher, gitRunner *git.Runner, modulePath string) error {
	tags, err := gitRunner.ListTags()
	if err != nil {
		return fmt.Errorf("listing tags: %w", err)
	}

	allParsed := module.ParseAllTags(tags)
	moduleTags := module.FilterTagsForModule(allParsed, modulePath)

	if len(moduleTags) == 0 {
		return fmt.Errorf("no tags found for module %q", modulePath)
	}

	module.SortVersionsAsc(moduleTags)

	if dryRun {
		fmt.Printf("[dry-run] Would publish %d versions of %s:\n", len(moduleTags), modulePath)
		for _, t := range moduleTags {
			fmt.Printf("[dry-run]   %s\n", t.Version)
		}
		fmt.Printf("[dry-run] Invalidation: /%s/versions.json\n", modulePath)
		return nil
	}

	var versions []*semver.Version
	for _, t := range moduleTags {
		fmt.Printf("Publishing %s version %s...\n", t.ModulePath, t.Version)
		if err := pub.PublishVersion(t); err != nil {
			return fmt.Errorf("publishing %s: %w", t.Tag, err)
		}
		versions = append(versions, t.Version)
	}

	if err := pub.GenerateVersionsJSON(modulePath, versions); err != nil {
		return err
	}

	fmt.Printf("\nPublished %d versions of %s\n", len(moduleTags), modulePath)
	return nil
}

func publishAllModules(pub *registry.Publisher, gitRunner *git.Runner) error {
	tags, err := gitRunner.ListTags()
	if err != nil {
		return fmt.Errorf("listing tags: %w", err)
	}

	allParsed := module.ParseAllTags(tags)
	if len(allParsed) == 0 {
		return fmt.Errorf("no module tags found in repository")
	}

	grouped := module.GroupTagsByModule(allParsed)

	if dryRun {
		fmt.Printf("[dry-run] Would publish %d modules:\n", len(grouped))
		for modPath, modTags := range grouped {
			fmt.Printf("[dry-run]   %s (%d versions)\n", modPath, len(modTags))
		}
		return nil
	}

	for modPath, modTags := range grouped {
		module.SortVersionsAsc(modTags)
		var versions []*semver.Version
		for _, t := range modTags {
			fmt.Printf("Publishing %s version %s...\n", t.ModulePath, t.Version)
			if err := pub.PublishVersion(t); err != nil {
				return fmt.Errorf("publishing %s: %w", t.Tag, err)
			}
			versions = append(versions, t.Version)
		}
		if err := pub.GenerateVersionsJSON(modPath, versions); err != nil {
			return err
		}
	}

	fmt.Printf("\nPublished %d modules\n", len(grouped))
	return nil
}

func collectModuleVersions(gitRunner *git.Runner, modulePath string) ([]*semver.Version, error) {
	tags, err := gitRunner.ListTags()
	if err != nil {
		return nil, err
	}
	allParsed := module.ParseAllTags(tags)
	moduleTags := module.FilterTagsForModule(allParsed, modulePath)

	var versions []*semver.Version
	for _, t := range moduleTags {
		versions = append(versions, t.Version)
	}
	return versions, nil
}
