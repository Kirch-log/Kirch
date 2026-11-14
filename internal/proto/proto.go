// Package proto is kirch://, Kirch's own push / pull protocol over TCP + TLS.
// The wire format is described in docs/protocol.md.
package proto

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Kirch-log/kirch/internal/objects"
)

const (
	// Version is the protocol version this build speaks.
	Version = 1
	// DefaultPort is used when a kirch:// URL has no port.
	DefaultPort = 9418
	// MaxFrame is the largest frame (type byte + payload) accepted.
	MaxFrame = 64 << 20
	// IdleTimeout closes a connection that sends nothing for this long.
	IdleTimeout = 60 * time.Second
)

// MsgType is the first byte of every frame.
type MsgType byte

const (
	Hello  MsgType = 1
	Refs   MsgType = 2
	Want   MsgType = 3
	Obj    MsgType = 4
	Done   MsgType = 5
	Update MsgType = 6
	OK     MsgType = 7
	Err    MsgType = 8
)

func (t MsgType) String() string {
	switch t {
	case Hello:
		return "HELLO"
	case Refs:
		return "REFS"
	case Want:
		return "WANT"
	case Obj:
		return "OBJ"
	case Done:
		return "DONE"
	case Update:
		return "UPDATE"
	case OK:
		return "OK"
	case Err:
		return "ERR"
	}
	return "type " + strconv.Itoa(int(t))
}

// Operations a HELLO can ask for.
const (
	OpFetch = "fetch"
	OpPush  = "push"
)

// Messages (JSON payloads).
type (
	HelloMsg struct {
		Version int    `json:"version"`
		Repo    string `json:"repo"`
		Op      string `json:"op"`
	}
	RefsMsg struct {
		Head     string            `json:"head"`
		Branches map[string]string `json:"branches"`
	}
	WantMsg struct {
		Wants []string `json:"wants"`
		Haves []string `json:"haves"`
	}
	DoneMsg struct {
		Count int `json:"count"`
	}
	UpdateMsg struct {
		Branch string `json:"branch"`
		Old    string `json:"old"`
		New    string `json:"new"`
	}
	OKMsg struct{}
)

// Error codes sent in ERR frames.
const (
	CodeBadRequest     = "bad_request"
	CodeVersion        = "version"
	CodeNotFound       = "not_found"
	CodeMissingObjects = "missing_objects"
	CodeNonFastForward = "non_fast_forward"
	CodeStale          = "stale"
	CodeInternal       = "internal"
)

// Error is an ERR frame: the other side refused.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Errorf makes an *Error.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Conn reads and writes frames on a connection.
type Conn struct {
	nc   net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
	Idle time.Duration
}

// NewConn wraps nc.
func NewConn(nc net.Conn) *Conn {
	return &Conn{nc: nc, r: bufio.NewReaderSize(nc, 64<<10), w: bufio.NewWriterSize(nc, 64<<10), Idle: IdleTimeout}
}

// Close closes the connection.
func (c *Conn) Close() error { return c.nc.Close() }

// WriteFrame buffers one frame. Call Flush to send.
func (c *Conn) WriteFrame(t MsgType, payload []byte) error {
	if len(payload)+1 > MaxFrame {
		return fmt.Errorf("frame too big (%d bytes, max %d)", len(payload)+1, MaxFrame)
	}
	c.nc.SetWriteDeadline(time.Now().Add(c.Idle))
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[:4], uint32(len(payload)+1))
	hdr[4] = byte(t)
	if _, err := c.w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := c.w.Write(payload)
	return err
}

// Flush sends buffered frames.
func (c *Conn) Flush() error {
	c.nc.SetWriteDeadline(time.Now().Add(c.Idle))
	return c.w.Flush()
}

