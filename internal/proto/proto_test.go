package proto

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"

	"github.com/Kirch-log/kirch/internal/objects"
)

func TestParseURL(t *testing.T) {
	good := map[string]URL{
		"kirch://localhost/evin/servo-board":      {Host: "localhost", Port: 9418, Owner: "evin", Name: "servo-board"},
		"kirch://example.com:7000/a/b":            {Host: "example.com", Port: 7000, Owner: "a", Name: "b"},
		"kirch://127.0.0.1:9418/team.x/board_v2/": {Host: "127.0.0.1", Port: 9418, Owner: "team.x", Name: "board_v2"},
		"kirch://[::1]:9000/o/r":                  {Host: "::1", Port: 9000, Owner: "o", Name: "r"},
		"kirch://[::1]/o/r":                       {Host: "::1", Port: 9418, Owner: "o", Name: "r"},
	}
	for in, want := range good {
		got, err := ParseURL(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if *got != want {
			t.Errorf("%s: got %+v, want %+v", in, *got, want)
		}
	}
	bad := []string{
		"http://x/a/b",
		"kirch://",
		"kirch://host",
		"kirch://host/onlyowner",
		"kirch://host/a/b/c",
		"kirch://host/../etc",
		"kirch://host/a/..",
		"kirch://host/A/b",
		"kirch://host:0/a/b",
		"kirch://host:99999/a/b",
		"kirch://host:x/a/b",
		"kirch:///a/b",
		"kirch://host/a/-b",
		"kirch://host/a/.hidden",
	}
	for _, in := range bad {
		if _, err := ParseURL(in); err == nil {
			t.Errorf("%s: want error", in)
		}
	}
}

func pipe(t *testing.T) (*Conn, *Conn) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	return NewConn(a), NewConn(b)
}

func TestFramesRoundTrip(t *testing.T) {
	a, b := pipe(t)
	go func() {
		a.Send(Hello, HelloMsg{Version: 1, Repo: "o/r", Op: OpFetch})
		a.SendError(Errorf(CodeStale, "moved"))
	}()
	var h HelloMsg
	if err := b.Expect(Hello, &h); err != nil {
		t.Fatal(err)
	}
	if h.Repo != "o/r" || h.Op != OpFetch || h.Version != 1 {
		t.Fatalf("got %+v", h)
	}
	var ok OKMsg
	err := b.Expect(OK, &ok)
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != CodeStale || pe.Message != "moved" {
		t.Fatalf("got %v, want ERR stale", err)
	}
}

func TestExpectWrongType(t *testing.T) {
	a, b := pipe(t)
	go func() { a.Send(Done, DoneMsg{}); a.Flush() }()
	var r RefsMsg
	err := b.Expect(Refs, &r)
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != CodeBadRequest {
		t.Fatalf("got %v", err)
	}
}

func TestObjectsRoundTrip(t *testing.T) {
	src := objects.NewStore(t.TempDir())
	dst := objects.NewStore(t.TempDir())
	var ids []string
	for _, s := range []string{"one", "two", string(make([]byte, 100000))} {
		id, err := src.Write(objects.Blob, []byte(s))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	a, b := pipe(t)
	errc := make(chan error, 1)
	go func() { errc <- a.SendObjects(src, ids) }()
	n, err := b.ReceiveObjects(dst)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("got %d objects", n)
	}
	for _, id := range ids {
		if !dst.Has(id) {
			t.Errorf("%s not stored", id)
		}
	}
}

func TestReceiveObjectsCountMismatch(t *testing.T) {
	a, b := pipe(t)
	go func() {
		a.SendObject(objects.Blob, []byte("x"))
		a.Send(Done, DoneMsg{Count: 2})
		a.Flush()
	}()
	if _, err := b.ReceiveObjects(objects.NewStore(t.TempDir())); err == nil {
		t.Fatal("want error for wrong DONE count")
	}
}

func TestBadFrames(t *testing.T) {
	for name, hdr := range map[string]uint32{"zero": 0, "huge": MaxFrame + 1} {
		a, b := pipe(t)
		go func() {
			var buf [4]byte
			binary.BigEndian.PutUint32(buf[:], hdr)
			a.nc.Write(buf[:])
		}()
		if _, _, err := b.ReadFrame(); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, _, err := DecodeObject([]byte{'z', 1, 2}); err == nil {
		t.Error("unknown object code: want error")
	}
	if _, _, err := DecodeObject([]byte{'b', 1, 2}); err == nil {
		t.Error("bad zlib: want error")
	}
	if _, _, err := DecodeObject(nil); err == nil {
		t.Error("empty: want error")
	}
}
