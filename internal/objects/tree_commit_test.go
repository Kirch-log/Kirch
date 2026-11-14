package objects

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	helloID = "ce013625030ba8dba906f756967f9e9ca394464a" // blob "hello\n"
	emptyID = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" // empty blob
)

// Git sorts folders as if their name ended in "/": "a.txt" < "a/" < "ab".
func TestTreeOrderMatchesGit(t *testing.T) {
	entries := []TreeEntry{
		{ModeFile, "ab", emptyID},
		{ModeDir, "a", "4b825dc642cb6eb9a060e54bf8d69288fbee4904"},
		{ModeFile, "a.txt", helloID},
	}
	data, err := EncodeTree(entries)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseTree(data)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range parsed {
		names = append(names, e.Name)
	}
	if !reflect.DeepEqual(names, []string{"a.txt", "a", "ab"}) {
		t.Fatalf("order = %v", names)
	}
}

// `printf 'hello\n' > hello.txt && git add . && git write-tree`
func TestTreeIDMatchesKnownGit(t *testing.T) {
	data, err := EncodeTree([]TreeEntry{{ModeFile, "hello.txt", helloID}})
	if err != nil {
		t.Fatal(err)
	}
	if got := Hash(Tree, data); got != "aaa96ced2d9a1c8e72c56b253a0e2fe78393feb7" {
		t.Fatalf("tree id = %s", got)
	}
	// The empty tree has a famous id.
	empty, _ := EncodeTree(nil)
	if got := Hash(Tree, empty); got != "4b825dc642cb6eb9a060e54bf8d69288fbee4904" {
		t.Fatalf("empty tree id = %s", got)
	}
}

func TestTreeRejects(t *testing.T) {
	bad := [][]TreeEntry{
		{{ModeFile, "a/b", helloID}},
		{{ModeFile, "", helloID}},
		{{ModeFile, "..", helloID}},
		{{"120000", "link", helloID}},
		{{ModeFile, "a", "nothex"}},
		{{ModeFile, "a", helloID}, {ModeFile, "a", emptyID}},
	}
	for _, entries := range bad {
		if _, err := EncodeTree(entries); err == nil {
			t.Errorf("EncodeTree(%v) succeeded", entries)
		}
	}
}

func TestSignatureRoundTrip(t *testing.T) {
	when := time.Date(2026, 11, 13, 21, 0, 0, 0, time.FixedZone("", -5*3600))
	s := Signature{Name: "Hridhayesh G R", Email: "h@example.com", When: when}
	if got := s.String(); got != "Hridhayesh G R <h@example.com> 1794621600 -0500" {
		t.Fatalf("String = %q", got)
	}
	back, err := ParseSignature(s.String())
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != s.Name || back.Email != s.Email || !back.When.Equal(when) || back.String() != s.String() {
		t.Fatalf("round trip = %+v", back)
	}
	// Half-hour zone (India).
	ist := Signature{Name: "A", Email: "a@b", When: time.Unix(0, 0).In(time.FixedZone("", 5*3600+30*60))}
	if !strings.HasSuffix(ist.String(), " 0 +0530") {
		t.Fatalf("IST = %q", ist.String())
	}
}

func TestCommitRoundTrip(t *testing.T) {
	when := time.Unix(1794621600, 0).In(time.FixedZone("", 0))
	sig := Signature{Name: "A", Email: "a@b", When: when}
	c := CommitData{
		Tree:      "aaa96ced2d9a1c8e72c56b253a0e2fe78393feb7",
		Parents:   []string{helloID},
		Author:    sig,
		Committer: sig,
		Message:   "Move servo to GPIO10",
	}
	data, err := EncodeCommit(c)
	if err != nil {
		t.Fatal(err)
	}
	want := "tree aaa96ced2d9a1c8e72c56b253a0e2fe78393feb7\n" +
		"parent " + helloID + "\n" +
		"author A <a@b> 1794621600 +0000\n" +
		"committer A <a@b> 1794621600 +0000\n" +
		"\nMove servo to GPIO10\n"
	if string(data) != want {
		t.Fatalf("got\n%s\nwant\n%s", data, want)
	}
	back, err := ParseCommit(data)
	if err != nil {
		t.Fatal(err)
	}
	if back.Tree != c.Tree || !reflect.DeepEqual(back.Parents, c.Parents) || back.Message != "Move servo to GPIO10\n" || back.Author.String() != sig.String() {
		t.Fatalf("parsed = %+v", back)
	}
}

func TestCommitRejectsBadSignature(t *testing.T) {
	sig := Signature{Name: "A <x>", Email: "a@b", When: time.Now()}
	_, err := EncodeCommit(CommitData{Tree: helloID, Author: sig, Committer: sig, Message: "m"})
	if err == nil {
		t.Fatal("accepted name with <>")
	}
	empty := Signature{Name: "", Email: "", When: time.Now()}
	if _, err := EncodeCommit(CommitData{Tree: helloID, Author: empty, Committer: empty}); err == nil {
		t.Fatal("accepted empty author")
	}
}

func TestParseCommitSkipsUnknownHeaders(t *testing.T) {
	data := "tree " + helloID + "\nauthor A <a@b> 1 +0000\ncommitter A <a@b> 1 +0000\n" +
		"gpgsig -----BEGIN PGP SIGNATURE-----\n abc\n -----END PGP SIGNATURE-----\n\nmsg\n"
	c, err := ParseCommit([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if c.Message != "msg\n" || c.Tree != helloID {
		t.Fatalf("parsed = %+v", c)
	}
}
