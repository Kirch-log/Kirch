package repo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Kirch-log/kirch/internal/objects"
)

func branchID(t *testing.T, r *Repo) string {
	t.Helper()
	_, id, err := r.Refs().Head()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// threeCommits: one (a=1, gone.txt), two (a=2, b=1, no gone.txt), three (a=3).
func threeCommits(t *testing.T) (r *Repo, one, two, three string) {
	t.Helper()
	r = newRepo(t, map[string]string{"a.txt": "1\n", "gone.txt": "g\n"})
	one = commitAll(t, r, "one")
	writeFile(t, filepath.Join(r.Root, "a.txt"), "2\n")
	writeFile(t, filepath.Join(r.Root, "sub", "b.txt"), "1\n")
	os.Remove(filepath.Join(r.Root, "gone.txt"))
	two = commitAll(t, r, "two")
	writeFile(t, filepath.Join(r.Root, "a.txt"), "3\n")
	three = commitAll(t, r, "three")
	return
}

func TestResetSoft(t *testing.T) {
	r, one, _, three := threeCommits(t)
	old, err := r.Reset(one, ResetSoft)
	if err != nil || old != three {
		t.Fatalf("Reset = %s, %v", old, err)
	}
	if branchID(t, r) != one {
		t.Fatal("branch not moved")
	}
	// Index and files still hold commit three: everything since one is staged.
	wantStatus(t, r, "M  a.txt", "D  gone.txt", "A  sub/b.txt")
	if got := readFile(t, r, "a.txt"); got != "3\n" {
		t.Fatalf("a.txt = %q", got)
	}
}

func TestResetMixed(t *testing.T) {
	r, one, _, _ := threeCommits(t)
	if _, err := r.Reset(one, ResetMixed); err != nil {
		t.Fatal(err)
	}
	// Index = commit one, files untouched: changes are unstaged, b.txt untracked.
	wantStatus(t, r, " M a.txt", " D gone.txt", "?? sub/")
}

func TestResetHard(t *testing.T) {
	r, one, two, _ := threeCommits(t)
	writeFile(t, filepath.Join(r.Root, "a.txt"), "uncommitted\n")
	writeFile(t, filepath.Join(r.Root, "keep.txt"), "untracked\n")
	if _, err := r.Reset(two, ResetHard); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, r, "a.txt"); got != "2\n" {
		t.Fatalf("a.txt = %q", got)
	}
	wantStatus(t, r, "?? keep.txt")

	if _, err := r.Reset(one, ResetHard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "sub")); !os.IsNotExist(err) {
		t.Fatal("sub/ should be gone with its only file")
	}
	if got := readFile(t, r, "gone.txt"); got != "g\n" {
		t.Fatalf("gone.txt = %q", got)
	}
	wantStatus(t, r, "?? keep.txt")
}

