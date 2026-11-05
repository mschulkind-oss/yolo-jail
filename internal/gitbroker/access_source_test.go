package gitbroker

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
)

func TestGitAccessPushUnverifiedCandidateHasNoCanonicalCall(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{noRead: true})
	proposal := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{{Old: strings.Repeat("0", 40), New: strings.Repeat("a", 40), Ref: "refs/heads/new"}}, PushOptions: []string{}}
	answer := hostProposal(t, f, "evaluate", proposal)
	if answer["verdict"] != "refused" || answer["source_status"] != "not-read" || answer["call"] != nil || answer["call_unavailable"] == nil || answer["scope_source"] == nil || answer["inputs_digest"] == nil {
		t.Fatalf("unverified reply fabricated a call or lacks source status: %+v", answer)
	}
	if f.sshRan() {
		t.Fatal("no-read proposal contacted remote")
	}
}
