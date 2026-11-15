package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kirch-log/kirch/internal/repo"
)

const ledNet = `(export (version "E")
  (design (source "/home/ana/blink/blink.kicad_sch"))
  (components
    (comp (ref "R1") (value "330") (libsource (lib "Device") (part "R")))
    (comp (ref "D1") (value "red") (libsource (lib "Device") (part "LED"))))
  (nets
    (net (code "1") (name "/LED") (node (ref "R1") (pin "2")) (node (ref "D1") (pin "2")))
    (net (code "2") (name "GND") (node (ref "D1") (pin "1")) (node (ref "R1") (pin "1")))))
`

func TestImportKiCad(t *testing.T) {
	root := t.TempDir()
	if _, err := repo.Init(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "hw"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(root, "hw"))
	if err := os.WriteFile("blink.net", []byte(ledNet), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runKirch(t, "import", "kicad", "blink.net")
	if !strings.Contains(out, "wrote     netlist.json") || !strings.Contains(out, "2 parts, 2 nets from blink.net") {
		t.Fatalf("first import:\n%s", out)
	}
	// Written at the top of the repository, not in hw/.
	data, err := os.ReadFile(filepath.Join(root, "netlist.json"))
	if err != nil || !strings.Contains(string(data), `"kicad:blink.kicad_sch"`) {
		t.Fatalf("netlist.json: %v\n%s", err, data)
	}
	if out := runKirch(t, "import", "kicad", "blink.net"); !strings.Contains(out, "unchanged netlist.json") {
		t.Fatalf("second import:\n%s", out)
	}
}
