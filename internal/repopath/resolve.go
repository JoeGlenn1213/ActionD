package repopath

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Resolve returns the best local path for a repository event name.
// It first tries the literal repo directory, then falls back to a normalized
// directory-name match so LGH names like "ActionD-Web.git" can map to
// a local checkout like "actiond-web".
func Resolve(root, repo string) string {
	return resolve(root, repo, defaultMappingsPath())
}

func resolve(root, repo, mappingsPath string) string {
	if mappedPath := resolveFromMappings(mappingsPath, repo); mappedPath != "" {
		return mappedPath
	}

	repoName := strings.TrimSuffix(repo, ".git")
	want := normalize(repoName)
	for _, candidateRoot := range candidateRoots(root, want) {
		if candidateRoot == "" {
			continue
		}
		if matchedPath := resolveInRoot(candidateRoot, repoName, want); matchedPath != "" {
			return matchedPath
		}
	}

	// Final fallback: only return a guessed path if it actually exists on disk.
	// Returning a non-existent path causes plugins to receive an unusable repo_path
	// (e.g. repo named "lgh" would yield "/lgh" when root="/", which always fails).
	if root != "" {
		candidate := filepath.Join(root, repoName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		candidate := filepath.Join(cwd, repoName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func candidateRoots(root, normalizedRepo string) []string {
	var roots []string
	seen := make(map[string]bool)

	appendRoot := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		roots = append(roots, dir)
	}

	appendRoot(root)

	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		if normalize(filepath.Base(cwd)) == normalizedRepo {
			appendRoot(filepath.Dir(cwd))
		}
		appendRoot(cwd)
	}

	return roots
}

func resolveInRoot(root, repoName, want string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		// Can't read the directory — don't return a guessed path that may not exist.
		return ""
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() == repoName {
			return filepath.Join(root, entry.Name())
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if normalize(entry.Name()) == want {
			return filepath.Join(root, entry.Name())
		}
	}

	// No match found in this root — return empty so the caller tries the next candidate.
	return ""
}

type mappingsFile struct {
	Repos []repoMapping `yaml:"repos"`
}

type repoMapping struct {
	Name       string `yaml:"name"`
	SourcePath string `yaml:"source_path"`
}

func resolveFromMappings(mappingsPath, repo string) string {
	if strings.TrimSpace(mappingsPath) == "" {
		return ""
	}

	data, err := os.ReadFile(mappingsPath)
	if err != nil {
		return ""
	}

	var mappings mappingsFile
	if err := yaml.Unmarshal(data, &mappings); err != nil {
		return ""
	}

	repoName := strings.TrimSuffix(strings.TrimSpace(repo), ".git")
	if repoName == "" {
		return ""
	}

	want := normalize(repoName)
	for _, entry := range mappings.Repos {
		if normalize(entry.Name) != want {
			continue
		}
		if strings.TrimSpace(entry.SourcePath) == "" {
			return ""
		}
		return entry.SourcePath
	}

	return ""
}

func defaultMappingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".localgithub", "mappings.yaml")
}

func normalize(name string) string {
	name = strings.TrimSuffix(strings.ToLower(name), ".git")

	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
