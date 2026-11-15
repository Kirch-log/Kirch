package kicad

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// demoSchematics returns KiCad's own demo schematics, if KiCad is installed.
func demoSchematics(t *testing.T) []string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("demo path only known on Windows")
	}
	var out []string
	for _, pat := range []string{`ecc83\ecc83-pp.kicad_sch`, `pic_programmer\pic_programmer.kicad_sch`, `interf_u\interf_u.kicad_sch`, `video\video.kicad_sch`} {
		found, _ := filepath.Glob(`C:\Program Files\KiCad\*\share\kicad\demos\` + pat)
		if len(found) > 0 {
			out = append(out, found[len(found)-1])
		}
	}
	if len(out) == 0 {
		t.Skip("KiCad demos not installed")
	}
	return out
}

// Oracle: in a correctly placed schematic, every pin tip touches something —
// a wire end, a junction, a label, a no-connect flag, a power port or another
// pin. If rotation or mirroring were wrong, pins would float in empty space.
func TestPinsLandOnWires(t *testing.T) {
	for _, file := range demoSchematics(t) {
		s, err := ReadSchematic(file)
		if err != nil {
			t.Fatal(err)
		}
		total, hit := 0, 0
		var misses []string
		for name, sh := range s.Sheets {
			spots := map[[2]float64]int{}
			mark := func(p [2]float64) { spots[[2]float64{round(p[0]), round(p[1])}]++ }
			for _, w := range sh.Wires {
				mark([2]float64{w[0], w[1]})
				mark([2]float64{w[2], w[3]})
			}
			for _, j := range sh.Junctions {
				mark(j)
			}
			for _, nc := range sh.NoConnect {
				mark(nc)
			}
			for _, l := range sh.Labels {
				mark(l.At)
			}
			for _, p := range sh.Power {
				mark(p.At)
			}
			for _, p := range sh.Parts {
				for _, pin := range p.Pins {
					mark(pin.At)
				}
			}
			for _, p := range sh.Parts {
				for num, pin := range p.Pins {
					total++
					// Touching = a wire / label / … there, or a second pin on the same spot.
					if spots[[2]float64{round(pin.At[0]), round(pin.At[1])}] > 1 {
						hit++
					} else if len(misses) < 8 {
						misses = append(misses, name+" "+p.Ref+"."+num)
					}
				}
			}
		}
		rate := float64(hit) / float64(total)
		t.Logf("%s: %d sheets, %d of %d pins touch a wire (%.1f%%)", filepath.Base(file), len(s.Sheets), hit, total, 100*rate)
		// Real designs leave some pins unused (no wire, no flag), so not 100%.
		if total < 20 || rate < 0.85 {
			t.Errorf("%s: only %.1f%% of pins touch anything; first misses: %v", filepath.Base(file), 100*rate, misses)
		}
	}
}

// A tiny hand-written schematic: one resistor rotated 90°, mirrored, one wire.
const tinySheet = `(kicad_sch (version 20250114) (generator "eeschema")
  (lib_symbols
    (symbol "Device:R"
      (symbol "R_0_1" (rectangle (start -1 -2.5) (end 1 2.5)))
      (symbol "R_1_1"
        (pin passive line (at 0 3.81 270) (length 1.27) (name "~") (number "1"))
        (pin passive line (at 0 -3.81 90) (length 1.27) (name "~") (number "2")))))
  (symbol (lib_id "Device:R") (at 100 50 90) (unit 1)
    (property "Reference" "R1" (at 0 0 0)) (property "Value" "10k" (at 0 0 0)))
  (symbol (lib_id "Device:R") (at 120 50 0) (mirror x) (unit 1)
    (property "Reference" "R2" (at 0 0 0)) (property "Value" "1k" (at 0 0 0)))
  (symbol (lib_id "power:GND") (at 130 60 0) (unit 1)
    (property "Reference" "#PWR01" (at 0 0 0)) (property "Value" "GND" (at 0 0 0)))
  (wire (pts (xy 103.81 50) (xy 120 46.19)))
  (label "SIG{slash}A" (at 110 48 0)))`

func TestParseSheetTransforms(t *testing.T) {
	sh, err := ParseSheet([]byte(tinySheet))
	if err != nil {
		t.Fatal(err)
	}
	if len(sh.Parts) != 2 || len(sh.Power) != 1 || sh.Power[0].Name != "GND" {
		t.Fatalf("parts %d power %+v", len(sh.Parts), sh.Power)
	}
	r1, r2 := sh.Parts[0], sh.Parts[1]
	// R1 at (100,50) rotated 90°: library pin 1 is above the body (0, 3.81 with
	// y up); rotating 90° counter-clockwise on screen turns "above" into "left".
	if got := r1.Pins["1"].At; got != [2]float64{96.19, 50} {
		t.Errorf("R1 pin 1 at %v", got)
	}
	if got := r1.Pins["2"].At; got != [2]float64{103.81, 50} {
		t.Errorf("R1 pin 2 at %v", got)
	}
	// R2 at (120,50) mirrored around x: pin 1 goes from the top to the bottom.
	if got := r2.Pins["1"].At; got != [2]float64{120, 53.81} {
		t.Errorf("R2 pin 1 at %v", got)
	}
	if got := r2.Pins["2"].At; got != [2]float64{120, 46.19} {
		t.Errorf("R2 pin 2 at %v", got)
	}
	if len(sh.Labels) != 1 || sh.Labels[0].Text != "SIG/A" {
		t.Fatalf("labels %+v", sh.Labels)
	}
	if r1.Box != [4]float64{96.19, 49, 103.81, 51} {
		t.Errorf("R1 box %v", r1.Box)
	}
}

func TestSchematicFileIsCanonicalAndStable(t *testing.T) {
	sh, err := ParseSheet([]byte(tinySheet))
	if err != nil {
		t.Fatal(err)
	}
	s := &Schematic{Source: "hw/tiny.kicad_sch", Main: "tiny.kicad_sch", Sheets: map[string]*Sheet{"tiny.kicad_sch": sh}}
	a, err := s.File()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.File()
	if string(a) != string(b) {
		t.Fatal("not stable")
	}
	for _, want := range []string{`"format": "kirch.schematic/1"`, `"source": "kicad:hw/tiny.kicad_sch"`, `"at": [`, `96.19`} {
		if !strings.Contains(string(a), want) {
			t.Errorf("schematic.json missing %s", want)
		}
	}
	if strings.Contains(string(a), "e-0") || strings.Contains(string(a), "-0,") {
		t.Error("numbers not written plainly")
	}
	if _, err := ParseSheet([]byte(`(export (nets))`)); err == nil {
		t.Error("a netlist is not a schematic")
	}
}
