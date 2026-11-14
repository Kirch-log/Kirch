package repo

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// statusLines returns "XY path" per tracked file and "?? path" per untracked one.
func statusLines(t *testing.T, r *Repo) []string {
	t.Helper()
	st, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range st.Files {
		out = append(out, string([]byte{f.Staged, f.Unstaged})+" "+f.Path)
	}
	for _, p := range st.Untracked {
		out = append(out, "?? "+p)
	}
	return out
}

func wantStatus(t *testing.T, r *Repo, want ...string) {
	t.Helper()
	if got := statusLines(t, r); !reflect.DeepEqual(got, want) {
		t.Fatalf("status = %q, want %q", got, want)
	}
}

func TestStatusNoCommits(t *testing.T) {
	r := newRepo(t, map[string]string{"a.txt": "a", "b.txt": "b"})
	st, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" || st.Head != "" {
		t.Fatalf("status = %+v", st)
	}
	wantStatus(t, r, "?? a.txt", "?? b.txt")
	if _, err := r.Add([]string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, r, "A  a.txt", "?? b.txt")
}

func TestStatusCleanAfterCommit(t *testing.T) {
	r := newRepo(t, map[string]string{"netlist.json": netlist, "firmware/main.cpp": "x\n"})
	addAll(t, r)
	if _, err := r.Commit("first", testAuthor); err != nil {
		t.Fatal(err)
	}
	st, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Clean() || st.HasStaged() {
		t.Fatalf("status after commit = %+v", st)
	}
}

func TestStatusChanges(t *testing.T) {
	r := newRepo(t, map[string]string{
		"keep.txt": "k", "edit.txt": "e", "staged.txt": "s", "gone.txt": "g", "removed.txt": "r",
	})
	addAll(t, r)
	if _, err := r.Commit("first", testAuthor); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(r.Root, "edit.txt"), "edited")
	writeFile(t, filepath.Join(r.Root, "staged.txt"), "staged change")
	writeFile(t, filepath.Join(r.Root, "new.txt"), "n")
	writeFile(t, filepath.Join(r.Root, "newdir", "sub", "f.txt"), "f")
	os.Remove(filepath.Join(r.Root, "gone.txt"))
	os.Remove(filepath.Join(r.Root, "removed.txt"))
	if _, err := r.Add([]string{"staged.txt", "removed.txt", "new.txt"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(r.Root, "new.txt"), "changed after add")

	wantStatus(t, r,
		" M edit.txt",
		" D gone.txt",
		"AM new.txt",
		"D  removed.txt",
		"M  staged.txt",
		"?? newdir/",
	)
}

func TestStatusSameSizeEditRightAfterAdd(t *testing.T) {
	r := newRepo(t, map[string]string{"a.txt": "aaaa"})
	addAll(t, r)
	// Same size, and likely the same mtime as the index: must still be seen.
	writeFile(t, filepath.Join(r.Root, "a.txt"), "bbbb")
	wantStatus(t, r, "AM a.txt")
}

func TestStatusUntrackedInTrackedFolder(t *testing.T) {
	r := newRepo(t, map[string]string{"firmware/main.cpp": "x"})
	addAll(t, r)
	writeFile(t, filepath.Join(r.Root, "firmware", "extra.cpp"), "y")
	writeFile(t, filepath.Join(r.Root, "firmware", "lib", "a", "b.h"), "z")
	wantStatus(t, r, "A  firmware/main.cpp", "?? firmware/extra.cpp", "?? firmware/lib/")
}

func TestStatusCircuitFilesComparedCanonically(t *testing.T) {
	r := newRepo(t, map[string]string{"netlist.json": netlist})
	addAll(t, r)
	if _, err := r.Commit("first", testAuthor); err != nil {
		t.Fatal(err)
	}
	// Same circuit, different spelling: not a change.
	writeFile(t, filepath.Join(r.Root, "netlist.json"), `{ "nets": { "SERVO_SIG": ["SERVO1.SIG", "U1.GPIO9"] }, "format": "kirch.netlist/1" }`)
	wantStatus(t, r)
	// Broken JSON: a change.
	writeFile(t, filepath.Join(r.Root, "netlist.json"), `{`)
	wantStatus(t, r, " M netlist.json")
}
