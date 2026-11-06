package image

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestAutoLoadOffloadFailureKeepsBothBuildsWords: when the plain build fails on macOS and the
// offload fails too, the report quotes both. The 2026-11-05 macOS nightly's report kept only the
// offload's "container builder did not start", so neither why the plain build had needed a
// Linux builder (what the cache did not serve) nor why the builder did not start was readable.
func TestAutoLoadOffloadFailureKeepsBothBuildsWords(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	const plainSaid = "error: a 'x86_64-linux' with features {} is required to build '/nix/store/aaa-yolo-jail-root.drv'"
	const offloadSaid = "container builder did not start: could not pull the Linux builder image"
	opts := AutoLoadOptions{
		Runtime: "podman",
		IsMacOS: true,
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"building '/nix/store/aaa-yolo-jail-root.drv'...", plainSaid}
		},
		BuildOffload: func(string, []any, string) (string, []string) {
			return "", []string{offloadSaid}
		},
		Run:       func(argv []string) (int, bool) { return 1, true },
		LookupEnv: denyStaleEnv,
	}
	if AutoLoadImage(opts).OK {
		t.Fatal("AutoLoadImage = true after both builds failed")
	}
	i := strings.Index(out.String(), BuildFailedMarker)
	if i < 0 {
		t.Fatalf("no failure report:\n%s", out.String())
	}
	section := out.String()[i:]
	for _, want := range []string{plainSaid, offloadSaid} {
		if !strings.Contains(section, want) {
			t.Errorf("the failure report does not quote %q:\n%s", want, section)
		}
	}
	if strings.Index(section, plainSaid) > strings.Index(section, offloadSaid) {
		t.Errorf("the report must quote the plain build before the offload it led to:\n%s", section)
	}
}

// TestOffloadFailureTailFitsTheReport: the combined tail stays within the report's line limit
// and always keeps every line of the offload's own reason, trimming the plain build's oldest.
func TestOffloadFailureTailFitsTheReport(t *testing.T) {
	plain := make([]string, 50)
	for i := range plain {
		plain[i] = fmt.Sprintf("plain %d", i)
	}
	got := offloadFailureTail(plain, []string{"offload reason"})
	if len(got) > buildTailTextLimit {
		t.Errorf("combined tail has %d lines, more than the report's %d", len(got), buildTailTextLimit)
	}
	if got[len(got)-1] != "offload reason" || !slices.Contains(got, "plain 49") || slices.Contains(got, "plain 0") {
		t.Errorf("combined tail = %q, want the plain build's newest lines then the offload's reason", got)
	}
	if got := offloadFailureTail(nil, []string{"r"}); !slices.Equal(got, []string{"r"}) {
		t.Errorf("with no plain tail the offload's own is the tail; got %q", got)
	}
}
