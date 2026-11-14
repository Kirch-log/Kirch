package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Kirch-log/kirch/internal/index"
	"github.com/Kirch-log/kirch/internal/objects"
)

// newRepo makes a repo with the given files (slash paths → content) and
// changes into its root for the duration of the test.
func newRepo(t *testing.T, files map[string]string) *Repo {
	t.Helper()
	root := t.TempDir()
	r, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	for p, c := range files {
		writeFile(t, filepath.Join(root, filepath.FromSlash(p)), c)
	}
	t.Chdir(root)
	return r
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func staged(t *testing.T, r *Repo) []string {
	t.Helper()
	idx, err := r.LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	return idx.Paths()
}

const netlist = `{"format":"kirch.netlist/1","nets":{"SERVO_SIG":["U1.GPIO9","SERVO1.SIG"]}}`

func TestAddFileStoresBlob(t *testing.T) {
	r := newRepo(t, map[string]string{"notes.txt": "hello\n"})
	res, err := r.Add([]string{"notes.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Staged, []string{"notes.txt"}) {
		t.Fatalf("Staged = %v", res.Staged)
	}
	idx, _ := r.LoadIndex()
	e := idx.Entries["notes.txt"]
	if e.Hash != "ce013625030ba8dba906f756967f9e9ca394464a" || e.Mode != index.ModeFile || e.Size != 6 {
		t.Fatalf("entry = %+v", e)
	}
	if !r.Objects.Has(e.Hash) {
		t.Fatal("blob not stored")
	}
}

func TestAddFolderRecursive(t *testing.T) {
	r := newRepo(t, map[string]string{
		"netlist.json":            netlist,
		"firmware/src/main.cpp":   "int main(){}\n",
		"firmware/platformio.ini": "[env]\n",
	})
	if _, err := r.Add([]string{"."}); err != nil {
		t.Fatal(err)
	}
	want := []string{"firmware/platformio.ini", "firmware/src/main.cpp", "netlist.json"}
	if got := staged(t, r); !reflect.DeepEqual(got, want) {
		t.Fatalf("staged = %v, want %v", got, want)
	}
}

func TestAddSkipsRepoFolders(t *testing.T) {
	r := newRepo(t, map[string]string{"a.txt": "a", ".git/config": "x", "sub/.git/HEAD": "y"})
	if _, err := r.Add([]string{"."}); err != nil {
		t.Fatal(err)
	}
	if got := staged(t, r); !reflect.DeepEqual(got, []string{"a.txt"}) {
		t.Fatalf("staged = %v", got)
	}
}

func TestAddFromSubfolder(t *testing.T) {
	r := newRepo(t, map[string]string{"firmware/main.cpp": "x"})
	t.Chdir(filepath.Join(r.Root, "firmware"))
	if _, err := r.Add([]string{"main.cpp"}); err != nil {
		t.Fatal(err)
	}
	if got := staged(t, r); !reflect.DeepEqual(got, []string{"firmware/main.cpp"}) {
		t.Fatalf("staged = %v", got)
	}
}

func TestAddCanonicalizesCircuitFiles(t *testing.T) {
	r := newRepo(t, map[string]string{"netlist.json": netlist})
	res, err := r.Add([]string{"netlist.json"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Rewritten, []string{"netlist.json"}) {
		t.Fatalf("Rewritten = %v", res.Rewritten)
	}
	onDisk, _ := os.ReadFile(filepath.Join(r.Root, "netlist.json"))
	if !strings.Contains(string(onDisk), "\"SERVO1.SIG\",\n      \"U1.GPIO9\"") {
		t.Fatalf("not canonical on disk:\n%s", onDisk)
	}
	idx, _ := r.LoadIndex()
	if idx.Entries["netlist.json"].Hash != objects.Hash(objects.Blob, onDisk) {
		t.Fatal("staged blob differs from rewritten file")
	}

	// Adding again: already canonical, nothing rewritten or re-staged.
	res, err = r.Add([]string{"netlist.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rewritten) != 0 || len(res.Staged) != 0 {
		t.Fatalf("second add changed things: %+v", res)
	}
}

// Same wiring written differently → same blob.
func TestDifferentSpellingSameHash(t *testing.T) {
	r := newRepo(t, map[string]string{
		"a/netlist.json": netlist,
		"b/netlist.json": "{\n\"nets\": {\"SERVO_SIG\": [\"SERVO1.SIG\", \"U1.GPIO9\"]},\n\"format\": \"kirch.netlist/1\"\n}",
	})
	if _, err := r.Add([]string{"."}); err != nil {
		t.Fatal(err)
	}
	idx, _ := r.LoadIndex()
	if idx.Entries["a/netlist.json"].Hash != idx.Entries["b/netlist.json"].Hash {
		t.Fatal("same circuit, different hashes")
	}
}

func TestInvalidNetlistStagesNothing(t *testing.T) {
	bad := `{"nets":{"A":["U1.GPIO9"],"B":["U1.GPIO9"]}}`
	r := newRepo(t, map[string]string{"z.txt": "z", "netlist.json": bad, "parts.json": `{"parts":{}}`})
	_, err := r.Add([]string{"."})
	if err == nil || !strings.Contains(err.Error(), "two nets") {
		t.Fatalf("err = %v", err)
	}
	if got := staged(t, r); len(got) != 0 {
		t.Fatalf("staged after error: %v", got)
	}
	// parts.json was valid but not canonical: it must not have been rewritten.
	parts, _ := os.ReadFile(filepath.Join(r.Root, "parts.json"))
	if string(parts) != `{"parts":{}}` {
		t.Fatalf("parts.json changed despite error: %q", parts)
	}
}

func TestAddDeletedFileRemovesIt(t *testing.T) {
	r := newRepo(t, map[string]string{"a.txt": "a", "b.txt": "b"})
	if _, err := r.Add([]string{"."}); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(r.Root, "a.txt"))
	res, err := r.Add([]string{"a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Removed, []string{"a.txt"}) {
		t.Fatalf("Removed = %v", res.Removed)
	}
	if got := staged(t, r); !reflect.DeepEqual(got, []string{"b.txt"}) {
		t.Fatalf("staged = %v", got)
	}
}

func TestAddFolderDropsDeletedFiles(t *testing.T) {
	r := newRepo(t, map[string]string{"fw/a.cpp": "a", "fw/b.cpp": "b", "top.txt": "t"})
	if _, err := r.Add([]string{"."}); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(r.Root, "fw", "a.cpp"))
	if _, err := r.Add([]string{"fw"}); err != nil {
		t.Fatal(err)
	}
	if got := staged(t, r); !reflect.DeepEqual(got, []string{"fw/b.cpp", "top.txt"}) {
		t.Fatalf("staged = %v", got)
	}
}

func TestAddErrors(t *testing.T) {
	r := newRepo(t, map[string]string{"a.txt": "a"})
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "x.txt"), "x")
	cases := map[string][]string{
		"nothing":       {},
		"no such file":  {"missing.txt"},
		"outside repo":  {filepath.Join(outside, "x.txt")},
		"inside .kirch": {".kirch/HEAD"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := r.Add(args); err == nil {
				t.Fatal("Add succeeded")
			}
		})
	}
}

// Oracle: git add on the same files gives the same index (git ls-files -s).
func TestIndexMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	r := newRepo(t, map[string]string{
		"netlist.json":          netlist,
		"parts.json":            `{"format":"kirch.parts/1","parts":{"U1":{"type":"mcu"}}}`,
		"firmware/src/main.cpp": "#include <Arduino.h>\nvoid setup(){}\nvoid loop(){}\n",
		"empty.txt":             "",
	})
	if _, err := r.Add([]string{"."}); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) string {
		cmd := exec.Command(git, args...)
		cmd.Dir = r.Root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	run("-c", "core.autocrlf=false", "add", ".", ":!.kirch")
	gitLines := strings.Split(strings.TrimSpace(run("ls-files", "-s")), "\n")

	idx, _ := r.LoadIndex()
	var ours []string
	for _, p := range idx.Paths() {
		e := idx.Entries[p]
		ours = append(ours, e.Mode+" "+e.Hash+" 0\t"+p)
	}
	sort.Strings(gitLines)
	sort.Strings(ours)
	if !reflect.DeepEqual(ours, gitLines) {
		t.Fatalf("kirch:\n%s\n\ngit:\n%s", strings.Join(ours, "\n"), strings.Join(gitLines, "\n"))
	}
}
