package cmd

import (
	"context"
	"fmt"
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

	// --dev and --module coexist (dev-publish a single module); all other pairs are mutually exclusive.
	publishCmd.MarkFlagsOneRequired("tag", "module", "all", "dev")
	publishCmd.MarkFlagsMutuallyExclusive("tag", "module", "all")
	publishCmd.MarkFlagsMutuallyExclusive("tag", "dev")
	publishCmd.MarkFlagsMutuallyExclusive("all", "dev")

	rootCmd.AddCommand(publishCmd)
}

func runPublish(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if cfg.BaseURL == "" {
		return fmt.Errorf("--base-url is required (or set TFR_BASE_URL)")
	}
	if cfg.InvalidationBaseURL != "" && !cfg.InvalidationFullURL {
		return fmt.Errorf("invalidation_base_url requires invalidation_full_url to be enabled")
	}

	if publishDev {
		return runPublishDev(cmd)
	}

	invalidationFormat, err := resolveInvalidationFormat()
	if err != nil {
		return err
	}

	gitRunner := git.NewRunner(cfg.RepoPath)
	publisher := registry.NewPublisher(gitRunner, cfg.OutputDir, cfg.BaseURL, cfg.ModulesPath)

	grouped, err := module.LoadAll(ctx, gitRunner)
	if err != nil {
		return fmt.Errorf("listing tags: %w", err)
	}

	var invalidationPaths []string
	switch {
	case publishTag != "":
		invalidationPaths, err = publishSingleTag(ctx, publisher, grouped, publishTag)
	case publishModule != "":
		invalidationPaths, err = publishModuleVersions(ctx, publisher, grouped, publishModule)
	case publishAll:
		invalidationPaths, err = publishAllModules(ctx, publisher, grouped)
	}
	if err != nil {
		return err
	}

	if !dryRun {
		if err := publisher.GenerateServiceDiscovery(); err != nil {
			return fmt.Errorf("generating service discovery: %w", err)
		}
		if err := generateTaggedDocs(ctx, gitRunner, grouped); err != nil {
			return err
		}
		if err := runGzip(); err != nil {
			return err
		}
	}

	return writeInvalidationFile(invalidationPaths, invalidationFormat)
}

func resolveInvalidationFormat() (registry.InvalidationFormat, error) {
	if cfg.InvalidationFile == "" {
		return registry.InvalidationFormat(""), nil
	}
	return registry.ParseInvalidationFormat(cfg.InvalidationFormat)
}

func generateTaggedDocs(ctx context.Context, gitRunner *git.Runner, grouped map[string][]module.TagInfo) error {
	if !cfg.HTML {
		return nil
	}

	repoRoot, _ := gitRunner.TopLevel(ctx)
	reader := registry.GitReadmeReader(gitRunner)
	if cfg.TerraformDocs && repoRoot != "" {
		reader = registry.EnrichedReadmeReader(reader, repoRoot, gitRunner)
	}
	gen, err := newHTMLGenerator(gitRunner, reader)
	if err != nil {
		return err
	}

	switch {
	case publishTag != "":
		info, _ := module.ParseTag(publishTag)
		if err := gen.GenerateForVersion(ctx, *info, grouped[info.ModulePath], grouped); err != nil {
			return fmt.Errorf("generating HTML documentation: %w", err)
		}
	case publishModule != "":
		if err := gen.GenerateForModule(ctx, publishModule, grouped[publishModule], grouped); err != nil {
			return fmt.Errorf("generating HTML documentation: %w", err)
		}
	case publishAll:
		if err := gen.GenerateAll(ctx, grouped); err != nil {
			return fmt.Errorf("generating HTML documentation: %w", err)
		}
	}
	fmt.Println("HTML documentation generated")
	return nil
}

func runGzip() error {
	if !cfg.Gzip {
		return nil
	}
	count, err := registry.GzipTextFiles(cfg.OutputDir)
	if err != nil {
		return fmt.Errorf("gzip compressing files: %w", err)
	}
	fmt.Printf("Gzip-compressed %d text files\n", count)
	return nil
}

