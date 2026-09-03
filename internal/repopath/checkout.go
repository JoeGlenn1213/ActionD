// Copyright (c) 2025 JoeGlenn1213
// ActionD repopath - isolated checkouts from LGH bare repositories

package repopath

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// keepCheckouts is the maximum number of per-sha checkouts kept per repo.
// Older ones are pruned (newest first, by mtime) so disk does not grow
// without bound.
const keepCheckouts = 10

// DefaultReposDir returns the default LGH bare-repository root.
func DefaultReposDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".localgithub", "repos")
}

// DefaultCheckoutRoot returns the default isolated-checkout root.
func DefaultCheckoutRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".localgithub", "checkouts")
}

// Checkout materializes a clean checkout of sha from the bare repository at
// <reposDir>/<repo> into <workRoot>/<repoName>/<sha>. The directory is
// reused when it already exists at the right sha (idempotent).
//
// Plugins run against this isolated checkout instead of the developer's live
// working tree, which prevents: dirty/uncommitted files leaking into CI,
// .env leaking into plugins, and CI running against "future" code (snapshot
// drift).
func Checkout(reposDir, repo, sha, workRoot string) (string, error) {
	if reposDir == "" || repo == "" || sha == "" {
		return "", fmt.Errorf("checkout requires reposDir, repo and sha")
	}
	repoName := strings.TrimSuffix(repo, ".git")
	if workRoot == "" {
		workRoot = DefaultCheckoutRoot()
	}
	if workRoot == "" {
		return "", fmt.Errorf("cannot determine checkout root")
	}

	bare := filepath.Join(reposDir, repo)
	if info, err := os.Stat(bare); err != nil || !info.IsDir() {
		return "", fmt.Errorf("bare repo not found: %s", bare)
	}

	dir := filepath.Join(workRoot, repoName, sha)
	if headMatches(dir, sha) {
		return dir, nil
	}

	git := gitBinary()
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if out, err := exec.Command(git, "clone", "--no-checkout", "--quiet", bare, tmp).CombinedOutput(); err != nil {
		return "", fmt.Errorf("clone %s: %v: %s", repo, err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command(git, "-C", tmp, "checkout", "--quiet", "--detach", sha).CombinedOutput(); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("checkout %s@%s: %v: %s", repo, sha, err, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(dir)
		if err2 := os.Rename(tmp, dir); err2 != nil {
			_ = os.RemoveAll(tmp)
			return "", fmt.Errorf("finalize checkout dir %s: %v", dir, err2)
		}
	}

	// Non-fatal housekeeping: keep the newest N checkouts per repo.
	if err := pruneCheckouts(filepath.Dir(dir), keepCheckouts); err != nil {
		fmt.Printf("⚠️  checkout prune skipped: %v\n", err)
	}

	return dir, nil
}

// pruneCheckouts removes the oldest per-sha checkout dirs under repoDir,
// keeping the `keep` most recently modified ones. Directories ending in
// ".tmp" are always removed (aborted clones).
func pruneCheckouts(repoDir string, keep int) error {
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		return err
	}
	type dated struct {
		name  string
		mtime time.Time
	}
	var dirs []dated
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if strings.HasSuffix(e.Name(), ".tmp") {
			_ = os.RemoveAll(filepath.Join(repoDir, e.Name()))
			continue
		}
		dirs = append(dirs, dated{name: e.Name(), mtime: info.ModTime()})
	}
	if len(dirs) <= keep {
		return nil
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mtime.After(dirs[j].mtime) })
	for _, d := range dirs[keep:] {
		_ = os.RemoveAll(filepath.Join(repoDir, d.name))
	}
	return nil
}

func headMatches(dir, sha string) bool {
	git := gitBinary()
	out, err := exec.Command(git, "-C", dir, "rev-parse", "HEAD").Output()
	return err == nil && strings.TrimSpace(string(out)) == sha
}

// gitBinary resolves the git executable with launchd-minimal-PATH fallbacks.
func gitBinary() string {
	if p, err := exec.LookPath("git"); err == nil {
		return p
	}
	for _, c := range []string{"/opt/homebrew/bin/git", "/usr/local/bin/git", "/usr/bin/git"} {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return "git"
}