func TestResetHardStagedNewFile(t *testing.T) {
	r, _, _, _ := threeCommits(t)
	writeFile(t, filepath.Join(r.Root, "new.txt"), "n\n")
	addAll(t, r)
	if _, err := r.Reset("HEAD", ResetHard); err != nil {
		t.Fatal(err)
	}
	// Like git: a staged new file is tracked, so --hard deletes it.
	if _, err := os.Stat(filepath.Join(r.Root, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("new.txt should be deleted")
	}
	wantStatus(t, r)
}

func TestResetPaths(t *testing.T) {
	r, _, _, _ := threeCommits(t)
	writeFile(t, filepath.Join(r.Root, "a.txt"), "4\n")
	writeFile(t, filepath.Join(r.Root, "sub", "c.txt"), "c\n")
	addAll(t, r)
	wantStatus(t, r, "M  a.txt", "A  sub/c.txt")
	n, err := r.ResetPaths("", []string{"sub"})
	if err != nil || n != 1 {
		t.Fatalf("ResetPaths = %d, %v", n, err)
	}
	wantStatus(t, r, "M  a.txt", "?? sub/c.txt")
	if n, err = r.ResetPaths("", []string{"."}); err != nil || n != 1 {
		t.Fatalf("ResetPaths . = %d, %v", n, err)
	}
	wantStatus(t, r, " M a.txt", "?? sub/c.txt")
}

func TestResetNoCommits(t *testing.T) {
	r := newRepo(t, nil)
	if _, err := r.Reset("HEAD", ResetHard); !errors.Is(err, ErrNoCommits) {
		t.Fatalf("err = %v", err)
	}
}

func TestRevert(t *testing.T) {
	r, one, _, three := threeCommits(t)
	// three only touched a.txt: reverting it puts a.txt back to 2.
	res, err := r.Revert(three, testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Parent != three {
		t.Fatalf("parent = %s", res.Parent)
	}
	if got := readFile(t, r, "a.txt"); got != "2\n" {
		t.Fatalf("a.txt = %q", got)
	}
	c, _ := r.ReadCommit(res.ID)
	want := fmt.Sprintf("Revert \"three\"\n\nThis reverts commit %s.\n", three)
	if c.Message != want {
		t.Fatalf("message = %q", c.Message)
	}
	wantStatus(t, r)

	// Files one made were changed again later: refused, nothing touched.
	var re *RevertError
	if _, err := r.Revert(one, testAuthor); !errors.As(err, &re) || strings.Join(re.Files, ",") != "a.txt,gone.txt" {
		t.Fatalf("err = %v", err)
	}
	if branchID(t, r) != res.ID {
		t.Fatal("refused revert moved the branch")
	}
	wantStatus(t, r)
}

func TestRevertAddsAndDeletes(t *testing.T) {
	r, _, two, _ := threeCommits(t)
	// Undo a.txt first so two is revertible on its own.
	if _, err := r.Revert("HEAD", testAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Revert(two, testAuthor); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, r, "gone.txt"); got != "g\n" {
		t.Fatalf("gone.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "sub")); !os.IsNotExist(err) {
		t.Fatal("sub/ should be removed")
	}
	wantStatus(t, r)
}

func TestRevertRefusesDirtyOrBlocked(t *testing.T) {
	r, _, two, three := threeCommits(t)
	writeFile(t, filepath.Join(r.Root, "a.txt"), "dirty\n")
	if _, err := r.Revert(three, testAuthor); !errors.Is(err, ErrDirty) {
		t.Fatalf("err = %v", err)
	}
	if _, err := r.Reset("HEAD", ResetHard); err != nil {
		t.Fatal(err)
	}
	// Reverting two would bring back gone.txt, but an untracked one is there.
	r.Revert(three, testAuthor)
	writeFile(t, filepath.Join(r.Root, "gone.txt"), "mine\n")
	var ce *CheckoutError
	if _, err := r.Revert(two, testAuthor); !errors.As(err, &ce) {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, r, "gone.txt"); got != "mine\n" {
		t.Fatal("untracked file was overwritten")
	}
}

// Oracle: git revert of the same commit, with the same author and time, makes
// a byte-identical commit (same id), and git reset --hard agrees with ours.
func TestRevertAndResetMatchGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	r, _, two, three := threeCommits(t)
	writeFile(t, filepath.Join(r.Root, "netlist.json"), netlist)
	four := commitAll(t, r, "four: wire servo")

	// Copy the repository for git to work on.
	gitDir := filepath.Join(t.TempDir(), "copy.git")
	work := t.TempDir()
	if err := os.CopyFS(gitDir, os.DirFS(r.Dir)); err != nil {
		t.Fatal(err)
	}
	when := testAuthor.String()[len(testAuthor.Name)+len(testAuthor.Email)+4:]
	run := func(dir string, args ...string) string {
		cmd := exec.Command(git, append([]string{"--git-dir=" + dir, "--work-tree=" + work}, args...)...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_AUTHOR_NAME="+testAuthor.Name, "GIT_AUTHOR_EMAIL="+testAuthor.Email, "GIT_AUTHOR_DATE="+when,
			"GIT_COMMITTER_NAME="+testAuthor.Name, "GIT_COMMITTER_EMAIL="+testAuthor.Email, "GIT_COMMITTER_DATE="+when)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(gitDir, "reset", "-q", "--hard")
	run(gitDir, "revert", "--no-edit", four)
	ours, err := r.Revert(four, testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	if theirs := run(gitDir, "rev-parse", "HEAD"); ours.ID != theirs {
		t.Fatalf("revert: ours %s, git %s\n%s", ours.ID, theirs, run(gitDir, "cat-file", "-p", theirs))
	}

	run(gitDir, "revert", "--no-edit", three)
	res, err := r.Revert(three, testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	if theirs := run(gitDir, "rev-parse", "HEAD"); res.ID != theirs {
		t.Fatalf("second revert: ours %s, git %s", res.ID, theirs)
	}

	// Reset --hard back to two: branch, index and files match git ls-tree.
	if _, err := r.Reset(two, ResetHard); err != nil {
		t.Fatal(err)
	}
	if got := run(r.Dir, "rev-parse", "main"); got != two {
		t.Fatalf("git sees main at %s, want %s", got, two)
	}
	want := strings.Split(run(r.Dir, "ls-tree", "-r", two), "\n")
	idx, _ := r.LoadIndex()
	var got []string
	for _, p := range idx.Paths() {
		e := idx.Entries[p]
		got = append(got, e.Mode+" blob "+e.Hash+"\t"+p)
		data, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(p)))
		if err != nil || objects.Hash(objects.Blob, data) != e.Hash {
			t.Fatalf("%s on disk doesn't match the index (%v)", p, err)
		}
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("index:\n%s\ngit ls-tree:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	wantStatus(t, r)
	run(r.Dir, "fsck", "--strict", "--no-dangling")
}
