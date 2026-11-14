package diff

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Kirch-log/kirch/internal/objects"
)

func lcs(a, b []string) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	return dp[0][0]
}

func randomLines(rng *rand.Rand, n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = string(rune('a'+rng.Intn(4))) + "\n"
	}
	return lines
}

// The edit script turns a into b and is as short as possible.
func TestLinesShortestAndCorrect(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		a, b := randomLines(rng, rng.Intn(15)), randomLines(rng, rng.Intn(15))
		edits := Lines(a, b)
		var gotA, gotB []string
		equal := 0
		for _, e := range edits {
			switch e.Op {
			case Equal:
				if a[e.A] != b[e.B] {
					t.Fatalf("Equal edit on different lines %q %q", a[e.A], b[e.B])
				}
				gotA, gotB = append(gotA, a[e.A]), append(gotB, b[e.B])
				equal++
			case Delete:
				gotA = append(gotA, a[e.A])
			case Insert:
				gotB = append(gotB, b[e.B])
			}
		}
		if strings.Join(gotA, "") != strings.Join(a, "") || strings.Join(gotB, "") != strings.Join(b, "") {
			t.Fatalf("edits don't rebuild a=%q b=%q", a, b)
		}
		if want := lcs(a, b); equal != want {
			t.Fatalf("a=%q b=%q: kept %d lines, best is %d", a, b, equal, want)
		}
	}
}

