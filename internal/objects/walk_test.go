package objects

import (
	"errors"
	"os"
	"testing"
	"time"
)

// graph builds small histories straight in a store.
type graph struct {
	t *testing.T
	s *Store
	n int
}

func newGraph(t *testing.T) *graph {
	return &graph{t: t, s: NewStore(t.TempDir())}
}

func (g *graph) write(typ Type, data []byte) string {
	g.t.Helper()
	id, err := g.s.Write(typ, data)
	if err != nil {
		g.t.Fatal(err)
	}
	return id
}

// commit stores files (name → content, one folder level via "dir/name") as a
// commit with the given parents.
func (g *graph) commit(files map[string]string, parents ...string) string {
	g.t.Helper()
	top := map[string][]TreeEntry{}
	var root []TreeEntry
	for name, content := range files {
		blob := g.write(Blob, []byte(content))
		if dir, base, ok := cut(name); ok {
			top[dir] = append(top[dir], TreeEntry{Mode: ModeFile, Name: base, ID: blob})
		} else {
			root = append(root, TreeEntry{Mode: ModeFile, Name: name, ID: blob})
		}
	}
	for dir, entries := range top {
		data, err := EncodeTree(entries)
		if err != nil {
			g.t.Fatal(err)
		}
		root = append(root, TreeEntry{Mode: ModeDir, Name: dir, ID: g.write(Tree, data)})
	}
	data, err := EncodeTree(root)
	if err != nil {
		g.t.Fatal(err)
	}
	tree := g.write(Tree, data)
	g.n++
	who := Signature{Name: "T", Email: "t@example.com", When: time.Unix(1700000000+int64(g.n), 0).UTC()}
	c, err := EncodeCommit(CommitData{Tree: tree, Parents: parents, Author: who, Committer: who, Message: "c\n"})
	if err != nil {
		g.t.Fatal(err)
	}
	return g.write(Commit, c)
}

func cut(name string) (string, string, bool) {
	for i := range name {
		if name[i] == '/' {
			return name[:i], name[i+1:], true
		}
	}
	return "", "", false
}

// copyIDs stores ids from src into dst in the given order, failing if any
// object arrives before something it points to.
func copyIDs(t *testing.T, src, dst *Store, ids []string) {
	t.Helper()
	for _, id := range ids {
		typ, data, err := src.Read(id)
		if err != nil {
			t.Fatal(err)
		}
		kids, err := src.children(id, typ, data)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range kids {
			if !dst.Has(k) {
				t.Fatalf("%s %s sent before %s it points to", typ, id[:7], k[:7])
			}
		}
		if _, err := dst.Write(typ, data); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMissingSendsEverythingInSafeOrder(t *testing.T) {
	g := newGraph(t)
	c1 := g.commit(map[string]string{"netlist.json": "a", "fw/main.cpp": "x"})
	c2 := g.commit(map[string]string{"netlist.json": "b", "fw/main.cpp": "x"}, c1)

	ids, err := g.s.Missing([]string{c2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 2 commits, 2 root trees, 1 fw tree (same both times), blobs a, b, x.
	if len(ids) != 8 {
		t.Fatalf("got %d objects, want 8", len(ids))
	}
	dst := NewStore(t.TempDir())
	copyIDs(t, g.s, dst, ids)
	if err := dst.CheckComplete(c2, nil); err != nil {
		t.Fatal(err)
	}
	if ids[len(ids)-1] != c2 {
		t.Errorf("newest commit should come last")
	}
}

func TestMissingSkipsWhatTheOtherSideHas(t *testing.T) {
	g := newGraph(t)
	c1 := g.commit(map[string]string{"netlist.json": "a", "fw/main.cpp": "x"})
	c2 := g.commit(map[string]string{"netlist.json": "b", "fw/main.cpp": "x"}, c1)

	have, err := g.s.HaveSet([]string{c1})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := g.s.Missing([]string{c2}, have)
	if err != nil {
		t.Fatal(err)
	}
	// Only the new commit, its root tree and blob "b". The fw tree and blob
	// "x" are unchanged, so they're in c1's snapshot.
	if len(ids) != 3 {
		t.Fatalf("got %d objects, want 3: %v", len(ids), ids)
	}
	dst := NewStore(t.TempDir())
	all, _ := g.s.Missing([]string{c1}, nil)
	copyIDs(t, g.s, dst, all)
	copyIDs(t, g.s, dst, ids)
	if err := dst.CheckComplete(c2, nil); err != nil {
		t.Fatal(err)
	}
}

func TestHaveSetIgnoresUnknownTips(t *testing.T) {
	g := newGraph(t)
	c1 := g.commit(map[string]string{"a": "1"})
	have, err := g.s.HaveSet([]string{"0123456789012345678901234567890123456789", "not-an-id", c1})
	if err != nil {
		t.Fatal(err)
	}
	if !have[c1] {
		t.Error("known tip missing from have set")
	}
}

func TestMissingMergeHistory(t *testing.T) {
	g := newGraph(t)
	base := g.commit(map[string]string{"a": "1"})
	left := g.commit(map[string]string{"a": "2"}, base)
	right := g.commit(map[string]string{"a": "1", "b": "3"}, base)
	merge := g.commit(map[string]string{"a": "2", "b": "3"}, left, right)

	ids, err := g.s.Missing([]string{merge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, id := range ids {
		seen[id]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%s listed %d times", id[:7], n)
		}
	}
	dst := NewStore(t.TempDir())
	copyIDs(t, g.s, dst, ids)
	if err := dst.CheckComplete(merge, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCheckCompleteFindsMissingObject(t *testing.T) {
	g := newGraph(t)
	c1 := g.commit(map[string]string{"netlist.json": "a"})
	c2 := g.commit(map[string]string{"netlist.json": "b"}, c1)

	// Delete blob "b" from the store.
	blob := Hash(Blob, []byte("b"))
	path := g.s.path(blob)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := g.s.CheckComplete(c2, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	// Trusting c2 itself skips the walk.
	if err := g.s.CheckComplete(c2, map[string]bool{c2: true}); err != nil {
		t.Fatalf("known tip should be trusted: %v", err)
	}
	// A blob isn't a commit.
	if err := g.s.CheckComplete(Hash(Blob, []byte("a")), nil); err == nil {
		t.Fatal("want error for non-commit tip")
	}
}
