package loopholedecl_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/releasematrix"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// TestEveryOfficialBinaryIsOnTheReleaseMatrix is the RELEASE MATRIX's census
// (docs/design/broker-as-a-pack.md §14.4, step 2), over every loophole manifest the packs embed
// carries, rebuilding nothing.
//
// An official pack's `binaries` names files at URLs, and for a program yolo builds itself those
// files come from yolo's own release. So each binary's builds must be exactly BP-D7's platforms
// — read from .goreleaser.yaml, the file the release runs on, rather than copied — each url must
// be the release file BP-D8 names, and each program must be a main package at cmd/<name> that
// does not link the packs embed. A build the release does not produce is §9's named failure
// mode, "supported, missing": `yolo pack install` would fail on a file no release publishes,
// for every user of the pack.
//
// It REPLACED TestNoShippedManifestDeclaresABinaryYet, which refused any official `binaries`
// at all until the release could build one. What it cannot see without building — that each
// sha256 is the digest this tree builds, and that each url names the version being released — is
// checked by the pin tool (tools/pack-binaries) at `just release`, before goreleaser uploads, and
// before PyPI publishes.
func TestEveryOfficialBinaryIsOnTheReleaseMatrix(t *testing.T) {
	entries, err := releasematrix.Manifests(packs.FS)
	if err != nil {
		t.Fatal(err)
	}

	// The census reads the embed itself, and must read ALL of it: every loophole
	// shippedManifestHome records (held total over the embed by TestShippedManifestHomeIsTotal),
	// and each from the pack that table says it lives in. A walk that missed one would leave
	// its binaries off the matrix with this test green.
	var read, want []string
	for _, e := range entries {
		read = append(read, e.Loophole)
		if home := shippedManifestHome[e.Loophole]; home != e.Pack {
			t.Errorf("the census read %s from packs/%s, and shippedManifestHome says packs/%s",
				e.Loophole, e.Pack, home)
		}
	}
	for name := range shippedManifestHome {
		want = append(want, name)
	}
	sort.Strings(read)
	sort.Strings(want)
	if len(read) == 0 || len(read) != len(want) {
		t.Fatalf("the census read the loopholes %v, and the embed ships %v", read, want)
	}

	cfg, err := os.ReadFile(filepath.Join("..", "..", releasematrix.GoreleaserConfig))
	if err != nil {
		t.Fatal(err)
	}
	release, err := releasematrix.ReleasePlatforms(cfg)
	if err != nil {
		t.Fatal(err)
	}
	official := releasematrix.WithBinaries(entries)
	for _, p := range releasematrix.Census(filepath.Join("..", ".."), official, release) {
		t.Error(p)
	}
	n := 0
	for _, e := range official {
		n += len(e.Manifest.Binaries)
	}
	t.Logf("%d official binaries, in %d manifests, against the release platforms %v", n,
		len(official), release)
}
