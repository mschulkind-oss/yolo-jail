package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// Every approve outcome is a one-line headline, indented facts, and the revoke command alone
// on its own indented line, one per entry.
func TestApproveOutcomesAreShortLines(t *testing.T) {
	const id = "r-d3c8000000000000000000000000000a"
	expires := time.Date(2026, 11, 2, 18, 0, 0, 0, time.UTC)
	issue := brokerstore.WorkingEntry{ID: "w-f8a1", Workspace: "/home/matt/code/yolo-jail", Repo: "github.com/mschulkind-oss/yolo-jail",
		Kind: brokerstore.WorkingIssue, Resource: "#70", Expires: expires}
	for _, c := range []struct {
		name      string
		print     func(*bytes.Buffer)
		headline  string
		want, not []string
		revoke    string
	}{
		{"--for", func(b *bytes.Buffer) {
			approveTimedOutcome(b, id, githubEntryName(issue), []brokerstore.WorkingEntry{issue})
		}, "", []string{"issue mschulkind-oss/yolo-jail#70, unasked, for a while.", "2026-11-02T18:00:00Z", "every jail of /home/matt/code/yolo-jail"},
			[]string{"github.com/"}, "w-f8a1"},
		{"once", func(b *bytes.Buffer) {
			approveOnceOutcome(b, id, brokerstore.Grant{ID: "g-1", Workspace: "/w", Digest: "d", Uses: 1, Expires: expires})
		}, "Allowed " + id + ": this exact command, once.", []string{"until it is used, or", "every jail of /w"}, nil, "g-1"},
		{"git --for", func(b *bytes.Buffer) {
			approveTimedOutcome(b, id, "pushes to refs/heads/a, refs/heads/b",
				[]brokerstore.WorkingEntry{{ID: "w-a", Workspace: "/w", Expires: expires}, {ID: "w-b", Workspace: "/w", Expires: expires}})
		}, "Allowed " + id + ": pushes to refs/heads/a, refs/heads/b, unasked, for a while.",
			[]string{"\n      yolo approve --revoke w-b\n"}, nil, "w-a"},
	} {
		var b bytes.Buffer
		c.print(&b)
		out := b.String()
		lines := strings.Split(out, "\n")
		if c.headline != "" && lines[0] != c.headline {
			t.Errorf("%s: headline %q, want %q", c.name, lines[0], c.headline)
		}
		for _, l := range lines[1:] {
			if l != "" && !strings.HasPrefix(l, "  ") {
				t.Errorf("%s: a fact is not indented: %q\n%s", c.name, l, out)
			}
			if len(l) > 100 {
				t.Errorf("%s: a line runs %d characters: %q", c.name, len(l), l)
			}
		}
		if !strings.Contains(out, "\n\n      yolo approve --revoke "+c.revoke+"\n") {
			t.Errorf("%s: the revoke command is not alone on its own line:\n%s", c.name, out)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: lacks %q:\n%s", c.name, w, out)
			}
		}
		for _, n := range c.not {
			if strings.Contains(out, n) {
				t.Errorf("%s: says %q:\n%s", c.name, n, out)
			}
		}
	}
}
