package repopath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrefersExactDirectory(t *testing.T) {
	root := t.TempDir()
	exact := filepath.Join(root, "demo-api")
	if err := os.Mkdir(exact, 0755); err != nil {
		t.Fatalf("mkdir exact repo: %v", err)
	}

	got := resolve(root, "demo-api.git", "")
	if got != exact {
		t.Fatalf("Resolve() = %q, want %q", got, exact)
	}
}

func TestResolveMatchesNormalizedDirectoryName(t *testing.T) {
	root := t.TempDir()
	matched := filepath.Join(root, "actiond-web")
	if err := os.Mkdir(matched, 0755); err != nil {
		t.Fatalf("mkdir matched repo: %v", err)
	}

	got := resolve(root, "ActionD-Web.git", "")
	if got != matched {
		t.Fatalf("Resolve() = %q, want %q", got, matched)
	}
}

func TestResolveFromMappings(t *testing.T) {
	root := t.TempDir()
	mapped := filepath.Join(root, "notes-store")
	if err := os.Mkdir(mapped, 0755); err != nil {
		t.Fatalf("mkdir mapped repo: %v", err)
	}

	mappingsPath := filepath.Join(root, "mappings.yaml")
	content := "repos:\n  - name: notes-store\n    source_path: " + mapped + "\n"
	if err := os.WriteFile(mappingsPath, []byte(content), 0644); err != nil {
		t.Fatalf("write mappings: %v", err)
	}

	got := resolveFromMappings(mappingsPath, "notes-store.git")
	if got != mapped {
		t.Fatalf("resolveFromMappings() = %q, want %q", got, mapped)
	}
}

func TestResolveFallsBackToCurrentWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "progressCC")
	if err := os.Mkdir(repoDir, 0755); err != nil {
		t.Fatalf("mkdir repo dir: %v", err)
	}

	t.Chdir(root)

	got := resolve("", "progressCC.git", "")
	if got != repoDir {
		t.Fatalf("resolve() = %q, want %q", got, repoDir)
	}
}

// TestResolveReturnsEmptyWhenNoMatchFound is the regression test for the
// env-check /lgh bug: when a repo named "lgh" is not found under the repo
// root, resolve must return "" rather than a non-existent path like "/lgh".
// Returning a non-existent path caused plugins to receive an unusable
// repo_path and crash with FileNotFoundError.
func TestResolveReturnsEmptyWhenNoMatchFound(t *testing.T) {
	root := t.TempDir() // empty dir, no "lgh" subdirectory

	got := resolve(root, "lgh.git", "")
	if got != "" {
		t.Fatalf("resolve() = %q, want empty string — non-existent path would cause plugin FileNotFoundError", got)
	}
}

// TestResolveFindsRepoWhenDirectoryExists confirms that Resolve still returns
// the correct path when the repository directory does exist under root.
func TestResolveFindsRepoWhenDirectoryExists(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "lgh")
	if err := os.Mkdir(repoDir, 0755); err != nil {
		t.Fatalf("mkdir lgh repo: %v", err)
	}

	got := resolve(root, "lgh.git", "")
	if got != repoDir {
		t.Fatalf("resolve() = %q, want %q", got, repoDir)
	}
}
