package cli

// configdiffretired_test.go is the READER half of the anti-laundering record: `yolo config
// diff` reports a key yolo wrote for a layer that no longer claims it, naming the layer.
//
// Why the reader needs pinning at all. The writer's fix (agentcfg.RetiredLayer, applied by
// entrypoint.rmwProvenance) keeps the true attribution in a file under the rendered home's
// state dir — a path the user has never heard of. Without a reader the orphaned key is still
// sitting in their config attributed to a pack they dropped, and the fix is invisible. Ruling
// R3 is about the RECORD not lying; this is where the record gets to speak.
//
// The load-bearing negative is the same one the rest of this command's tests carry: the new
// label must not be reported as a WINNER of the key. A retired layer is not setting the key —
// nothing is — so "set by dropme" or "dropme won" would be a fresh confident-wrong-answer
// installed by the fix for one.
//
// Every test drives a t.TempDir() home and a t.TempDir() record dir. The real $HOME is never
// read or written.

import (
	"bytes"
	"strings"
	"testing"
)

// THE READER, at the host notch: the retired key is listed, the layer that last claimed it is
// named, and nothing claims the key is still being set.
func TestConfigDiffReportsARetiredKeyAndNamesTheOwner(t *testing.T) {
	writeOverlayFixture(t, map[string]string{"acme": acmeOwnerPackJSON})
	withHostSurfaces(t)
	withSidecarDir(t)
	dir := withHostProvenanceDir(t)
	// What an apply AFTER the drop measured: `dropme` is gone from `packs`, so nothing declares
	// fileSuggestion — but the key is in the file and yolo is the one that put it there.
	writeHostProvenance(t, dir, "acme", "settings",
		"fileSuggestion\tretired:config-overlay:dropme\ntelemetry\tmanaged\n")

	var out, errw bytes.Buffer
	if rc := configDiff([]string{"acme"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configDiff rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "fileSuggestion") {
		t.Errorf("the retired key is not reported at all — the record holds the only trace of "+
			"where it came from, so a reader that skips it leaves the fix invisible:\n%s", got)
	}
	if !strings.Contains(got, "config-overlay:dropme") {
		t.Errorf("the report must name the layer that last claimed the key — that is the whole "+
			"reason the label carries it:\n%s", got)
	}
	if !strings.Contains(got, "no longer asserts it") {
		t.Errorf("the report must say the layer no longer asserts the key:\n%s", got)
	}
	// THE LOAD-BEARING NEGATIVE: a retired layer is not a winner. Nothing is setting this key.
	if strings.Contains(got, "set by dropme") || strings.Contains(got, "dropme won") {
		t.Errorf("a retired layer is reported as still SETTING the key — it is not; the pack is "+
			"gone and the value is inert leftover output:\n%s", got)
	}
	// And the raw label must not leak as a layer name in the live-contribution vocabulary.
	if strings.Contains(got, "but retired:") {
		t.Errorf("the retired label leaked into the \"but X won\" line, which reads as a live "+
			"layer beating a contribution:\n%s", got)
	}
}

// A retired-only surface is REACHED. This is the case a reader keyed on live contributions
// structurally cannot see: an orphaned key's defining property is that no pack declares it any
// more, so filtering surfaces by "has a config-overlay" skips exactly the surface that needs
// reporting.
func TestConfigDiffReachesASurfaceWithOnlyRetiredKeys(t *testing.T) {
	// ONLY the owner is configured — no contributor pack at all.
	writeOverlayFixture(t, map[string]string{"acme": acmeOwnerPackJSON})
	withHostSurfaces(t)
	withSidecarDir(t)
	dir := withHostProvenanceDir(t)
	writeHostProvenance(t, dir, "acme", "settings", "fileSuggestion\tretired:config-overlay:gone\n")

	var out, errw bytes.Buffer
	if rc := configDiff([]string{"acme"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configDiff rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "acme/settings") || !strings.Contains(got, "fileSuggestion") {
		t.Errorf("a surface whose ONLY finding is a retired key was skipped — that is the one "+
			"surface where the report is needed, since nothing declares the key any more:\n%s", got)
	}
	// With no live contributor the heading must not print an empty contributor list.
	if strings.Contains(got, "config-overlay from  ") || strings.Contains(got, "from \n") {
		t.Errorf("the heading printed an empty contributor list:\n%s", got)
	}
	// The precedence footer describes LIVE overlays and ends in "drop the contributing pack" —
	// advice already taken, and the cause of these keys.
	if strings.Contains(got, "Drop the contributing pack") {
		t.Errorf("a retired-only report ends by advising the action that CAUSED it:\n%s", got)
	}
	// The retirement footer, which has the actual remedy, must be there instead.
	if !strings.Contains(got, "written by a past apply") {
		t.Errorf("the retirement explanation is missing, so the line above it has no context:\n%s", got)
	}
}

// A LIVE contribution and a RETIRED key on the SAME surface are both reported, each in its own
// vocabulary. Neither may absorb the other: the live one has a winner and a precedence rule,
// the retired one has neither.
func TestConfigDiffReportsLiveAndRetiredKeysTogether(t *testing.T) {
	writeOverlayFixture(t, map[string]string{
		"acme":     acmeOwnerPackJSON,
		"acme-fzf": acmeFzfPackJSON,
	})
	withHostSurfaces(t)
	withSidecarDir(t)
	dir := withHostProvenanceDir(t)
	writeHostProvenance(t, dir, "acme", "settings",
		"fileSuggestion\tconfig-overlay:acme-fzf\nlegacyKey\tretired:config-overlay:dropme\n")

	var out, errw bytes.Buffer
	if rc := configDiff([]string{"acme"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configDiff rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "set by acme-fzf") {
		t.Errorf("the LIVE contribution lost its win line when a retired key joined it:\n%s", got)
	}
	if !strings.Contains(got, "legacyKey") || !strings.Contains(got, "config-overlay:dropme") {
		t.Errorf("the retired key is missing alongside the live one:\n%s", got)
	}
	// Both footers, because both mechanisms are on screen.
	if !strings.Contains(got, "Drop the contributing pack") {
		t.Errorf("the live precedence footer is missing:\n%s", got)
	}
	if !strings.Contains(got, "written by a past apply") {
		t.Errorf("the retirement footer is missing:\n%s", got)
	}
}

// A `host` key is NOT reported as retired. The reader's filter is the label, and the whole
// point of the writer's fix is that these two states stay distinguishable — a reader that
// blurred them would undo it from the other end.
func TestConfigDiffDoesNotReportPlainHostKeysAsRetired(t *testing.T) {
	writeOverlayFixture(t, map[string]string{
		"acme":     acmeOwnerPackJSON,
		"acme-fzf": acmeFzfPackJSON,
	})
	withHostSurfaces(t)
	withSidecarDir(t)
	dir := withHostProvenanceDir(t)
	writeHostProvenance(t, dir, "acme", "settings",
		"fileSuggestion\tconfig-overlay:acme-fzf\nuserOwned\thost\n")

	var out, errw bytes.Buffer
	if rc := configDiff([]string{"acme"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configDiff rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if strings.Contains(got, "userOwned") {
		t.Errorf("a key attributed to `host` is the USER's and has nothing to report; listing it "+
			"as yolo's leftover output is the laundering defect pointed the other way:\n%s", got)
	}
	if strings.Contains(got, "written by a past apply") {
		t.Errorf("the retirement footer printed with no retired key on screen:\n%s", got)
	}
}

// A CORRUPT retired label claims nothing on the reader side either. `retired:` with no layer
// behind it is not a retirement — the same closed-vocabulary rule the writer applies, so a
// truncated record cannot make the reader announce an orphan that does not exist.
func TestConfigDiffIgnoresATruncatedRetiredLabel(t *testing.T) {
	writeOverlayFixture(t, map[string]string{
		"acme":     acmeOwnerPackJSON,
		"acme-fzf": acmeFzfPackJSON,
	})
	withHostSurfaces(t)
	withSidecarDir(t)
	dir := withHostProvenanceDir(t)
	writeHostProvenance(t, dir, "acme", "settings",
		"fileSuggestion\tconfig-overlay:acme-fzf\nmangled\tretired:\n")

	var out, errw bytes.Buffer
	if rc := configDiff([]string{"acme"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configDiff rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if strings.Contains(got, "mangled") {
		t.Errorf("a truncated `retired:` label was reported as a retirement — a corrupt record "+
			"must prove nothing on the reading side too:\n%s", got)
	}
}
