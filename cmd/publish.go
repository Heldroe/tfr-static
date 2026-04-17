package cmd

import (
	"fmt"
	"os"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
	"github.com/Heldroe/tfr-static/internal/git"
	"github.com/Heldroe/tfr-static/internal/module"
	"github.com/Heldroe/tfr-static/internal/registry"
)

var (
	publishTag          string
	publishModule       string
	publishAll          bool
	publishDev          bool
	dryRun              bool
	invalidationFile    string
	invalidationFormatS string
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
  tfr-static publish --all

  # Publish from working tree (dev mode)
  tfr-static publish --dev
  tfr-static publish --dev --module hetzner/server`,
	RunE: runPublish,
}

func init() {
	publishCmd.Flags().StringVar(&publishTag, "tag", os.Getenv("TFR_TAG"), "specific tag to publish (also reads TFR_TAG env var)")
	publishCmd.Flags().StringVar(&publishModule, "module", "", "regenerate all versions of this module")
	publishCmd.Flags().BoolVar(&publishAll, "all", false, "regenerate all versions of all modules")
	publishCmd.Flags().BoolVar(&publishDev, "dev", false, "publish from working tree as 0.0.0-dev")
	publishCmd.Flags().BoolVar(&dryRun, "dry-run", false, "only show what would be done (including invalidations)")
	publishCmd.Flags().StringVar(&invalidationFile, "invalidation-file", "", "write invalidation paths to this file")
	publishCmd.Flags().StringVar(&invalidationFormatS, "invalidation-format", "txt", "format of the invalidation file: txt, json, cloudfront")
	publishCmd.Flags().Bool("html", false, "generate HTML documentation pages")
	publishCmd.Flags().String("html-index", "index.html", "filename for HTML index pages")
	publishCmd.Flags().Bool("gzip", false, "gzip-compress text files in the output directory for pre-compressed S3 upload")
	publishCmd.Flags().Bool("terraform-docs", false, "generate terraform-docs output in HTML pages")
	rootCmd.AddCommand(publishCmd)
}

func runPublish(cmd *cobra.Command, args []string) error {
	if cfg.BaseURL == "" {
		return fmt.Errorf("--base-url is required (or set TFR_BASE_URL)")
	}

	// Resolve invalidation flags with env fallback
	if invalidationFile == "" {
		invalidationFile = os.Getenv("TFR_INVALIDATION_FILE")
	}
	if !cmd.Flags().Lookup("invalidation-format").Changed {
		if env := os.Getenv("TFR_INVALIDATION_FORMAT"); env != "" {
			invalidationFormatS = env
		}
	}

	// --dev is mutually exclusive with --tag and --all, but compatible with --module
	if publishDev {
		if publishTag != "" {
			return fmt.Errorf("--dev and --tag are mutually exclusive")
		}
		if publishAll {
			return fmt.Errorf("--dev and --all are mutually exclusive")
		}
		return runPublishDev(cmd)
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
		return fmt.Errorf("one of --tag, --module, --all, or --dev is required")
	}
	if flagCount > 1 {
		return fmt.Errorf("only one of --tag, --module, or --all may be specified")
	}

	// Validate invalidation format early
	var invalidationFormat registry.InvalidationFormat
	if invalidationFile != "" {
		var err error
		invalidationFormat, err = registry.ParseInvalidationFormat(invalidationFormatS)
		if err != nil {
			return err
		}
	}

	gitRunner := git.NewRunner(cfg.RepoPath)
	publisher := registry.NewPublisher(gitRunner, cfg.OutputDir, cfg.BaseURL, cfg.ModulesPath)

	var invalidationPaths []string
	var err error
	switch {
	case publishTag != "":
		invalidationPaths, err = publishSingleTag(publisher, gitRunner, publishTag)
	case publishModule != "":
		invalidationPaths, err = publishModuleVersions(publisher, gitRunner, publishModule)
	case publishAll:
		invalidationPaths, err = publishAllModules(publisher, gitRunner)
	}
	if err != nil {
		return err
	}

	if !dryRun {
		if err := publisher.GenerateServiceDiscovery(); err != nil {
			return fmt.Errorf("generating service discovery: %w", err)
		}

		if cfg.HTML {
			repoRoot, _ := gitRunner.TopLevel()
			reader := registry.GitReadmeReader(gitRunner)
			if cfg.TerraformDocs && repoRoot != "" {
				reader = registry.EnrichedReadmeReader(reader, repoRoot)
			}
			gen := registry.NewHTMLGenerator(gitRunner, cfg.OutputDir, cfg.HTMLIndex)
			gen.ReadmeReader = reader
			allGrouped := groupedFromGit(gitRunner)

			switch {
			case publishTag != "":
				info, _ := module.ParseTag(publishTag)
				moduleTags := allGrouped[info.ModulePath]
				if err := gen.GenerateForVersion(*info, moduleTags, allGrouped); err != nil {
					return fmt.Errorf("generating HTML documentation: %w", err)
				}
			case publishModule != "":
				moduleTags := allGrouped[publishModule]
				if err := gen.GenerateForModule(publishModule, moduleTags, allGrouped); err != nil {
					return fmt.Errorf("generating HTML documentation: %w", err)
				}
			case publishAll:
				if err := gen.GenerateAll(allGrouped); err != nil {
					return fmt.Errorf("generating HTML documentation: %w", err)
				}
			}
			fmt.Println("HTML documentation generated")
		}

		if cfg.Gzip {
			count, err := registry.GzipTextFiles(cfg.OutputDir)
			if err != nil {
				return fmt.Errorf("gzip compressing files: %w", err)
			}
			fmt.Printf("Gzip-compressed %d text files\n", count)
		}
	}

	if invalidationFile != "" && len(invalidationPaths) > 0 {
		invalidationPaths = dedup(invalidationPaths)
		if err := registry.WriteInvalidationFile(invalidationPaths, invalidationFile, invalidationFormat); err != nil {
			return err
		}
		fmt.Printf("Invalidation file written to %s (%s format, %d paths)\n", invalidationFile, invalidationFormatS, len(invalidationPaths))
	}

	return nil
}

func publishSingleTag(pub *registry.Publisher, gitRunner *git.Runner, tag string) ([]string, error) {
	info, err := module.ParseTag(tag)
	if err != nil {
		return nil, fmt.Errorf("invalid tag %q: %w", tag, err)
	}

	// Verify the module path exists at this tag
	exists, err := gitRunner.PathExistsAtTag(info.Tag, info.ModulePath)
	if err != nil {
		return nil, fmt.Errorf("checking module path: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("module path %q does not exist at tag %q", info.ModulePath, info.Tag)
	}

	paths := registry.InvalidationPathsForNewVersion(info.ModulePath, cfg.HTML, cfg.HTMLIndex)

	if dryRun {
		fmt.Printf("[dry-run] Would publish %s version %s\n", info.ModulePath, info.Version)
		for _, p := range paths {
			fmt.Printf("[dry-run] Invalidation: %s\n", p)
		}
		return paths, nil
	}

	fmt.Printf("Publishing %s version %s...\n", info.ModulePath, info.Version)
	if err := pub.PublishVersion(*info); err != nil {
		return nil, err
	}

	// Generate versions.json with all known versions for this module
	versions, err := collectModuleVersions(gitRunner, info.ModulePath)
	if err != nil {
		return nil, fmt.Errorf("collecting versions: %w", err)
	}
	if err := pub.GenerateVersionsJSON(info.ModulePath, versions); err != nil {
		return nil, err
	}

	fmt.Printf("Published %s version %s\n", info.ModulePath, info.Version)
	fmt.Println("\nInvalidation paths:")
	for _, p := range paths {
		fmt.Printf("  %s\n", p)
	}

	return paths, nil
}

func publishModuleVersions(pub *registry.Publisher, gitRunner *git.Runner, modulePath string) ([]string, error) {
	tags, err := gitRunner.ListTags()
	if err != nil {
		return nil, fmt.Errorf("listing tags: %w", err)
	}

	allParsed := module.ParseAllTags(tags)
	moduleTags := module.FilterTagsForModule(allParsed, modulePath)

	if len(moduleTags) == 0 {
		return nil, fmt.Errorf("no tags found for module %q", modulePath)
	}

	module.SortVersionsAsc(moduleTags)

	var versions []*semver.Version
	for _, t := range moduleTags {
		versions = append(versions, t.Version)
	}

	paths := registry.InvalidationPathsForModuleRebuild(modulePath, versions, cfg.HTML, cfg.HTMLIndex)

	if dryRun {
		fmt.Printf("[dry-run] Would publish %d versions of %s:\n", len(moduleTags), modulePath)
		for _, t := range moduleTags {
			fmt.Printf("[dry-run]   %s\n", t.Version)
		}
		for _, p := range paths {
			fmt.Printf("[dry-run] Invalidation: %s\n", p)
		}
		return paths, nil
	}

	for _, t := range moduleTags {
		fmt.Printf("Publishing %s version %s...\n", t.ModulePath, t.Version)
		if err := pub.PublishVersion(t); err != nil {
			return nil, fmt.Errorf("publishing %s: %w", t.Tag, err)
		}
	}

	if err := pub.GenerateVersionsJSON(modulePath, versions); err != nil {
		return nil, err
	}

	fmt.Printf("\nPublished %d versions of %s\n", len(moduleTags), modulePath)
	return paths, nil
}

func publishAllModules(pub *registry.Publisher, gitRunner *git.Runner) ([]string, error) {
	tags, err := gitRunner.ListTags()
	if err != nil {
		return nil, fmt.Errorf("listing tags: %w", err)
	}

	allParsed := module.ParseAllTags(tags)
	if len(allParsed) == 0 {
		return nil, fmt.Errorf("no module tags found in repository")
	}

	grouped := module.GroupTagsByModule(allParsed)

	var paths []string
	for modPath, modTags := range grouped {
		var versions []*semver.Version
		for _, t := range modTags {
			versions = append(versions, t.Version)
		}
		paths = append(paths, registry.InvalidationPathsForModuleRebuild(modPath, versions, cfg.HTML, cfg.HTMLIndex)...)
	}

	if dryRun {
		fmt.Printf("[dry-run] Would publish %d modules:\n", len(grouped))
		for modPath, modTags := range grouped {
			fmt.Printf("[dry-run]   %s (%d versions)\n", modPath, len(modTags))
		}
		for _, p := range paths {
			fmt.Printf("[dry-run] Invalidation: %s\n", p)
		}
		return paths, nil
	}

	for modPath, modTags := range grouped {
		module.SortVersionsAsc(modTags)
		var versions []*semver.Version
		for _, t := range modTags {
			fmt.Printf("Publishing %s version %s...\n", t.ModulePath, t.Version)
			if err := pub.PublishVersion(t); err != nil {
				return nil, fmt.Errorf("publishing %s: %w", t.Tag, err)
			}
			versions = append(versions, t.Version)
		}
		if err := pub.GenerateVersionsJSON(modPath, versions); err != nil {
			return nil, err
		}
	}

	fmt.Printf("\nPublished %d modules\n", len(grouped))
	return paths, nil
}

func runPublishDev(cmd *cobra.Command) error {
	gitRunner := git.NewRunner(cfg.RepoPath)
	repoRoot, err := gitRunner.TopLevel()
	if err != nil {
		return fmt.Errorf("resolving repository root: %w", err)
	}

	modules, err := module.DiscoverModules(repoRoot)
	if err != nil {
		return fmt.Errorf("discovering modules: %w", err)
	}

	if publishModule != "" {
		var filtered []module.Module
		for _, m := range modules {
			if m.Path == publishModule {
				filtered = append(filtered, m)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("module %q not found in working tree", publishModule)
		}
		modules = filtered
	}

	if len(modules) == 0 {
		return fmt.Errorf("no modules found in repository")
	}

	devVersion := semver.MustParse("0.0.0-dev")
	publisher := registry.NewPublisher(gitRunner, cfg.OutputDir, cfg.BaseURL, cfg.ModulesPath)

	if dryRun {
		for _, m := range modules {
			fmt.Printf("[dry-run] Would publish %s version 0.0.0-dev from working tree\n", m.Path)
		}
		return nil
	}

	for _, m := range modules {
		fmt.Printf("Publishing %s version 0.0.0-dev from working tree...\n", m.Path)
		if err := publisher.PublishVersionFromWorkTree(repoRoot, m.Path, devVersion); err != nil {
			return fmt.Errorf("publishing %s: %w", m.Path, err)
		}

		// Generate versions.json with real tagged versions + dev
		versions, err := collectModuleVersions(gitRunner, m.Path)
		if err != nil {
			versions = nil // no tags yet is fine
		}
		versions = append(versions, devVersion)
		if err := publisher.GenerateVersionsJSON(m.Path, versions); err != nil {
			return fmt.Errorf("generating versions.json for %s: %w", m.Path, err)
		}
	}

	if err := publisher.GenerateServiceDiscovery(); err != nil {
		return fmt.Errorf("generating service discovery: %w", err)
	}

	if cfg.HTML {
		// Build grouped TagInfo from real tags + dev entries
		grouped := groupedFromGit(gitRunner)
		if grouped == nil {
			grouped = make(map[string][]module.TagInfo)
		}

		// Add dev entries for published modules
		for _, m := range modules {
			grouped[m.Path] = append(grouped[m.Path], module.TagInfo{
				Tag:        m.Path + "-0.0.0-dev",
				ModulePath: m.Path,
				Version:    devVersion,
			})
		}

		reader := registry.FilesystemReadmeReader(repoRoot)
		if cfg.TerraformDocs {
			reader = registry.EnrichedReadmeReader(reader, repoRoot)
		}
		if err := generateHTML(gitRunner, cfg.OutputDir, cfg.HTMLIndex, reader, grouped); err != nil {
			return fmt.Errorf("generating HTML documentation: %w", err)
		}
		fmt.Println("HTML documentation generated")
	}

	if cfg.Gzip {
		count, err := registry.GzipTextFiles(cfg.OutputDir)
		if err != nil {
			return fmt.Errorf("gzip compressing files: %w", err)
		}
		fmt.Printf("Gzip-compressed %d text files\n", count)
	}

	fmt.Printf("\nPublished %d modules as 0.0.0-dev\n", len(modules))
	return nil
}

func generateHTML(gitRunner *git.Runner, outputDir, indexFile string, reader registry.ReadmeReader, grouped map[string][]module.TagInfo) error {
	if len(grouped) == 0 {
		return nil
	}
	gen := registry.NewHTMLGenerator(gitRunner, outputDir, indexFile)
	gen.ReadmeReader = reader
	return gen.GenerateAll(grouped)
}

func groupedFromGit(gitRunner *git.Runner) map[string][]module.TagInfo {
	tags, err := gitRunner.ListTags()
	if err != nil {
		return nil
	}
	allParsed := module.ParseAllTags(tags)
	return module.GroupTagsByModule(allParsed)
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

func dedup(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, s := range items {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
