// Package repo creates and finds Kirch repositories.
//
// Layout (same shape as .git, so real git tools can inspect it):
//
//	.kirch/
//	├── HEAD          "ref: refs/heads/main\n"
//	├── objects/      see internal/objects
//	└── refs/heads/   one file per branch, holding a commit id
package repo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Kirch-log/kirch/internal/objects"
)

// DirName is the folder that holds a repository's data.
const DirName = ".kirch"

// DefaultBranch is the branch HEAD points to after init.
const DefaultBranch = "main"

var (
	// ErrNotRepo means no .kirch folder was found.
	ErrNotRepo = errors.New("not a kirch repository (no .kirch folder here or above)")
	// ErrExists means init was run where a repository already exists.
	ErrExists = errors.New("already a kirch repository")
)

// Repo is an opened repository.
type Repo struct {
	Root    string // working folder (the one containing .kirch)
	Dir     string // Root/.kirch
	Objects *objects.Store
}

func open(root string) *Repo {
	dir := filepath.Join(root, DirName)
	return &Repo{
		Root:    root,
		Dir:     dir,
		Objects: objects.NewStore(filepath.Join(dir, "objects")),
	}
}

// Init creates a new, empty repository in root.
func Init(root string) (*Repo, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	r := open(abs)
	if _, err := os.Stat(r.Dir); err == nil {
		return nil, fmt.Errorf("%w in %s", ErrExists, abs)
	}
	for _, d := range []string{
		r.Dir,
		filepath.Join(r.Dir, "objects"),
		filepath.Join(r.Dir, "refs", "heads"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	head := "ref: refs/heads/" + DefaultBranch + "\n"
	if err := os.WriteFile(filepath.Join(r.Dir, "HEAD"), []byte(head), 0o644); err != nil {
		return nil, err
	}
	return r, nil
}

// Find opens the repository containing start (start itself or any parent).
func Find(start string) (*Repo, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	for {
		if isRepo(dir) {
			return open(dir), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, ErrNotRepo
		}
		dir = parent
	}
}

func isRepo(root string) bool {
	info, err := os.Stat(filepath.Join(root, DirName, "HEAD"))
	return err == nil && !info.IsDir()
}
