// Package protocol is what kirch and a Kirch server send each other for
// clone / fetch / pull / push over HTTP (docs/protocol.md).
//
// Three endpoints, under a repository URL like https://host/ana/servo-board:
//
//	GET  <repo>/info/refs     → Refs (JSON)
//	POST <repo>/kirch-fetch   FetchRequest (JSON) → bundle
//	POST <repo>/kirch-push?branch=B&old=O&new=N   bundle → PushResult (JSON)
//
// A bundle is a gzip stream of objects in the order they must be written
// (blobs and trees before the commits that use them):
//
//	KIRCH-BUNDLE 1\n
//	<type> <size> <id>\n<size bytes of content>     (repeated)
//	end\n
package protocol

import (
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Kirch-log/kirch/internal/objects"
)

// Paths under a repository URL.
const (
	RefsPath  = "/info/refs"
	FetchPath = "/kirch-fetch"
	PushPath  = "/kirch-push"

	BundleType = "application/x-kirch-bundle"
	header     = "KIRCH-BUNDLE 1\n"
)

// Refs is the server's list of branches.
type Refs struct {
	Head     string            `json:"head"`     // default branch
	Branches map[string]string `json:"branches"` // name → commit id
}

// FetchRequest asks for the history of Want. Have lists commits the client
// already has, so the server can leave out everything they contain.
type FetchRequest struct {
	Want []string `json:"want"`
	Have []string `json:"have"`
}

// PushResult says how a push moved the branch.
type PushResult struct {
	Branch  string `json:"branch"`
	Old     string `json:"old"` // "" = new branch
	New     string `json:"new"`
	Objects int    `json:"objects"` // objects received
}

// Error is the JSON body of every failed request.
type Error struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"` // e.g. "non_fast_forward", "stale"
}

// Error codes clients act on.
const (
	CodeNonFastForward = "non_fast_forward" // remote has commits you don't have
	CodeStale          = "stale"            // branch moved during the push; retry
)

// WriteBundle writes the objects ids (in that order) from store.
func WriteBundle(w io.Writer, store *objects.Store, ids []string) error {
	zw := gzip.NewWriter(w)
	if _, err := io.WriteString(zw, header); err != nil {
		return err
	}
	for _, id := range ids {
		typ, data, err := store.Read(id)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(zw, "%s %d %s\n", typ, len(data), id); err != nil {
			return err
		}
		if _, err := zw.Write(data); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(zw, "end\n"); err != nil {
		return err
	}
	return zw.Close()
}

// ErrTooBig means a bundle went over the size limit.
var ErrTooBig = errors.New("bundle is larger than the server allows")

// ReadBundle stores every object in a bundle and returns their ids in
// order. Each object's id is recomputed from its content and must match the
// one the sender gave. maxBytes limits the unpacked size (0 = no limit).
func ReadBundle(r io.Reader, store *objects.Store, maxBytes int64) ([]string, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("bad bundle: %w", err)
	}
	defer zr.Close()
	var src io.Reader = zr
	if maxBytes > 0 {
		src = &limited{r: zr, left: maxBytes}
	}
	br := bufio.NewReader(src)
	line, err := br.ReadString('\n')
	if errors.Is(err, ErrTooBig) {
		return nil, ErrTooBig
	}
	if err != nil || line != header {
		return nil, errors.New("bad bundle: missing header")
	}
	var ids []string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return ids, fmt.Errorf("bad bundle: %w", unwrapLimit(err))
		}
		if line == "end\n" {
			return ids, nil
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			return ids, fmt.Errorf("bad bundle: object header %q", strings.TrimSpace(line))
		}
		typ := objects.Type(parts[0])
		if typ != objects.Blob && typ != objects.Tree && typ != objects.Commit {
			return ids, fmt.Errorf("bad bundle: unknown object type %q", parts[0])
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || size < 0 || maxBytes > 0 && size > maxBytes {
			return ids, fmt.Errorf("bad bundle: object size %q", parts[1])
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(br, data); err != nil {
			return ids, fmt.Errorf("bad bundle: %w", unwrapLimit(err))
		}
		id, err := store.Write(typ, data)
		if err != nil {
			return ids, err
		}
		if id != parts[2] {
			return ids, fmt.Errorf("bad bundle: object %s has content for %s", parts[2], id)
		}
		ids = append(ids, id)
	}
}

type limited struct {
	r    io.Reader
	left int64
}

func (l *limited) Read(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, ErrTooBig
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	return n, err
}

func unwrapLimit(err error) error {
	if errors.Is(err, ErrTooBig) {
		return ErrTooBig
	}
	return err
}
