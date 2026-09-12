package cli

// hostownedreset_test.go pins host-side `yolo config reset` across the three
// `host_management` values (docs/design/config-ownership-and-promotion.md §6.1, §6.3.3).
//
// # Why reset is the one host-side write `own` unlocks, and why that is not parity
//
// ComposeStateful's adoption is safe against reset ONLY because reset also truncates the
// surface to its pure render. Without the truncation the sequence is reset → no baseline →
// adopt, and adoption puts back exactly the edits the user asked to discard — reset as a
// silent no-op. So an owned home whose reset refused would have an adoption path with nothing
// to discard against, which is why §10's `own` step lands the two together.
//
// Under `none` and `assert` the refusal stays: the guard's premise is a file yolo does not own
// in this context, and those two contracts are that premise.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// hostResetFixture builds a scratch real home under the named contract, seeds the host capture
// store with an edit, and leaves the surface file holding that edit.
//
// HOST-SIDE, which is the default on a bare runner: surfacesAreLocal() reads YOLO_VERSION and
// the /workspace mount, and this deliberately does NOT stub it — the whole subject is what the
// host-side path does.
func hostResetFixture(t *testing.T, mode string) (home, store, surfacePath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude"],"host_management":"`+mode+`"}`)

	s, ok := surfaceManifest().Lookup("claude", "settings")
	if !ok {
		t.Fatal("missing claude/settings in the surface manifest")
	}
	surfacePath = expandHome(s.Path)
	writeFile(t, surfacePath, `{"theme":"the user's edit"}`)

	// The store, at the path the RENDER would have written it to — resolved through the same
	// Target, never hand-joined, so this fixture cannot seed a directory reset then misses.
	store = render.Host(home, nil, render.OwnershipOwn).SidecarDir()
	if store == "" {
		t.Fatal("render.Host(...).SidecarDir() is empty for an owned target")
	}
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(store, "claude-settings.overlay.json"), `{"theme":"the user's edit"}`)
	writeFile(t, filepath.Join(store, "claude-settings.last_render"), `{"theme":"yolo's"}`)
	return home, store, surfacePath
}

// UNDER `own`, RESET WORKS, and it does both halves: it deletes the capture sidecars from the
// HOST store (not some workspace tree) and truncates the surface to its pure render, so there
// is nothing left for the next apply's adoption to find and put back.
func TestHostSideResetWorksUnderOwn(t *testing.T) {
	_, store, surfacePath := hostResetFixture(t, "own")

	var out, errw bytes.Buffer
	if rc := configReset([]string{"claude", "--surface", "settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset under `own`: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if _, err := os.Stat(filepath.Join(store, "claude-settings.overlay.json")); !os.IsNotExist(err) {
		t.Errorf("the capture overlay survived reset in the host capture store (err=%v) — "+
			"the next apply would re-apply the very edit the user just discarded", err)
	}
	// THE BASELINE IS RE-SEEDED, NOT DELETED (OQ-CO7 D1, reseedResetBaseline). `last_render`
	// means "the exact bytes yolo wrote last", and reset has just written them; deleting it
	// instead left the next render unable to tell reset's own output from the user's file,
	// which is how a reset spent the one-per-surface adoption archive on a copy of yolo's own
	// render. What the deletion existed to prevent is still prevented, by a stronger
	// statement: the baseline must not be STALE — it must be exactly what is on disk now.
	baseline, err := os.ReadFile(filepath.Join(store, "claude-settings.last_render"))
	if err != nil {
		t.Fatalf("reset wrote the surface and left no baseline for those bytes: %v", err)
	}
	// The store holds the USER'S OWN config bytes, so it is 0600 in a real home — the mode
	// render.Target decides for this notch, not a literal spelled at the write.
	if info, err := os.Stat(filepath.Join(store, "claude-settings.last_render")); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("the re-seeded baseline is mode %04o in a real home, want 0600 — it holds "+
			"the user's own config bytes", got)
	}
	// The trailer names the command that re-renders THIS notch. Pointing a host-side user at
	// "the next jail launch" sends them to relaunch a container that has nothing to do with
	// the file they just truncated.
	if !strings.Contains(out.String(), "yolo host apply") {
		t.Errorf("reset under `own` did not name the command that re-renders the host:\n%s",
			out.String())
	}
	data, err := os.ReadFile(surfacePath)
	if err != nil {
		t.Fatalf("read the surface after reset: %v", err)
	}
	if strings.Contains(string(data), "the user's edit") {
		t.Errorf("reset left the edit in the file. Deleting the sidecars alone is NOT the "+
			"discard: the next apply finds no baseline, takes the first-migration branch, and "+
			"ADOPTS this file — so the edit comes back and reset was a no-op:\n%s", data)
	}
	if string(baseline) != string(data) {
		t.Errorf("the re-seeded baseline is not the file reset wrote:\n baseline: %q\n file:"+
			"     %q\n\nA baseline that disagrees with the file is exactly the stale one the "+
			"deletion existed to avoid: the next render diffs the two and captures the "+
			"difference as a user edit.", baseline, data)
	}
}

