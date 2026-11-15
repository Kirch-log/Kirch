package kicad

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ErrNoKiCad means kicad-cli (installed with KiCad 7 or newer) wasn't found.
var ErrNoKiCad = errors.New("kicad-cli not found: install KiCad 7 or newer, or set KIRCH_KICAD_CLI to its path (or export the netlist yourself in KiCad: File → Export → Netlist → KiCad, and import the .net file)")

// FindCLI returns the path of kicad-cli: $KIRCH_KICAD_CLI, then PATH, then
// the usual install folders (newest KiCad version first).
func FindCLI() (string, error) {
	if p := os.Getenv("KIRCH_KICAD_CLI"); p != "" {
		return p, nil
	}
	if p, err := exec.LookPath("kicad-cli"); err == nil {
		return p, nil
	}
	var patterns []string
	switch runtime.GOOS {
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), `C:\Program Files`} {
			if base != "" {
				patterns = append(patterns, filepath.Join(base, "KiCad", "*", "bin", "kicad-cli.exe"))
			}
		}
	case "darwin":
		patterns = []string{"/Applications/KiCad/KiCad.app/Contents/MacOS/kicad-cli"}
	default:
		patterns = []string{"/usr/bin/kicad-cli", "/usr/local/bin/kicad-cli"}
	}
	for _, pat := range patterns {
		found, _ := filepath.Glob(pat)
		sort.Sort(sort.Reverse(sort.StringSlice(found))) // 10.0 before 9.0 (good enough)
		for _, f := range found {
			if _, err := os.Stat(f); err == nil {
				return f, nil
			}
		}
	}
	return "", ErrNoKiCad
}

// ExportNetlist runs kicad-cli on a schematic (.kicad_sch) and returns the
// netlist in KiCad's own format.
func ExportNetlist(schematic string) ([]byte, error) {
	cli, err := FindCLI()
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "kirch-kicad-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "netlist.net")
	cmd := exec.Command(cli, "sch", "export", "netlist", "--format", "kicadsexpr", "--output", out, schematic)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("kicad-cli couldn't export %s: %v\n%s", schematic, err, strings.TrimSpace(string(msg)))
	}
	return os.ReadFile(out)
}

// Load reads a KiCad design: a schematic (.kicad_sch, exported through
// kicad-cli), a project (.kicad_pro, uses the schematic next to it), or a
// netlist already exported from KiCad (.net).
func Load(file string) (*Netlist, error) {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".kicad_pro":
		file = strings.TrimSuffix(file, filepath.Ext(file)) + ".kicad_sch"
		fallthrough
	case ".kicad_sch":
		if _, err := os.Stat(file); err != nil {
			return nil, err
		}
		data, err := ExportNetlist(file)
		if err != nil {
			return nil, err
		}
		n, err := Parse(data)
		if err == nil {
			n.Source = filepath.Base(file)
		}
		return n, err
	default:
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		return Parse(data)
	}
}
