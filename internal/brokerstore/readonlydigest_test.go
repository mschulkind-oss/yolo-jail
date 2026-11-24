package brokerstore

import "testing"

// A read-only request has no write to bind its original invocation through, so the
// digest binds it itself: the replay argv is what an approval re-classifies.
func TestReadOnlyRequestDigestBindsItsOriginalInvocation(t *testing.T) {
	read := Request{Argv: []string{"query", "one"}, OriginalArgv: []string{"q", "one"}, Repo: "local-tasks", StdinDigest: "body",
		CallReads: []CallRead{{Chain: []CallResource{{Type: "task", ID: "a1"}}}}}
	other := read
	other.OriginalArgv = []string{"q", "two"}
	if CommandDigest(read) == CommandDigest(other) {
		t.Fatal("a read-only digest ignores the original invocation it replays")
	}
}
