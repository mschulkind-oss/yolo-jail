package render

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// THE PER-KIND HOST ANSWER a briefing's `describes` is gated on (boundary-broker.md BB-D69): a
// kind applies at the host when `yolo host apply` renders it or `yolo host --` delivers it at
// launch, and HostLeavesUndone is the apply's "does not apply" half of the same census. Every
// known kind is covered, so a kind added to either map is decided here or this fails.
func TestHostDeliversIsTheCensusPerKind(t *testing.T) {
	fields := HostFields()
	withheld := map[packdecl.Kind]bool{
		packdecl.KindIntercept: true, // refused, and no host verb delivers it (BB-D17)
		packdecl.KindState:     true,
		packdecl.KindMount:     true,
		packdecl.KindReadsHost: true,
		packdecl.KindHook:      true, // honored, with no renderer behind it
	}
	for _, k := range packdecl.KnownKinds() {
		if got := HostDelivers(fields, k); got == withheld[k] {
			t.Errorf("HostDelivers(%s) = %v, want %v", k, got, !withheld[k])
		}
		_, atLaunch := HostAtLaunch(k)
		if undone := HostLeavesUndone(fields, k); undone != (withheld[k] || (atLaunch && !fields.Honors(k))) {
			t.Errorf("HostLeavesUndone(%s) = %v", k, undone)
		}
	}
}
