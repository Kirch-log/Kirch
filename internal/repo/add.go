package repo

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/canon"
	"github.com/Kirch-log/kirch/internal/index"
	"github.com/Kirch-log/kirch/internal/objects"
)

// IndexFile is the staging area's path inside .kirch.
func (r *Repo) IndexFile() string { return filepath.Join(r.Dir, "index") }

// LoadIndex reads the staging area.
func (r *Repo) LoadIndex() (*index.Index, error) { return index.Load(r.IndexFile()) }

// AddResult says what Add changed. All paths are repo-relative with "/".
type AddResult struct {
	Staged    []string // new or changed files now in the index
	Rewritten []string // circuit files rewritten on disk into canonical form
	Removed   []string // files deleted on disk, now dropped from the index
}

type pending struct {
	abs, rel string
	content  []byte
	rewrite  bool
	mode     string
}

// Add stages files and folders (like git add). Folders are added recursively.
// A path that was deleted on disk is removed from the index.
//
// Circuit files (netlist.json, parts.json, layout.json, capture.json) are
// checked and rewritten into canonical form first (docs/formats.md). If any
// file is invalid, nothing is staged and nothing on disk is changed.
func (r *Repo) Add(paths []string) (*AddResult, error) {
	if len(paths) == 0 {
		return nil, errors.New("nothing specified, nothing added")
	}
	idx, err := r.LoadIndex()
	if err != nil {
		return nil, err
	}

	// Pass 1: find files, read and check them. No writes yet.
	var files []pending
	seen := map[string]bool{}
	removed := map[string]bool{}
	for _, arg := range paths {
		abs, rel, err := r.resolve(arg)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			n := dropMissing(idx, rel, removed, nil)
			if n == 0 {
				return nil, fmt.Errorf("pathspec %q did not match any files", arg)
			}
		case err != nil:
			return nil, err
		case info.IsDir():
			found := map[string]bool{}
			if err := r.walk(abs, func(fileAbs, fileRel string) error {
				found[fileRel] = true
				if seen[fileRel] {
					return nil
				}
				seen[fileRel] = true
				p, err := prepare(fileAbs, fileRel)
				if err != nil {
					return err
				}
				files = append(files, p)
				return nil
			}); err != nil {
				return nil, err
			}
			dropMissing(idx, rel, removed, found)
		default:
			if seen[rel] {
				continue
			}
			seen[rel] = true
			p, err := prepare(abs, rel)
			if err != nil {
				return nil, err
			}
			files = append(files, p)
		}
	}

	// Pass 2: write canonical files back, store blobs, update the index.
	res := &AddResult{}
	for _, f := range files {
		if f.rewrite {
			info, err := os.Stat(f.abs)
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(f.abs, f.content, info.Mode().Perm()); err != nil {
				return nil, err
			}
			res.Rewritten = append(res.Rewritten, f.rel)
		}
		id, err := r.Objects.Write(objects.Blob, f.content)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(f.abs)
		if err != nil {
			return nil, err
		}
		old, had := idx.Entries[f.rel]
		idx.Entries[f.rel] = index.Entry{Hash: id, Mode: f.mode, Size: info.Size(), MtimeNs: info.ModTime().UnixNano()}
		if !had || old.Hash != id || old.Mode != f.mode {
			res.Staged = append(res.Staged, f.rel)
		}
	}
	for p := range removed {
		res.Removed = append(res.Removed, p)
	}
	sort.Strings(res.Staged)
	sort.Strings(res.Rewritten)
	sort.Strings(res.Removed)

	if err := idx.Save(r.IndexFile()); err != nil {
		return nil, err
	}
	return res, nil
}

// resolve turns a command-line path into an absolute path and a repo-relative
// slash path ("." for the repo root).
func (r *Repo) resolve(arg string) (abs, rel string, err error) {
	abs, err = filepath.Abs(arg)
	if err != nil {
		return "", "", err
	}
	relOS, err := filepath.Rel(r.Root, abs)
	if err != nil || relOS == ".." || strings.HasPrefix(relOS, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%s is outside the repository at %s", arg, r.Root)
	}
	rel = filepath.ToSlash(relOS)
	if rel != "." {
		if err := index.CheckPath(rel); err != nil {
			return "", "", err
		}
	}
	return abs, rel, nil
}

// walk calls fn for every regular file under dir, skipping .kirch and .git.
func (r *Repo) walk(dir string, fn func(abs, rel string) error) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != dir && (strings.EqualFold(name, DirName) || strings.EqualFold(name, ".git")) {
				return filepath.SkipDir
			}
			if p == dir && (strings.EqualFold(name, DirName) || strings.EqualFold(name, ".git")) {
				return filepath.SkipDir
			}
			return nil
		}
		relOS, err := filepath.Rel(r.Root, p)
		if err != nil {
			return err
		}
		return fn(p, filepath.ToSlash(relOS))
	})
}

func prepare(abs, rel string) (pending, error) {
	info, err := os.Lstat(abs)
	if err != nil {
		return pending{}, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return pending{}, fmt.Errorf("%s is a symbolic link; links aren't supported yet", rel)
	}
	if !info.Mode().IsRegular() {
		return pending{}, fmt.Errorf("%s is not a regular file", rel)
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return pending{}, err
	}
	p := pending{abs: abs, rel: rel, content: content, mode: index.ModeFile}
	if info.Mode().Perm()&0o111 != 0 {
		p.mode = index.ModeExec
	}
	if canon.Applies(rel) {
		formatted, err := canon.Format(path.Base(rel), content)
		if err != nil {
			return pending{}, fmt.Errorf("%s: %w", rel, err)
		}
		if !bytes.Equal(formatted, content) {
			p.content = formatted
			p.rewrite = true
		}
	}
	return p, nil
}

// dropMissing removes index entries at or under rel that no longer exist on
// disk (or, when found is given, that the folder walk didn't see).
// Returns how many entries matched rel.
func dropMissing(idx *index.Index, rel string, removed map[string]bool, found map[string]bool) int {
	matched := 0
	for p := range idx.Entries {
		if !(rel == "." || p == rel || strings.HasPrefix(p, rel+"/")) {
			continue
		}
		matched++
		if found != nil && found[p] {
			continue
		}
		delete(idx.Entries, p)
		removed[p] = true
	}
	return matched
}
