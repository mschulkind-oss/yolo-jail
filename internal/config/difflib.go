package config

import (
	"fmt"
	"sort"
)

// difflib.go ports the slice of Python's difflib that _check_config_changes
// uses: SequenceMatcher over line lists + unified_diff. The diff is shown to a
// human at the interactive config-change prompt, so it mirrors Python's output
// format exactly (header lines, @@ hunk ranges, ' '/'+'/'-' prefixes, 3 lines
// of context). Faithful to CPython Lib/difflib.py (SequenceMatcher,
// get_grouped_opcodes, unified_diff) for the autojunk-disabled small inputs a
// config snapshot produces.

type opcode struct {
	tag            string
	i1, i2, j1, j2 int
}

type seqMatcher struct {
	a, b     []string
	b2j      map[string][]int
	bjunk    map[string]struct{}
	bpopular map[string]struct{}
}

func newSeqMatcher(a, b []string) *seqMatcher {
	m := &seqMatcher{a: a, b: b}
	m.chainB()
	return m
}

// chainB builds b2j and the autojunk popular-element set, mirroring
// SequenceMatcher.__chain_b (isjunk=None; autojunk=True default).
func (m *seqMatcher) chainB() {
	b := m.b
	m.b2j = map[string][]int{}
	for i, elt := range b {
		m.b2j[elt] = append(m.b2j[elt], i)
	}
	m.bjunk = map[string]struct{}{}
	// autojunk: for len(b) >= 200, elements appearing > 1% (plus 1) are popular.
	m.bpopular = map[string]struct{}{}
	n := len(b)
	if n >= 200 {
		ntest := n/100 + 1
		for elt, idxs := range m.b2j {
			if len(idxs) > ntest {
				m.bpopular[elt] = struct{}{}
			}
		}
		for elt := range m.bpopular {
			delete(m.b2j, elt)
		}
	}
}