// ReadFrame reads the next frame.
func (c *Conn) ReadFrame() (MsgType, []byte, error) {
	c.nc.SetReadDeadline(time.Now().Add(c.Idle))
	var hdr [4]byte
	if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > MaxFrame {
		return 0, nil, Errorf(CodeBadRequest, "bad frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	return MsgType(buf[0]), buf[1:], nil
}

// Send buffers a JSON message.
func (c *Conn) Send(t MsgType, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteFrame(t, data)
}

// SendError sends an ERR frame and flushes.
func (c *Conn) SendError(e *Error) error {
	if err := c.Send(Err, e); err != nil {
		return err
	}
	return c.Flush()
}

// Expect reads one frame of type t into v. An ERR frame comes back as *Error.
func (c *Conn) Expect(t MsgType, v any) error {
	got, payload, err := c.ReadFrame()
	if err != nil {
		return err
	}
	return decode(t, got, payload, v)
}

func decode(want, got MsgType, payload []byte, v any) error {
	if got == Err {
		e := &Error{}
		if err := json.Unmarshal(payload, e); err != nil || e.Message == "" {
			return Errorf(CodeBadRequest, "the other side sent a broken ERR frame")
		}
		return e
	}
	if got != want {
		return Errorf(CodeBadRequest, "expected %s, got %s", want, got)
	}
	if err := json.Unmarshal(payload, v); err != nil {
		return Errorf(CodeBadRequest, "bad %s message: %v", want, err)
	}
	return nil
}

var objCodes = map[objects.Type]byte{objects.Blob: 'b', objects.Tree: 't', objects.Commit: 'c'}

// SendObject buffers an OBJ frame: type code + zlib(content).
func (c *Conn) SendObject(t objects.Type, content []byte) error {
	code, ok := objCodes[t]
	if !ok {
		return fmt.Errorf("unknown object type %q", t)
	}
	var buf bytes.Buffer
	buf.WriteByte(code)
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(content); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return c.WriteFrame(Obj, buf.Bytes())
}

// DecodeObject reads an OBJ payload.
func DecodeObject(payload []byte) (objects.Type, []byte, error) {
	if len(payload) < 1 {
		return "", nil, Errorf(CodeBadRequest, "empty OBJ frame")
	}
	var t objects.Type
	for typ, code := range objCodes {
		if code == payload[0] {
			t = typ
		}
	}
	if t == "" {
		return "", nil, Errorf(CodeBadRequest, "unknown object type code %q", payload[0])
	}
	zr, err := zlib.NewReader(bytes.NewReader(payload[1:]))
	if err != nil {
		return "", nil, Errorf(CodeBadRequest, "bad OBJ compression: %v", err)
	}
	defer zr.Close()
	content, err := io.ReadAll(io.LimitReader(zr, MaxFrame+1))
	if err != nil {
		return "", nil, Errorf(CodeBadRequest, "bad OBJ compression: %v", err)
	}
	if len(content) > MaxFrame {
		return "", nil, Errorf(CodeBadRequest, "object too big")
	}
	return t, content, nil
}

// ReceiveObjects stores OBJ frames until DONE and returns how many arrived.
// The store re-hashes each object, so ids are never taken on trust.
func (c *Conn) ReceiveObjects(dst *objects.Store) (int, error) {
	n := 0
	for {
		t, payload, err := c.ReadFrame()
		if err != nil {
			return n, err
		}
		switch t {
		case Obj:
			typ, content, err := DecodeObject(payload)
			if err != nil {
				return n, err
			}
			if _, err := dst.Write(typ, content); err != nil {
				return n, err
			}
			n++
		case Done:
			var d DoneMsg
			if err := decode(Done, t, payload, &d); err != nil {
				return n, err
			}
			if d.Count != n {
				return n, Errorf(CodeBadRequest, "DONE says %d objects, got %d", d.Count, n)
			}
			return n, nil
		default:
			var ignore struct{}
			return n, decode(Obj, t, payload, &ignore)
		}
	}
}

// SendObjects writes ids from src as OBJ frames, then DONE, and flushes.
func (c *Conn) SendObjects(src *objects.Store, ids []string) error {
	for _, id := range ids {
		t, content, err := src.Read(id)
		if err != nil {
			return err
		}
		if err := c.SendObject(t, content); err != nil {
			return err
		}
	}
	if err := c.Send(Done, DoneMsg{Count: len(ids)}); err != nil {
		return err
	}
	return c.Flush()
}

// URL is a parsed kirch://host[:port]/owner/name.
type URL struct {
	Host  string
	Port  int
	Owner string
	Name  string
}

// Scheme is the URL prefix for Kirch servers.
const Scheme = "kirch://"

// IsURL reports whether s is a kirch:// URL rather than a folder.
func IsURL(s string) bool { return strings.HasPrefix(s, Scheme) }

var namePart = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// CheckRepoName validates one part of owner/name.
func CheckRepoName(part string) error {
	if !namePart.MatchString(part) || strings.Contains(part, "..") {
		return fmt.Errorf("%q is not a valid name (lowercase letters, digits, . _ -; up to 63 characters)", part)
	}
	return nil
}

// SplitRepo checks and splits "owner/name".
func SplitRepo(s string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(s, "/")
	if !ok {
		return "", "", fmt.Errorf("%q is not owner/name", s)
	}
	if err := CheckRepoName(owner); err != nil {
		return "", "", err
	}
	if err := CheckRepoName(name); err != nil {
		return "", "", err
	}
	return owner, name, nil
}

// ParseURL reads kirch://host[:port]/owner/name.
func ParseURL(s string) (*URL, error) {
	if !IsURL(s) {
		return nil, fmt.Errorf("%q is not a kirch:// URL", s)
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(s, Scheme), "/")
	hostport, path, ok := strings.Cut(rest, "/")
	if !ok || hostport == "" {
		return nil, fmt.Errorf("%q: want kirch://host/owner/name", s)
	}
	owner, name, err := SplitRepo(path)
	if err != nil {
		return nil, fmt.Errorf("%q: %w", s, err)
	}
	u := &URL{Host: hostport, Port: DefaultPort, Owner: owner, Name: name}
	if h, p, err := net.SplitHostPort(hostport); err == nil {
		port, err := strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("%q: bad port %q", s, p)
		}
		u.Host, u.Port = h, port
	} else if strings.Contains(hostport, ":") && !strings.HasPrefix(hostport, "[") {
		return nil, fmt.Errorf("%q: bad host %q", s, hostport)
	}
	u.Host = strings.Trim(u.Host, "[]")
	if u.Host == "" {
		return nil, fmt.Errorf("%q: missing host", s)
	}
	return u, nil
}

// Addr is host:port for dialing.
func (u *URL) Addr() string { return net.JoinHostPort(u.Host, strconv.Itoa(u.Port)) }

// Repo is "owner/name".
func (u *URL) Repo() string { return u.Owner + "/" + u.Name }