func writeInvalidationFile(paths []string, format registry.InvalidationFormat) error {
	if cfg.InvalidationFile == "" || len(paths) == 0 {
		return nil
	}

	invalidationBaseURL := ""
	if cfg.InvalidationFullURL {
		invalidationBaseURL = cfg.InvalidationBaseURL
		if invalidationBaseURL == "" {
			invalidationBaseURL = cfg.BaseURL
		}
		invalidationBaseURL = strings.TrimRight(invalidationBaseURL, "/")
	}

	paths = dedup(paths)
	for i, p := range paths {
		if invalidationBaseURL != "" {
			p = invalidationBaseURL + p
		}
		if cfg.InvalidationURLEncode {
			p = url.QueryEscape(p)
		}
		paths[i] = p
	}
	if err := registry.WriteInvalidationFile(paths, cfg.InvalidationFile, format); err != nil {
		return err
	}
	fmt.Printf("Invalidation file written to %s (%s format, %d paths)\n", cfg.InvalidationFile, cfg.InvalidationFormat, len(paths))
	return nil
}

func publishSingleTag(ctx context.Context, pub *registry.Publisher, grouped map[string][]module.TagInfo, tag string) ([]string, error) {
	info, err := module.ParseTag(tag)
	if err != nil {
		return nil, fmt.Errorf("invalid tag %q: %w", tag, err)
	}

	exists, err := pub.Git.PathExistsAtTag(ctx, info.Tag, info.ModulePath)
	if err != nil {
		return nil, fmt.Errorf("checking module path: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("module path %q does not exist at tag %q", info.ModulePath, info.Tag)
	}

	paths := registry.InvalidationPathsForNewVersion(info.ModulePath, cfg.HTML, cfg.HTMLIndex, cfg.InvalidationDirs)

	if dryRun {
		fmt.Printf("[dry-run] Would publish %s version %s\n", info.ModulePath, info.Version)
		for _, p := range paths {
			fmt.Printf("[dry-run] Invalidation: %s\n", p)
		}
		return paths, nil
	}

	fmt.Printf("Publishing %s version %s...\n", info.ModulePath, info.Version)
	if err := pub.PublishVersion(ctx, *info); err != nil {
		return nil, err
	}

	if err := pub.GenerateVersionsJSON(info.ModulePath, tagVersions(grouped[info.ModulePath])); err != nil {
		return nil, err
	}

	fmt.Printf("Published %s version %s\n", info.ModulePath, info.Version)
	fmt.Println("\nInvalidation paths:")
	for _, p := range paths {
		fmt.Printf("  %s\n", p)
	}

	return paths, nil
}

func publishModuleVersions(ctx context.Context, pub *registry.Publisher, grouped map[string][]module.TagInfo, modulePath string) ([]string, error) {
	moduleTags := grouped[modulePath]
	if len(moduleTags) == 0 {
		return nil, fmt.Errorf("no tags found for module %q", modulePath)
	}

	module.SortVersionsAsc(moduleTags)
	versions := tagVersions(moduleTags)

	paths := registry.InvalidationPathsForModuleRebuild(modulePath, versions, cfg.HTML, cfg.HTMLIndex, cfg.InvalidationDirs)

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
		if err := pub.PublishVersion(ctx, t); err != nil {
			return nil, fmt.Errorf("publishing %s: %w", t.Tag, err)
		}
	}

	if err := pub.GenerateVersionsJSON(modulePath, versions); err != nil {
		return nil, err
	}

	fmt.Printf("\nPublished %d versions of %s\n", len(moduleTags), modulePath)
	return paths, nil
}

func publishAllModules(ctx context.Context, pub *registry.Publisher, grouped map[string][]module.TagInfo) ([]string, error) {
	if len(grouped) == 0 {
		return nil, fmt.Errorf("no module tags found in repository")
	}

	moduleVersions := make(map[string][]*semver.Version, len(grouped))
	for modPath, modTags := range grouped {
		module.SortVersionsAsc(modTags)
		moduleVersions[modPath] = tagVersions(modTags)
	}

	var paths []string
	for modPath, versions := range moduleVersions {
		paths = append(paths, registry.InvalidationPathsForModuleRebuild(modPath, versions, cfg.HTML, cfg.HTMLIndex, cfg.InvalidationDirs)...)
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
			if err := pub.PublishVersion(ctx, t); err != nil {
				return nil, fmt.Errorf("publishing %s: %w", t.Tag, err)
			}
		}
		if err := pub.GenerateVersionsJSON(modPath, moduleVersions[modPath]); err != nil {
			return nil, err
		}
	}

	fmt.Printf("\nPublished %d modules\n", len(grouped))
	return paths, nil
}

