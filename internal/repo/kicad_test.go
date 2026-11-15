package repo

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Kirch-log/kirch/internal/kicad"
)

// kicadDemo copies KiCad's ECC83 demo project into a new repo. Skips the
// test when KiCad (and its demos) aren't installed.
func kicadDemo(t *testing.T) *Repo {
	t.Helper()
	if _, err := kicad.FindCLI(); err != nil {
		t.Skip("KiCad not installed")
	}
	if runtime.GOOS != "windows" {
		t.Skip("demo path only known on Windows")
	}
	found, _ := filepath.Glob(`C:\Program Files\KiCad\*\share\kicad\demos\ecc83`)
	if len(found) == 0 {
		t.Skip("KiCad demos not installed")
	}
	src := found[len(found)-1]
	r := newRepo(t, nil)
	hw := filepath.Join(r.Root, "hw")
	for _, name := range []string{"ecc83-pp.kicad_sch", "ecc83-pp_v2.kicad_sch", "ecc83-pp.kicad_pro", "ecc83-pp.kicad_sym", "sym-lib-table"} {
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(hw, name), string(data))
	}
	// Keep the alternative design out of the folder the import looks at.
	if err := os.Rename(filepath.Join(hw, "ecc83-pp_v2.kicad_sch"), filepath.Join(r.Root, "v2.kicad_sch.txt")); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestKiCadAutoReimport(t *testing.T) {
	r := kicadDemo(t)
	res, err := r.ImportKiCad(filepath.Join(r.Root, "hw", "ecc83-pp.kicad_pro"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Schematic != "hw/ecc83-pp.kicad_sch" || len(res.Written) != 2 || res.Parts != 15 {
		t.Fatalf("import = %+v", res)
	}
	if r.KiCadSchematic() != "hw/ecc83-pp.kicad_sch" {
		t.Fatalf("netlist.json doesn't point back at the schematic:\n%s", readFile(t, r, "netlist.json"))
	}
	commitIn(t, r, "import")

	// Unchanged design: nothing to do, KiCad isn't even run.
	t.Setenv("KIRCH_KICAD_CLI", filepath.Join(t.TempDir(), "missing-kicad-cli"))
	if res, err := r.SyncKiCad(); err != nil || res != nil {
		t.Fatalf("sync of unchanged design = %+v, %v", res, err)
	}

	// Change the design (the demo's second version).
	v2, _ := os.ReadFile(filepath.Join(r.Root, "v2.kicad_sch.txt"))
	writeFile(t, filepath.Join(r.Root, "hw", "ecc83-pp.kicad_sch"), string(v2))
	if sch, changed, err := r.KiCadChanged(); err != nil || !changed || sch != "hw/ecc83-pp.kicad_sch" {
		t.Fatalf("KiCadChanged = %s %v %v", sch, changed, err)
	}
	// Without KiCad: a clear error, nothing written.
	before := readFile(t, r, "netlist.json")
	if _, err := r.SyncKiCad(); !errors.Is(err, kicad.ErrNoKiCad) {
		t.Fatalf("sync without KiCad: %v", err)
	}
	if readFile(t, r, "netlist.json") != before {
		t.Fatal("netlist.json changed without KiCad")
	}

	// With KiCad: re-imported, and the wiring change is visible.
	os.Unsetenv("KIRCH_KICAD_CLI")
	res, err = r.SyncKiCad()
	if err != nil || res == nil || len(res.Written) == 0 {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	if readFile(t, r, "netlist.json") == before {
		t.Fatal("netlist.json not updated")
	}
	files, err := r.DiffWorktree("")
	if err != nil {
		t.Fatal(err)
	}
	var changed []string
	for _, f := range files {
		changed = append(changed, f.Path)
	}
	if !strings.Contains(strings.Join(changed, ","), "netlist.json") {
		t.Fatalf("diff shows %v", changed)
	}
	if _, changed, _ := r.KiCadChanged(); changed {
		t.Fatal("still marked changed after re-import")
	}
}

// After a clone nothing is recorded yet: the first sync re-imports, finds
// the same files, and writes nothing.
func TestKiCadSyncAfterClone(t *testing.T) {
	r := kicadDemo(t)
	if _, err := r.ImportKiCad(filepath.Join(r.Root, "hw", "ecc83-pp.kicad_sch")); err != nil {
		t.Fatal(err)
	}
	commitIn(t, r, "import")
	clone, err := Clone(r.Root, filepath.Join(t.TempDir(), "clone"))
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, _ := clone.KiCadChanged(); !changed {
		t.Fatal("fresh clone should count as changed (nothing recorded)")
	}
	res, err := clone.SyncKiCad()
	if err != nil || res == nil || len(res.Written) != 0 {
		t.Fatalf("sync after clone = %+v, %v", res, err)
	}
	st, _ := clone.Status()
	if !st.Clean() {
		t.Fatalf("clone not clean after sync: %+v", st)
	}
}

func TestKiCadSchematicIgnoresBadSource(t *testing.T) {
	r := newRepo(t, map[string]string{
		"netlist.json": `{"format":"kirch.netlist/1","source":"kicad:../../etc/x.kicad_sch","nets":{}}`,
	})
	if got := r.KiCadSchematic(); got != "" {
		t.Fatalf("KiCadSchematic = %q, want none", got)
	}
	if res, err := r.SyncKiCad(); res != nil || err != nil {
		t.Fatalf("SyncKiCad = %+v, %v", res, err)
	}
}
