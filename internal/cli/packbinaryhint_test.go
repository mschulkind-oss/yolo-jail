package cli

// Between releases main pins its own build of each official program while each url still names
// the last release (docs/design/broker-as-a-pack.md BP-D15), so `yolo pack install` of a yolo
// installed from a checkout that changed a program meets the release's file failing its digest.
// That stop names the step back: `just install` in that checkout, which builds the program into
// the cache with no download. Only for an official pack, whose manifest is the checkout's, and
// only when this yolo was installed from one.
//
// Mutation check: drop the fromSourceHint call in fetchPackBinaries and this test fails.

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

func TestPackInstallNamesJustInstallForAFromSourceOfficialBuild(t *testing.T) {
	url := serveBinary(t, []byte("the last release's build"))
	pinned := sha256Hex([]byte("this tree's build"))
	binaryPackHome(t, url, pinned)
	p, problems := packload.LoadDir(filepath.Join(os.Getenv("HOME"), "packs", "toolpack"), "toolpack")
	if len(problems) > 0 {
		t.Fatalf("loading the fixture pack: %v", problems)
	}
	prev := version.SourceDir
	t.Cleanup(func() { version.SourceDir = prev })

	const checkout = "/home/someone/code/yolo-jail-fork"
	for _, tc := range []struct {
		name      string
		official  bool
		sourceDir string
		hint      bool
	}{
		{"an official pack, from a checkout", true, checkout, true},
		{"a pack the user configured", false, checkout, false},
		{"a yolo not installed from a checkout", true, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.Official, version.SourceDir = tc.official, tc.sourceDir
			var out, errw bytes.Buffer
			rc := fetchPackBinaries([]*packload.Pack{p}, packBinaryFetcher(), runtime.GOOS,
				runtime.GOARCH, richtext.Printer{W: &out}, &errw)
			if rc != 1 || !strings.Contains(errw.String(), pinned) {
				t.Fatalf("install over a mismatched build: rc %d\n%s%s", rc, out.String(), errw.String())
			}
			got := strings.Contains(errw.String(), "run `just install` there") &&
				strings.Contains(errw.String(), checkout)
			if got != tc.hint {
				t.Errorf("hint shown = %v, want %v:\n%s", got, tc.hint, errw.String())
			}
		})
	}
}
