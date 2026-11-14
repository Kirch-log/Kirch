package repo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// commitIn stages everything in r (by absolute path, so the current folder
// doesn't matter) and commits.
func commitIn(t *testing.T, r *Repo, msg string) string {
	t.Helper()
	if _, err := r.Add([]string{r.Root}); err != nil {
		t.Fatal(err)
	}
	res, err := r.Commit(msg, testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	return res.ID
}

func put(t *testing.T, r *Repo, rel, content string) {
	t.Helper()
	writeFile(t, filepath.Join(r.Root, filepath.FromSlash(rel)), content)
}

func tipOf(t *testing.T, r *Repo, branch string) string {
	t.Helper()
	id, err := r.Refs().Branch(branch)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// shared makes the "shared folder" repository with one commit, and a clone.
func shared(t *testing.T) (origin, clone *Repo) {
	t.Helper()
	origin = newRepo(t, map[string]string{"netlist.json": netlist, "firmware/main.cpp": "v1\n"})
	commitIn(t, origin, "first")
	clone, err := Clone(origin.Root, filepath.Join(t.TempDir(), "clone"))
	if err != nil {
		t.Fatal(err)
	}
	return origin, clone
}

func TestClone(t *testing.T) {
	origin, clone := shared(t)
	main := tipOf(t, origin, "main")
	if tipOf(t, clone, "main") != main || headBranch(t, clone) != "main" {
		t.Fatal("clone's main doesn't match origin")
	}
	if id, _ := clone.Refs().RemoteBranch("origin", "main"); id != main {
		t.Fatalf("origin/main = %s", id)
	}
	if got := readFile(t, clone, "firmware/main.cpp"); got != "v1\n" {
		t.Fatalf("main.cpp = %q", got)
	}
	if id, err := clone.Resolve("origin/main"); err != nil || id != main {
		t.Fatalf("Resolve(origin/main) = %s, %v", id, err)
	}
	st, _ := clone.Status()
	if !st.Clean() {
		t.Fatalf("clone not clean: %+v", st)
	}
	if _, err := Clone(origin.Root, clone.Root); err == nil {
		t.Fatal("cloning into a non-empty folder must fail")
	}
}

func TestCloneEmpty(t *testing.T) {
	origin := newRepo(t, nil)
	clone, err := Clone(origin.Root, filepath.Join(t.TempDir(), "c"))
	if err != nil {
		t.Fatal(err)
	}
	if headBranch(t, clone) != "main" || tipOf(t, clone, "main") != "" {
		t.Fatal("empty clone should be on an empty main")
	}
}

func TestPushUpdatesCheckedOutRemote(t *testing.T) {
	origin, clone := shared(t)
	put(t, clone, "firmware/main.cpp", "v2\n")
	tip := commitIn(t, clone, "v2")
	up, err := clone.Push("origin", "")
	if err != nil {
		t.Fatal(err)
	}
	if up.New != tip || up.Old == "" || tipOf(t, origin, "main") != tip {
		t.Fatalf("push = %+v", up)
	}
	// origin has main checked out and was clean: its files follow.
	if got := readFile(t, origin, "firmware/main.cpp"); got != "v2\n" {
		t.Fatalf("origin main.cpp = %q", got)
	}
	st, _ := origin.Status()
	if !st.Clean() {
		t.Fatalf("origin not clean after push: %+v", st)
	}
	if up, err := clone.Push("origin", "main"); err != nil || up.Old != up.New {
		t.Fatalf("second push = %+v, %v", up, err)
	}
}

func TestPushRefusesDirtyRemote(t *testing.T) {
	origin, clone := shared(t)
	before := tipOf(t, origin, "main")
	put(t, origin, "firmware/main.cpp", "someone's work\n")
	put(t, clone, "notes.txt", "n\n")
	commitIn(t, clone, "notes")
	if _, err := clone.Push("origin", ""); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("err = %v", err)
	}
	if tipOf(t, origin, "main") != before || readFile(t, origin, "firmware/main.cpp") != "someone's work\n" {
		t.Fatal("refused push changed the remote")
	}
}

func TestPushNewBranchAndFetch(t *testing.T) {
	origin, clone := shared(t)
	if _, err := clone.CreateBranch("probe-fix", ""); err != nil {
		t.Fatal(err)
	}
	if err := clone.Checkout("probe-fix"); err != nil {
		t.Fatal(err)
	}
	put(t, clone, "capture.json", `{"format":"kirch.capture/1"}`)
	tip := commitIn(t, clone, "capture")
	up, err := clone.Push("origin", "probe-fix")
	if err != nil || up.Old != "" || tipOf(t, origin, "probe-fix") != tip {
		t.Fatalf("push = %+v, %v", up, err)
	}
	// origin is on main, so its files are untouched.
	if _, err := os.Stat(filepath.Join(origin.Root, "capture.json")); !os.IsNotExist(err) {
		t.Fatal("pushing another branch changed origin's files")
	}

	// A second clone sees the new branch after fetch; deleting it on origin
	// removes it on the next fetch.
	other, err := Clone(origin.Root, filepath.Join(t.TempDir(), "other"))
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := other.Refs().RemoteBranch("origin", "probe-fix"); id != tip {
		t.Fatal("clone missing origin/probe-fix")
	}
	if _, err := origin.DeleteBranch("probe-fix", true); err != nil {
		t.Fatal(err)
	}
	ups, err := other.Fetch("origin")
	if err != nil || len(ups) != 1 || ups[0].Branch != "probe-fix" || ups[0].New != "" {
		t.Fatalf("fetch = %+v, %v", ups, err)
	}
}

func TestPushRejectedThenPullMerges(t *testing.T) {
	origin, alice := shared(t)
	bob, err := Clone(origin.Root, filepath.Join(t.TempDir(), "bob"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, alice, "firmware/main.cpp", "alice\n")
	aliceTip := commitIn(t, alice, "alice")
	if _, err := alice.Push("origin", ""); err != nil {
		t.Fatal(err)
	}

	put(t, bob, "parts.json", `{"format":"kirch.parts/1","parts":{"R1":{"type":"resistor"}}}`)
	bobTip := commitIn(t, bob, "bob")
	if _, err := bob.Push("origin", ""); !errors.Is(err, ErrNonFastForward) {
		t.Fatalf("push over alice's commit: %v", err)
	}
	if tipOf(t, origin, "main") != aliceTip {
		t.Fatal("rejected push moved origin")
	}

	t.Chdir(bob.Root)
	_, res, err := bob.Pull("origin", "", testAuthor)
	if err != nil || res.Commit == nil || len(res.Conflicts) != 0 {
		t.Fatalf("pull = %+v, %v", res, err)
	}
	c, _ := bob.ReadCommit(res.Commit.ID)
	wantMsg := "Merge branch 'main' of " + filepath.ToSlash(origin.Root) + "\n"
	if len(c.Parents) != 2 || c.Parents[0] != bobTip || c.Parents[1] != aliceTip || c.Message != wantMsg {
		t.Fatalf("merge commit = %+v", c)
	}
	if readFile(t, bob, "firmware/main.cpp") != "alice\n" {
		t.Fatal("pull didn't bring alice's change")
	}
	if _, err := bob.Push("origin", ""); err != nil {
		t.Fatal(err)
	}

	// alice pulls: a fast-forward to bob's merge.
	_, res, err = alice.Pull("origin", "", testAuthor)
	if err != nil || !res.FastForward || tipOf(t, alice, "main") != tipOf(t, origin, "main") {
		t.Fatalf("alice pull = %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(alice.Root, "parts.json")); err != nil {
		t.Fatal("alice didn't get parts.json")
	}
}

func TestRemoteErrors(t *testing.T) {
	r := newRepo(t, nil)
	if _, _, err := r.OpenRemote("origin"); !errors.Is(err, ErrNoRemote) {
		t.Fatalf("err = %v", err)
	}
	if err := r.AddRemote("origin", t.TempDir()); err == nil {
		t.Fatal("a folder without a repository can't be a remote")
	}
	if err := r.AddRemote("a/b", r.Root); err == nil {
		t.Fatal("bad remote name accepted")
	}
}

// Oracle: after clone, push and fetch, git accepts every repository
// (fsck --strict) and sees the same branches and remote-tracking branches.
func TestRemotesMatchGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	origin, clone := shared(t)
	put(t, clone, "firmware/main.cpp", "v2\n")
	commitIn(t, clone, "v2")
	if _, err := clone.Push("origin", ""); err != nil {
		t.Fatal(err)
	}
	run := func(r *Repo, args ...string) string {
		cmd := exec.Command(git, append([]string{"--git-dir=" + r.Dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	for _, r := range []*Repo{origin, clone} {
		run(r, "fsck", "--strict", "--no-dangling")
	}
	if got, want := run(clone, "rev-parse", "origin/main"), tipOf(t, origin, "main"); got != want {
		t.Fatalf("git origin/main = %s, want %s", got, want)
	}
	// git can clone what kirch pushed, and gets the same files.
	dest := filepath.Join(t.TempDir(), "gitclone")
	cmd := exec.Command(git, "clone", "-q", "--no-hardlinks", origin.Dir, dest)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	var files []string
	filepath.WalkDir(dest, func(p string, d os.DirEntry, err error) error {
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dest, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)
	if strings.Join(files, ",") != "firmware/main.cpp,netlist.json" {
		t.Fatalf("git clone files = %v", files)
	}
	if data, _ := os.ReadFile(filepath.Join(dest, "firmware", "main.cpp")); string(data) != "v2\n" {
		t.Fatalf("git clone main.cpp = %q", data)
	}
}

func TestAddRemoteURL(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"kirch://host", "kirch://host/a/../b", "kirch://host/A/b"} {
		if err := r.AddRemote("x", bad); err == nil {
			t.Errorf("%s: want error", bad)
		}
	}
	if err := r.AddRemote("origin", "kirch://example.com:7000/evin/board/"); err != nil {
		t.Fatal(err)
	}
	loc, err := r.RemoteLocation("origin")
	if err != nil || loc != "kirch://example.com:7000/evin/board" {
		t.Fatalf("got %q, %v", loc, err)
	}
	if _, _, err := r.OpenRemote("origin"); err == nil {
		t.Fatal("OpenRemote on a server URL should fail")
	}
	// Unreachable server: a clear error, no panic.
	if err := r.AddRemote("dead", "kirch://127.0.0.1:1/o/r"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Fetch("dead"); err == nil || !strings.Contains(err.Error(), "can't reach") {
		t.Fatalf("got %v", err)
	}
}