// UNDER `none` AND `assert`, RESET STILL REFUSES, and leaves both the store and the file
// alone. This is the half that keeps the exemption an ownership statement rather than a hole:
// truncating a real dotfile the user owns is the Phase-0 data loss the guard exists for.
func TestHostSideResetRefusesUnderNoneAndAssert(t *testing.T) {
	for _, mode := range []string{"none", "assert"} {
		t.Run(mode, func(t *testing.T) {
			_, store, surfacePath := hostResetFixture(t, mode)
			before, err := os.ReadFile(surfacePath)
			if err != nil {
				t.Fatal(err)
			}

			var out, errw bytes.Buffer
			if rc := configReset([]string{"claude", "--surface", "settings"}, &out, &errw, false); rc != 1 {
				t.Fatalf("reset under %q: rc=%d, want 1\n%s%s", mode, rc, out.String(), errw.String())
			}
			if !strings.Contains(errw.String(), "refusing") {
				t.Errorf("reset under %q did not say it was refusing:\n%s", mode, errw.String())
			}
			if _, err := os.Stat(filepath.Join(store, "claude-settings.overlay.json")); err != nil {
				t.Errorf("a refused reset deleted a sidecar under %q: %v", mode, err)
			}
			after, err := os.ReadFile(surfacePath)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Errorf("a refused reset truncated the user's file under %q:\n%s", mode, after)
			}
		})
	}
}

