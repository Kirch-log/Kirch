package kicad

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Kirch-log/kirch/internal/canon"
	"github.com/Kirch-log/kirch/internal/diff"
)

// A small servo board netlist in KiCad 10's format (written by hand in the
// same shape kicad-cli produces).
const servoNet = `(export
	(version "E")
	(design
		(source "C:\\Users\\ana\\servo-board\\servo-board.kicad_sch")
		(date "2026-11-15T10:00:00")
		(tool "Eeschema 10.0.1")
		(sheet (number "1") (name "/") (tstamps "/")
			(title_block (title "Servo board") (rev "A"))))
	(components
		(comp (ref "U1") (value "ESP32-S3-DevKitC-1")
			(footprint "Module:ESP32-S3-DevKitC")
			(libsource (lib "MCU_Espressif") (part "ESP32-S3-DevKitC") (description "ESP32-S3 dev board"))
			(units (unit (name "A") (pins (pin (num "1")) (pin (num "9")) (pin (num "10")) (pin (num "21"))))))
		(comp (ref "SERVO1") (value "SG90")
			(libsource (lib "Motor") (part "Motor_Servo") (description "Servo motor"))
			(units (unit (name "A") (pins (pin (num "1")) (pin (num "2")) (pin (num "3"))))))
		(comp (ref "R1") (value "10k") (footprint "Resistor_SMD:R_0603")
			(libsource (lib "Device") (part "R") (description "Resistor")))
		(comp (ref "#PWR01") (value "GND") (libsource (lib "power") (part "GND"))))
	(libparts
		(libpart (lib "Device") (part "R")
			(pins (pin (num "1") (name "~") (type "passive")) (pin (num "2") (name "~") (type "passive"))))
		(libpart (lib "Motor") (part "Motor_Servo")
			(pins (pin (num "1") (name "PWM") (type "input")) (pin (num "2") (name "+") (type "power_in")) (pin (num "3") (name "-") (type "power_in")))))
	(nets
		(net (code "1") (name "GND")
			(node (ref "U1") (pin "1") (pinfunction "GND") (pintype "power_in"))
			(node (ref "SERVO1") (pin "3") (pinfunction "-") (pintype "power_in"))
			(node (ref "#PWR01") (pin "1") (pintype "power_in")))
		(net (code "2") (name "/SERVO{slash}SIG")
			(node (ref "U1") (pin "9") (pinfunction "GPIO9_9") (pintype "bidirectional"))
			(node (ref "SERVO1") (pin "1") (pinfunction "PWM") (pintype "input"))
			(node (ref "R1") (pin "1") (pintype "passive")))
		(net (code "3") (name "unconnected-(U1-GPIO10-Pad10)")
			(node (ref "U1") (pin "10") (pinfunction "GPIO10") (pintype "bidirectional")))
		(net (code "4") (name "+5V")
			(node (ref "U1") (pin "21") (pinfunction "5V") (pintype "power_out"))
			(node (ref "SERVO1") (pin "2") (pinfunction "+") (pintype "power_in"))
			(node (ref "R1") (pin "2") (pintype "passive")))))
`

func TestParseServoBoard(t *testing.T) {
	n, err := Parse([]byte(servoNet))
	if err != nil {
		t.Fatal(err)
	}
	if n.Source != "servo-board.kicad_sch" || n.Title != "Servo board" {
		t.Fatalf("source %q title %q", n.Source, n.Title)
	}
	if _, ok := n.Parts["#PWR01"]; ok || len(n.Parts) != 3 {
		t.Fatalf("parts = %v (power symbols must be skipped)", keys(n.Parts))
	}
	u1 := n.Parts["U1"]
	want := map[string]string{"1": "GND", "9": "GPIO9", "10": "GPIO10", "21": "5V"}
	if !reflect.DeepEqual(u1.PinNames, want) {
		t.Fatalf("U1 pin names = %v", u1.PinNames)
	}
	if strings.Join(u1.Pins, ",") != "1,9,10,21" {
		t.Fatalf("U1 pins = %v (natural order)", u1.Pins)
	}
	if got := n.Parts["SERVO1"].PinNames; got["1"] != "PWM" || got["2"] != "+" {
		t.Fatalf("SERVO1 names from libparts = %v", got)
	}
	if len(n.Parts["R1"].PinNames) != 0 {
		t.Fatalf("R1 has no pin names (~), got %v", n.Parts["R1"].PinNames)
	}
}

