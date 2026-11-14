package objects

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Tree entry modes, as git writes them.
const (
	ModeFile = "100644"
	ModeExec = "100755"
	ModeDir  = "40000"
)

// TreeEntry is one file or folder in a tree.
type TreeEntry struct {
	Mode string // ModeFile, ModeExec or ModeDir
	Name string // one path component, no "/"
	ID   string // blob id (file) or tree id (folder)
}

// IsDir reports whether the entry is a folder.
func (e TreeEntry) IsDir() bool { return e.Mode == ModeDir }

// EncodeTree returns git's binary tree format:
//
//	repeat: "<mode> <name>\x00<20-byte id>"
//
// sorted the way git sorts (folders compare as if their name ended in "/").
func EncodeTree(entries []TreeEntry) ([]byte, error) {
	sorted := append([]TreeEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sortKey(sorted[i]) < sortKey(sorted[j]) })
	var buf bytes.Buffer
	for i, e := range sorted {
		if e.Name == "" || strings.ContainsAny(e.Name, "/\x00") || e.Name == "." || e.Name == ".." {
			return nil, fmt.Errorf("bad tree entry name %q", e.Name)
		}
		if i > 0 && sorted[i-1].Name == e.Name {
			return nil, fmt.Errorf("duplicate tree entry %q", e.Name)
		}
		if e.Mode != ModeFile && e.Mode != ModeExec && e.Mode != ModeDir {
			return nil, fmt.Errorf("bad mode %q for %q", e.Mode, e.Name)
		}
		raw, err := hex.DecodeString(e.ID)
		if err != nil || len(raw) != 20 {
			return nil, fmt.Errorf("bad id %q for %q", e.ID, e.Name)
		}
		buf.WriteString(e.Mode)
		buf.WriteByte(' ')
		buf.WriteString(e.Name)
		buf.WriteByte(0)
		buf.Write(raw)
	}
	return buf.Bytes(), nil
}

func sortKey(e TreeEntry) string {
	if e.IsDir() {
		return e.Name + "/"
	}
	return e.Name
}

// ParseTree decodes git's binary tree format.
func ParseTree(data []byte) ([]TreeEntry, error) {
	var entries []TreeEntry
	for len(data) > 0 {
		sp := bytes.IndexByte(data, ' ')
		if sp < 0 {
			return nil, errors.New("tree: missing mode")
		}
		mode := string(data[:sp])
		data = data[sp+1:]
		nul := bytes.IndexByte(data, 0)
		if nul < 0 || len(data) < nul+1+20 {
			return nil, errors.New("tree: truncated entry")
		}
		name := string(data[:nul])
		id := hex.EncodeToString(data[nul+1 : nul+21])
		data = data[nul+21:]
		if mode != ModeFile && mode != ModeExec && mode != ModeDir {
			return nil, fmt.Errorf("tree: unsupported mode %q for %q", mode, name)
		}
		entries = append(entries, TreeEntry{Mode: mode, Name: name, ID: id})
	}
	return entries, nil
}
