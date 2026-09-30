package packsrc

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// TestMain arms the git configuration tripwire (testsupport.ArmGitConfigTripwire) for the
// whole package, so a fixture here that reads the machine's git configuration instead of
// testsupport.HermeticGitEnv fails on every machine, not only on one that signs its commits.
func TestMain(m *testing.M) {
	testsupport.ArmGitConfigTripwire()
	os.Exit(m.Run())
}

// TestGitConfigTripwireIsArmedHere pins the TestMain call above: without it a fixture that
// reads the machine's git configuration passes again on every machine that does not sign.
func TestGitConfigTripwireIsArmedHere(t *testing.T) {
	if !testsupport.GitConfigTripwireArmed() {
		t.Fatal("the git configuration tripwire is not armed; this package's TestMain must call testsupport.ArmGitConfigTripwire")
	}
}
