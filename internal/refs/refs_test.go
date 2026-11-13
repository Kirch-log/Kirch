package refs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const (
	idA = "ce013625030ba8dba906f756967f9e9ca394464a"
	idB = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "refs", "heads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return New(dir)
}

func TestHeadOnEmptyRepo(t *testing.T) {
	s := newStore(t)
	branch, id, err := s.Head()
	if err != nil || branch != "main" || id != "" {
		t.Fatalf("Head = %q %q %v", branch, id, err)
	}
}

func TestUpdateAndRead(t *testing.T) {
	s := newStore(t)
	if err := s.UpdateBranch("main", idA, ""); err != nil {
		t.Fatal(err)
	}
	if _, id, _ := s.Head(); id != idA {
		t.Fatalf("Head id = %s", id)
	}
	if err := s.UpdateBranch("main", idB, idA); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.Branch("main"); id != idB {
		t.Fatalf("Branch = %s", id)
	}
	data, _ := os.ReadFile(filepath.Join(s.Dir, "refs", "heads", "main"))
	if string(data) != idB+"\n" {
		t.Fatalf("file = %q", data)
	}
}

func TestStaleUpdateFails(t *testing.T) {
	s := newStore(t)
	if err := s.UpdateBranch("main", idA, ""); err != nil {
		t.Fatal(err)
	}
	// Someone thinks the branch is still empty.
	if err := s.UpdateBranch("main", idB, ""); !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	if id, _ := s.Branch("main"); id != idA {
		t.Fatal("stale update changed the branch")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "refs", "heads", "main.lock")); !os.IsNotExist(err) {
		t.Fatal("lock file left behind")
	}
}

func TestLockedBranch(t *testing.T) {
	s := newStore(t)
	lock := filepath.Join(s.Dir, "refs", "heads", "main.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateBranch("main", idA, ""); !errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("someone else's lock was removed")
	}
}

func TestNestedBranchName(t *testing.T) {
	s := newStore(t)
	if err := s.UpdateBranch("firmware/wifi", idA, ""); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.Branch("firmware/wifi"); id != idA {
		t.Fatalf("Branch = %s", id)
	}
}

func TestDamagedRef(t *testing.T) {
	s := newStore(t)
	os.WriteFile(filepath.Join(s.Dir, "refs", "heads", "main"), []byte("garbage\n"), 0o644)
	if _, _, err := s.Head(); err == nil {
		t.Fatal("damaged ref accepted")
	}
}

func TestDetachedHeadNotSupported(t *testing.T) {
	s := newStore(t)
	os.WriteFile(filepath.Join(s.Dir, "HEAD"), []byte(idA+"\n"), 0o644)
	if _, _, err := s.Head(); err == nil {
		t.Fatal("detached HEAD accepted")
	}
}

func TestBranchNames(t *testing.T) {
	good := []string{"main", "firmware/wifi-probe", "hridhayesh", "v1.2", "fix_42"}
	bad := []string{"", "HEAD", "-x", "a..b", "a b", "a~1", "a^", "a:b", "a?", "a*", "a[", `a\b`, "a.lock", "a.", ".hidden", "a/.b", "/a", "a/", "a//b", "a@{1}"}
	for _, n := range good {
		if err := CheckBranchName(n); err != nil {
			t.Errorf("%q rejected: %v", n, err)
		}
	}
	for _, n := range bad {
		if CheckBranchName(n) == nil {
			t.Errorf("%q accepted", n)
		}
	}
}
