package cmd

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
	"github.com/Heldroe/tfr-static/internal/git"
	"github.com/Heldroe/tfr-static/internal/module"
	"github.com/Heldroe/tfr-static/internal/registry"
)

var (
	publishTag    string
	publishModule string
	publishAll    bool
	publishDev    bool
	dryRun        bool
)

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish module versions to the static registry",
	Long: `Generate static registry files (archive, download HTML, versions)
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
	publishCmd.Flags().String("invalidation-file", "", "write invalidation paths to this file")
	publishCmd.Flags().String("invalidation-format", "txt", "format of the invalidation file: txt, json, cloudfront")
	publishCmd.Flags().Bool("invalidation-full-url", false, "prepend the base URL to invalidation paths")
	publishCmd.Flags().String("invalidation-base-url", "", "override the base URL used for invalidation paths (requires --invalidation-full-url)")
	publishCmd.Flags().Bool("invalidation-url-encode", false, "URL-encode invalidation paths")
	publishCmd.Flags().Bool("invalidation-dirs", false, "include directory paths (trailing /) for index files in invalidation output")
	publishCmd.Flags().Bool("html", false, "generate HTML documentation pages")
	publishCmd.Flags().String("html-index", "index.html", "filename for HTML index pages")
	publishCmd.Flags().String("html-base", "", "path to a custom base HTML template file")
	publishCmd.Flags().Bool("gzip", false, "gzip-compress text files in the output directory for pre-compressed S3 upload")
	publishCmd.Flags().Bool("terraform-docs", false, "generate terraform-docs output in HTML pages")
	rootCmd.AddCommand(publishCmd)
}

func runPublish(cmd *cobra.Command, args []string) error {
	if cfg.BaseURL == "" {
		return fmt.Errorf("--base-url is required (or set TFR_BASE_URL)")
	}

	if cfg.InvalidationBaseURL != "" && !cfg.InvalidationFullURL {
		return fmt.Errorf("invalidation_base_url requires invalidation_full_url to be enabled")
	}
	invalidationBaseURL := ""
	if cfg.InvalidationFullURL {
		invalidationBaseURL = cfg.InvalidationBaseURL
		if invalidationBaseURL == "" {
			invalidationBaseURL = cfg.BaseURL
		}
		invalidationBaseURL = strings.TrimRight(invalidationBaseURL, "/")
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

	var invalidationFormat registry.InvalidationFormat
	if cfg.InvalidationFile != "" {
		var err error
		invalidationFormat, err = registry.ParseInvalidationFormat(cfg.InvalidationFormat)
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
				reader = registry.EnrichedReadmeReader(reader, repoRoot, gitRunner)
			}
			gen, err := newHTMLGenerator(gitRunner, reader)
			if err != nil {
				return err
			}
			allGrouped := groupedFromGit(gitRunner)

			switch {
			case publishTag != "":
				info, _ := module.ParseTag(publishTag)
				regPath, err := resolveRegistryPath(info.ModulePath)
				if err != nil {
					return fmt.Errorf("resolving registry path for HTML: %w", err)
				}
				info.RegistryPath = regPath
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

	if cfg.InvalidationFile != "" && len(invalidationPaths) > 0 {
		invalidationPaths = dedup(invalidationPaths)
		for i, p := range invalidationPaths {
			if invalidationBaseURL != "" {
				p = invalidationBaseURL + p
			}
			if cfg.InvalidationURLEncode {
				p = url.QueryEscape(p)
			}
			invalidationPaths[i] = p
		}
		if err := registry.WriteInvalidationFile(invalidationPaths, cfg.InvalidationFile, invalidationFormat); err != nil {
			return err
		}
		fmt.Printf("Invalidation file written to %s (%s format, %d paths)\n", cfg.InvalidationFile, cfg.InvalidationFormat, len(invalidationPaths))
	}

	return nil
}

func resolveRegistryPath(dirPath string) (string, error) {
	regPath, autoMapped, err := module.RegistryPath(dirPath, cfg.Namespace, cfg.ModuleMappings)
	if err != nil {
		return "", err
	}
	if autoMapped {
		log.Printf("module %q -> registry path %q", dirPath, regPath)
	}
	return regPath, nil
}

func publishSingleTag(pub *registry.Publisher, gitRunner *git.Runner, tag string) ([]string, error) {
	info, err := module.ParseTag(tag)
	if err != nil {
		return nil, fmt.Errorf("invalid tag %q: %w", tag, err)
	}

	regPath, err := resolveRegistryPath(info.ModulePath)
	if err != nil {
		return nil, err
	}
	info.RegistryPath = regPath

	exists, err := gitRunner.PathExistsAtTag(info.Tag, info.ModulePath)
	if err != nil {
		return nil, fmt.Errorf("checking module path: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("module path %q does not exist at tag %q", info.ModulePath, info.Tag)
	}

	paths := registry.InvalidationPathsForNewVersion(info.RegistryPath, cfg.HTML, cfg.HTMLIndex, cfg.InvalidationDirs)

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

	versions, err := collectModuleVersions(gitRunner, info.ModulePath)
	if err != nil {
		return nil, fmt.Errorf("collecting versions: %w", err)
	}
	if err := pub.GenerateVersionsJSON(info.RegistryPath, versions); err != nil {
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
	regPath, err := resolveRegistryPath(modulePath)
	if err != nil {
		return nil, err
	}

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

	for i := range moduleTags {
		moduleTags[i].RegistryPath = regPath
	}

	var versions []*semver.Version
	for _, t := range moduleTags {
		versions = append(versions, t.Version)
	}

	paths := registry.InvalidationPathsForModuleRebuild(regPath, versions, cfg.HTML, cfg.HTMLIndex, cfg.InvalidationDirs)

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

	if err := pub.GenerateVersionsJSON(regPath, versions); err != nil {
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

	allDirPaths := make([]string, 0, len(grouped))
	for modPath := range grouped {
		allDirPaths = append(allDirPaths, modPath)
	}
	if err := module.DetectAmbiguities(allDirPaths, cfg.Namespace, cfg.ModuleMappings); err != nil {
		return nil, err
	}

	regPaths := make(map[string]string, len(grouped))
	for modPath, modTags := range grouped {
		regPath, err := resolveRegistryPath(modPath)
		if err != nil {
			return nil, err
		}
		regPaths[modPath] = regPath
		for i := range modTags {
			modTags[i].RegistryPath = regPath
		}
		grouped[modPath] = modTags
	}

	moduleVersions := make(map[string][]*semver.Version, len(grouped))
	for modPath, modTags := range grouped {
		module.SortVersionsAsc(modTags)
		versions := make([]*semver.Version, len(modTags))
		for i, t := range modTags {
			versions[i] = t.Version
		}
		moduleVersions[modPath] = versions
	}

	var paths []string
	for modPath, versions := range moduleVersions {
		paths = append(paths, registry.InvalidationPathsForModuleRebuild(regPaths[modPath], versions, cfg.HTML, cfg.HTMLIndex, cfg.InvalidationDirs)...)
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
		for _, t := range modTags {
			fmt.Printf("Publishing %s version %s...\n", t.ModulePath, t.Version)
			if err := pub.PublishVersion(t); err != nil {
				return nil, fmt.Errorf("publishing %s: %w", t.Tag, err)
			}
		}
		if err := pub.GenerateVersionsJSON(regPaths[modPath], moduleVersions[modPath]); err != nil {
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

	dirPaths := make([]string, len(modules))
	for i, m := range modules {
		dirPaths[i] = m.Path
	}
	if err := module.DetectAmbiguities(dirPaths, cfg.Namespace, cfg.ModuleMappings); err != nil {
		return err
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
		regPath, err := resolveRegistryPath(m.Path)
		if err != nil {
			return err
		}

		fmt.Printf("Publishing %s version 0.0.0-dev from working tree...\n", m.Path)
		if err := publisher.PublishVersionFromWorkTree(repoRoot, m.Path, regPath, devVersion); err != nil {
			return fmt.Errorf("publishing %s: %w", m.Path, err)
		}

		// Generate versions with real tagged versions + dev
		versions, err := collectModuleVersions(gitRunner, m.Path)
		if err != nil {
			versions = nil // no tags yet is fine
		}
		versions = append(versions, devVersion)
		if err := publisher.GenerateVersionsJSON(regPath, versions); err != nil {
			return fmt.Errorf("generating versions for %s: %w", m.Path, err)
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
			regPath, err := resolveRegistryPath(m.Path)
			if err != nil {
				return fmt.Errorf("resolving registry path for %s: %w", m.Path, err)
			}
			grouped[m.Path] = append(grouped[m.Path], module.TagInfo{
				Tag:          m.Path + "-0.0.0-dev",
				ModulePath:   m.Path,
				RegistryPath: regPath,
				Version:      devVersion,
			})
		}

		reader := registry.FilesystemReadmeReader(repoRoot)
		if cfg.TerraformDocs {
			reader = registry.EnrichedReadmeReader(reader, repoRoot, gitRunner)
		}
		if err := generateHTML(gitRunner, reader, grouped); err != nil {
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

func newHTMLGenerator(gitRunner *git.Runner, reader registry.ReadmeReader) (*registry.HTMLGenerator, error) {
	gen := registry.NewHTMLGenerator(gitRunner, cfg.OutputDir, cfg.HTMLIndex)
	gen.ReadmeReader = reader
	gen.BaseURL = cfg.BaseURL
	if cfg.HTMLBase != "" {
		if err := gen.LoadBaseTemplate(cfg.HTMLBase); err != nil {
			return nil, err
		}
	}
	return gen, nil
}

func generateHTML(gitRunner *git.Runner, reader registry.ReadmeReader, grouped map[string][]module.TagInfo) error {
	if len(grouped) == 0 {
		return nil
	}
	gen, err := newHTMLGenerator(gitRunner, reader)
	if err != nil {
		return err
	}
	return gen.GenerateAll(grouped)
}

func groupedFromGit(gitRunner *git.Runner) map[string][]module.TagInfo {
	tags, err := gitRunner.ListTags()
	if err != nil {
		return nil
	}
	allParsed := module.ParseAllTags(tags)
	grouped := module.GroupTagsByModule(allParsed)
	for modPath, modTags := range grouped {
		regPath, err := resolveRegistryPath(modPath)
		if err != nil {
			log.Printf("skipping module %q in HTML: %v", modPath, err)
			delete(grouped, modPath)
			continue
		}
		for i := range modTags {
			modTags[i].RegistryPath = regPath
		}
		grouped[modPath] = modTags
	}
	return grouped
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

