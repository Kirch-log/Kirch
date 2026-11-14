package repo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kirch-log/kirch/internal/objects"
)

func TestInitCreatesLayout(t *testing.T) {
	root := t.TempDir()
	r, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"objects", filepath.Join("refs", "heads")} {
		if info, err := os.Stat(filepath.Join(r.Dir, d)); err != nil || !info.IsDir() {
			t.Errorf("%s missing: %v", d, err)
		}
	}
	head, err := os.ReadFile(filepath.Join(r.Dir, "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	if string(head) != "ref: refs/heads/main\n" {
		t.Errorf("HEAD = %q", head)
	}
}

func TestInitTwiceFails(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root); !errors.Is(err, ErrExists) {
		t.Fatalf("second Init err = %v, want ErrExists", err)
	}
}

func TestFindFromSubfolder(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "firmware", "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Find(sub)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(root)
	if r.Root != want {
		t.Errorf("Root = %s, want %s", r.Root, want)
	}
}

func TestFindOutsideRepo(t *testing.T) {
	if _, err := Find(t.TempDir()); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("err = %v, want ErrNotRepo", err)
	}
}

// An empty .kirch folder (no HEAD) is not a repository.
func TestFindIgnoresEmptyDataDir(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Find(root); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("err = %v, want ErrNotRepo", err)
	}
}

func TestObjectsGoIntoRepo(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.Objects.Write(objects.Blob, []byte("hello\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "objects", id[:2], id[2:])); err != nil {
		t.Fatal(err)
	}
}

// Oracle: git accepts our .kirch folder as a git directory.
func TestGitAcceptsLayout(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.Objects.Write(objects.Blob, []byte("hello\n"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(git, "--git-dir="+r.Dir, "cat-file", "-p", id)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git cat-file: %v %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "hello" {
		t.Fatalf("git read %q", out)
	}
	headCmd := exec.Command(git, "--git-dir="+r.Dir, "symbolic-ref", "HEAD")
	head, err := headCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git symbolic-ref: %v %s", err, head)
	}
	if strings.TrimSpace(string(head)) != "refs/heads/main" {
		t.Fatalf("git sees HEAD = %q", head)
	}
}
