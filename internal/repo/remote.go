package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/refs"
)

// Remotes are other repositories on this computer (a folder, e.g. a shared
// or synced drive), saved by name in .kirch/remotes.json:
//
//	{"origin": "D:/shared/servo-board"}
//
// Push / pull over the network comes with the platform (Part 2).

// ErrNonFastForward means the remote branch has commits we don't have.
var ErrNonFastForward = errors.New("the remote branch has commits you don't have; kirch pull first, then push")

// ErrNoRemote means the remote name isn't set up.
var ErrNoRemote = errors.New("no such remote (add one with kirch remote add <name> <folder>)")

func (r *Repo) remotesFile() string { return filepath.Join(r.Dir, "remotes.json") }

// Remotes returns remote name → folder.
func (r *Repo) Remotes() (map[string]string, error) {
	data, err := os.ReadFile(r.remotesFile())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("remotes.json is damaged: %w", err)
	}
	return m, nil
}

// AddRemote saves a remote. The folder must hold a kirch repository.
func (r *Repo) AddRemote(name, folder string) error {
	if err := refs.CheckBranchName(name); err != nil || strings.Contains(name, "/") {
		return fmt.Errorf("%q is not a valid remote name", name)
	}
	abs, err := filepath.Abs(folder)
	if err != nil {
		return err
	}
	if !isRepo(abs) {
		return fmt.Errorf("%s is not a kirch repository", abs)
	}
	if abs == r.Root {
		return errors.New("a repository can't be its own remote")
	}
	m, err := r.Remotes()
	if err != nil {
		return err
	}
	if _, ok := m[name]; ok {
		return fmt.Errorf("remote %q already exists", name)
	}
	m[name] = filepath.ToSlash(abs)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.remotesFile(), append(data, '\n'), 0o644)
}

// OpenRemote opens the repository a remote name points to.
func (r *Repo) OpenRemote(name string) (*Repo, string, error) {
	m, err := r.Remotes()
	if err != nil {
		return nil, "", err
	}
	folder, ok := m[name]
	if !ok {
		return nil, "", fmt.Errorf("%q: %w", name, ErrNoRemote)
	}
	path := filepath.FromSlash(folder)
	if !isRepo(path) {
		return nil, "", fmt.Errorf("remote %q: %s is not a kirch repository (moved or deleted?)", name, folder)
	}
	return open(path), folder, nil
}

// copyObjects copies every object reachable from tips that dst is missing.
// Objects are written parents-last (blobs, then trees, then commits), so a
// commit in dst always has everything below it, even after an interrupted
// copy; reaching an object dst already has stops the walk there.
func copyObjects(src, dst *objects.Store, tips []string) (int, error) {
	type obj struct {
		typ  objects.Type
		data []byte
	}
	var order []string
	found := map[string]obj{}
	stack := append([]string(nil), tips...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, ok := found[id]; ok || dst.Has(id) {
			continue
		}
		typ, data, err := src.Read(id)
		if err != nil {
			return 0, err
		}
		found[id] = obj{typ, data}
		order = append(order, id)
		switch typ {
		case objects.Commit:
			c, err := objects.ParseCommit(data)
			if err != nil {
				return 0, err
			}
			stack = append(stack, c.Tree)
			stack = append(stack, c.Parents...)
		case objects.Tree:
			entries, err := objects.ParseTree(data)
			if err != nil {
				return 0, err
			}
			for _, e := range entries {
				stack = append(stack, e.ID)
			}
		}
	}
	// Write in reverse discovery order: everything an object points to was
	// discovered after it.
	for i := len(order) - 1; i >= 0; i-- {
		o := found[order[i]]
		if _, err := dst.Write(o.typ, o.data); err != nil {
			return 0, err
		}
	}
	return len(order), nil
}

// RefUpdate is one branch that moved during fetch or push.
type RefUpdate struct {
	Branch   string
	Old, New string // "" = didn't exist / deleted
}

// Fetch copies the remote's branches and their history, and records their
// tips as remote-tracking branches (refs/remotes/<remote>/<branch>, shown as
// "origin/main"). Local branches and files are not touched.
func (r *Repo) Fetch(remote string) ([]RefUpdate, error) {
	rem, _, err := r.OpenRemote(remote)
	if err != nil {
		return nil, err
	}
	theirs, err := rem.Refs().Branches()
	if err != nil {
		return nil, err
	}
	known, err := r.Refs().RemoteBranches(remote)
	if err != nil {
		return nil, err
	}
	var tips []string
	for _, id := range theirs {
		tips = append(tips, id)
	}
	if _, err := copyObjects(rem.Objects, r.Objects, tips); err != nil {
		return nil, err
	}
	var updates []RefUpdate
	for b, id := range theirs {
		if known[b] != id {
			if err := r.Refs().SetRemoteBranch(remote, b, id); err != nil {
				return nil, err
			}
			updates = append(updates, RefUpdate{Branch: b, Old: known[b], New: id})
		}
	}
	for b, id := range known {
		if _, ok := theirs[b]; !ok { // deleted on the remote
			if err := r.Refs().SetRemoteBranch(remote, b, ""); err != nil {
				return nil, err
			}
			updates = append(updates, RefUpdate{Branch: b, Old: id})
		}
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].Branch < updates[j].Branch })
	return updates, nil
}

