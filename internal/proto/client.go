package proto

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/Kirch-log/kirch/internal/objects"
)

// TLSConfig is what clients use: the system's certificate roots plus the
// PEM file in KIRCH_CA_FILE, if set (for a dev server's self-signed cert).
func TLSConfig(host string) (*tls.Config, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if file := os.Getenv("KIRCH_CA_FILE"); file != "" {
		pem, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("KIRCH_CA_FILE: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("KIRCH_CA_FILE: no certificates in %s", file)
		}
	}
	return &tls.Config{RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS12}, nil
}

// Client is one connection to a Kirch server, for one operation.
type Client struct {
	conn *Conn
	// Refs is what the server said it has, right after HELLO.
	Refs RefsMsg
}

// Dial connects to the server in rawURL, says HELLO for op and reads REFS.
func Dial(ctx context.Context, rawURL, op string) (*Client, error) {
	u, err := ParseURL(rawURL)
	if err != nil {
		return nil, err
	}
	cfg, err := TLSConfig(u.Host)
	if err != nil {
		return nil, err
	}
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 15 * time.Second}, Config: cfg}
	nc, err := d.DialContext(ctx, "tcp", u.Addr())
	if err != nil {
		return nil, fmt.Errorf("can't reach %s: %w", u.Addr(), err)
	}
	cl := &Client{conn: NewConn(nc)}
	if err := cl.hello(u.Repo(), op); err != nil {
		nc.Close()
		return nil, err
	}
	return cl, nil
}

func (cl *Client) hello(repo, op string) error {
	if err := cl.conn.Send(Hello, HelloMsg{Version: Version, Repo: repo, Op: op}); err != nil {
		return err
	}
	if err := cl.conn.Flush(); err != nil {
		return err
	}
	if err := cl.conn.Expect(Refs, &cl.Refs); err != nil {
		return err
	}
	if cl.Refs.Branches == nil {
		cl.Refs.Branches = map[string]string{}
	}
	for b, id := range cl.Refs.Branches {
		if !objects.ValidID(id) {
			return Errorf(CodeBadRequest, "server sent bad id for branch %q", b)
		}
	}
	return nil
}

// Close ends the connection.
func (cl *Client) Close() error { return cl.conn.Close() }

// Fetch asks for wants (telling the server about haves) and stores what
// comes back in dst. Returns how many objects arrived.
func (cl *Client) Fetch(dst *objects.Store, wants, haves []string) (int, error) {
	if wants == nil {
		wants = []string{}
	}
	if haves == nil {
		haves = []string{}
	}
	if err := cl.conn.Send(Want, WantMsg{Wants: wants, Haves: haves}); err != nil {
		return 0, err
	}
	if err := cl.conn.Flush(); err != nil {
		return 0, err
	}
	return cl.conn.ReceiveObjects(dst)
}

// Push sends ids from src, then asks the server to move branch old → new.
func (cl *Client) Push(src *objects.Store, ids []string, branch, old, new string) error {
	if err := cl.conn.SendObjects(src, ids); err != nil {
		return err
	}
	if err := cl.conn.Send(Update, UpdateMsg{Branch: branch, Old: old, New: new}); err != nil {
		return err
	}
	if err := cl.conn.Flush(); err != nil {
		return err
	}
	var ok OKMsg
	err := cl.conn.Expect(OK, &ok)
	if errors.Is(err, io.EOF) {
		return errors.New("the server closed the connection before confirming the push")
	}
	return err
}
