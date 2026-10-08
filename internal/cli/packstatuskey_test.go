package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestPackStatusKeyPrintsTheFullBuildLine: `yolo pack status <pack>/<name>` is where a launch's
// digest points (OQ-RO9), so it prints the selected fork's build line whole with the digest the
// launch shows. Driven through packMain, so the dispatch is pinned too.
func TestPackStatusKeyPrintsTheFullBuildLine(t *testing.T) {
	newPatchedFixture(t, "")
	var out, errw bytes.Buffer
	if rc := packMain([]string{"status", "forkpack/tool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	for _, want := range []string{"fork forkpack/tool", "recipe " + packload.BuildLineDigest("sh build.sh"), "    sh build.sh\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("`yolo pack status forkpack/tool` lacks %q:\n%s", want, out.String())
		}
	}
}

// TestPackStatusKeyNamesTheKeysThatExist: a key nothing selected builds is refused with the keys
// that are, and a word that is not a key at all is a usage error naming the shape.
func TestPackStatusKeyNamesTheKeysThatExist(t *testing.T) {
	newPatchedFixture(t, "")
	var out, errw bytes.Buffer
	if rc := packMain([]string{"status", "forkpack/nope"}, &out, &errw, false); rc != 1 ||
		!strings.Contains(errw.String(), "the selected ones are forkpack/tool") {
		t.Errorf("an unknown key: rc=%d\n%s", rc, errw.String())
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"status", "claude"}, &out, &errw, false); rc != 2 ||
		!strings.Contains(errw.String(), "is not a <pack>/<name> key") || out.Len() != 0 {
		t.Errorf("a bare word: rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	errw.Reset()
	if rc := packMain([]string{"status", "a/b", "c"}, &out, &errw, false); rc != 2 ||
		!strings.Contains(errw.String(), `unexpected argument "c"`) {
		t.Errorf("two arguments: rc=%d\n%s", rc, errw.String())
	}
}
