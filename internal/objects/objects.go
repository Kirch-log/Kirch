// Package objects stores and reads Kirch objects (blobs, trees, commits).
//
// The format is identical to git's loose objects, so real git can read what we
// write and is used as the test oracle:
//
//	stored bytes = zlib( "<type> <size>\x00<content>" )
//	object id    = SHA-1 of the uncompressed bytes, as 40 hex chars
//	file path    = <objects dir>/<first 2 hex>/<remaining 38 hex>
package objects

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// Type is the kind of object.
type Type string

const (
	Blob   Type = "blob"   // file contents
	Tree   Type = "tree"   // directory listing
	Commit Type = "commit" // snapshot + parents + message
)

func (t Type) valid() bool { return t == Blob || t == Tree || t == Commit }

var (
	// ErrNotFound means no object with that id is stored.
	ErrNotFound = errors.New("object not found")
	// ErrCorrupt means the stored bytes don't form a valid object.
	ErrCorrupt = errors.New("object corrupt")
)

// Hash returns the object id of content as type t, without storing it.
func Hash(t Type, content []byte) string {
	sum := sha1.Sum(encode(t, content))
	return hex.EncodeToString(sum[:])
}

func encode(t Type, content []byte) []byte {
	header := string(t) + " " + strconv.Itoa(len(content)) + "\x00"
	return append([]byte(header), content...)
}

// Store is a directory of loose objects, normally .kirch/objects.
type Store struct {
	Dir string
}

// NewStore returns a Store rooted at dir. The directory is created on first write.
func NewStore(dir string) *Store { return &Store{Dir: dir} }

func (s *Store) path(id string) string {
	return filepath.Join(s.Dir, id[:2], id[2:])
}

// Write stores content as type t and returns its id. Writing an object that
// already exists is a no-op (same content always has the same id).
func (s *Store) Write(t Type, content []byte) (string, error) {
	if !t.valid() {
		return "", fmt.Errorf("unknown object type %q", t)
	}
	raw := encode(t, content)
	sum := sha1.Sum(raw)
	id := hex.EncodeToString(sum[:])
	dest := s.path(id)

	if _, err := os.Stat(dest); err == nil {
		return id, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}

	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write(raw); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}

	// Write to a temp file then rename, so a crash never leaves half an object.
	tmp, err := os.CreateTemp(filepath.Dir(dest), "tmp-obj-")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(compressed.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		// Another writer may have stored the same object first.
		if _, statErr := os.Stat(dest); statErr == nil {
			return id, nil
		}
		return "", err
	}
	// Objects never change once written; make that obvious (git does the same).
	_ = os.Chmod(dest, 0o444)
	return id, nil
}

// Read returns the type and content of the object with the given id.
// It checks the header, the size and that the content really hashes to id.
func (s *Store) Read(id string) (Type, []byte, error) {
	if !ValidID(id) {
		return "", nil, fmt.Errorf("invalid object id %q", id)
	}
	f, err := os.Open(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return "", nil, err
	}
	defer f.Close()

	zr, err := zlib.NewReader(f)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %s: %v", ErrCorrupt, id, err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %s: %v", ErrCorrupt, id, err)
	}

	t, content, err := decode(raw)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %s: %v", ErrCorrupt, id, err)
	}
	sum := sha1.Sum(raw)
	if hex.EncodeToString(sum[:]) != id {
		return "", nil, fmt.Errorf("%w: %s: content does not match its id", ErrCorrupt, id)
	}
	return t, content, nil
}

// Has reports whether an object with that id is stored.
func (s *Store) Has(id string) bool {
	if !ValidID(id) {
		return false
	}
	_, err := os.Stat(s.path(id))
	return err == nil
}

func decode(raw []byte) (Type, []byte, error) {
	nul := bytes.IndexByte(raw, 0)
	if nul < 0 {
		return "", nil, errors.New("missing header end")
	}
	header := string(raw[:nul])
	space := bytes.IndexByte(raw[:nul], ' ')
	if space < 0 {
		return "", nil, fmt.Errorf("bad header %q", header)
	}
	t := Type(header[:space])
	if !t.valid() {
		return "", nil, fmt.Errorf("unknown type %q", t)
	}
	size, err := strconv.Atoi(header[space+1:])
	if err != nil || size < 0 {
		return "", nil, fmt.Errorf("bad size in header %q", header)
	}
	content := raw[nul+1:]
	if len(content) != size {
		return "", nil, fmt.Errorf("header says %d bytes, found %d", size, len(content))
	}
	return t, content, nil
}

// ValidID reports whether s looks like an object id (40 lowercase hex chars).
func ValidID(s string) bool {
	return len(s) == 40 && isHex(s)
}

// ErrAmbiguous means a short id matches more than one object.
var ErrAmbiguous = errors.New("short id is ambiguous")

// Expand turns a short id (at least 4 hex chars) into the full id of the one
// stored object that starts with it.
func (s *Store) Expand(prefix string) (string, error) {
	if len(prefix) < 4 || len(prefix) > 40 || !isHex(prefix) {
		return "", fmt.Errorf("%q is not an object id", prefix)
	}
	if len(prefix) == 40 {
		if !s.Has(prefix) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, prefix)
		}
		return prefix, nil
	}
	names, err := os.ReadDir(filepath.Join(s.Dir, prefix[:2]))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	found := ""
	for _, n := range names {
		id := prefix[:2] + n.Name()
		if !ValidID(id) || id[:len(prefix)] != prefix {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("%w: %s", ErrAmbiguous, prefix)
		}
		found = id
	}
	if found == "" {
		return "", fmt.Errorf("%w: %s", ErrNotFound, prefix)
	}
	return found, nil
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
