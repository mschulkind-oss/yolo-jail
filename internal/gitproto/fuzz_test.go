package gitproto

import (
	"bytes"
	"strings"
	"testing"
)

// FuzzReadCommands throws arbitrary bytes at the push-command reader, the one parse that stands
// between a jail's push and the per-ref policy (docs/reference/git-broker.md ID-9). The oracle
// checks every accepted command independently of the parser (well-formed ids of one length, a
// ref with no space, NUL or control byte), then re-encodes what was accepted and reads it back:
// a round trip must give the same commands, so the broker forwards what it judged.
//
// Run it for longer than the seed corpus with, for example,
//
//	go test -run XXX -fuzz FuzzReadCommands -fuzztime 30s ./internal/gitproto/
//
// An input that fails is written under testdata/fuzz/FuzzReadCommands/, where `go test` replays
// it from then on; keep it there with the fix.
func FuzzReadCommands(f *testing.F) {
	one := strings.Repeat("1", 40)
	for _, lines := range [][]string{
		{zero + " " + one + " refs/heads/agent/x\x00report-status side-band-64k push-options\n", "", "ci.skip\n", ""},
		{one + " " + zero + " refs/heads/old\x00report-status\n", ""},
		{""},
		{"shallow " + one + "\n", zero + " " + one + " refs/tags/v1\x00atomic\n", ""},
		{"push-cert\x00report-status\n", ""},
	} {
		var b bytes.Buffer
		w := NewWriter(&b)
		for _, l := range lines {
			if l == "" {
				_ = w.Flush()
			} else {
				_ = w.Line(l)
			}
		}
		f.Add(b.Bytes())
	}
	f.Add([]byte("0003"))
	f.Add([]byte("ffff"))
	f.Fuzz(func(t *testing.T, data []byte) {
		cmds, caps, opts, err := ReadCommands(NewReader(bytes.NewReader(data)))
		if err != nil {
			return
		}
		for _, c := range cmds {
			if !oracleID(c.Old) || !oracleID(c.New) || len(c.Old) != len(c.New) {
				t.Fatalf("accepted ids %q %q", c.Old, c.New)
			}
			if c.Ref == "" {
				t.Fatal("accepted an empty ref")
			}
			for _, r := range c.Ref {
				if r <= 0x20 || r == 0x7f {
					t.Fatalf("accepted ref %q", c.Ref)
				}
			}
		}
		var b bytes.Buffer
		if err := WriteCommands(NewWriter(&b), cmds, caps, opts); err != nil {
			t.Fatalf("re-encoding an accepted push: %v", err)
		}
		cmds2, _, opts2, err := ReadCommands(NewReader(&b))
		if err != nil || len(cmds2) != len(cmds) || len(opts2) != len(opts) {
			t.Fatalf("round trip: %v %v vs %v", err, cmds2, cmds)
		}
		for i := range cmds {
			if cmds[i] != cmds2[i] {
				t.Fatalf("round trip changed %+v to %+v", cmds[i], cmds2[i])
			}
		}
	})
}

// oracleID is the fuzz oracle's own reading of an object id: 40 or 64 lower-case hex digits.
func oracleID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}
