package listeners

import (
	"net/netip"
	"strings"
	"testing"
)

// describe_test.go pins the TRI-STATE of the prose renderers, which is the reason
// they are in this package rather than in either caller. Every test below is an
// unowned socket, because an owned one has only one right answer and the unowned
// case has three — and collapsing any two of them is how a held port reads as free.

func unownedSocket() Listener {
	return Listener{
		Kind:  KindTCP,
		Addr:  netip.MustParseAddr("127.0.0.1"),
		Port:  8214,
		Inode: 318411330,
		UID:   1000,
	}
}

// ARM 1: attribution never ran. The socket's owner is not unknown, it was not
// asked for — and the sentence has to name the gap, because "not looked up" with
// no reason is indistinguishable from a bug in the walk.
func TestDescribeOwnersSaysWhenAttributionNeverRan(t *testing.T) {
	snap := Snapshot{
		Gaps: []Gap{{Source: "attribution", Reason: "skipped by Options.SkipAttribution"}},
	}
	got := DescribeOwners(unownedSocket(), snap)
	for _, want := range []string{"socket inode 318411330", "not looked up", "skipped by Options"} {
		if !strings.Contains(got, want) {
			t.Errorf("DescribeOwners = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "no readable /proc") {
		t.Errorf("a walk that never ran was reported as an exhaustive one: %q", got)
	}
}

// ARM 2: it ran and could not finish. The owner may simply not have been reached,
// so the counters are in the sentence — they are what tells the reader whether to
// re-run with a bigger budget or to look in another namespace.
func TestDescribeOwnersSaysWhenTheWalkWasIncomplete(t *testing.T) {
	snap := Snapshot{
		AttributionRan: true,
		PIDsFound:      412,
		PIDsScanned:    99,
		PIDsUnreadable: 313,
		Capped:         true,
	}
	got := DescribeOwners(unownedSocket(), snap)
	for _, want := range []string{"could not be identified", "99 of 412", "capped=true"} {
		if !strings.Contains(got, want) {
			t.Errorf("DescribeOwners = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "another namespace's process") {
		t.Errorf("an incomplete walk asserted where the holder is not: %q", got)
	}
}

// ARM 3: it ran exhaustively and found nothing. This is the ONLY arm allowed to say
// where the holder is not, and it still does not say the port is free — the socket
// is in the table.
func TestDescribeOwnersConcludesOnlyFromAnExhaustiveWalk(t *testing.T) {
	snap := Snapshot{AttributionRan: true, PIDsFound: 412, PIDsScanned: 412}
	got := DescribeOwners(unownedSocket(), snap)
	for _, want := range []string{"no readable /proc/<pid>/fd", "another namespace's process"} {
		if !strings.Contains(got, want) {
			t.Errorf("DescribeOwners = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "could not be identified") {
		t.Errorf("an exhaustive walk hedged: %q", got)
	}
}

// An owned socket names pid, comm and argv. comm is truncated to 15 bytes by the
// kernel, so for anything behind a wrapper the argv is the only thing that
// identifies the holder.
func TestDescribeOwnersNamesEveryHolder(t *testing.T) {
	l := unownedSocket()
	l.Owners = []Owner{
		{PID: 41, Comm: "socat", Cmdline: "socat TCP-LISTEN:8214,bind=127.0.0.1 UNIX:/run/x"},
		{PID: 42, Comm: "socat"},
	}
	got := DescribeOwners(l, Snapshot{AttributionRan: true})
	for _, want := range []string{"held by pid 41", "(socat)", "TCP-LISTEN:8214", "held by pid 42"} {
		if !strings.Contains(got, want) {
			t.Errorf("DescribeOwners = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "socket inode") {
		t.Errorf("an owned socket fell through to an unowned arm: %q", got)
	}
}

// A gapless snapshot that still could not answer must say so rather than return an
// empty string: an empty reason inside "could not be identified: " is the silence
// this package exists to delete.
func TestDescribeGapsNeverReturnsNothing(t *testing.T) {
	got := DescribeGaps(Snapshot{})
	if strings.TrimSpace(got) == "" {
		t.Fatal("DescribeGaps returned nothing for a gapless snapshot")
	}
	if !strings.Contains(got, "no stated reason") {
		t.Errorf("DescribeGaps = %q, want the no-stated-reason answer", got)
	}
}

func TestDescribeGapsNamesEverySource(t *testing.T) {
	snap := Snapshot{Gaps: []Gap{
		{Source: "net/tcp", Reason: "open: permission denied"},
		{Source: "net/tcp6", Reason: "open: no such file or directory"},
	}}
	got := DescribeGaps(snap)
	for _, want := range []string{"net/tcp: open: permission denied", "net/tcp6"} {
		if !strings.Contains(got, want) {
			t.Errorf("DescribeGaps = %q, missing %q", got, want)
		}
	}
}

// The argv crosses a line-framed readiness pipe on one caller's path, and it is an
// arbitrary process's argv on every caller's path.
func TestBoundArgvIsSingleLineAndBounded(t *testing.T) {
	argv := "socat TCP-LISTEN:8214,bind=127.0.0.1 a\nb " + strings.Repeat("x", 2000)
	got := BoundArgv(argv)
	if strings.ContainsAny(got, "\n\x00") {
		t.Errorf("BoundArgv left a newline or NUL in %q", got)
	}
	if !strings.Contains(got, "TCP-LISTEN:8214,bind=127.0.0.1") {
		t.Errorf("BoundArgv dropped the argument that identifies the holder: %q", got)
	}
	if len(got) > maxArgv+16 {
		t.Errorf("BoundArgv returned %d bytes; the readiness record is one line", len(got))
	}
}
