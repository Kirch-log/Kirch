package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/repo"
)

const mainCpp = `#include <Arduino.h>
#include <ESP32Servo.h>

Servo servo;
const int SERVO_PIN = 9;
const int TRIG_PIN = 3;

void setup() {
  Serial.begin(115200);
  servo.attach(SERVO_PIN);
  pinMode(TRIG_PIN, OUTPUT);
}

long readDistance() {
  digitalWrite(TRIG_PIN, LOW);
  delayMicroseconds(2);
  digitalWrite(TRIG_PIN, HIGH);
  delayMicroseconds(10);
  digitalWrite(TRIG_PIN, LOW);
  return pulseIn(16, HIGH) / 58;
}

void loop() {
  long cm = readDistance();
  servo.write(cm < 20 ? 0 : 90);
  delay(100);
}
`

// Oracle: kirch diff --text prints exactly what git diff prints, for files
// vs index, index vs last commit, files vs a commit, and commit vs commit.
// Without --text, netlist.json shows wiring changes instead.
func TestDiffMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	r, err := repo.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	write := func(p, content string) {
		t.Helper()
		abs := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	when := int64(1794621600)
	commit := func(msg string) string {
		t.Helper()
		if _, err := r.Add([]string{"."}); err != nil {
			t.Fatal(err)
		}
		when += 60
		res, err := r.Commit(msg, objects.Signature{Name: "T", Email: "t@example.com", When: time.Unix(when, 0).UTC()})
		if err != nil {
			t.Fatal(err)
		}
		return res.ID
	}

	write("firmware/src/main.cpp", mainCpp)
	write("netlist.json", `{"format":"kirch.netlist/1","nets":{"GND":["SERVO1.GND","U1.GND"],"SERVO_SIG":["SERVO1.SIG","U1.GPIO9"]}}`)
	write("notes.txt", "Servo on GPIO9.\nNo newline at the end")
	write("old.txt", "remove me\n")
	c1 := commit("first")

	write("firmware/src/main.cpp", strings.NewReplacer("SERVO_PIN = 9", "SERVO_PIN = 10", "delay(100)", "delay(50)").Replace(mainCpp))
	write("netlist.json", `{"format":"kirch.netlist/1","nets":{"GND":["SERVO1.GND","U1.GND"],"SERVO_SIG":["SERVO1.SIG","U1.GPIO10"]}}`)
	write("notes.txt", "Servo on GPIO10.\nNo newline at the end\n")
	write("new.txt", "")
	os.Remove(filepath.Join(root, "old.txt"))
	c2 := commit("move servo")

	// Staged and unstaged changes on top.
	write("notes.txt", "Servo on GPIO10.\nNo newline at the end\nStaged line.\n")
	write("staged-new.txt", "brand new\n")
	if _, err := r.Add([]string{"notes.txt", "staged-new.txt"}); err != nil {
		t.Fatal(err)
	}
	write("firmware/src/main.cpp", strings.Replace(mainCpp, "  Serial.begin(115200);\n", "", 1))
	write("new.txt", "now has text\n")

	run := mirrorInGit(t, git, r)
	run("update-index", "-q", "--refresh")
	for _, c := range []struct{ kirch, git []string }{
		{[]string{"diff", "--text"}, []string{"diff"}},
		{[]string{"diff", "--text", "--staged"}, []string{"diff", "--cached"}},
		{[]string{"diff", "--text", c1}, []string{"diff", c1}},
		{[]string{"diff", "--text", c1, c2}, []string{"diff", c1, c2}},
		{[]string{"diff", "--text", c2, c1}, []string{"diff", c2, c1}},
		{[]string{"diff", "--text", c1, "notes.txt", "firmware"}, []string{"diff", c1, "--", "notes.txt", "firmware"}},
		// Keep "--" cases last: pflag remembers where "--" was across runs
		// in one process, which would turn later commits into paths.
		{[]string{"diff", "--text", c1, c2, "--", "firmware", "old.txt"}, []string{"diff", c1, c2, "--", "firmware", "old.txt"}},
	} {
		got := runKirch(t, c.kirch...)
		want := run(append([]string{"--no-pager"}, append(c.git, "--no-color", "--no-ext-diff")...)...)
		if want == "" {
			t.Errorf("git %v printed nothing; the test setup is wrong", c.git)
		}
		if got != want {
			t.Errorf("kirch %v:\n%s\ngit %v:\n%s", c.kirch, got, c.git, want)
		}
	}

	got := runKirch(t, "diff", c1, c2)
	if !strings.Contains(got, "diff --git a/netlist.json b/netlist.json\n") ||
		!strings.Contains(got, "wiring:\n    SERVO1.SIG: U1.GPIO9 → U1.GPIO10\n") {
		t.Fatalf("no wiring diff in:\n%s", got)
	}
	if !strings.Contains(got, "-const int SERVO_PIN = 9;\n+const int SERVO_PIN = 10;\n") {
		t.Fatalf("no code diff in:\n%s", got)
	}
}
