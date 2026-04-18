package module

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Module represents a discovered terraform module.
type Module struct {
	Path string // Relative path within the repo, e.g. "hetzner/server"
}

// TagInfo holds parsed information from a git tag.
type TagInfo struct {
	Tag        string
	ModulePath string
	Version    *semver.Version
}

// ParseTag parses a git tag into module path and version.
// Tags have the format: {module_path}-{semver}
// We scan from the right for a '-' followed by a valid semver to handle
// module paths containing dashes (e.g. aws/ec2/security-group-1.0.0).
func ParseTag(tag string) (*TagInfo, error) {
	for i := len(tag) - 1; i >= 0; i-- {
		if tag[i] == '-' {
			vStr := tag[i+1:]
			v, err := semver.StrictNewVersion(vStr)
			if err == nil {
				modulePath := tag[:i]
				if modulePath == "" {
					return nil, fmt.Errorf("empty module path in tag: %s", tag)
				}
				return &TagInfo{
					Tag:        tag,
					ModulePath: modulePath,
					Version:    v,
				}, nil
			}
		}
	}
	return nil, fmt.Errorf("no valid semver found in tag: %s", tag)
}

// FormatTag creates a tag string from a module path and version.
func FormatTag(modulePath string, version *semver.Version) string {
	return modulePath + "-" + version.Original()
}

// DiscoverModules walks the repository directory and finds all directories
// containing .tf files (excluding the root directory).
// Any directories in excludeDirs are skipped (matched by name, not path).
func DiscoverModules(repoPath string, excludeDirs ...string) ([]Module, error) {
	var modules []Module
	seen := make(map[string]bool)
	excludeSet := make(map[string]bool, len(excludeDirs))
	for _, d := range excludeDirs {
		excludeSet[d] = true
	}

	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories (like .git, .terraform)
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}

		// Skip excluded directories
		if info.IsDir() && excludeSet[info.Name()] {
			return filepath.SkipDir
		}

		// We only care about .tf files
		if info.IsDir() || filepath.Ext(path) != ".tf" {
			return nil
		}

		dir := filepath.Dir(path)
		relDir, err := filepath.Rel(repoPath, dir)
		if err != nil {
			return err
		}

		// Exclude root directory
		if relDir == "." {
			return nil
		}

		// Normalize to forward slashes for consistency
		relDir = filepath.ToSlash(relDir)

		if !seen[relDir] {
			seen[relDir] = true
			modules = append(modules, Module{Path: relDir})
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discovering modules: %w", err)
	}

	sort.Slice(modules, func(i, j int) bool {
		return modules[i].Path < modules[j].Path
	})

	return modules, nil
}

// GroupTagsByModule groups parsed tag infos by module path.
func GroupTagsByModule(tags []TagInfo) map[string][]TagInfo {
	result := make(map[string][]TagInfo)
	for _, t := range tags {
		result[t.ModulePath] = append(result[t.ModulePath], t)
	}
	return result
}

// SortVersionsDesc sorts tag infos by version descending (newest first).
func SortVersionsDesc(tags []TagInfo) {
	sort.Slice(tags, func(i, j int) bool {
		return tags[i].Version.GreaterThan(tags[j].Version)
	})
}

// SortVersionsAsc sorts tag infos by version ascending (oldest first).
func SortVersionsAsc(tags []TagInfo) {
	sort.Slice(tags, func(i, j int) bool {
		return tags[i].Version.LessThan(tags[j].Version)
	})
}

// LatestVersion returns the latest version from a list of tag infos.
// Returns nil if the list is empty.
func LatestVersion(tags []TagInfo) *TagInfo {
	if len(tags) == 0 {
		return nil
	}
	latest := tags[0]
	for _, t := range tags[1:] {
		if t.Version.GreaterThan(latest.Version) {
			latest = t
		}
	}
	return &latest
}

// NextVersion computes the next version given a bump type.
func NextVersion(current *semver.Version, bump string) (*semver.Version, error) {
	var next semver.Version
	switch bump {
	case "patch":
		next = current.IncPatch()
	case "minor":
		next = current.IncMinor()
	case "major":
		next = current.IncMajor()
	default:
		return nil, fmt.Errorf("unknown bump type: %s", bump)
	}
	return &next, nil
}

// ContainsPath reports whether any module in the slice has the given path.
func ContainsPath(modules []Module, path string) bool {
	for _, m := range modules {
		if m.Path == path {
			return true
		}
	}
	return false
}

// FilterTagsForModule filters parsed tags to only those matching a module path.
func FilterTagsForModule(tags []TagInfo, modulePath string) []TagInfo {
	var result []TagInfo
	for _, t := range tags {
		if t.ModulePath == modulePath {
			result = append(result, t)
		}
	}
	return result
}

// ParseAllTags parses a list of raw tag strings, skipping those that don't
// match the expected format.
func ParseAllTags(rawTags []string) []TagInfo {
	var result []TagInfo
	for _, raw := range rawTags {
		info, err := ParseTag(raw)
		if err != nil {
			continue // skip non-module tags
		}
		result = append(result, *info)
	}
	return result
}
