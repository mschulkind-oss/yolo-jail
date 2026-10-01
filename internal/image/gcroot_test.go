package image

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// recordingRooter is a Rooter that records what it was asked to root.
type rootCall struct{ link, storePath, failMsg string }

func recordingRooter(calls *[]rootCall, err error) Rooter {
	return func(link, storePath string, _ io.Writer, failMsg string) error {
		*calls = append(*calls, rootCall{link, storePath, failMsg})
		return err
	}
}

// Each registration hands its Rooter its OWN directory's link — the property the separate
// functions exist to keep (registerGCRoot's doc) — and a message naming what is unprotected.
func TestEachRegistrationRootsItsOwnLinkThroughTheRooter(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const sp = "/nix/store/0123456789abcdfghijklmnpqrsvwxyz-x"
	var calls []rootCall
	root := recordingRooter(&calls, nil)

	if link, err := RegisterImageRoot(sp, root, nil); err != nil || link != ImageRootLink(sp) {
		t.Errorf("RegisterImageRoot = %q, %v; want %q", link, err, ImageRootLink(sp))
	}
	if link, err := RegisterPrefixRoot(sp, root, nil); err != nil || link != PrefixRootLink(sp) {
		t.Errorf("RegisterPrefixRoot = %q, %v; want %q", link, err, PrefixRootLink(sp))
	}
	if len(calls) != 2 || calls[0].link != ImageRootLink(sp) || calls[1].link != PrefixRootLink(sp) {
		t.Fatalf("the Rooter was asked for %+v", calls)
	}
	if !strings.Contains(calls[0].failMsg, "running image") || !strings.Contains(calls[1].failMsg, "own binaries") {
		t.Errorf("failure messages do not name what is left unprotected: %+v", calls)
	}
	for _, d := range []string{ImageRootsDir(), PrefixRootsDir()} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("%s was not created for the Rooter to write into", d)
		}
	}

	// A Rooter's failure is the registration's: no link is reported.
	failing := recordingRooter(&calls, errors.New("refused"))
	if link, err := RegisterImageRoot(sp, failing, nil); err == nil || link != "" {
		t.Errorf("a failed root reported %q, %v", link, err)
	}
}

// A launch with no root the host would honor passes no Rooter, and nothing is created.
func TestNoRooterRegistersNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const sp = "/nix/store/0123456789abcdfghijklmnpqrsvwxyz-x"
	if link, err := RegisterImageRoot(sp, nil, nil); err == nil || link != "" {
		t.Errorf("RegisterImageRoot with no Rooter = %q, %v", link, err)
	}
	if _, err := os.Stat(ImageRootsDir()); !os.IsNotExist(err) {
		t.Errorf("the roots dir was created for a registration with no Rooter (%v)", err)
	}
}
