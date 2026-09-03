// Copyright (c) 2025 JoeGlenn1213
// ActionD repopath - isolated checkout tests

package repopath

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckoutMaterializesPushedSha(t *testing.T) {
	root := t.TempDir()
	reposDir := filepath.Join(root, "repos")
	workRoot := filepath.Join(root, "checkouts")
	if err := os.MkdirAll(reposDir, 0755); err != nil {
		t.Fatal(err)
	}

	bare := filepath.Join(reposDir, "demo.git")
	runGit(t, root, "init", "--bare", bare)

	work := filepath.Join(root, "work")
	runGit(t, root, "clone", bare, work)
	writeFile(t, filepath.Join(work, "go.mod"), "module demo\n")
	runGit(t, work, "add", "go.mod")
	runGit(t, work, "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "first")
	sha := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	runGit(t, work, "push", "origin", "HEAD:master")

	dir, err := Checkout(reposDir, "demo.git", sha, workRoot)
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("expected go.mod in checkout: %v", err)
	}

	// Idempotent reuse: same dir returned for the same sha.
	dir2, err := Checkout(reposDir, "demo.git", sha, workRoot)
	if err != nil {
		t.Fatalf("second Checkout: %v", err)
	}
	if dir2 != dir {
		t.Fatalf("expected idempotent reuse, got %q and %q", dir, dir2)
	}

	// A later commit must NOT appear in the first sha's checkout.
	writeFile(t, filepath.Join(work, "extra.txt"), "x")
	runGit(t, work, "add", "extra.txt")
	runGit(t, work, "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "second")
	runGit(t, work, "push", "origin", "HEAD:master")
	if _, err := os.Stat(filepath.Join(dir, "extra.txt")); err == nil {
		t.Fatal("checkout of the first sha must not contain files from the second commit")
	}

	// Unknown sha fails closed.
	if _, err := Checkout(reposDir, "demo.git", strings.Repeat("0", 40), workRoot); err == nil {
		t.Fatal("expected error for unknown sha")
	}

	// Missing bare repo fails closed.
	if _, err := Checkout(reposDir, "nope.git", sha, workRoot); err == nil {
		t.Fatal("expected error for missing bare repo")
	}

	// Missing args fail closed.
	if _, err := Checkout("", "demo.git", sha, workRoot); err == nil {
		t.Fatal("expected error for empty reposDir")
	}
	if _, err := Checkout(reposDir, "", sha, workRoot); err == nil {
		t.Fatal("expected error for empty repo")
	}
	if _, err := Checkout(reposDir, "demo.git", "", workRoot); err == nil {
		t.Fatal("expected error for empty sha")
	}
}

func TestPruneCheckoutsKeepsNewest(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		d := filepath.Join(repoDir, fmt.Sprintf("sha%02d", i))
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(d, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	tmpDir := filepath.Join(repoDir, "shaXX.tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := pruneCheckouts(repoDir, 10); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 10 {
		t.Fatalf("expected 10 dirs after prune, got %d", len(entries))
	}
	if _, err := os.Stat(filepath.Join(repoDir, "sha14")); err != nil {
		t.Fatal("newest checkout should be kept")
	}
	if _, err := os.Stat(filepath.Join(repoDir, "sha00")); err == nil {
		t.Fatal("oldest checkout should be pruned")
	}
	if _, err := os.Stat(tmpDir); err == nil {
		t.Fatal(".tmp dir should always be removed")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
