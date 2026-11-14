// Package diff compares file versions: a line diff printed as a git-style
// patch, and a wiring diff that reads netlist.json changes as pin moves.
package diff

// Op is what happens to a line.
type Op int

const (
	Equal  Op = iota // line is in both
	Delete           // line only in the old version
	Insert           // line only in the new version
)

// Edit is one step turning a into b. A is the line index in a (Equal,
// Delete), B the line index in b (Equal, Insert).
type Edit struct {
	Op   Op
	A, B int
}

// Lines returns a shortest edit script from a to b (Myers' algorithm, the
// default in git).
func Lines(a, b []string) []Edit {
	// Common prefix and suffix are equal lines; only diff the middle.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var edits []Edit
	for i := 0; i < pre; i++ {
		edits = append(edits, Edit{Equal, i, i})
	}
	for _, e := range myers(a[pre:len(a)-suf], b[pre:len(b)-suf]) {
		edits = append(edits, Edit{e.Op, e.A + pre, e.B + pre})
	}
	for i := 0; i < suf; i++ {
		edits = append(edits, Edit{Equal, len(a) - suf + i, len(b) - suf + i})
	}
	return edits
}

func myers(a, b []string) []Edit {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil
	}
	off := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int
	for d := 0; d <= max; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // step down: insert
			} else {
				x = v[off+k-1] + 1 // step right: delete
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, n, m, off)
			}
		}
	}
	panic("diff: no edit script found")
}

// backtrack walks the saved frontiers from the end back to the start.
func backtrack(trace [][]int, n, m, off int) []Edit {
	var rev []Edit
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[off+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			rev = append(rev, Edit{Equal, x, y})
		}
		if d == 0 {
			break
		}
		if x == prevX {
			y--
			rev = append(rev, Edit{Insert, x, y})
		} else {
			x--
			rev = append(rev, Edit{Delete, x, y})
		}
	}
	edits := make([]Edit, len(rev))
	for i, e := range rev {
		edits[len(rev)-1-i] = e
	}
	return edits
}
