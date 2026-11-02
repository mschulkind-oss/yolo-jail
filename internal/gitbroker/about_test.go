package gitbroker

import (
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// A push request's headline counts its refs and names the remote, never a ref: a ref name
// is the agent's text, so it is in Refs alone, and a remote that is not safe to show
// unescaped is not named.
func TestPushAboutCountsTheRefsAndNamesOnlyASafeRemote(t *testing.T) {
	refs := []string{"refs/heads/feat/skip-<T>-check", "refs/heads/ma\u200bin"}
	a := pushAbout("github.com/o/r", refs)
	if a.Action != brokerstore.ActionPush || a.Target != "github.com/o/r" || a.Headline != "push 2 refs to github.com/o/r" || !slices.Equal(a.Refs, refs) {
		t.Fatalf("%+v", a)
	}
	if a := pushAbout("github.com/o/r", refs[:1]); a.Headline != "push 1 ref to github.com/o/r" {
		t.Fatalf("%+v", a)
	}
	if a := pushAbout("https://h/o/r?a=1&b=<x>", refs); a.Target != "a git remote" || a.Headline != "push 2 refs to a git remote" {
		t.Fatalf("%+v", a)
	}
}
