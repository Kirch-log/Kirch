package merge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestTextClean(t *testing.T) {
	base := "a\nb\nc\nd\ne\n"
	ours := "A\nb\nc\nd\ne\n"
	theirs := "a\nb\nc\nd\nE\nf\n"
	got, n := Text([]byte(base), []byte(ours), []byte(theirs), "HEAD", "side")
	if n != 0 || string(got) != "A\nb\nc\nd\nE\nf\n" {
		t.Fatalf("got %q, %d conflicts", got, n)
	}
}

func TestTextSameChangeBothSides(t *testing.T) {
	got, n := Text([]byte("a\n"), []byte("b\n"), []byte("b\n"), "HEAD", "side")
	if n != 0 || string(got) != "b\n" {
		t.Fatalf("got %q, %d", got, n)
	}
}

func TestTextConflict(t *testing.T) {
	got, n := Text([]byte("a\nb\nc\n"), []byte("a\nB1\nc\n"), []byte("a\nB2\nc"), "HEAD", "side")
	// theirs also dropped the final newline, so line c joins the conflict
	want := "a\n<<<<<<< HEAD\nB1\nc\n=======\nB2\nc\n>>>>>>> side\n"
	if n != 1 || string(got) != want {
		t.Fatalf("got %q, %d", got, n)
	}
}

// Oracle: for random-ish edits, our result matches git merge-file whenever
// git merges cleanly, and we report a conflict exactly when git does.
func TestTextMatchesGitMergeFile(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	base := lines(1, 20)
	cases := []struct{ ours, theirs string }{
		{edit(base, 2, "x"), edit(base, 15, "y")},
		{edit(base, 2, "x"), edit(base, 2, "y")},
		{edit(base, 5, "x"), edit(base, 6, "y")},
		{edit(base, 5, "x"), edit(base, 7, "y")},
		{base + "end1\n", base + "end2\n"},
		{"new\n" + base, base + "end\n"},
		{strings.Replace(base, "line 3\nline 4\n", "", 1), edit(base, 12, "z")},
		{edit(edit(base, 1, "p"), 19, "q"), edit(base, 10, "r")},
	}
	dir := t.TempDir()
	write := func(name, s string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for i, c := range cases {
		o, b, th := write("ours", c.ours), write("base", base), write("theirs", c.theirs)
		cmd := exec.Command(git, "merge-file", "-p", "-L", "HEAD", "-L", "base", "-L", "side", o, b, th)
		want, err := cmd.Output()
		gitConflict := err != nil
		got, n := Text([]byte(base), []byte(c.ours), []byte(c.theirs), "HEAD", "side")
		if gitConflict != (n > 0) {
			t.Fatalf("case %d: git conflict=%v, ours %d\n%s", i, gitConflict, n, got)
		}
		if !gitConflict && string(got) != string(want) {
			t.Fatalf("case %d:\nours:\n%s\ngit:\n%s", i, got, want)
		}
	}
}

func lines(from, to int) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		b.WriteString("line " + itoa(i) + "\n")
	}
	return b.String()
}

func edit(s string, n int, with string) string {
	return strings.Replace(s, "line "+itoa(n)+"\n", with+"\n", 1)
}

func itoa(i int) string { return strconv.Itoa(i) }

const baseNet = `{"format":"kirch.netlist/1","nets":{"GND":["U1.GND","SERVO1.GND"],"SIG":["U1.GPIO9","SERVO1.SIG"],"ECHO":["US1.ECHO","U1.GPIO3"]}}`

func TestNetlistDifferentNets(t *testing.T) {
	// ours moves the servo signal; theirs adds a sensor ground.
	ours := strings.Replace(baseNet, `"U1.GPIO9"`, `"U1.GPIO10"`, 1)
	theirs := strings.Replace(baseNet, `"SERVO1.GND"`, `"SERVO1.GND","US1.GND"`, 1)
	got, conflicts, err := Netlist([]byte(baseNet), []byte(ours), []byte(theirs))
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("err %v, conflicts %v", err, conflicts)
	}
	want := `{
  "format": "kirch.netlist/1",
  "nets": {
    "ECHO": [
      "U1.GPIO3",
      "US1.ECHO"
    ],
    "GND": [
      "SERVO1.GND",
      "U1.GND",
      "US1.GND"
    ],
    "SIG": [
      "SERVO1.SIG",
      "U1.GPIO10"
    ]
  }
}
`
	if string(got) != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestNetlistSameChangeBothSides(t *testing.T) {
	both := strings.Replace(baseNet, `"U1.GPIO9"`, `"U1.GPIO10"`, 1)
	if _, conflicts, err := Netlist([]byte(baseNet), []byte(both), []byte(both)); err != nil || len(conflicts) != 0 {
		t.Fatalf("%v %v", err, conflicts)
	}
}

