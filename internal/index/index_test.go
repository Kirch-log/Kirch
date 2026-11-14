package index

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingIsEmpty(t *testing.T) {
	idx, err := Load(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Entries) != 0 {
		t.Fatalf("entries = %v", idx.Entries)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "index")
	idx := New()
	idx.Entries["netlist.json"] = Entry{Hash: "ce013625030ba8dba906f756967f9e9ca394464a", Mode: ModeFile, Size: 6, MtimeNs: 123}
	idx.Entries["firmware/main.cpp"] = Entry{Hash: "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391", Mode: ModeFile}
	if err := idx.Save(file); err != nil {
		t.Fatal(err)
	}
	got, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, idx) {
		t.Fatalf("got %+v, want %+v", got, idx)
	}
	if p := got.Paths(); !reflect.DeepEqual(p, []string{"firmware/main.cpp", "netlist.json"}) {
		t.Fatalf("Paths = %v", p)
	}
}

func TestLoadRejectsDamage(t *testing.T) {
	for name, content := range map[string]string{
		"not json":    "{",
		"bad version": `{"version": 99, "entries": {}}`,
		"escape path": `{"version": 1, "entries": {"../x": {"hash": "a"}}}`,
		"kirch path":  `{"version": 1, "entries": {".kirch/HEAD": {"hash": "a"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "index")
			if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(file); err == nil {
				t.Fatal("Load succeeded")
			}
		})
	}
}

func TestCheckPath(t *testing.T) {
	good := []string{"a", "netlist.json", "firmware/src/main.cpp", ".gitignore", "a..b"}
	bad := []string{"", ".", "/abs", "a//b", "a/./b", "../up", "a/..", `a\b`, ".kirch/x", ".git/config", "sub/.GIT/x"}
	for _, p := range good {
		if err := CheckPath(p); err != nil {
			t.Errorf("CheckPath(%q) = %v", p, err)
		}
	}
	for _, p := range bad {
		if CheckPath(p) == nil {
			t.Errorf("CheckPath(%q) accepted", p)
		}
	}
}

func TestEqualIgnoresMtime(t *testing.T) {
	a, b := New(), New()
	a.Entries["x"] = Entry{Hash: "h", Mode: ModeFile, MtimeNs: 1}
	b.Entries["x"] = Entry{Hash: "h", Mode: ModeFile, MtimeNs: 2}
	if !Equal(a, b) {
		t.Fatal("not equal")
	}
	b.Entries["x"] = Entry{Hash: "other", Mode: ModeFile}
	if Equal(a, b) {
		t.Fatal("equal with different hash")
	}
}
