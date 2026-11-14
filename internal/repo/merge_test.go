package repo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const net3 = `{"format":"kirch.netlist/1","nets":{
"A_NET":["U1.GPIO1","R1.1"],
"B_NET":["U1.GPIO2","R2.1"],
"C_NET":["U1.GPIO3","R3.1"],
"D_NET":["U1.GPIO4","R4.1"],
"E_NET":["U1.GPIO5","R5.1"]}}`

// forked: main and side both grew from a common commit.
func forked(t *testing.T) (r *Repo, base string) {
	t.Helper()
	text := "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n"
	r = newRepo(t, map[string]string{"firmware/main.cpp": text, "netlist.json": net3, "old.txt": "o\n"})
	base = commitAll(t, r, "base")
	if _, err := r.CreateBranch("side", ""); err != nil {
		t.Fatal(err)
	}
	return r, base
}

func mustMerge(t *testing.T, r *Repo, rev string) *MergeResult {
	t.Helper()
	res, err := r.Merge(rev, testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestMergeUpToDateAndFastForward(t *testing.T) {
	r, base := forked(t)
	if res := mustMerge(t, r, "side"); !res.UpToDate {
		t.Fatalf("res = %+v", res)
	}
	r.Checkout("side")
	writeFile(t, filepath.Join(r.Root, "new.txt"), "n\n")
	tip := commitAll(t, r, "side work")
	r.Checkout("main")
	res := mustMerge(t, r, "side")
	if !res.FastForward || res.From != base || res.To != tip || branchID(t, r) != tip {
		t.Fatalf("res = %+v", res)
	}
	if readFile(t, r, "new.txt") != "n\n" {
		t.Fatal("new.txt not written")
	}
	wantStatus(t, r)
}

func TestMergeRefusesDirty(t *testing.T) {
	r, _ := forked(t)
	writeFile(t, filepath.Join(r.Root, "old.txt"), "dirty\n")
	if _, err := r.Merge("side", testAuthor); !errors.Is(err, ErrDirty) {
		t.Fatalf("err = %v", err)
	}
}

// Each side rewires a different net: merges on its own.
func TestMergeNetlistDifferentNets(t *testing.T) {
	r, _ := forked(t)
	writeFile(t, filepath.Join(r.Root, "netlist.json"), strings.Replace(net3, `"U1.GPIO1"`, `"U1.GPIO11"`, 1))
	commitAll(t, r, "move A")
	r.Checkout("side")
	writeFile(t, filepath.Join(r.Root, "netlist.json"), strings.Replace(net3, `"R2.1"`, `"R2.1","R9.1"`, 1))
	commitAll(t, r, "add R9 to B")
	r.Checkout("main")
	res := mustMerge(t, r, "side")
	if len(res.Conflicts) != 0 || res.Commit == nil {
		t.Fatalf("res = %+v", res)
	}
	got := readFile(t, r, "netlist.json")
	if !strings.Contains(got, `"U1.GPIO11"`) || !strings.Contains(got, `"R9.1"`) || strings.Contains(got, `"U1.GPIO1"`) {
		t.Fatalf("netlist.json:\n%s", got)
	}
	c, _ := r.ReadCommit(res.Commit.ID)
	if len(c.Parents) != 2 || c.Message != "Merge branch 'side'\n" {
		t.Fatalf("commit = %+v", c)
	}
	wantStatus(t, r)
}

func TestMergeTextConflictThenResolve(t *testing.T) {
	r, _ := forked(t)
	writeFile(t, filepath.Join(r.Root, "firmware", "main.cpp"), "l1\nOURS\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n")
	ours := commitAll(t, r, "ours")
	r.Checkout("side")
	writeFile(t, filepath.Join(r.Root, "firmware", "main.cpp"), "l1\nTHEIRS\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n")
	theirs := commitAll(t, r, "theirs")
	r.Checkout("main")

	res := mustMerge(t, r, "side")
	if len(res.Conflicts) != 1 || res.Conflicts[0].Path != "firmware/main.cpp" || res.Conflicts[0].Kind != "content" {
		t.Fatalf("res = %+v", res)
	}
	want := "l1\n<<<<<<< HEAD\nOURS\n=======\nTHEIRS\n>>>>>>> side\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n"
	if got := readFile(t, r, "firmware/main.cpp"); got != want {
		t.Fatalf("file:\n%s", got)
	}
	if branchID(t, r) != ours {
		t.Fatal("branch moved before the conflict was resolved")
	}
	if _, err := r.Commit("", testAuthor); err == nil || !strings.Contains(err.Error(), "firmware/main.cpp") {
		t.Fatalf("commit with conflicts: %v", err)
	}
	if err := r.Checkout("side"); !errors.Is(err, ErrMerging) {
		t.Fatalf("checkout during merge: %v", err)
	}
	if _, err := r.Merge("side", testAuthor); !errors.Is(err, ErrMerging) {
		t.Fatalf("second merge: %v", err)
	}

	writeFile(t, filepath.Join(r.Root, "firmware", "main.cpp"), "l1\nBOTH\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n")
	addAll(t, r)
	done, err := r.Commit("", testAuthor) // empty = default merge message
	if err != nil {
		t.Fatal(err)
	}
	c, _ := r.ReadCommit(done.ID)
	if len(c.Parents) != 2 || c.Parents[0] != ours || c.Parents[1] != theirs || c.Message != "Merge branch 'side'\n" {
		t.Fatalf("merge commit = %+v", c)
	}
	if m, _ := r.Merging(); m != nil {
		t.Fatal("merge state left behind")
	}
	wantStatus(t, r)
}

func TestMergeWiringConflict(t *testing.T) {
	r, _ := forked(t)
	// ours: U1.GPIO1 moved from A_NET to B_NET
	writeFile(t, filepath.Join(r.Root, "netlist.json"), strings.Replace(strings.Replace(net3, `"U1.GPIO1",`, ``, 1), `"U1.GPIO2"`, `"U1.GPIO2","U1.GPIO1"`, 1))
	commitAll(t, r, "GPIO1 to B")
	r.Checkout("side")
	writeFile(t, filepath.Join(r.Root, "netlist.json"), strings.Replace(strings.Replace(net3, `"U1.GPIO1",`, ``, 1), `"U1.GPIO3"`, `"U1.GPIO3","U1.GPIO1"`, 1))
	commitAll(t, r, "GPIO1 to C")
	r.Checkout("main")

	res := mustMerge(t, r, "side")
	if len(res.Conflicts) != 1 || res.Conflicts[0].Kind != "wiring" || len(res.Conflicts[0].Details) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if d := res.Conflicts[0].Details[0]; d != "pin U1.GPIO1: ours on B_NET, theirs on C_NET" {
		t.Fatalf("conflict = %q", d)
	}
	if _, err := r.Add([]string{"netlist.json"}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("add with conflicts left in: %v", err)
	}
	if err := r.MergeAbort(); err != nil {
		t.Fatal(err)
	}
	if m, _ := r.Merging(); m != nil {
		t.Fatal("abort left merge state")
	}
	wantStatus(t, r)
	if strings.Contains(readFile(t, r, "netlist.json"), "conflicts") {
		t.Fatal("abort didn't restore netlist.json")
	}
}

func TestMergeDeleteVsModify(t *testing.T) {
	r, _ := forked(t)
	os.Remove(filepath.Join(r.Root, "old.txt"))
	commitAll(t, r, "delete old")
	r.Checkout("side")
	writeFile(t, filepath.Join(r.Root, "old.txt"), "changed\n")
	commitAll(t, r, "change old")
	r.Checkout("main")
	res := mustMerge(t, r, "side")
	if len(res.Conflicts) != 1 || res.Conflicts[0].Kind != "modify/delete" {
		t.Fatalf("res = %+v", res)
	}
	if readFile(t, r, "old.txt") != "changed\n" {
		t.Fatal("their changed file should be left on disk")
	}
}

// Oracle: a clean merge makes the same merge commit (same id) as git merge,
// and git sees MERGE_HEAD while a conflicted merge is unfinished.
func TestMergeMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	r, _ := forked(t)
	writeFile(t, filepath.Join(r.Root, "firmware", "main.cpp"), "l1\nOURS\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n")
	writeFile(t, filepath.Join(r.Root, "netlist.json"), strings.Replace(net3, `"U1.GPIO1"`, `"U1.GPIO11"`, 1))
	writeFile(t, filepath.Join(r.Root, "docs", "ours.md"), "o\n")
	commitAll(t, r, "ours")
	r.Checkout("side")
	writeFile(t, filepath.Join(r.Root, "firmware", "main.cpp"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nTHEIRS\n")
	writeFile(t, filepath.Join(r.Root, "netlist.json"), strings.Replace(net3, `"R5.1"`, `"R5.1","R9.1"`, 1))
	os.Remove(filepath.Join(r.Root, "old.txt"))
	writeFile(t, filepath.Join(r.Root, "theirs.txt"), "t\n")
	commitAll(t, r, "theirs")
	r.Checkout("main")

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
	run(gitDir, "merge", "-q", "--no-edit", "side")
	res := mustMerge(t, r, "side")
	if theirs := run(gitDir, "rev-parse", "HEAD"); res.To != theirs {
		t.Fatalf("merge: ours %s, git %s\ngit tree:\n%s", res.To, theirs, run(gitDir, "ls-tree", "-r", "HEAD"))
	}
	wantStatus(t, r)
	run(r.Dir, "fsck", "--strict", "--no-dangling")

	// A conflicted merge: git sees it as in progress.
	r.CreateBranch("other", "")
	writeFile(t, filepath.Join(r.Root, "theirs.txt"), "main\n")
	commitAll(t, r, "main edit")
	r.Checkout("other")
	writeFile(t, filepath.Join(r.Root, "theirs.txt"), "other\n")
	commitAll(t, r, "other edit")
	r.Checkout("main")
	if res := mustMerge(t, r, "other"); len(res.Conflicts) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if got := run(r.Dir, "rev-parse", "MERGE_HEAD"); got != branchOf(t, r, "other") {
		t.Fatalf("git MERGE_HEAD = %s", got)
	}
}

func branchOf(t *testing.T, r *Repo, name string) string {
	t.Helper()
	id, err := r.Refs().Branch(name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
