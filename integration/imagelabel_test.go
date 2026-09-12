package integration

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// The OWNER LABEL, end to end — minimal-disk-footprint.md OQ-DF3's REACH half.
//
// WHAT THIS PROTECTS. `PruneOldImages` proves an image is yolo's two ways: the
// repository name, and — since the REACH ruling — a label baked into the image
// config, which is the only evidence that SURVIVES the loss of a tag. The label
// is spelled in Nix (`flake.nix`, `mkOciImage`'s `config.Labels`) and read in Go
// (`prune.JailImageOwnerLabel`), and nothing but agreement between those two
// makes the reap work. `TestOwnerLabelSpellingMatchesTheFlake` in internal/prune
// pins the two SPELLINGS against each other by reading flake.nix; this pins them
// against a REAL, BUILT IMAGE, which is the only place a Nix-side mistake that
// keeps the string intact (a label attached to the wrong attrset, an image
// variant that never gets it, a podman that stores it somewhere else) can show
// up. Same class as internal/entrypoint/shippedclients_test.go, one language
// pair over.
//
// WHY IT MATTERS THAT IT NEVER GOES RED SILENTLY. A drifted key does not break a
// launch and does not fail a unit test: images keep building, jails keep
// starting, and every image built from that moment on is simply unattributable
// forever — the exact class the ruling created the label to stop producing, and
// the class it also ruled yolo may never clean up after the fact.
//
// integration/ had zero prune coverage before this file.
func TestImageOwnerLabel(t *testing.T) {
	requireJail(t)
	rt := detectRuntime()
	if rt == "" {
		t.Skip("no container runtime")
	}
	if rt != "podman" {
		// `--filter label=` is podman's spelling. Apple Container is NOT
		// MEASURED for it, has no image reaper of its own yet (OQ-BF6), and the
		// label probe is written to contribute zero rows rather than fail there.
		t.Skipf("label filtering is not measured on %s", rt)
	}
	ref := imageExists(rt)
	if ref == "" {
		t.Skip("no jail image loaded (ensureJailImage reported it)")
	}

	// 1. The image carries the owner label with the exact value Go filters for.
	got := inspectLabel(t, rt, ref, prune.JailImageOwnerLabel)
	if got != prune.JailImageOwnerValue {
		t.Fatalf("%s has %s=%q, want %q — flake.nix's config.Labels and "+
			"internal/prune have drifted, and every image built from here on is "+
			"unattributable once it loses its tag",
			ref, prune.JailImageOwnerLabel, got, prune.JailImageOwnerValue)
	}

	// 2. The provenance label rides along. It is NOT an image key — imageIdentity
	// is one value per flake.nix+flake.lock, shared by the full/minimal/lean
	// variants and every `packages:` list, because nix cannot reference a
	// derivation's own output path. It is here so a nameless row can still be
	// traced back to the flake that built it.
	//
	// The value is a `sha256:` hash, not the `/nix/store/…` path it was until
	// 2026-09-12: a store path carries the evaluating host's system, so no second
	// host could compute it (docs/design/darwin-image-provenance.md, OQ-IP1). The
	// label is checked for SHAPE only — nothing keys off it — but a shape check is
	// what would catch the value silently becoming a store path again.
	if id := inspectLabel(t, rt, ref, "org.yolo-jail.image-identity"); !strings.HasPrefix(id, identityPrefix) {
		t.Errorf("%s carries image-identity %q, want a %s digest", ref, id, identityPrefix)
	}

	// 3. THE PROBE THE REAP ACTUALLY ISSUES finds it. This is the assertion that
	// fails if the label exists but is not filterable — the property the whole
	// design rests on, since a `<none>` row is reachable no other way.
	filter := "label=" + prune.JailImageOwnerLabel + "=" + prune.JailImageOwnerValue
	out, err := exec.Command(rt, "images", "--filter", filter, "--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatalf("%s images --filter %s: %v", rt, filter, err)
	}
	wantID := imageID(t, rt, ref)
	if !strings.Contains(string(out), wantID) {
		t.Errorf("the loaded image %s (%s) is not returned by `--filter %s`; got:\n%s",
			ref, wantID, filter, out)
	}
}

// TestImagesRepoAndFilterAreMutuallyExclusive pins the measured constraint the
// two-probe union exists for: podman refuses a positional repository argument
// together with `--filter`, so ownership CANNOT be asked as one query.
//
// MEASURED 2026-09-08, podman 5.8.4: `Error: cannot specify an image and a
// filter(s)`. If this ever goes green, that is not a defect to repair here —
// it means `PruneOldImages` could ask one question instead of two (though the
// answer would still have to be a UNION, not an intersection, which is what
// podman's AND-across-keys filter semantics would give). Read
// minimal-disk-footprint.md OQ-DF3 before changing either side.
func TestImagesRepoAndFilterAreMutuallyExclusive(t *testing.T) {
	requireJail(t)
	rt := detectRuntime()
	if rt != "podman" {
		t.Skipf("measured on podman only, not %s", rt)
	}
	out, err := exec.Command(rt, "images", jailImageRepo,
		"--filter", "label="+prune.JailImageOwnerLabel, "--format", "{{.ID}}").CombinedOutput()
	if err == nil {
		t.Fatalf("`%s images %s --filter label=…` now SUCCEEDS (output %q) — the union in "+
			"internal/prune/probes.go is two probes because this combination was a hard "+
			"error; re-read minimal-disk-footprint.md OQ-DF3 before simplifying it",
			rt, jailImageRepo, out)
	}
	if !strings.Contains(string(out), "filter") {
		t.Errorf("expected podman's 'cannot specify an image and a filter(s)', got: %s", out)
	}
}

// inspectLabel reads one label off a loaded image. `{{index .Labels "k"}}` is
// used rather than `{{.Labels}}` so the assertion is about one key rather than
// about how podman formats a map.
func inspectLabel(t *testing.T, rt, ref, key string) string {
	t.Helper()
	out, err := exec.Command(rt, "image", "inspect", ref,
		"--format", `{{index .Labels "`+key+`"}}`).Output()
	if err != nil {
		t.Fatalf("%s image inspect %s: %v", rt, ref, err)
	}
	return strings.TrimSpace(string(out))
}

// imageID returns the short image ID of ref, in the same 12-hex spelling
// `images --format {{.ID}}` prints — the identity the reap matches on.
func imageID(t *testing.T, rt, ref string) string {
	t.Helper()
	out, err := exec.Command(rt, "images", "--format", "{{.ID}}", "--filter",
		"reference="+ref).Output()
	if err != nil {
		t.Fatalf("%s images --filter reference=%s: %v", rt, ref, err)
	}
	id := lastNonEmptyLine(string(out))
	if id == "" {
		t.Fatalf("no image ID for %s", ref)
	}
	return id
}
