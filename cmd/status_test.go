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

func TestQuotePath(t *testing.T) {
	for in, want := range map[string]string{
		"a.txt":      "a.txt",
		"a b.txt":    `"a b.txt"`,
		`q"t\x`:      `"q\"t\\x"`,
		"tab\there":  `"tab\there"`,
		"é.txt":      `"\303\251.txt"`,
		"dir/x y/z/": `"dir/x y/z/"`,
	} {
		if got := quotePath(in); got != want {
			t.Errorf("quotePath(%q) = %s, want %s", in, got, want)
		}
	}
}

// Oracle: with the same last commit, index and files, kirch status --porcelain
// prints exactly what git status --porcelain prints.
func TestStatusMatchesGit(t *testing.T) {
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
	write := func(p, content string) {
		t.Helper()
		abs := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for p, c := range map[string]string{
		"keep.txt": "k\n", "edit.txt": "e\n", "both.txt": "b\n", "gone.txt": "g\n", "removed.txt": "r\n",
		"firmware/main.cpp": "void setup(){}\n",
	} {
		write(p, c)
	}
	if _, err := r.Add([]string{"."}); err != nil {
		t.Fatal(err)
	}
	author := objects.Signature{Name: "T", Email: "t@example.com", When: time.Unix(1794621600, 0).UTC()}
	if _, err := r.Commit("first", author); err != nil {
		t.Fatal(err)
	}

	write("edit.txt", "edited\n")
	write("both.txt", "staged\n")
	write("new.txt", "n\n")
	write("added then gone.txt", "x\n")
	os.Remove(filepath.Join(root, "gone.txt"))
	os.Remove(filepath.Join(root, "removed.txt"))
	if _, err := r.Add([]string{"both.txt", "new.txt", "removed.txt", "added then gone.txt"}); err != nil {
		t.Fatal(err)
	}
	write("both.txt", "staged, then changed again\n")
	os.Remove(filepath.Join(root, "added then gone.txt"))
	write("untracked file.txt", "u\n")
	write("newdir/sub/f.txt", "f\n")
	write("firmware/extra.cpp", "e\n")
	write("firmware/lib/a/b.h", "h\n")

	run := mirrorInGit(t, git, r)
	got := runKirch(t, "status", "--porcelain")
	if want := run("status", "--porcelain"); got != want {
		t.Fatalf("kirch status --porcelain:\n%s\ngit status --porcelain:\n%s", got, want)
	}
}

// mirrorInGit makes a .git next to .kirch with the same last commit and
// index, so git commands see what kirch sees. It returns a git runner.
func mirrorInGit(t *testing.T, git string, r *repo.Repo) func(args ...string) string {
	t.Helper()
	globalCfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(globalCfg, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+globalCfg,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES="+filepath.Join(r.Root, ".kirch", "objects"))
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-c", "core.autocrlf=false"}, args...)...)
		cmd.Dir = r.Root
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(r.Root, ".git", "info", "exclude"), []byte(".kirch/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, head, err := r.Refs().Head()
	if err != nil {
		t.Fatal(err)
	}
	run("update-ref", "HEAD", head)
	idx, err := r.LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range idx.Paths() {
		e := idx.Entries[p]
		run("update-index", "--add", "--cacheinfo", e.Mode+","+e.Hash+","+p)
	}
	return run
}