// Both sides swap GPIO9 for a different pin. Combining them would wire
// GPIO10 and GPIO11 together, which nobody did: must be a conflict.
func TestNetlistSameNetConflict(t *testing.T) {
	ours := strings.Replace(baseNet, `"U1.GPIO9"`, `"U1.GPIO11"`, 1)
	theirs := strings.Replace(baseNet, `"U1.GPIO9"`, `"U1.GPIO10"`, 1)
	got, conflicts, err := Netlist([]byte(baseNet), []byte(ours), []byte(theirs))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(conflicts, "|") != "net SIG: ours SERVO1.SIG U1.GPIO11, theirs SERVO1.SIG U1.GPIO10" {
		t.Fatalf("conflicts = %q", conflicts)
	}
	s := string(got)
	if !strings.Contains(s, `"conflicts": {`) || strings.Count(s, `"U1.GPIO11"`) != 2 || strings.Count(s, `"U1.GPIO10"`) != 1 {
		t.Fatalf("got:\n%s", s)
	}
}

// Each side moves the same pin to a different (otherwise untouched) net.
func TestNetlistPinInTwoNets(t *testing.T) {
	base := `{"format":"kirch.netlist/1","nets":{"A":["P.1","X.1"],"B":["X.2"],"C":["X.3"]}}`
	ours := `{"format":"kirch.netlist/1","nets":{"A":["X.1"],"B":["X.2","P.1"],"C":["X.3"]}}`
	theirs := `{"format":"kirch.netlist/1","nets":{"A":["X.1"],"B":["X.2"],"C":["X.3","P.1"]}}`
	got, conflicts, err := Netlist([]byte(base), []byte(ours), []byte(theirs))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(conflicts, "|") != "pin P.1: ours on B, theirs on C" {
		t.Fatalf("conflicts = %q", conflicts)
	}
	if strings.Count(string(got), `"P.1"`) != 2 { // once in net B, once under conflicts
		t.Fatalf("got:\n%s", got)
	}
	if _, _, err := Netlist([]byte(base), got, got); err == nil {
		t.Fatal("a netlist with conflicts left in must not be accepted")
	}
}

func TestNetlistBothAdded(t *testing.T) {
	ours := `{"format":"kirch.netlist/1","nets":{"A":["U1.1","U2.1"]}}`
	theirs := `{"format":"kirch.netlist/1","nets":{"B":["U3.1","U4.1"]}}`
	got, conflicts, err := Netlist(nil, []byte(ours), []byte(theirs))
	if err != nil || len(conflicts) != 0 || !strings.Contains(string(got), `"B": [`) {
		t.Fatalf("%v %v\n%s", err, conflicts, got)
	}
}

func TestNetlistRenameVsEdit(t *testing.T) {
	// ours renames SIG; theirs adds a pin to SIG: conflict on SIG.
	ours := strings.Replace(baseNet, `"SIG"`, `"SERVO_SIG"`, 1)
	theirs := strings.Replace(baseNet, `"SERVO1.SIG"`, `"SERVO1.SIG","LED1.A"`, 1)
	got, conflicts, err := Netlist([]byte(baseNet), []byte(ours), []byte(theirs))
	if err != nil || len(conflicts) == 0 || !strings.HasPrefix(conflicts[0], "net SIG: ours (deleted)") {
		t.Fatalf("%v %q\n%s", err, conflicts, got)
	}
}

func TestNetlistBadInput(t *testing.T) {
	if _, _, err := Netlist([]byte(baseNet), []byte("{"), []byte(baseNet)); err == nil {
		t.Fatal("want error for invalid JSON")
	}
}
