package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/repo"
)

// Oracle: kirch branch lists branches exactly like git branch.
func TestBranchListMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	r, err := repo.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add([]string{"."}); err != nil {
		t.Fatal(err)
	}
	author := objects.Signature{Name: "T", Email: "t@example.com", When: time.Unix(1794621600, 0).UTC()}
	if _, err := r.Commit("first", author); err != nil {
		t.Fatal(err)
	}
	runKirch(t, "branch", "zeta")
	runKirch(t, "branch", "feature/probe")
	runKirch(t, "checkout", "-b", "alpha")
	runKirch(t, "branch", "gone")
	if out := runKirch(t, "branch", "-d", "gone"); out[:len("Deleted branch gone (was ")] != "Deleted branch gone (was " {
		t.Fatalf("delete output = %q", out)
	}

	cmd := exec.Command(git, "--git-dir=.kirch", "branch", "--no-color")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	want, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch: %v\n%s", err, want)
	}
	if got := runKirch(t, "branch"); got != string(want) {
		t.Fatalf("kirch branch:\n%s\ngit branch:\n%s", got, want)
	}
	if got := runKirch(t, "checkout", "main"); got != "Switched to branch 'main'\n" {
		t.Fatalf("checkout output = %q", got)
	}
	if got := runKirch(t, "checkout", "main"); got != "Already on 'main'\n" {
		t.Fatalf("checkout output = %q", got)
	}
}