func TestPatchFormat(t *testing.T) {
	old := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n"
	new := "line1\nline2\nline3\nline4\nCHANGED\nline6\nline7\nline8\nline9\nline10\n"
	var buf bytes.Buffer
	WritePatch(&buf, File{
		Path: "a.txt", OldMode: "100644", NewMode: "100644",
		OldID: objects.Hash(objects.Blob, []byte(old)), NewID: objects.Hash(objects.Blob, []byte(new)),
		Old: []byte(old), New: []byte(new),
	})
	want := fmt.Sprintf(`diff --git a/a.txt b/a.txt
index %s..%s 100644
--- a/a.txt
+++ b/a.txt
@@ -2,7 +2,7 @@ line1
 line2
 line3
 line4
-line5
+CHANGED
 line6
 line7
 line8
`, objects.Hash(objects.Blob, []byte(old))[:7], objects.Hash(objects.Blob, []byte(new))[:7])
	if buf.String() != want {
		t.Fatalf("patch:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func file(path, old, new string, oldExists, newExists bool) File {
	f := File{Path: path}
	if oldExists {
		f.Old, f.OldID, f.OldMode = []byte(old), objects.Hash(objects.Blob, []byte(old)), "100644"
	}
	if newExists {
		f.New, f.NewID, f.NewMode = []byte(new), objects.Hash(objects.Blob, []byte(new)), "100644"
	}
	return f
}

// Oracle: git apply turns the old files into the new ones using our patch.
func TestGitAppliesOurPatches(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	rng := rand.New(rand.NewSource(2))
	var files []File
	for i := 0; i < 60; i++ {
		words := []string{"x", "y", "z", "void setup() {", "}", ""}
		gen := func() string {
			var b strings.Builder
			for n := rng.Intn(40); n > 0; n-- {
				b.WriteString(words[rng.Intn(len(words))] + "\n")
			}
			s := b.String()
			if rng.Intn(4) == 0 && s != "" {
				s = s[:len(s)-1] // no newline at the end
			}
			return s
		}
		old, new := gen(), gen()
		if old == new {
			continue
		}
		files = append(files, file(fmt.Sprintf("f%d.txt", i), old, new, true, true))
	}
	files = append(files,
		file("added.txt", "", "new\nfile\n", false, true),
		file("deleted.txt", "gone\n", "", true, false),
		file("no-eol.txt", "a\nb", "a\nb\n", true, true),
	)

	dir := t.TempDir()
	var patch bytes.Buffer
	for _, f := range files {
		if f.OldID != "" {
			if err := os.WriteFile(filepath.Join(dir, f.Path), f.Old, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		WritePatch(&patch, f)
	}
	patchFile := filepath.Join(t.TempDir(), "p.diff")
	if err := os.WriteFile(patchFile, patch.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(git, "apply", "--whitespace=nowarn", patchFile)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git apply: %v\n%s", err, out)
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(dir, f.Path))
		if f.NewID == "" {
			if !os.IsNotExist(err) {
				t.Errorf("%s should be deleted", f.Path)
			}
			continue
		}
		if err != nil || string(got) != string(f.New) {
			t.Errorf("%s after git apply = %q, want %q (%v)", f.Path, got, f.New, err)
		}
	}
}

func wiringStrings(t *testing.T, old, new string) []string {
	t.Helper()
	changes, err := Wiring([]byte(old), []byte(new))
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, c := range changes {
		out = append(out, c.String())
	}
	return out
}

func netlist(nets string) string {
	return `{"format":"kirch.netlist/1","nets":{` + nets + `}}`
}

func TestWiring(t *testing.T) {
	base := netlist(`"GND":["SERVO1.GND","U1.GND","US1.GND"],"SERVO_SIG":["SERVO1.SIG","U1.GPIO9"],"5V":["SERVO1.VCC","U1.5V"]`)
	cases := []struct {
		name, new string
		want      []string
	}{
		{"same", base, []string{}},
		{"servo signal moved (docs/formats.md example)",
			netlist(`"GND":["SERVO1.GND","U1.GND","US1.GND"],"SERVO_SIG":["SERVO1.SIG","U1.GPIO10"],"5V":["SERVO1.VCC","U1.5V"]`),
			[]string{"SERVO1.SIG: U1.GPIO9 → U1.GPIO10"}},
		{"swap on a net with several pins",
			netlist(`"GND":["SERVO1.GND","U1.GND","US2.GND"],"SERVO_SIG":["SERVO1.SIG","U1.GPIO9"],"5V":["SERVO1.VCC","U1.5V"]`),
			[]string{"net GND: US1.GND → US2.GND"}},
		{"net added",
			netlist(`"GND":["SERVO1.GND","U1.GND","US1.GND"],"SERVO_SIG":["SERVO1.SIG","U1.GPIO9"],"5V":["SERVO1.VCC","U1.5V"],"US_TRIG":["U1.GPIO3","US1.TRIG"]`),
			[]string{"+ net US_TRIG: U1.GPIO3, US1.TRIG"}},
		{"net removed",
			netlist(`"GND":["SERVO1.GND","U1.GND","US1.GND"],"5V":["SERVO1.VCC","U1.5V"]`),
			[]string{"- net SERVO_SIG: SERVO1.SIG, U1.GPIO9"}},
		{"net renamed",
			netlist(`"GROUND":["SERVO1.GND","U1.GND","US1.GND"],"SERVO_SIG":["SERVO1.SIG","U1.GPIO9"],"5V":["SERVO1.VCC","U1.5V"]`),
			[]string{"net GND renamed to GROUND"}},
		{"pin moved between nets",
			netlist(`"GND":["SERVO1.GND","U1.GND"],"SERVO_SIG":["SERVO1.SIG","U1.GPIO9"],"5V":["SERVO1.VCC","U1.5V","US1.GND"]`),
			[]string{"US1.GND moved from net GND to net 5V"}},
		{"pin connected and disconnected",
			netlist(`"GND":["SERVO1.GND","U1.GND","US1.GND","US1.X","US1.Y"],"SERVO_SIG":["SERVO1.SIG","U1.GPIO9"],"5V":["U1.5V"]`),
			[]string{"- SERVO1.VCC disconnected from net 5V", "+ US1.X connected to net GND", "+ US1.Y connected to net GND"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wiringStrings(t, base, c.new); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q\nwant %q", got, c.want)
			}
		})
	}
	// Whole file added: every net is new.
	if got := wiringStrings(t, "", netlist(`"A":["X.1","Y.1"]`)); !reflect.DeepEqual(got, []string{"+ net A: X.1, Y.1"}) {
		t.Fatalf("added file: %q", got)
	}
	if _, err := Wiring([]byte("{"), []byte(base)); err == nil {
		t.Fatal("broken JSON accepted")
	}
}
