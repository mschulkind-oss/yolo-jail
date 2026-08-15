package cli

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/crossaudit"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// TestMainInstallsTheCrossingAudit guards the one line of wiring that makes the
// boundary audit real. Without it every record is built and thrown away, and the
// only symptom is an audit log nobody notices is empty — so it is asserted here
// rather than left to the two packages that would each still pass their own tests.
//
// `--version` on purpose: it is the cheapest path through Main, and it also pins
// the OTHER half of the contract — installing opens nothing, so an invocation that
// crosses no boundary writes no log and creates no directory.
func TestMainInstallsTheCrossingAudit(t *testing.T) {
	prev := svcendpoint.CrossingSink()
	svcendpoint.SetCrossingSink(nil)
	t.Cleanup(func() { svcendpoint.SetCrossingSink(prev) })

	// A HOME nothing else writes to, so the "creates nothing" assertion below is
	// about this run rather than about whatever the real state dir already holds.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")

	if rc := Main([]string{"yolo", "--version"}); rc != 0 {
		t.Fatalf("yolo --version rc = %d, want 0", rc)
	}
	if svcendpoint.CrossingSink() == nil {
		t.Fatal("cli.Main left no crossing sink installed; the boundary audit " +
			"records nothing in any real yolo process")
	}
	if _, err := os.Lstat(crossaudit.DefaultPath()); err == nil {
		t.Errorf("%s exists after an invocation that crossed no boundary; the "+
			"sink must open lazily, on the first crossing", crossaudit.DefaultPath())
	}
}