// Push sends a local branch and its history to the remote and moves the
// remote's branch to it. Refused (nothing changes on the remote's branch)
// when the remote branch has commits we don't have.
//
// If the remote has that branch checked out, its files are updated too, but
// only when it has no uncommitted changes (like git's updateInstead).
// Returns an update with Old == New when the remote was already up to date.
func (r *Repo) Push(remote, branch string) (*RefUpdate, error) {
	if branch == "" {
		current, _, err := r.Refs().Head()
		if err != nil {
			return nil, err
		}
		branch = current
	}
	tip, err := r.Refs().Branch(branch)
	if err != nil {
		return nil, err
	}
	if tip == "" {
		return nil, fmt.Errorf("branch %q has no commits to push", branch)
	}
	rem, _, err := r.OpenRemote(remote)
	if err != nil {
		return nil, err
	}
	old, err := rem.Refs().Branch(branch)
	if err != nil {
		return nil, err
	}
	up := &RefUpdate{Branch: branch, Old: old, New: tip}
	if old == tip {
		return up, r.Refs().SetRemoteBranch(remote, branch, tip)
	}
	if old != "" {
		if !r.Objects.Has(old) {
			return nil, ErrNonFastForward
		}
		ok, err := r.IsAncestor(old, tip)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrNonFastForward
		}
	}
	if _, err := copyObjects(r.Objects, rem.Objects, []string{tip}); err != nil {
		return nil, err
	}

	remoteCurrent, _, err := rem.Refs().Head()
	if err != nil {
		return nil, err
	}
	if remoteCurrent == branch {
		if err := rem.updateCheckedOut(tip); err != nil {
			return nil, fmt.Errorf("remote %q has %s checked out: %w", remote, branch, err)
		}
	}
	if err := rem.Refs().UpdateBranch(branch, tip, old); err != nil {
		return nil, err
	}
	return up, r.Refs().SetRemoteBranch(remote, branch, tip)
}

// updateCheckedOut moves this repository's files and index to commit id,
// refusing if anyone has uncommitted work here.
func (r *Repo) updateCheckedOut(id string) error {
	if m, err := r.Merging(); err != nil {
		return err
	} else if m != nil {
		return errors.New("it is in the middle of a merge")
	}
	st, err := r.Status()
	if err != nil {
		return err
	}
	if len(st.Files) > 0 {
		return errors.New("it has uncommitted changes; commit them there first")
	}
	head := map[string]objects.TreeEntry{}
	if st.Head != "" {
		if head, err = r.commitFiles(st.Head); err != nil {
			return err
		}
	}
	target, err := r.commitFiles(id)
	if err != nil {
		return err
	}
	if err := r.checkInTheWay(head, target); err != nil {
		return err
	}
	return r.matchFiles(target)
}

// Pull fetches from the remote and merges its copy of branch ("" = the
// current branch) into the current branch.
func (r *Repo) Pull(remote, branch string, author objects.Signature) ([]RefUpdate, *MergeResult, error) {
	current, _, err := r.Refs().Head()
	if err != nil {
		return nil, nil, err
	}
	if branch == "" {
		branch = current
	}
	updates, err := r.Fetch(remote)
	if err != nil {
		return nil, nil, err
	}
	id, err := r.Refs().RemoteBranch(remote, branch)
	if err != nil {
		return nil, nil, err
	}
	if id == "" {
		return updates, nil, fmt.Errorf("remote %q has no branch %q", remote, branch)
	}
	_, folder, err := r.OpenRemote(remote)
	if err != nil {
		return nil, nil, err
	}
	msg := withInto(fmt.Sprintf("Merge branch '%s' of %s", branch, folder), current)
	res, err := r.merge(remote+"/"+branch, msg, author)
	return updates, res, err
}

// Clone copies the repository in src into a new folder dest (which must not
// exist or be empty), sets src up as remote "origin", and checks out the
// branch src has checked out.
func Clone(src, dest string) (*Repo, error) {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return nil, err
	}
	if !isRepo(srcAbs) {
		return nil, fmt.Errorf("%s is not a kirch repository", srcAbs)
	}
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("%s already exists and is not empty", dest)
	}
	r, err := Init(dest)
	if err != nil {
		return nil, err
	}
	if err := r.AddRemote("origin", srcAbs); err != nil {
		return nil, err
	}
	if _, err := r.Fetch("origin"); err != nil {
		return nil, err
	}
	branch, id, err := open(srcAbs).Refs().Head()
	if err != nil {
		return nil, err
	}
	if err := r.Refs().SetHead(branch); err != nil {
		return nil, err
	}
	if id == "" {
		return r, nil // empty repository
	}
	if err := r.Refs().UpdateBranch(branch, id, ""); err != nil {
		return nil, err
	}
	files, err := r.commitFiles(id)
	if err != nil {
		return nil, err
	}
	return r, r.matchFiles(files)
}
