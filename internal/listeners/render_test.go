package listeners

import (
	"strings"
	"testing"
)

// The rendering's job is that a reader cannot mistake one verdict for another, so
// each case asserts the DISTINGUISHING text.
func TestRenderSaysWhichVerdictItIs(t *testing.T) {
	cases := []struct {
		name     string
		snap     Snapshot
		want     []string
		unwanted []string
	}{
		{
			name: "unavailable",
			snap: CollectFrom(&mapSource{}, Options{}),
			want: []string{"UNAVAILABLE", "gaps:", "net/tcp"},
			// The word that would make a reader conclude the port is free.
			unwanted: []string{"none ("},
		},
		{
			name:     "nothing listening",
			snap:     CollectFrom(emptyTables(), Options{}),
			want:     []string{"none (all 3 tables read cleanly)"},
			unwanted: []string{"UNAVAILABLE", "PARTIAL"},
		},
		{
			name: "partial tables",
			snap: func() Snapshot {
				m := emptyTables()
				m.failFiles = map[string]bool{"net/tcp6": true}
				m.files["net/tcp"] = tcpTable(listenerOn8214)
				return CollectFrom(m, Options{SkipAttribution: true})
			}(),
			want: []string{"PARTIAL (1 found, 2 of 3 tables read)", "127.0.0.1:8214", "attribution: not run"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Render(c.snap)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, u := range c.unwanted {
				if strings.Contains(got, u) {
					t.Errorf("must not contain %q in:\n%s", u, got)
				}
			}
		})
	}
}

func TestRenderTableNamesTheOwnerAndMarksTheUnattributed(t *testing.T) {
	m := oneSocket()
	m.files["net/unix"] = unixTable(unixListening) // nobody in /proc holds this one
	withProcess(m, "17", "socat", "socat\x00TCP-LISTEN:8214,fork\x00", map[string]string{"3": "socket:[4242]"})

	out := Render(CollectFrom(m, Options{}))
	if !strings.Contains(out, "127.0.0.1:8214") || !strings.Contains(out, "socat (socat TCP-LISTEN:8214,fork)") {
		t.Errorf("want the collision and its owner's argv:\n%s", out)
	}
	if !strings.Contains(out, "17") {
		t.Errorf("want the owning pid:\n%s", out)
	}
	// The unix socket has no owner, and the walk was exhaustive: that cell must
	// read "nobody holds it", not "unknown". Blank would be worse still — in a
	// table with continuation rows, blank means "same as above".
	unixLine := lineContaining(out, "/tmp/cc-socks/2.sock")
	if !strings.HasSuffix(strings.TrimRight(unixLine, " "), "none") {
		t.Errorf("owner-less row under a COMPLETE walk must say none: %q", unixLine)
	}
	if !strings.Contains(out, "attribution: complete") {
		t.Errorf("every process was readable here, so attribution is complete:\n%s", out)
	}

	// The same socket under a walk that could not finish must NOT say "none".
	m.dirs["."] = append(m.dirs["."], "99")
	m.failDirs = map[string]bool{"99/fd": true}
	partial := Render(CollectFrom(m, Options{}))
	unixLine = lineContaining(partial, "/tmp/cc-socks/2.sock")
	if !strings.HasSuffix(strings.TrimRight(unixLine, " "), "?") {
		t.Errorf("owner-less row under a PARTIAL walk must read unknown, not none: %q", unixLine)
	}
	if !strings.Contains(partial, "attribution: PARTIAL") {
		t.Errorf("an unreadable process must make the footer say partial:\n%s", partial)
	}
}

// Two owners of ONE socket continue the row: a second row repeating the inode would
// read as a second socket, which is the misreading that cost four hypotheses.
func TestRenderContinuesTheRowForASecondOwner(t *testing.T) {
	m := oneSocket()
	withProcess(m, "4", "parent", "socat\x00", map[string]string{"3": "socket:[4242]"})
	withProcess(m, "9", "child", "socat\x00", map[string]string{"3": "socket:[4242]"})

	out := Render(CollectFrom(m, Options{}))
	if n := strings.Count(out, "4242"); n != 1 {
		t.Errorf("inode 4242 appears %d times, want 1 (one socket, two owners):\n%s", n, out)
	}
	if n := strings.Count(out, "127.0.0.1:8214"); n != 1 {
		t.Errorf("address appears %d times, want 1:\n%s", n, out)
	}
	for _, pid := range []string{"4", "9"} {
		if !strings.Contains(out, pid) {
			t.Errorf("missing owner pid %s:\n%s", pid, out)
		}
	}
}

func TestRenderReportsAnExhaustedBudget(t *testing.T) {
	m := oneSocket()
	withProcess(m, "4", "holder", "holder\x00", map[string]string{"3": "socket:[4242]"})
	withProcess(m, "9", "holder", "holder\x00", map[string]string{"3": "socket:[4242]"})

	out := Render(CollectFrom(m, Options{MaxPIDs: 1}))
	if !strings.Contains(out, "BUDGET REACHED") {
		t.Errorf("a capped walk must say so, or a missing owner reads as absent:\n%s", out)
	}
	if !strings.Contains(out, "fd-scan") {
		t.Errorf("want the gap naming the cut-short phase:\n%s", out)
	}
}

func TestRenderTruncatesALongArgvSoTheTableFitsAPaste(t *testing.T) {
	long := strings.Repeat("x", 500)
	out := Render(Snapshot{
		TablesAttempted: 3, TablesRead: 3, AttributionRan: true,
		Sockets: []Listener{{Kind: KindTCP, Port: 1, Inode: 1, Owners: []Owner{{PID: 1, Comm: "c", Cmdline: long}}}},
	})
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 120 {
			t.Fatalf("line of %d chars would not survive a paste: %q", len(line), line)
		}
	}
	if !strings.Contains(out, "...") {
		t.Errorf("a truncated argv must be marked as truncated:\n%s", out)
	}
}

func TestProcessTextPrefersWhateverIdentifiesTheProcess(t *testing.T) {
	cases := []struct {
		owner Owner
		want  string
	}{
		{Owner{Comm: "socat", Cmdline: "socat -d"}, "socat (socat -d)"},
		{Owner{Comm: "kthreadd"}, "kthreadd"},
		{Owner{Comm: "same", Cmdline: "same"}, "same"},
		{Owner{Cmdline: "no-comm --flag"}, "no-comm --flag"},
		{Owner{}, "?"},
	}
	for _, c := range cases {
		if got := processText(c.owner); got != c.want {
			t.Errorf("processText(%+v) = %q, want %q", c.owner, got, c.want)
		}
	}
}

func lineContaining(s, substr string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, substr) {
			return line
		}
	}
	return ""
}
