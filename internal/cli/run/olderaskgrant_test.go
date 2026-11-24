package run

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

// A grant file and a scope file of one launch carry independently drawn launch ids, so a config
// apply onto a running jail must still recognize the launch's own grant (it was refused as
// "old source-read grant is unknown or changed" for every first apply).
func TestOlderGrantMatchesTheLaunchDespiteItsOwnLaunchID(t *testing.T) {
	sf := brokerscope.File{PID: 7, Workspace: "/w", Container: "c", LaunchID: "scopeid"}
	in := callbrokerinput.Launch{Pack: "aws", ReadGrantStanding: "granted"}
	grant := &loopholegrant.Block{Pack: "aws", Workspace: "/w", Container: "c", LaunchID: "grantid", PID: 7,
		Kinds: map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.Granted}}}
	if !olderGrantMatchesLaunch(grant, in, sf) {
		t.Fatal("the launch's own grant was refused")
	}
	other := *grant
	other.PID = 8
	if olderGrantMatchesLaunch(&other, in, sf) {
		t.Fatal("another launch's grant was accepted")
	}
	if olderGrantMatchesLaunch(nil, in, sf) {
		t.Fatal("a missing grant was accepted")
	}
}