// findLongestMatch mirrors SequenceMatcher.find_longest_match(alo,ahi,blo,bhi).
func (m *seqMatcher) findLongestMatch(alo, ahi, blo, bhi int) (int, int, int) {
	a, b, b2j := m.a, m.b, m.b2j
	besti, bestj, bestsize := alo, blo, 0
	j2len := map[int]int{}
	for i := alo; i < ahi; i++ {
		newj2len := map[int]int{}
		for _, j := range b2j[a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = newj2len
	}
	// Extend the best match over adjacent junk, then non-junk.
	_ = b
	for besti > alo && bestj > blo && !m.isBJunk(b[bestj-1]) && a[besti-1] == b[bestj-1] {
		besti, bestj, bestsize = besti-1, bestj-1, bestsize+1
	}
	for besti+bestsize < ahi && bestj+bestsize < bhi &&
		!m.isBJunk(b[bestj+bestsize]) && a[besti+bestsize] == b[bestj+bestsize] {
		bestsize++
	}
	for besti > alo && bestj > blo && m.isBJunk(b[bestj-1]) && a[besti-1] == b[bestj-1] {
		besti, bestj, bestsize = besti-1, bestj-1, bestsize+1
	}
	for besti+bestsize < ahi && bestj+bestsize < bhi &&
		m.isBJunk(b[bestj+bestsize]) && a[besti+bestsize] == b[bestj+bestsize] {
		bestsize++
	}
	return besti, bestj, bestsize
}

func (m *seqMatcher) isBJunk(s string) bool {
	_, ok := m.bjunk[s]
	return ok
}

// getMatchingBlocks mirrors SequenceMatcher.get_matching_blocks.
func (m *seqMatcher) getMatchingBlocks() [][3]int {
	la, lb := len(m.a), len(m.b)
	type qentry struct{ alo, ahi, blo, bhi int }
	queue := []qentry{{0, la, 0, lb}}
	var matching [][3]int
	for len(queue) > 0 {
		q := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		i, j, k := m.findLongestMatch(q.alo, q.ahi, q.blo, q.bhi)
		if k > 0 {
			matching = append(matching, [3]int{i, j, k})
			if q.alo < i && q.blo < j {
				queue = append(queue, qentry{q.alo, i, q.blo, j})
			}
			if i+k < q.ahi && j+k < q.bhi {
				queue = append(queue, qentry{i + k, q.ahi, j + k, q.bhi})
			}
		}
	}
	sort.Slice(matching, func(x, y int) bool {
		if matching[x][0] != matching[y][0] {
			return matching[x][0] < matching[y][0]
		}
		if matching[x][1] != matching[y][1] {
			return matching[x][1] < matching[y][1]
		}
		return matching[x][2] < matching[y][2]
	})
	// Collapse adjacent equal blocks.
	var i1, j1, k1 int
	var nonAdjacent [][3]int
	for _, blk := range matching {
		i2, j2, k2 := blk[0], blk[1], blk[2]
		if i1+k1 == i2 && j1+k1 == j2 {
			k1 += k2
		} else {
			if k1 > 0 {
				nonAdjacent = append(nonAdjacent, [3]int{i1, j1, k1})
			}
			i1, j1, k1 = i2, j2, k2
		}
	}
	if k1 > 0 {
		nonAdjacent = append(nonAdjacent, [3]int{i1, j1, k1})
	}
	nonAdjacent = append(nonAdjacent, [3]int{la, lb, 0})
	return nonAdjacent
}

// getOpcodes mirrors SequenceMatcher.get_opcodes.
func (m *seqMatcher) getOpcodes() []opcode {
	i, j := 0, 0
	var answer []opcode
	for _, blk := range m.getMatchingBlocks() {
		ai, bj, size := blk[0], blk[1], blk[2]
		tag := ""
		if i < ai && j < bj {
			tag = "replace"
		} else if i < ai {
			tag = "delete"
		} else if j < bj {
			tag = "insert"
		}
		if tag != "" {
			answer = append(answer, opcode{tag, i, ai, j, bj})
		}
		i, j = ai+size, bj+size
		if size > 0 {
			answer = append(answer, opcode{"equal", ai, i, bj, j})
		}
	}
	return answer
}

// getGroupedOpcodes mirrors SequenceMatcher.get_grouped_opcodes(n=3).
func (m *seqMatcher) getGroupedOpcodes(n int) [][]opcode {
	codes := m.getOpcodes()
	if len(codes) == 0 {
		codes = []opcode{{"equal", 0, 1, 0, 1}}
	}
	// Fixup leading/trailing equal ranges.
	if codes[0].tag == "equal" {
		c := codes[0]
		codes[0] = opcode{c.tag, max(c.i1, c.i2-n), c.i2, max(c.j1, c.j2-n), c.j2}
	}
	last := len(codes) - 1
	if codes[last].tag == "equal" {
		c := codes[last]
		codes[last] = opcode{c.tag, c.i1, min(c.i2, c.i1+n), c.j1, min(c.j2, c.j1+n)}
	}
	nn := n + n
	var groups [][]opcode
	var group []opcode
	for _, c := range codes {
		tag, i1, i2, j1, j2 := c.tag, c.i1, c.i2, c.j1, c.j2
		if tag == "equal" && i2-i1 > nn {
			group = append(group, opcode{tag, i1, min(i2, i1+n), j1, min(j2, j1+n)})
			groups = append(groups, group)
			group = nil
			i1, j1 = max(i1, i2-n), max(j1, j2-n)
		}
		group = append(group, opcode{tag, i1, i2, j1, j2})
	}
	if len(group) > 0 && !(len(group) == 1 && group[0].tag == "equal") {
		groups = append(groups, group)
	}
	return groups
}

// formatRangeUnified mirrors difflib._format_range_unified.
func formatRangeUnified(start, stop int) string {
	beginning := start + 1
	length := stop - start
	if length == 1 {
		return fmt.Sprintf("%d", beginning)
	}
	if length == 0 {
		beginning--
	}
	return fmt.Sprintf("%d,%d", beginning, length)
}

// unifiedDiff mirrors difflib.unified_diff(a, b, fromfile, tofile, lineterm="")
// with n=3. Returns the diff lines (no trailing newlines — lineterm="").
func unifiedDiff(a, b []string, fromfile, tofile string) []string {
	var out []string
	started := false
	sm := newSeqMatcher(a, b)
	for _, group := range sm.getGroupedOpcodes(3) {
		if !started {
			started = true
			out = append(out, "--- "+fromfile)
			out = append(out, "+++ "+tofile)
		}
		first := group[0]
		last := group[len(group)-1]
		file1Range := formatRangeUnified(first.i1, last.i2)
		file2Range := formatRangeUnified(first.j1, last.j2)
		out = append(out, fmt.Sprintf("@@ -%s +%s @@", file1Range, file2Range))
		for _, c := range group {
			if c.tag == "equal" {
				for _, line := range a[c.i1:c.i2] {
					out = append(out, " "+line)
				}
				continue
			}
			if c.tag == "replace" || c.tag == "delete" {
				for _, line := range a[c.i1:c.i2] {
					out = append(out, "-"+line)
				}
			}
			if c.tag == "replace" || c.tag == "insert" {
				for _, line := range b[c.j1:c.j2] {
					out = append(out, "+"+line)
				}
			}
		}
	}
	return out
}
