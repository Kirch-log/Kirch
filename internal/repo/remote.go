package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/refs"
)

// Remotes are other copies of the repository, saved by name in
// .kirch/remotes.json. A remote is either a folder on this computer (a shared
// or synced drive) or a Kirch server URL:
//
//	{"origin": "https://kirch.example/ana/servo-board", "backup": "D:/shared/servo-board"}

// ErrNonFastForward means the remote branch has commits we don't have.
var ErrNonFastForward = errors.New("the remote branch has commits you don't have; kirch pull first, then push")

// ErrNoRemote means the remote name isn't set up.
var ErrNoRemote = errors.New("no such remote (add one with kirch remote add <name> <folder or URL>)")

// IsURL reports whether a remote location is a server URL (not a folder).
func IsURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func (r *Repo) remotesFile() string { return filepath.Join(r.Dir, "remotes.json") }

// Remotes returns remote name → folder or URL.
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

// AddRemote saves a remote: a folder holding a kirch repository, or a server
// URL (https://host/owner/repo).
func (r *Repo) AddRemote(name, location string) error {
	if err := refs.CheckBranchName(name); err != nil || strings.Contains(name, "/") {
		return fmt.Errorf("%q is not a valid remote name", name)
	}
	var saved string
	if IsURL(location) {
		u, err := checkRemoteURL(location)
		if err != nil {
			return err
		}
		saved = u
	} else {
		abs, err := filepath.Abs(location)
		if err != nil {
			return err
		}
		if !isRepo(abs) {
			return fmt.Errorf("%s is not a kirch repository", abs)
		}
		if abs == r.Root {
			return errors.New("a repository can't be its own remote")
		}
		saved = filepath.ToSlash(abs)
	}
	m, err := r.Remotes()
	if err != nil {
		return err
	}
	if _, ok := m[name]; ok {
		return fmt.Errorf("remote %q already exists", name)
	}
	m[name] = saved
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.remotesFile(), append(data, '\n'), 0o644)
}

