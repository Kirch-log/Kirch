package repo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/Kirch-log/kirch/internal/protocol"
	"github.com/Kirch-log/kirch/internal/refs"
)

// Credentials for a Kirch server come from the environment, never from
// files in the repository: KIRCH_USER and KIRCH_TOKEN (a personal access
// token made on the website), sent as HTTP basic auth like GitHub.
const (
	envUser  = "KIRCH_USER"
	envToken = "KIRCH_TOKEN"
)

// httpClient is shared; transfers can be large, so the timeout is generous.
var httpClient = &http.Client{Timeout: 10 * time.Minute}

// httpEnd is a repository on a Kirch server.
type httpEnd struct {
	base string // https://host/owner/repo
	user string
}

func newHTTPEnd(loc string) (*httpEnd, error) {
	u, err := url.Parse(loc)
	if err != nil {
		return nil, err
	}
	user := os.Getenv(envUser)
	if name := u.User.Username(); name != "" {
		user = name
	}
	u.User = nil
	return &httpEnd{base: u.String(), user: user}, nil
}

func (h *httpEnd) location() string { return h.base }

func (h *httpEnd) do(method, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, h.base+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("User-Agent", "kirch")
	if token := os.Getenv(envToken); token != "" {
		user := h.user
		if user == "" {
			user = "token"
		}
		req.SetBasicAuth(user, token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("can't reach %s: %w", h.base, err)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, h.failure(resp)
	}
	return resp, nil
}

// failure turns an error response into a readable error.
func (h *httpEnd) failure(resp *http.Response) error {
	var e protocol.Error
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if json.Unmarshal(data, &e) != nil || e.Error == "" {
		e.Error = http.StatusText(resp.StatusCode)
	}
	switch {
	case e.Code == protocol.CodeNonFastForward:
		return ErrNonFastForward
	case e.Code == protocol.CodeStale:
		return refs.ErrStale
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%s: %s (set %s and %s)", h.base, e.Error, envUser, envToken)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%s: repository not found (or you can't see it)", h.base)
	}
	return fmt.Errorf("%s: %s", h.base, e.Error)
}

func (h *httpEnd) refs() (*protocol.Refs, error) {
	resp, err := h.do(http.MethodGet, protocol.RefsPath, "", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out protocol.Refs
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: bad answer from server: %w", h.base, err)
	}
	for b, id := range out.Branches {
		if refs.CheckBranchName(b) != nil || !validID(id) {
			return nil, fmt.Errorf("%s: server sent a bad branch %q → %q", h.base, b, id)
		}
	}
	if out.Branches == nil {
		out.Branches = map[string]string{}
	}
	return &out, nil
}

func (h *httpEnd) head() (string, error) {
	r, err := h.refs()
	if err != nil {
		return "", err
	}
	return r.Head, nil
}

func (h *httpEnd) branches() (map[string]string, error) {
	r, err := h.refs()
	if err != nil {
		return nil, err
	}
	return r.Branches, nil
}

func (h *httpEnd) fetch(dst *Repo, want, have []string) error {
	body, err := json.Marshal(protocol.FetchRequest{Want: want, Have: have})
	if err != nil {
		return err
	}
	resp, err := h.do(http.MethodPost, protocol.FetchPath, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = protocol.ReadBundle(resp.Body, dst.Objects, 0)
	return err
}

func (h *httpEnd) push(src *Repo, branch, old, new string, have []string) error {
	ids, err := ObjectsToSend(src.Objects, []string{new}, have)
	if err != nil {
		return err
	}
	// Stream the bundle while it's being made.
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(protocol.WriteBundle(pw, src.Objects, ids)) }()
	q := url.Values{"branch": {branch}, "old": {old}, "new": {new}}
	resp, err := h.do(http.MethodPost, protocol.PushPath+"?"+q.Encode(), protocol.BundleType, pr)
	pr.CloseWithError(errors.New("request finished"))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func validID(id string) bool {
	if len(id) != 40 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
