// Package index is the staging area: the list of files that the next commit
// will contain. Stored as JSON in .kirch/index.json (not git's binary .git/index):
//
//	{
//	  "version": 1,
//	  "entries": {
//	    "netlist.json": {"hash": "…", "mode": "100644", "size": 412, "mtime_ns": …}
//	  }
//	}
//
// Paths are relative to the repository root, with forward slashes.
package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const version = 1

// File modes, as git writes them in trees.
const (
	ModeFile = "100644"
	ModeExec = "100755"
)

// Entry is one staged file.
type Entry struct {
	Hash    string `json:"hash"`
	Mode    string `json:"mode"`
	Size    int64  `json:"size"`
	MtimeNs int64  `json:"mtime_ns"` // lets status skip re-hashing unchanged files
}

// Index is the staged file list.
type Index struct {
	Entries map[string]Entry `json:"entries"`
	Version int              `json:"version"`
}

// New returns an empty index.
func New() *Index { return &Index{Version: version, Entries: map[string]Entry{}} }

// Load reads the index file. A missing file is an empty index.
func Load(file string) (*Index, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	idx := New()
	if err := json.Unmarshal(data, idx); err != nil {
		return nil, fmt.Errorf("index is damaged: %w", err)
	}
	if idx.Version != version {
		return nil, fmt.Errorf("index version %d not supported (want %d)", idx.Version, version)
	}
	if idx.Entries == nil {
		idx.Entries = map[string]Entry{}
	}
	for p := range idx.Entries {
		if err := CheckPath(p); err != nil {
			return nil, fmt.Errorf("index is damaged: %w", err)
		}
	}
	return idx, nil
}

// Save writes the index atomically (temp file + rename).
func (idx *Index) Save(file string) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(file), "index-tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, file); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// Paths returns the staged paths in sorted (git) order.
func (idx *Index) Paths() []string {
	paths := make([]string, 0, len(idx.Entries))
	for p := range idx.Entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// CheckPath rejects paths that could escape the repository or clash with it.
func CheckPath(p string) error {
	switch {
	case p == "" || p == ".":
		return errors.New("empty path")
	case strings.Contains(p, "\\"):
		return fmt.Errorf("path %q must use forward slashes", p)
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("path %q must be relative", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("bad path %q", p)
		}
		if strings.EqualFold(part, ".kirch") || strings.EqualFold(part, ".git") {
			return fmt.Errorf("path %q is inside a repository folder", p)
		}
	}
	return nil
}

// Equal reports whether two indexes stage the same content (ignores mtimes).
func Equal(a, b *Index) bool {
	if len(a.Entries) != len(b.Entries) {
		return false
	}
	for p, ea := range a.Entries {
		eb, ok := b.Entries[p]
		if !ok || ea.Hash != eb.Hash || ea.Mode != eb.Mode {
			return false
		}
	}
	return true
}
