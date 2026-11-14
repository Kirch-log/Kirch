package repo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kirch-log/kirch/internal/objects"
)

var testAuthor = objects.Signature{
	Name:  "Test Author",
	Email: "test@example.com",
	When:  time.Unix(1794621600, 0).In(time.FixedZone("", -5*3600)),
}

func addAll(t *testing.T, r *Repo) {
	t.Helper()
	if _, err := r.Add([]string{"."}); err != nil {
		t.Fatal(err)
	}
}

func TestFirstCommit(t *testing.T) {
	r := newRepo(t, map[string]string{"netlist.json": netlist, "firmware/main.cpp": "x\n"})
	addAll(t, r)
	res, err := r.Commit("First circuit", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "main" || res.Parent != "" {
		t.Fatalf("result = %+v", res)
	}
	if _, head, _ := r.Refs().Head(); head != res.ID {
		t.Fatalf("main = %s, want %s", head, res.ID)
	}
	c, err := r.ReadCommit(res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tree != res.Tree || len(c.Parents) != 0 || c.Message != "First circuit\n" || c.Author.Name != "Test Author" {
		t.Fatalf("commit = %+v", c)
	}
}

func TestSecondCommitHasParent(t *testing.T) {
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
	if second.Parent != first.ID {
		t.Fatalf("parent = %s, want %s", second.Parent, first.ID)
	}
	c, _ := r.ReadCommit(second.ID)
	if len(c.Parents) != 1 || c.Parents[0] != first.ID {
		t.Fatalf("parents = %v", c.Parents)
	}
}

func TestCommitErrors(t *testing.T) {
	r := newRepo(t, map[string]string{"a.txt": "1"})
	if _, err := r.Commit("nothing staged", testAuthor); err == nil {
		t.Fatal("committed an empty index")
	}
	addAll(t, r)
	if _, err := r.Commit("   ", testAuthor); err == nil {
		t.Fatal("committed with empty message")
	}
	if _, err := r.Commit("ok", testAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit("again", testAuthor); !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("err = %v, want ErrNothingToCommit", err)
	}
	bad := testAuthor
	bad.Email = ""
	writeFile(t, filepath.Join(r.Root, "a.txt"), "2")
	addAll(t, r)
	if _, err := r.Commit("no email", bad); err == nil {
		t.Fatal("committed without author email")
	}
}

// Oracle: git builds the same tree and commit ids from the same files,
// and git log / git fsck accept our repository.
func TestCommitMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	r := newRepo(t, map[string]string{
		"netlist.json":          netlist,
		"parts.json":            `{"format":"kirch.parts/1","parts":{"U1":{"type":"mcu"}}}`,
		"firmware/src/main.cpp": "void setup(){}\n",
		"firmware/a.txt":        "a\n",
		"firmware.txt":          "sorts before firmware/\n",
	})
	addAll(t, r)
	first, err := r.Commit("First circuit", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(r.Root, "firmware", "a.txt"), "changed\n")
	addAll(t, r)
	second, err := r.Commit("Change a.txt", testAuthor)
	if err != nil {
		t.Fatal(err)
	}

	gitEnv := append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME="+testAuthor.Name, "GIT_AUTHOR_EMAIL="+testAuthor.Email,
		"GIT_COMMITTER_NAME="+testAuthor.Name, "GIT_COMMITTER_EMAIL="+testAuthor.Email,
		fmt.Sprintf("GIT_AUTHOR_DATE=%d -0500", testAuthor.When.Unix()),
		fmt.Sprintf("GIT_COMMITTER_DATE=%d -0500", testAuthor.When.Unix()),
		// Lets the separate .git repo below see kirch's parent commit.
		"GIT_ALTERNATE_OBJECT_DIRECTORIES="+filepath.Join(r.Root, ".kirch", "objects"),
	)
	run := func(args ...string) string {
		cmd := exec.Command(git, args...)
		cmd.Dir = r.Root
		cmd.Env = gitEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	// git reads our .kirch folder directly.
	log := run("--git-dir=.kirch", "log", "--format=%H %P %s")
	wantLog := second.ID + " " + first.ID + " Change a.txt\n" + first.ID + "  First circuit"
	if log != wantLog {
		t.Fatalf("git log:\n%s\nwant:\n%s", log, wantLog)
	}
	run("--git-dir=.kirch", "fsck", "--strict", "--no-dangling")

	// git computes the same tree and commit from the same files.
	run("init", "-q")
	run("-c", "core.autocrlf=false", "add", ".", ":!.kirch")
	if tree := run("write-tree"); tree != second.Tree {
		t.Fatalf("git write-tree = %s, kirch = %s", tree, second.Tree)
	}
	if id := run("commit-tree", second.Tree, "-p", first.ID, "-m", "Change a.txt"); id != second.ID {
		t.Fatalf("git commit-tree = %s, kirch = %s", id, second.ID)
	}
}