var devVersion = semver.MustParse("0.0.0-dev")

func runPublishDev(cmd *cobra.Command) error {
	ctx := cmd.Context()
	gitRunner := git.NewRunner(cfg.RepoPath)
	repoRoot, err := gitRunner.TopLevel(ctx)
	if err != nil {
		return fmt.Errorf("resolving repository root: %w", err)
	}

	modules, err := discoverDevModules(repoRoot)
	if err != nil {
		return err
	}

	if dryRun {
		for _, m := range modules {
			fmt.Printf("[dry-run] Would publish %s version 0.0.0-dev from working tree\n", m.Path)
		}
		return nil
	}

	grouped, _ := module.LoadAll(ctx, gitRunner)
	if grouped == nil {
		grouped = make(map[string][]module.TagInfo)
	}

	publisher := registry.NewPublisher(gitRunner, cfg.OutputDir, cfg.BaseURL, cfg.ModulesPath)
	for _, m := range modules {
		fmt.Printf("Publishing %s version 0.0.0-dev from working tree...\n", m.Path)
		if err := publisher.PublishVersionFromWorkTree(repoRoot, m.Path, devVersion); err != nil {
			return fmt.Errorf("publishing %s: %w", m.Path, err)
		}
		versions := append(tagVersions(grouped[m.Path]), devVersion)
		if err := publisher.GenerateVersionsJSON(m.Path, versions); err != nil {
			return fmt.Errorf("generating versions.json for %s: %w", m.Path, err)
		}
	}

	if err := publisher.GenerateServiceDiscovery(); err != nil {
		return fmt.Errorf("generating service discovery: %w", err)
	}
	if err := generateDevDocs(ctx, gitRunner, repoRoot, modules, grouped); err != nil {
		return err
	}
	if err := runGzip(); err != nil {
		return err
	}

	fmt.Printf("\nPublished %d modules as 0.0.0-dev\n", len(modules))
	return nil
}

func discoverDevModules(repoRoot string) ([]module.Module, error) {
	modules, err := module.DiscoverModules(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("discovering modules: %w", err)
	}

	if publishModule != "" {
		if !module.ContainsPath(modules, publishModule) {
			return nil, fmt.Errorf("module %q not found in working tree", publishModule)
		}
		return []module.Module{{Path: publishModule}}, nil
	}

	if len(modules) == 0 {
		return nil, fmt.Errorf("no modules found in repository")
	}
	return modules, nil
}

func generateDevDocs(ctx context.Context, gitRunner *git.Runner, repoRoot string, modules []module.Module, grouped map[string][]module.TagInfo) error {
	if !cfg.HTML {
		return nil
	}

	for _, m := range modules {
		grouped[m.Path] = append(grouped[m.Path], module.TagInfo{
			Tag:        m.Path + "-0.0.0-dev",
			ModulePath: m.Path,
			Version:    devVersion,
		})
	}

	reader := registry.FilesystemReadmeReader(repoRoot)
	if cfg.TerraformDocs {
		reader = registry.EnrichedReadmeReader(reader, repoRoot, gitRunner)
	}
	if err := generateHTML(ctx, gitRunner, reader, grouped); err != nil {
		return fmt.Errorf("generating HTML documentation: %w", err)
	}
	fmt.Println("HTML documentation generated")
	return nil
}

func newHTMLGenerator(gitRunner *git.Runner, reader registry.ReadmeReader) (*registry.HTMLGenerator, error) {
	gen := registry.NewHTMLGenerator(gitRunner, cfg.OutputDir, cfg.HTMLIndex)
	gen.ReadmeReader = reader
	if cfg.HTMLBase != "" {
		if err := gen.LoadBaseTemplate(cfg.HTMLBase); err != nil {
			return nil, err
		}
	}
	return gen, nil
}

func generateHTML(ctx context.Context, gitRunner *git.Runner, reader registry.ReadmeReader, grouped map[string][]module.TagInfo) error {
	if len(grouped) == 0 {
		return nil
	}
	gen, err := newHTMLGenerator(gitRunner, reader)
	if err != nil {
		return err
	}
	return gen.GenerateAll(ctx, grouped)
}

func tagVersions(tags []module.TagInfo) []*semver.Version {
	versions := make([]*semver.Version, 0, len(tags))
	for _, t := range tags {
		versions = append(versions, t.Version)
	}
	return versions
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

