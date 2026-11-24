package gitbroker

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
)

func TestScopedApprovalNativePushAskDigest(t *testing.T) {
	old, next := strings.Repeat("a", 40), strings.Repeat("b", 40)
	q := PushAsk("fixture.invalid/team/project", []gitproto.Command{{Old: old, New: next, Ref: "refs/heads/main"}}, []string{"ci.skip"}, []string{"refs/heads/main"})
	wantArgv := []string{"git-receive-pack", old + " " + next + " refs/heads/main", "push-option=ci.skip"}
	if !reflect.DeepEqual(q.Argv, wantArgv) || q.Set != setPush || len(q.NeedsApproval) != 1 || len(q.CallWrites) != 0 || q.StdinDigest != pushStdinDigest {
		t.Fatalf("shipped producer intent changed: %+v", q)
	}
	const frozen = "ab30cc93e3af3c6f5f6e9429f9556744c563ec3b05fd83b810514de836a2da13"
	if q.Digest != "" || brokerstore.CommandDigest(q) != frozen {
		t.Fatalf("canonical PushAsk producer observed, no transmission: unfiled=%q digest=%s", q.Digest, brokerstore.CommandDigest(q))
	}
	broker := &Broker{start: "fixture-start"}
	produced := broker.pushRequest(q.Repo, []gitproto.Command{{Old: old, New: next, Ref: "refs/heads/main"}}, []string{"ci.skip"})
	if !reflect.DeepEqual(produced.Argv, wantArgv) || produced.Digest != frozen || brokerstore.CommandDigest(produced) != frozen {
		t.Fatalf("actual native producer omitted or changed: %+v", produced)
	}
}
