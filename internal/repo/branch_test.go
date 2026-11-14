package repo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Kirch-log/kirch/internal/objects"
)

func commitAll(t *testing.T, r *Repo, msg string) string {
	t.Helper()
	addAll(t, r)
	res, err := r.Commit(msg, testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	return res.ID
}

func readFile(t *testing.T, r *Repo, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func headBranch(t *testing.T, r *Repo) string {
	t.Helper()
	b, _, err := r.Refs().Head()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCreateAndDeleteBranch(t *testing.T) {
	r := newRepo(t, map[string]string{"a.txt": "1"})
	first := commitAll(t, r, "one")
	writeFile(t, filepath.Join(r.Root, "a.txt"), "2")
	second := commitAll(t, r, "two")

	if id, err := r.CreateBranch("feature/x", ""); err != nil || id != second {
		t.Fatalf("CreateBranch = %s, %v", id, err)
	}
	if id, err := r.CreateBranch("old", first[:7]); err != nil || id != first {
		t.Fatalf("CreateBranch at first = %s, %v", id, err)
	}
	for _, bad := range []string{"old", "feature", "feature/x/y", "bad name", "main"} {
		if _, err := r.CreateBranch(bad, ""); err == nil {
			t.Errorf("CreateBranch(%q) succeeded", bad)
		}
	}
	if _, err := r.CreateBranch("nowhere", "nope"); err == nil {
		t.Error("CreateBranch at unknown revision succeeded")
	}

	if _, err := r.DeleteBranch("main", true); err == nil {
		t.Error("deleted the current branch")
	}
	if _, err := r.DeleteBranch("missing", false); err == nil {
		t.Error("deleted a missing branch")
	}
	// old and feature/x are both reachable from main: safe to delete.
	if id, err := r.DeleteBranch("feature/x", false); err != nil || id != second {
		t.Fatalf("DeleteBranch = %s, %v", id, err)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "refs", "heads", "feature")); !os.IsNotExist(err) {
		t.Error("empty refs/heads/feature folder left behind")
	}
	if _, err := r.DeleteBranch("old", false); err != nil {
		t.Fatal(err)
	}
	branches, _ := r.Refs().Branches()
	if len(branches) != 1 || branches["main"] != second {
		t.Fatalf("branches = %v", branches)
	}
}

func TestDeleteUnmergedBranch(t *testing.T) {
	r := newRepo(t, map[string]string{"a.txt": "1"})
	commitAll(t, r, "one")
	if _, err := r.CreateBranch("side", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Checkout("side"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(r.Root, "a.txt"), "side work")
	commitAll(t, r, "side work")
	if err := r.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DeleteBranch("side", false); !errors.Is(err, ErrNotMerged) {
		t.Fatalf("err = %v, want ErrNotMerged", err)
	}
	if _, err := r.DeleteBranch("side", true); err != nil {
		t.Fatal(err)
	}
}

func TestCheckoutUpdatesFiles(t *testing.T) {
	r := newRepo(t, map[string]string{
		"same.txt": "same", "changed.txt": "main version", "only-main/deep/x.txt": "x",
	})
	mainID := commitAll(t, r, "main")
	if _, err := r.CreateBranch("side", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Checkout("side"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(r.Root, "changed.txt"), "side version")
	writeFile(t, filepath.Join(r.Root, "only-side", "y.txt"), "y")
	os.RemoveAll(filepath.Join(r.Root, "only-main"))
	sideID := commitAll(t, r, "side")

	if err := r.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	if headBranch(t, r) != "main" {
		t.Fatal("HEAD did not move to main")
	}
	if got := readFile(t, r, "changed.txt"); got != "main version" {
		t.Fatalf("changed.txt = %q", got)
	}
	if readFile(t, r, "only-main/deep/x.txt") != "x" {
		t.Fatal("only-main/deep/x.txt not restored")
	}
	if _, err := os.Stat(filepath.Join(r.Root, "only-side")); !os.IsNotExist(err) {
		t.Fatal("only-side folder still there on main")
	}
	wantStatus(t, r)

	if err := r.Checkout("side"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "only-main")); !os.IsNotExist(err) {
		t.Fatal("only-main folder still there on side")
	}
	wantStatus(t, r)
	if ids := logIDs(t, r, "HEAD", 0); len(ids) != 2 || ids[0] != sideID || ids[1] != mainID {
		t.Fatalf("log on side = %v", ids)
	}
}

func TestCheckoutKeepsChangesToSameFiles(t *testing.T) {
	r := newRepo(t, map[string]string{"same.txt": "same", "changed.txt": "main"})
	commitAll(t, r, "main")
	r.CreateBranch("side", "")
	r.Checkout("side")
	writeFile(t, filepath.Join(r.Root, "changed.txt"), "side")
	commitAll(t, r, "side")

	writeFile(t, filepath.Join(r.Root, "same.txt"), "my edit")
	writeFile(t, filepath.Join(r.Root, "new.txt"), "staged new file")
	if _, err := r.Add([]string{"new.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	if readFile(t, r, "same.txt") != "my edit" || readFile(t, r, "changed.txt") != "main" {
		t.Fatal("files wrong after checkout")
	}
	wantStatus(t, r, "A  new.txt", " M same.txt")
}

func TestCheckoutRefusesToLoseWork(t *testing.T) {
	r := newRepo(t, map[string]string{"changed.txt": "main"})
	commitAll(t, r, "main")
	r.CreateBranch("side", "")
	r.Checkout("side")
	writeFile(t, filepath.Join(r.Root, "changed.txt"), "side")
	writeFile(t, filepath.Join(r.Root, "side-only.txt"), "s")
	commitAll(t, r, "side")
	r.Checkout("main")

	// Uncommitted edit to a file that differs on side.
	writeFile(t, filepath.Join(r.Root, "changed.txt"), "unsaved work")
	var ce *CheckoutError
	if err := r.Checkout("side"); !errors.As(err, &ce) || len(ce.Changed) != 1 || ce.Changed[0] != "changed.txt" {
		t.Fatalf("err = %v", err)
	}
	if headBranch(t, r) != "main" || readFile(t, r, "changed.txt") != "unsaved work" {
		t.Fatal("refused checkout still changed something")
	}
	writeFile(t, filepath.Join(r.Root, "changed.txt"), "main")

	// Untracked file where side has a tracked one.
	writeFile(t, filepath.Join(r.Root, "side-only.txt"), "mine")
	if err := r.Checkout("side"); !errors.As(err, &ce) || len(ce.Untracked) != 1 {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, r, "side-only.txt") != "mine" {
		t.Fatal("untracked file overwritten")
	}
	os.Remove(filepath.Join(r.Root, "side-only.txt"))
	if err := r.Checkout("side"); err != nil {
		t.Fatal(err)
	}
}

// Oracle: after a checkout, git sees HEAD on the new branch, and our index
// and files hold exactly the files of that branch's commit (git ls-tree -r).
func TestCheckoutMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	r := newRepo(t, map[string]string{
		"netlist.json": netlist, "firmware/src/main.cpp": "a\n", "firmware/old.h": "o\n", "notes.txt": "n\n",
	})
	commitAll(t, r, "main")
	r.CreateBranch("side", "")
	r.Checkout("side")
	writeFile(t, filepath.Join(r.Root, "firmware", "src", "main.cpp"), "b\n")
	writeFile(t, filepath.Join(r.Root, "firmware", "new", "x.h"), "x\n")
	os.Remove(filepath.Join(r.Root, "firmware", "old.h"))
	commitAll(t, r, "side")

	run := func(args ...string) string {
		cmd := exec.Command(git, append([]string{"--git-dir=.kirch"}, args...)...)
		cmd.Dir = r.Root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	for _, b := range []string{"main", "side", "main"} {
		if err := r.Checkout(b); err != nil {
			t.Fatal(err)
		}
		if got := run("symbolic-ref", "HEAD"); got != "refs/heads/"+b {
			t.Fatalf("git symbolic-ref HEAD = %s, want %s", got, b)
		}
		want := strings.Split(run("ls-tree", "-r", b), "\n")
		idx, _ := r.LoadIndex()
		var ours []string
		for _, p := range idx.Paths() {
			e := idx.Entries[p]
			ours = append(ours, e.Mode+" blob "+e.Hash+"\t"+p)
			data, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(p)))
			if err != nil || objects.Hash(objects.Blob, data) != e.Hash {
				t.Fatalf("%s on disk doesn't match the index (%v)", p, err)
			}
		}
		sort.Strings(want)
		sort.Strings(ours)
		if strings.Join(ours, "\n") != strings.Join(want, "\n") {
			t.Fatalf("on %s, index:\n%s\ngit ls-tree:\n%s", b, strings.Join(ours, "\n"), strings.Join(want, "\n"))
		}
		wantStatus(t, r)
	}
	r.DeleteBranch("side", true)
	run("fsck", "--strict", "--no-dangling")
}