// checkRemoteURL validates a server URL and returns it without a trailing
// slash. Passwords / tokens in the URL are refused: they would be saved in
// remotes.json in plain text.
func checkRemoteURL(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not a valid URL", s)
	}
	if _, hasPass := u.User.Password(); hasPass {
		return "", errors.New("don't put a token in the URL (it would be saved in plain text); set KIRCH_USER and KIRCH_TOKEN instead")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%q: a repository URL has no ?query or #fragment", s)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// RemoteLocation returns the folder or URL a remote name points to.
func (r *Repo) RemoteLocation(name string) (string, error) {
	m, err := r.Remotes()
	if err != nil {
		return "", err
	}
	loc, ok := m[name]
	if !ok {
		return "", fmt.Errorf("%q: %w", name, ErrNoRemote)
	}
	return loc, nil
}

// OpenRemote opens the repository a folder remote points to.
func (r *Repo) OpenRemote(name string) (*Repo, string, error) {
	loc, err := r.RemoteLocation(name)
	if err != nil {
		return nil, "", err
	}
	if IsURL(loc) {
		return nil, "", fmt.Errorf("remote %q is a server (%s), not a folder", name, loc)
	}
	p := filepath.FromSlash(loc)
	if !isRepo(p) {
		return nil, "", fmt.Errorf("remote %q: %s is not a kirch repository (moved or deleted?)", name, loc)
	}
	return open(p), loc, nil
}

// remoteEnd is the other side of a fetch or push: a folder or a server.
type remoteEnd interface {
	location() string
	head() (string, error) // the remote's default branch
	branches() (map[string]string, error)
	// fetch copies the history of want into dst (have: commits dst has).
	fetch(dst *Repo, want, have []string) error
	// push sends src's history of new and moves branch from old to new,
	// failing with ErrNonFastForward / refs.ErrStale if it moved meanwhile.
	push(src *Repo, branch, old, new string, have []string) error
}

func (r *Repo) openEnd(name string) (remoteEnd, error) {
	loc, err := r.RemoteLocation(name)
	if err != nil {
		return nil, err
	}
	return endFor(loc)
}

func endFor(loc string) (remoteEnd, error) {
	if IsURL(loc) {
		return newHTTPEnd(loc)
	}
	p := filepath.FromSlash(loc)
	if !isRepo(p) {
		return nil, fmt.Errorf("%s is not a kirch repository (moved or deleted?)", loc)
	}
	return &folderEnd{rem: open(p), loc: loc}, nil
}

// folderEnd is a repository folder on this computer.
type folderEnd struct {
	rem *Repo
	loc string
}

func (f *folderEnd) location() string { return f.loc }

func (f *folderEnd) head() (string, error) {
	b, _, err := f.rem.Refs().Head()
	return b, err
}

func (f *folderEnd) branches() (map[string]string, error) { return f.rem.Refs().Branches() }

func (f *folderEnd) fetch(dst *Repo, want, _ []string) error {
	_, err := copyObjects(f.rem.Objects, dst.Objects, want)
	return err
}

func (f *folderEnd) push(src *Repo, branch, old, new string, _ []string) error {
	if _, err := copyObjects(src.Objects, f.rem.Objects, []string{new}); err != nil {
		return err
	}
	current, _, err := f.rem.Refs().Head()
	if err != nil {
		return err
	}
	if current == branch {
		if err := f.rem.updateCheckedOut(new); err != nil {
			return fmt.Errorf("the remote has %s checked out: %w", branch, err)
		}
	}
	return f.rem.Refs().UpdateBranch(branch, new, old)
}

// walkObjects visits every object reachable from tips once, skipping (and
// not descending into) ids for which skip is true. It returns the ids in
// the order they must be written: everything an object points to comes
// before it (blobs, then trees, then commits).
func walkObjects(store *objects.Store, tips []string, skip func(string) bool) ([]string, error) {
	var order []string
	seen := map[string]bool{}
	stack := append([]string(nil), tips...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] || skip != nil && skip(id) {
			continue
		}
		seen[id] = true
		typ, data, err := store.Read(id)
		if err != nil {
			return nil, err
		}
		order = append(order, id)
		switch typ {
		case objects.Commit:
			c, err := objects.ParseCommit(data)
			if err != nil {
				return nil, err
			}
			stack = append(stack, c.Tree)
			stack = append(stack, c.Parents...)
		case objects.Tree:
			entries, err := objects.ParseTree(data)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				stack = append(stack, e.ID)
			}
		}
	}
	// Everything an object points to was discovered after it: reverse.
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order, nil
}