// --force STILL REACHES IT, at every contract. The flag overrides a REFUSAL and is unrelated to
// the ownership contract, so `own` must not have turned it into the only way in — or into a
// second, quieter path that skips the store the exemption resolves.
func TestHostSideResetForceStillWorksUnderAssert(t *testing.T) {
	_, store, _ := hostResetFixture(t, "assert")
	var out, errw bytes.Buffer
	if rc := configReset([]string{"claude", "--surface", "settings", "--force"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset --force under `assert`: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	// It resolved the WORKSPACE tree, not the host store: --force is the old escape hatch and
	// its meaning has not moved. The store's files are still there because nothing under
	// `assert` keeps one — that contract composes no whole file.
	if _, err := os.Stat(filepath.Join(store, "claude-settings.overlay.json")); err != nil {
		t.Errorf("--force under `assert` reached the host capture store, which that contract "+
			"does not keep: %v", err)
	}
}

// AND CAPTURE IS STILL REFUSED UNDER `own`, deliberately. Its premise is the G2 PRIVACY defect
// rather than reset's data-loss one, and while `own` does relocate the destination out of the
// workspace, `yolo config capture` host-side buys only visibility — it folds early what the
// next apply folds anyway. Nothing depends on it the way adoption depends on reset, so leaving
// it refused keeps the new store with exactly two writers: the render, and the reset that
// discards.
//
// Written down as a test because the asymmetry is the kind that reads like an oversight.
func TestHostSideCaptureStaysRefusedUnderOwn(t *testing.T) {
	hostResetFixture(t, "own")
	var out, errw bytes.Buffer
	if rc := configCapture([]string{"claude", "--surface", "settings"}, &out, &errw, false); rc != 1 {
		t.Fatalf("capture under `own`: rc=%d, want 1 (refused)\n%s%s",
			rc, out.String(), errw.String())
	}
	if !strings.Contains(errw.String(), "refusing") {
		t.Errorf("capture under `own` did not refuse:\n%s", errw.String())
	}
}

// AND THE TRUNCATION RENDERS THE GUARDED POSTURE, not the autonomous one. This is the half
// that keeps the exemption from being a jail-bypass leak with extra steps.
//
// `own` is what turned the CLI's surface manifest from a REPORTING input into a host-side
// WRITER, and those are not the same manifest: packload.Pack.Surfaces() is SurfacesFor(true)
// — the autonomous posture — whose own docstring says "The host path calls
// SurfacesFor(false)". Printing a pack's autonomous declarations at either notch is harmless;
// composing them into a real ~/.claude/settings.json is the leak the 2026-08-01
// autonomy-as-notch-policy ruling exists to prevent (§4.2), and which
// internal/entrypoint/hostrender.go states in as many words where it resolves the same
// posture from the Target's Profile.
//
// Asserted against the POSTURE's own keys rather than against a second composition, so this
// cannot pass by both halves sharing one bug: `allow`, `deny`, `acceptEdits`,
// `additionalDirectories: ["/"]` and `skipDangerousModePermissionPrompt: true` are declared
// by packs/claude/pack.json's `autonomous` block and by nothing else, and the `guarded` block
// declares the three values wanted below.
func TestHostSideResetTruncatesToTheGuardedPosture(t *testing.T) {
	_, _, surfacePath := hostResetFixture(t, "own")

	var out, errw bytes.Buffer
	if rc := configReset([]string{"claude", "--surface", "settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset under `own`: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	data, err := os.ReadFile(surfacePath)
	if err != nil {
		t.Fatalf("read the surface after reset: %v", err)
	}
	var got struct {
		Permissions struct {
			AdditionalDirectories []string        `json:"additionalDirectories"`
			Allow                 json.RawMessage `json:"allow"`
			DefaultMode           string          `json:"defaultMode"`
			Deny                  json.RawMessage `json:"deny"`
		} `json:"permissions"`
		SkipDangerous bool `json:"skipDangerousModePermissionPrompt"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode the truncated surface: %v\n%s", err, data)
	}
	if got.Permissions.DefaultMode != "default" {
		t.Errorf("permissions.defaultMode = %q, want %q — reset composed the AUTONOMOUS "+
			"posture into a real home. The host notch renders autonomy OFF:\n%s",
			got.Permissions.DefaultMode, "default", data)
	}
	if got.SkipDangerous {
		t.Errorf("skipDangerousModePermissionPrompt is true — that key is the jail's "+
			"permission bypass, written into the user's own ~/.claude/settings.json:\n%s", data)
	}
	if len(got.Permissions.AdditionalDirectories) != 0 {
		t.Errorf("permissions.additionalDirectories = %v, want empty — \"/\" is the jail's "+
			"whole-filesystem grant:\n%s", got.Permissions.AdditionalDirectories, data)
	}
	// allow/deny are declared ONLY by the autonomous posture, so their presence is also what
	// the NEXT apply's adoption would capture back as if the user had written it — reset
	// leaving a fresh overlay instead of nothing to adopt.
	if got.Permissions.Allow != nil || got.Permissions.Deny != nil {
		t.Errorf("permissions.allow/deny survived the truncation (allow=%s deny=%s) — only "+
			"the autonomous posture declares them, so the next `yolo host apply` adopts them "+
			"back as a user overlay:\n%s", got.Permissions.Allow, got.Permissions.Deny, data)
	}
}

// AND THE COUPLING IS MEASURED THROUGH THE NEXT APPLY, which is the property the exemption's
// whole argument rests on and the one no other test here runs.
//
// The file assertions above pin what reset WROTE. This pins what that means: reset's premise
// is that it truncates the surface to the render the next apply computes, so adoption's
// first-migration branch finds nothing to take back. Stated only in a comment, that premise
// was FALSE as shipped — the truncation composed a different (autonomous) posture, so the
// apply adopted `permissions.allow`/`deny` as if the user had typed them and pinned them as
// an overlay indefinitely. A reset that leaves a fresh overlay is not a discard.
//
// The assertion is on the OVERLAY rather than the file, deliberately: the file can agree by
// accident (the apply rewrites it either way), while an empty overlay is only reachable when
// the two compositions actually match. It fails if the truncation's notch resolution is
// removed, which the file assertions alone do not guarantee for a future posture key.
func TestHostSideResetLeavesTheNextApplyNothingToAdopt(t *testing.T) {
	home, store, _ := hostResetFixture(t, "own")

	var claude *packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "claude" {
			claude = p
			break
		}
	}
	if claude == nil {
		t.Fatal("the claude pack is not embedded")
	}

	var out, errw bytes.Buffer
	if rc := configReset([]string{"claude", "--surface", "settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset under `own`: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	// The command reset's own trailer names, at the notch it named it for.
	if _, err := entrypoint.RenderHostPack(claude, home, render.OwnershipOwn, false, nil); err != nil {
		t.Fatalf("the host apply reset told the user to run: %v", err)
	}

	overlay := filepath.Join(store, "claude-settings.overlay.json")
	data, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatalf("read the overlay the apply left: %v", err)
	}
	var captured map[string]any
	if err := json.Unmarshal(data, &captured); err != nil {
		t.Fatalf("decode the overlay: %v\n%s", err, data)
	}
	if len(captured) != 0 {
		t.Errorf("the apply after reset adopted %d key(s) as a user overlay, from a file the "+
			"user had just asked to be reset — so reset truncated to a DIFFERENT render than "+
			"the one the apply computes, and those keys are now pinned as though the user "+
			"wrote them:\n%s", len(captured), data)
	}
}
