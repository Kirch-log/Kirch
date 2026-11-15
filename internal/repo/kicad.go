package repo

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/index"
	"github.com/Kirch-log/kirch/internal/kicad"
)

// KiCad designs in a repository (docs/platform.md, "How circuits get in").
//
// kirch import kicad writes netlist.json and parts.json at the top of the
// repository, with "source": "kicad:<path of the .kicad_sch>". From then on
// kirch add re-imports by itself whenever the design changed, so people just
// keep working in KiCad.
//
// .kirch/kicad.json remembers a fingerprint of the design files at the last
// import, so an unchanged design isn't exported again on every kirch add.

// KiCadResult says what an import did.
type KiCadResult struct {
	Schematic string   // repo-relative path of the .kicad_sch
	Written   []string // files that changed: "netlist.json", "parts.json"
	Parts     int
	Nets      int // nets with at least two pins
}

type kicadState struct {
	Schematic   string `json:"schematic"`
	Fingerprint string `json:"fingerprint"`
}

func (r *Repo) kicadStateFile() string { return filepath.Join(r.Dir, "kicad.json") }

// ImportKiCad imports a KiCad design (.kicad_sch, .kicad_pro or an exported
// .net) into netlist.json and parts.json at the top of the repository.
func (r *Repo) ImportKiCad(file string) (*KiCadResult, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(abs), ".kicad_pro") {
		abs = strings.TrimSuffix(abs, filepath.Ext(abs)) + ".kicad_sch"
	}
	n, err := kicad.Load(abs)
	if err != nil {
		return nil, err
	}
	res := &KiCadResult{}
	isSchematic := strings.EqualFold(filepath.Ext(abs), ".kicad_sch")
	if rel, err := filepath.Rel(r.Root, abs); err == nil && isSchematic && !strings.HasPrefix(rel, "..") {
		res.Schematic = filepath.ToSlash(rel)
		n.Source = res.Schematic // repo-relative, so kirch add can find it again
	}
	if err := r.writeKiCad(n, res); err != nil {
		return nil, err
	}
	if res.Schematic != "" {
		fp, err := r.kicadFingerprint(res.Schematic)
		if err != nil {
			return nil, err
		}
		if err := r.saveKiCadState(kicadState{Schematic: res.Schematic, Fingerprint: fp}); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func (r *Repo) writeKiCad(n *kicad.Netlist, res *KiCadResult) error {
	netlist, parts, err := n.Files()
	if err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{"netlist.json", netlist}, {"parts.json", parts}} {
		path := filepath.Join(r.Root, f.name)
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, f.data) {
			continue
		}
		if err := os.WriteFile(path, f.data, 0o644); err != nil {
			return err
		}
		res.Written = append(res.Written, f.name)
	}
	res.Parts = len(n.Parts)
	for _, net := range n.Nets {
		if len(net.Pins) > 1 {
			res.Nets++
		}
	}
	return nil
}

// KiCadSchematic returns the repo-relative schematic that netlist.json was
// imported from ("" if it wasn't imported from a schematic in this repo).
func (r *Repo) KiCadSchematic() string {
	data, err := os.ReadFile(filepath.Join(r.Root, "netlist.json"))
	if err != nil {
		return ""
	}
	var doc struct {
		Source string `json:"source"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	rel, ok := strings.CutPrefix(doc.Source, "kicad:")
	if !ok || !strings.HasSuffix(strings.ToLower(rel), ".kicad_sch") || index.CheckPath(rel) != nil {
		return ""
	}
	if _, err := os.Stat(r.abs(rel)); err != nil {
		return ""
	}
	return rel
}

// KiCadChanged reports whether the design changed since the last import
// here. Unknown (no import recorded on this computer, e.g. right after a
// clone) counts as changed.
func (r *Repo) KiCadChanged() (schematic string, changed bool, err error) {
	schematic = r.KiCadSchematic()
	if schematic == "" {
		return "", false, nil
	}
	fp, err := r.kicadFingerprint(schematic)
	if err != nil {
		return schematic, false, err
	}
	st, _ := r.loadKiCadState()
	return schematic, st.Schematic != schematic || st.Fingerprint != fp, nil
}

// SyncKiCad re-imports the design if it changed since the last import.
// Returns nil when there's no KiCad design or nothing changed.
func (r *Repo) SyncKiCad() (*KiCadResult, error) {
	schematic, changed, err := r.KiCadChanged()
	if err != nil || !changed {
		return nil, err
	}
	return r.ImportKiCad(r.abs(schematic))
}

// kicadFingerprint hashes every schematic sheet in the design's folder
// (sub-sheets of a hierarchical design live next to the main one).
func (r *Repo) kicadFingerprint(schematic string) (string, error) {
	dir := filepath.Dir(r.abs(schematic))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".kicad_sch") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	h := sha1.New()
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s %d\n", name, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (r *Repo) loadKiCadState() (kicadState, error) {
	var st kicadState
	data, err := os.ReadFile(r.kicadStateFile())
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(data, &st)
	return st, err
}

func (r *Repo) saveKiCadState(st kicadState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.kicadStateFile(), append(data, '\n'), 0o644)
}