// Closure returns every object reachable from tips. It fails if one is
// missing, so it also checks that a commit's history is complete.
func Closure(store *objects.Store, tips []string) (map[string]bool, error) {
	ids, err := walkObjects(store, tips, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// ObjectsToSend lists the objects reachable from want that are not
// reachable from have (the other side has those), in write order. Have ids
// this store doesn't know are ignored.
func ObjectsToSend(store *objects.Store, want, have []string) ([]string, error) {
	var known []string
	for _, h := range have {
		if objects.ValidID(h) && store.Has(h) {
			known = append(known, h)
		}
	}
	exclude, err := Closure(store, known)
	if err != nil {
		return nil, err
	}
	return walkObjects(store, want, func(id string) bool { return exclude[id] })
}

// copyObjects copies every object reachable from tips that dst is missing,
// parents-last, so a commit in dst always has everything below it, even
// after an interrupted copy; reaching an object dst has stops the walk.
func copyObjects(src, dst *objects.Store, tips []string) (int, error) {
	ids, err := walkObjects(src, tips, dst.Has)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		typ, data, err := src.Read(id)
		if err != nil {
			return 0, err
		}
		if _, err := dst.Write(typ, data); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// RefUpdate is one branch that moved during fetch or push.
type RefUpdate struct {
	Branch   string
	Old, New string // "" = didn't exist / deleted
}

// localTips lists every commit this repository has a name for (branches and
// remote-tracking branches): what a server can leave out of a fetch.
func (r *Repo) localTips() ([]string, error) {
	var tips []string
	branches, err := r.Refs().Branches()
	if err != nil {
		return nil, err
	}
	for _, id := range branches {
		tips = append(tips, id)
	}
	remotes, err := r.Remotes()
	if err != nil {
		return nil, err
	}
	for name := range remotes {
		rb, err := r.Refs().RemoteBranches(name)
		if err != nil {
			return nil, err
		}
		for _, id := range rb {
			tips = append(tips, id)
		}
	}
	sort.Strings(tips)
	return tips, nil
}

// Fetch copies the remote's branches and their history, and records their
// tips as remote-tracking branches (refs/remotes/<remote>/<branch>, shown as
// "origin/main"). Local branches and files are not touched.
func (r *Repo) Fetch(remote string) ([]RefUpdate, error) {
	end, err := r.openEnd(remote)
	if err != nil {
		return nil, err
	}
	theirs, err := end.branches()
	if err != nil {
		return nil, err
	}
	known, err := r.Refs().RemoteBranches(remote)
	if err != nil {
		return nil, err
	}
	var want []string
	for _, id := range theirs {
		if !r.Objects.Has(id) {
			want = append(want, id)
		}
	}
	if len(want) > 0 {
		have, err := r.localTips()
		if err != nil {
			return nil, err
		}
		sort.Strings(want)
		if err := end.fetch(r, want, have); err != nil {
			return nil, err
		}
		if _, err := Closure(r.Objects, want); err != nil {
			return nil, fmt.Errorf("fetch from %s is incomplete: %w", end.location(), err)
		}
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

// Push sends a local branch ("" = current) and its history to the remote
// and moves the remote's branch to it. Refused (nothing changes on the
// remote's branch) when the remote branch has commits we don't have.
//
// A folder remote that has that branch checked out gets its files updated
// too, but only when it has no uncommitted changes (like git's
// updateInstead). Returns an update with Old == New when already up to date.
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
	end, err := r.openEnd(remote)
	if err != nil {
		return nil, err
	}
	theirs, err := end.branches()
	if err != nil {
		return nil, err
	}
	old := theirs[branch]
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
	// Every remote branch we also have: the remote has their history, so
	// none of it needs sending (a new branch off main sends only new work).
	var have []string
	for _, id := range theirs {
		if r.Objects.Has(id) {
			have = append(have, id)
		}
	}
	sort.Strings(have)
	if err := end.push(r, branch, old, tip, have); err != nil {
		if errors.Is(err, refs.ErrStale) {
			return nil, fmt.Errorf("%w (someone pushed while you were pushing)", ErrNonFastForward)
		}
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
	loc, err := r.RemoteLocation(remote)
	if err != nil {
		return nil, nil, err
	}
	msg := withInto(fmt.Sprintf("Merge branch '%s' of %s", branch, loc), current)
	res, err := r.merge(remote+"/"+branch, msg, author)
	return updates, res, err
}

// CloneName is the folder kirch clone makes when none is given: the last
// part of the folder or URL.
func CloneName(src string) string {
	if IsURL(src) {
		if u, err := url.Parse(src); err == nil {
			src = u.Path
		}
		return strings.TrimSuffix(path.Base(strings.TrimRight(src, "/")), ".kirch")
	}
	return filepath.Base(filepath.Clean(src))
}

// Clone copies the repository at src (a folder or a server URL) into a new
// folder dest (which must not exist or be empty), sets src up as remote
// "origin", and checks out src's default branch.
func Clone(src, dest string) (*Repo, error) {
	loc := src
	if !IsURL(src) {
		abs, err := filepath.Abs(src)
		if err != nil {
			return nil, err
		}
		if !isRepo(abs) {
			return nil, fmt.Errorf("%s is not a kirch repository", abs)
		}
		loc = abs
	} else if _, err := checkRemoteURL(src); err != nil {
		return nil, err
	}
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("%s already exists and is not empty", dest)
	}
	end, err := endFor(loc)
	if err != nil {
		return nil, err
	}
	branch, err := end.head() // check we can reach it before making dest
	if err != nil {
		return nil, err
	}
	r, err := Init(dest)
	if err != nil {
		return nil, err
	}
	if err := r.AddRemote("origin", loc); err != nil {
		return nil, err
	}
	if _, err := r.Fetch("origin"); err != nil {
		return nil, err
	}
	if branch == "" {
		branch = DefaultBranch
	}
	if err := r.Refs().SetHead(branch); err != nil {
		return nil, err
	}
	id, err := r.Refs().RemoteBranch("origin", branch)
	if err != nil {
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
