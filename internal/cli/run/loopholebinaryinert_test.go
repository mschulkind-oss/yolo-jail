package run

import (
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// A LAUNCH NEVER FETCHES A PACK BINARY (docs/design/broker-as-a-pack.md BP-D1), so a loophole
// whose declared build is not in the cache would otherwise simply not run on a working backend,
// with nothing said. The inert report says so, names the loophole and its pack, and names the
// one command that fixes it. Deleting the BinaryInertNotes loop in platformInertLines fails this.
func TestAnUnfetchedPackBinaryDrawsAnInertLineNamingPackInstall(t *testing.T) {
	cache := t.TempDir()
	prev := loopholes.BinaryCacheDir
	loopholes.BinaryCacheDir = func() string { return cache }
	t.Cleanup(func() { loopholes.BinaryCacheDir = prev })

	sum := strings.Repeat("d", 64)
	p := writeLoopholePack(t, "acme", "acme-tool", `{
	  "name": "acme-tool", "default_enabled": true, "transport": "none",
	  "binaries": {"toold": {"linux/`+runtime.GOARCH+`": {
	    "url": "https://example.test/toold", "sha256": "`+sum+`"}}},
	  "jail_daemon": {"cmd": ["{jail_binary:toold}"]}
	}`)
	got := inertOutput(t, "podman", p)
	for _, want := range []string{"acme: loophole acme-tool is waiting for its binary toold",
		"yolo pack install"} {
		if !strings.Contains(got, want) {
			t.Errorf("the launch's inert report lacks %q:\n%s", want, got)
		}
	}
}
