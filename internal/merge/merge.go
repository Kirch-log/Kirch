// Package merge combines two versions of a file that grew apart from a
// common base: a line-based three-way merge for text (like git), and a
// net-by-net merge for netlist.json (README, "Circuit files").
package merge

import (
	"bytes"

	"github.com/Kirch-log/kirch/internal/diff"
)

// Text merges ours and theirs line by line. Where both changed the same
// lines differently, both versions are kept between git-style markers
// (<<<<<<< oursLabel / ======= / >>>>>>> theirsLabel). Returns the merged
// text and the number of conflicting regions.
func Text(base, ours, theirs []byte, oursLabel, theirsLabel string) ([]byte, int) {
	b, o, t := diff.SplitLines(base), diff.SplitLines(ours), diff.SplitLines(theirs)
	mo, mt := match(b, o), match(b, t)

	var out bytes.Buffer
	conflicts := 0
	i, io, it := 0, 0, 0
	for {
		// Next base line kept unchanged by both sides: everything before it
		// is one chunk.
		k := i
		for k < len(b) && (mo[k] < 0 || mt[k] < 0) {
			k++
		}
		ko, kt := len(o), len(t)
		if k < len(b) {
			ko, kt = mo[k], mt[k]
		}
		cb, co, ct := b[i:k], o[io:ko], t[it:kt]
		switch {
		case equal(co, cb):
			writeLines(&out, ct)
		case equal(ct, cb), equal(co, ct):
			writeLines(&out, co)
		default:
			conflicts++
			marker(&out, "<<<<<<< "+oursLabel)
			writeLines(&out, co)
			marker(&out, "=======")
			writeLines(&out, ct)
			marker(&out, ">>>>>>> "+theirsLabel)
		}
		if k == len(b) {
			break
		}
		out.WriteString(b[k])
		i, io, it = k+1, ko+1, kt+1
	}
	return out.Bytes(), conflicts
}

// match maps each line of a to the line of b it stays as, or -1.
func match(a, b []string) []int {
	m := make([]int, len(a))
	for i := range m {
		m[i] = -1
	}
	for _, e := range diff.Lines(a, b) {
		if e.Op == diff.Equal {
			m[e.A] = e.B
		}
	}
	return m
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// writeLines writes lines as they are (marker adds a missing "\n" first).
func writeLines(w *bytes.Buffer, lines []string) {
	for _, l := range lines {
		w.WriteString(l)
	}
}

func marker(w *bytes.Buffer, m string) {
	if w.Len() > 0 && w.Bytes()[w.Len()-1] != '\n' {
		w.WriteByte('\n')
	}
	w.WriteString(m + "\n")
}
