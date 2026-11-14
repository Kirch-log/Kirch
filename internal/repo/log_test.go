package repo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kirch-log/kirch/internal/objects"
)

// commitAt writes a commit with the empty tree, made at unix time secs.
func commitAt(t *testing.T, r *Repo, msg string, secs int64, parents ...string) string {
	t.Helper()
	tree, err := r.Objects.Write(objects.Tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := testAuthor
	sig.When = time.Unix(secs, 0).In(sig.When.Location())
	data, err := objects.EncodeCommit(objects.CommitData{
		Tree: tree, Parents: parents, Author: sig, Committer: sig, Message: msg + "\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.Objects.Write(objects.Commit, data)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func setBranch(t *testing.T, r *Repo, name, id string) {
	t.Helper()
	old, err := r.Refs().Branch(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Refs().UpdateBranch(name, id, old); err != nil {
		t.Fatal(err)
	}
}

func logIDs(t *testing.T, r *Repo, rev string, max int) []string {
	t.Helper()
	entries, err := r.Log(rev, max)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	return ids
}

func TestLogLinear(t *testing.T) {
	r := newRepo(t, map[string]string{"a.txt": "1"})
	addAll(t, r)
	first, err := r.Commit("one", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(r.Root, "a.txt"), "2")
	addAll(t, r)
	second, err := r.Commit("two", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := r.Log("HEAD", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ID != second.ID || entries[1].ID != first.ID {
		t.Fatalf("log = %+v", entries)
	}
	if entries[0].Message != "two\n" || entries[1].Author.Email != testAuthor.Email {
		t.Fatalf("log data = %+v", entries)
	}
	if ids := logIDs(t, r, "", 1); len(ids) != 1 || ids[0] != second.ID {
		t.Fatalf("max 1 = %v", ids)
	}
	if ids := logIDs(t, r, first.ID, 0); len(ids) != 1 || ids[0] != first.ID {
		t.Fatalf("from first = %v", ids)
	}
}

func TestLogEmptyBranch(t *testing.T) {
	r := newRepo(t, nil)
	if _, err := r.Log("HEAD", 0); !errors.Is(err, ErrNoCommits) {
		t.Fatalf("err = %v, want ErrNoCommits", err)
	}
}

func TestResolve(t *testing.T) {
	r := newRepo(t, nil)
	c1 := commitAt(t, r, "one", 1794621600)
	c2 := commitAt(t, r, "two", 1794621700, c1)
	setBranch(t, r, "main", c2)
	setBranch(t, r, "old", c1)

	for rev, want := range map[string]string{
		"": c2, "HEAD": c2, "main": c2, "old": c1, c1: c1, c1[:7]: c1, c2[:4]: c2,
	} {
		got, err := r.Resolve(rev)
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %s, %v; want %s", rev, got, err, want)
		}
	}
	tree, _ := r.Objects.Write(objects.Tree, nil)
	for _, rev := range []string{"nope", "abc", "0000000", tree} {
		if _, err := r.Resolve(rev); err == nil {
			t.Errorf("Resolve(%q) succeeded", rev)
		}
	}
}

// Oracle: git rev-list walks our history in the same order, including a
// merge and commits with the same date.
func TestLogOrderMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	r := newRepo(t, nil)
	base := int64(1794621600)
	root := commitAt(t, r, "root", base)
	a1 := commitAt(t, r, "a1", base+10, root)
	b1 := commitAt(t, r, "b1", base+20, root)
	a2 := commitAt(t, r, "a2", base+30, a1)
	b2 := commitAt(t, r, "b2", base+30, b1) // same date as a2
	merge := commitAt(t, r, "merge", base+40, a2, b2)
	top := commitAt(t, r, "top", base+50, merge)
	setBranch(t, r, "main", top)
	setBranch(t, r, "side", b2)

	for _, rev := range []string{"main", "side", merge, a2} {
		cmd := exec.Command(git, "--git-dir=.kirch", "rev-list", rev)
		cmd.Dir = r.Root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git rev-list %s: %v\n%s", rev, err, out)
		}
		want := strings.Fields(string(out))
		got := logIDs(t, r, rev, 0)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("log %s:\n got %v\nwant %v", rev, got, want)
		}
	}
}
