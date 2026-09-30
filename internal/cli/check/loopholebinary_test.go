package check

import (
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// A loophole the user has on, whose downloaded program is not fetched yet, is WARNED in
// `yolo check` rather than noted: a launch never downloads one (docs/design/broker-as-a-pack.md
// BP-D5), so the loophole stays off until the user runs the command the row names. Deleting the
// UnfetchedBinaryReason branch in checkLoopholes turns the row into a quiet [OK] and fails this.
func TestCheckWarnsAboutALoopholeWaitingForItsBinary(t *testing.T) {
	moduleRoot := isolatedModuleDir(t)
	cache := t.TempDir()
	prev := loopholes.BinaryCacheDir
	loopholes.BinaryCacheDir = func() string { return cache }
	t.Cleanup(func() { loopholes.BinaryCacheDir = prev })

	writeLoopholeManifest(t, moduleRoot, "acme-tool",
		`"name":"acme-tool","description":"d","transport":"none","default_enabled":true,`+
			`"binaries":{"toold":{"`+runtime.GOOS+`/`+runtime.GOARCH+`":{`+
			`"url":"https://example.test/toold","sha256":"`+strings.Repeat("e", 64)+`"}}},`+
			`"doctor_cmd":["{binary:toold}","--self-check"]`)

	r, out := runCheckLoopholes(t, t.TempDir())
	if r.warned != 1 || !strings.Contains(out, "loophole acme-tool: inactive (waiting for its binary toold") ||
		!strings.Contains(out, "yolo pack install") {
		t.Errorf("an unfetched binary was not warned with its fix (warned=%d):\n%s", r.warned, out)
	}
}