func TestFiles(t *testing.T) {
	n, err := Parse([]byte(servoNet))
	if err != nil {
		t.Fatal(err)
	}
	netlist, parts, err := n.Files()
	if err != nil {
		t.Fatal(err)
	}
	var nl struct {
		Source string              `json:"source"`
		Nets   map[string][]string `json:"nets"`
	}
	if err := json.Unmarshal(netlist, &nl); err != nil {
		t.Fatal(err)
	}
	wantNets := map[string][]string{
		"GND":        {"SERVO1.3", "U1.1"},
		"/SERVO/SIG": {"R1.1", "SERVO1.1", "U1.9"},
		"+5V":        {"R1.2", "SERVO1.2", "U1.21"},
	}
	if !reflect.DeepEqual(nl.Nets, wantNets) || nl.Source != "kicad:servo-board.kicad_sch" {
		t.Fatalf("netlist.json:\n%s", netlist)
	}

	var pj struct {
		Title string                    `json:"title"`
		Parts map[string]map[string]any `json:"parts"`
	}
	if err := json.Unmarshal(parts, &pj); err != nil {
		t.Fatal(err)
	}
	types := map[string]string{}
	for ref, p := range pj.Parts {
		types[ref] = p["type"].(string)
	}
	if !reflect.DeepEqual(types, map[string]string{"U1": "mcu", "SERVO1": "part", "R1": "resistor"}) {
		t.Fatalf("types = %v", types)
	}
	if pj.Parts["R1"]["footprint"] != "Resistor_SMD:R_0603" || pj.Parts["U1"]["symbol"] != "MCU_Espressif:ESP32-S3-DevKitC" {
		t.Fatalf("parts.json:\n%s", parts)
	}

	// Canonical: kirch add wouldn't change a byte, and a second export is identical.
	for name, data := range map[string][]byte{"netlist.json": netlist, "parts.json": parts} {
		formatted, err := canon.Format(name, data)
		if err != nil || !bytes.Equal(formatted, data) {
			t.Fatalf("%s not canonical (%v)", name, err)
		}
	}
	netlist2, parts2, _ := n.Files()
	if !bytes.Equal(netlist, netlist2) || !bytes.Equal(parts, parts2) {
		t.Fatal("same design gave different bytes")
	}
}

// Moving the servo from GPIO9 to GPIO10 in KiCad shows up as one wiring change.
func TestRewireShowsInWiringDiff(t *testing.T) {
	before, _ := Parse([]byte(servoNet))
	// Swap the two pins: GPIO10 now drives the servo, GPIO9 is unconnected.
	moved := strings.NewReplacer(
		`(node (ref "U1") (pin "9") (pinfunction "GPIO9_9")`, `(node (ref "U1") (pin "10") (pinfunction "GPIO10")`,
		`(node (ref "U1") (pin "10") (pinfunction "GPIO10")`, `(node (ref "U1") (pin "9") (pinfunction "GPIO9")`,
	).Replace(servoNet)
	if moved == servoNet {
		t.Fatal("test setup: nothing replaced")
	}
	after, err := Parse([]byte(moved))
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := before.Files()
	b, _, _ := after.Files()
	changes, err := diff.Wiring(a, b)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, c := range changes {
		lines = append(lines, c.String())
	}
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "U1.9") || !strings.Contains(got, "U1.10") {
		t.Fatalf("wiring changes:\n%s", got)
	}
}

func TestParseErrors(t *testing.T) {
	for name, src := range map[string]string{
		"empty":          "",
		"not kicad":      `{"format":"x"}`,
		"other s-expr":   `(kicad_sch (version 1))`,
		"unclosed":       `(export (nets (net (name "A")`,
		"no nets":        `(export (components))`,
		"open quote":     `(export (components (comp (ref "R1)))`,
		"trailing text":  `(export (components) (nets)) junk`,
		"stray close":    `)`,
		"deep recursion": strings.Repeat("(", 500),
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestOlderKiCadUnquoted(t *testing.T) {
	// KiCad 5 wrote atoms without quotes.
	old := `(export (version D)
  (components (comp (ref R1) (value 1k) (libsource (lib Device) (part R))) (comp (ref D1) (value LED) (libsource (lib Device) (part LED))))
  (nets (net (code 1) (name /A) (node (ref R1) (pin 2)) (node (ref D1) (pin 1)))))`
	n, err := Parse([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	nl, parts, err := n.Files()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nl), `"D1.1"`) || !strings.Contains(string(parts), `"type": "led"`) {
		t.Fatalf("netlist:\n%s\nparts:\n%s", nl, parts)
	}
}

// With KiCad installed, import a real demo design end to end.
func TestKiCadCLI(t *testing.T) {
	if _, err := FindCLI(); err != nil {
		t.Skip("KiCad not installed")
	}
	var demo string
	if runtime.GOOS == "windows" {
		found, _ := filepath.Glob(`C:\Program Files\KiCad\*\share\kicad\demos\ecc83\ecc83-pp.kicad_sch`)
		if len(found) > 0 {
			demo = found[len(found)-1]
		}
	}
	if demo == "" {
		t.Skip("KiCad demo projects not found")
	}
	n, err := Load(demo)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Parts) < 10 || len(n.Nets) < 5 || n.Source != "ecc83-pp.kicad_sch" {
		t.Fatalf("parts %d nets %d source %q", len(n.Parts), len(n.Nets), n.Source)
	}
	if _, _, err := n.Files(); err != nil {
		t.Fatal(err)
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
