package cli

// hostownedreset_test.go pins host-side `yolo config reset` and `yolo config capture` across
// the three `host_management` values (docs/design/config-ownership-and-promotion.md §6.1,
// §6.3.3, OQ-CO3).
//
// # Why `own` unlocks reset, and why that is not parity
//
// ComposeStateful's adoption is safe against reset ONLY because reset also truncates the
// surface to its pure render. Without the truncation the sequence is reset → no baseline →
// adopt, and adoption puts back exactly the edits the user asked to discard — reset as a
// silent no-op. So an owned home whose reset refused would have an adoption path with nothing
// to discard against, which is why §10's `own` step lands the two together.
//
// Under `none` and `assert` the refusal stays: the guard's premise is a file yolo does not own
// in this context, and those two contracts are that premise.
//
// Capture joined reset under `own` on 2026-10-04 (OQ-CO3, ruled yes-under-own): its premise is
// a credential copied into the WORKSPACE tree, and under `own` it writes the host's own 0600
// store instead, with the bytes the next apply records there anyway.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	if rc := configReset(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset under `own`: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	// The overlay holds NOTHING after reset: absent, or the empty one the re-render writes
	// (persistStatefulSurface, the owned write's own sidecar) — never the discarded edit.
	if n := capturedKeysAt(t, filepath.Join(store, "claude-settings.overlay.json")); n != 0 {
		t.Errorf("the capture overlay survived reset in the host capture store (%d key(s)) — "+
			"the next apply would re-apply the very edit the user just discarded", n)
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
			if rc := configReset(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 1 {
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
	if rc := configReset(hostTargetForTest(), []string{"claude/settings", "--force"}, &out, &errw, false); rc != 0 {
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

// UNDER `own`, CAPTURE WORKS (OQ-CO3, ruled yes-under-own 2026-09-10): it folds the edit in
// your real file into the HOST capture store — 0600, never the workspace tree a jail reads —
// and leaves the file itself byte for byte. It was refused until 2026-10-04, so `config diff
// --at host` could not show an edit made since the last apply.
func TestHostSideCaptureWorksUnderOwn(t *testing.T) {
	_, store, surfacePath := hostResetFixture(t, "own")
	writeFile(t, surfacePath, `{"theme":"yolo's","myEdit":"present"}`)
	before := mustReadFile(t, surfacePath)
	var out, errw bytes.Buffer
	if rc := configCapture(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("capture under `own`: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	overlay := filepath.Join(store, "claude-settings.overlay.json")
	if got := mustReadFile(t, overlay); !strings.Contains(got, "myEdit") {
		t.Errorf("the edit in your file was not captured into the host store: %s", got)
	}
	if info, err := os.Stat(overlay); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("the captured overlay is mode %04o, want 0600 — it holds your own config", got)
	}
	if after := mustReadFile(t, surfacePath); after != before {
		t.Errorf("capture changed the surface file:\nbefore %s\nafter  %s", before, after)
	}
}

// UNDER `none` AND `assert` CAPTURE IS STILL REFUSED — there is no store to capture into — and
// --force no longer pretends otherwise: it reached a capture that found no baseline and called
// every surface "never rendered here". The refusal names the contract that keeps a store.
func TestHostSideCaptureStillRefusedUnderAssertAndNone(t *testing.T) {
	for _, mode := range []string{"assert", "none"} {
		for _, args := range [][]string{{"claude/settings"}, {"claude/settings", "--force"}} {
			t.Run(mode+" "+strings.Join(args, " "), func(t *testing.T) {
				_, store, surfacePath := hostResetFixture(t, mode)
				before := mustReadFile(t, surfacePath)
				overlayBefore := mustReadFile(t, filepath.Join(store, "claude-settings.overlay.json"))
				var out, errw bytes.Buffer
				if rc := configCapture(hostTargetForTest(), args, &out, &errw, false); rc != 1 {
					t.Fatalf("capture under %q: rc=%d, want 1\n%s%s", mode, rc, out.String(), errw.String())
				}
				if !strings.Contains(errw.String(), `"host_management": "own"`) {
					t.Errorf("the refusal does not name the contract that keeps a store:\n%s", errw.String())
				}
				if strings.Contains(errw.String(), "--force") {
					t.Errorf("the refusal offers --force, which does nothing here:\n%s", errw.String())
				}
				if mustReadFile(t, surfacePath) != before ||
					mustReadFile(t, filepath.Join(store, "claude-settings.overlay.json")) != overlayBefore {
					t.Errorf("a refused capture wrote something")
				}
			})
		}
	}
}

// THE VERB WRITES WHAT THE NEXT APPLY RECORDS: a capture under `own`, then the real `yolo host
// apply --assert`, leaves the overlay exactly as the capture wrote it, and the edit in your file.
// If the two disagreed the verb would be a second writer of the store with its own answer. (The
// two tests above pin the keys a top-level edit like this one cannot reach: a key only the jail's
// posture manages, and one the computed layer writes.)
func TestOwnedCaptureVerbIsIdempotentWithTheApply(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["claude"],"host_management":"own"}`)
	ownedApply(t)
	editJSONAt(t, home, ".claude/settings.json", func(doc map[string]any) {
		doc["myEdit"] = "present"
	})

	var out, errw bytes.Buffer
	if rc := configCapture(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("capture: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	overlay := filepath.Join(render.Host(home, nil, render.OwnershipOwn).SidecarDir(),
		"claude-settings.overlay.json")
	byVerb := mustReadFile(t, overlay)
	if !strings.Contains(byVerb, "myEdit") {
		t.Fatalf("fixture: the verb captured nothing: %s", byVerb)
	}
	ownedApply(t)
	sameOverlay(t, "the apply after the verb recorded a different overlay", byVerb,
		mustReadFile(t, overlay))
	if readJSONAt(t, home, ".claude/settings.json")["myEdit"] != "present" {
		t.Errorf("the edit the verb captured is gone after the apply")
	}
}

// ownedApply runs the real `yolo host apply --assert` in the current HOME, answering yes to any
// prompt, and returns what it printed.
func ownedApply(t *testing.T) string {
	t.Helper()
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false,
		strings.NewReader("y\ny\ny\ny\n")); rc != 0 {
		t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	return out.String() + errw.String()
}

// editJSONAt decodes home/rel, hands it to edit, and writes it back.
func editJSONAt(t *testing.T, home, rel string, edit func(map[string]any)) {
	t.Helper()
	doc := readJSONAt(t, home, rel)
	edit(doc)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, rel), string(raw))
}

// sameOverlay fails unless the two overlay sidecar texts decode to the same value.
func sameOverlay(t *testing.T, what, a, b string) {
	t.Helper()
	var x, y any
	if err := json.Unmarshal([]byte(a), &x); err != nil {
		t.Fatalf("%s: %v\n%s", what, err, a)
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		t.Fatalf("%s: %v\n%s", what, err, b)
	}
	if !reflect.DeepEqual(x, y) {
		t.Errorf("%s:\n verb  %s\n apply %s", what, a, b)
	}
}

// THE VERB CAPTURES AT THE HOST POSTURE, WITH THE COMPUTED LAYER — the apply's own composition —
// so an edit the apply already captured survives it. MEASURED before the fix (the reviewer's probe,
// 2026-10-04): the verb composed the claude pack at the JAIL posture, whose managed layer owns
// permissions.deny, so its narrowing erased a deny rule an earlier apply had captured — "0 keys now
// recorded" — and the next `yolo host apply --assert` wrote the file without it.
func TestOwnedCaptureVerbKeepsAnEditTheApplyCaptured(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["claude"],"host_management":"own"}`)
	ownedApply(t)
	editJSONAt(t, home, ".claude/settings.json", func(doc map[string]any) {
		doc["permissions"].(map[string]any)["deny"] = []any{"Bash(rm:*)"}
	})
	ownedApply(t) // captures the deny rule into the host store, and keeps it in the file
	overlay := filepath.Join(render.Host(home, nil, render.OwnershipOwn).SidecarDir(),
		"claude-settings.overlay.json")
	if !strings.Contains(mustReadFile(t, overlay), "Bash(rm:*)") {
		t.Fatalf("fixture: the apply did not capture the deny rule: %s", mustReadFile(t, overlay))
	}

	var out, errw bytes.Buffer
	if rc := configCapture(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("capture: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	byVerb := mustReadFile(t, overlay)
	if !strings.Contains(byVerb, "Bash(rm:*)") {
		t.Errorf("`yolo config capture` erased the deny rule the apply had captured:\n%s\n%s",
			byVerb, out.String())
	}
	ownedApply(t)
	sameOverlay(t, "the apply after the verb recorded a different overlay", byVerb,
		mustReadFile(t, overlay))
	perms, _ := readJSONAt(t, home, ".claude/settings.json")["permissions"].(map[string]any)
	if deny, _ := perms["deny"].([]any); len(deny) != 1 || deny[0] != "Bash(rm:*)" {
		t.Errorf("your deny rule is gone from the file after capture + apply: %v", perms)
	}
}

// AND A KEY THE COMPUTED LAYER WRITES IS NOT RECORDED: the apply narrows it out of the overlay
// (computed outranks a captured edit), so a verb that recorded it was a second writer with its own
// answer. MEASURED before the fix: env.ENABLE_LSP_TOOL, computed from your lsp_servers, edited to
// "0" in the file — the verb said "1 key now recorded" and the next apply's overlay was {}.
func TestOwnedCaptureVerbDoesNotRecordAComputedKey(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["claude"],"host_management":"own",
		"lsp_servers":{"gopls":{"command":"gopls"}}}`)
	ownedApply(t)
	env, _ := readJSONAt(t, home, ".claude/settings.json")["env"].(map[string]any)
	if env["ENABLE_LSP_TOOL"] == nil || env["ENABLE_LSP_TOOL"] == "0" {
		t.Fatalf("fixture: the apply did not compute env.ENABLE_LSP_TOOL: %v", env)
	}
	editJSONAt(t, home, ".claude/settings.json", func(doc map[string]any) {
		doc["env"].(map[string]any)["ENABLE_LSP_TOOL"] = "0"
	})

	var out, errw bytes.Buffer
	if rc := configCapture(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("capture: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	overlay := filepath.Join(render.Host(home, nil, render.OwnershipOwn).SidecarDir(),
		"claude-settings.overlay.json")
	byVerb := mustReadFile(t, overlay)
	if strings.Contains(byVerb, "ENABLE_LSP_TOOL") {
		t.Errorf("the verb recorded a key the computed layer writes, which the apply narrows out:\n%s",
			byVerb)
	}
	ownedApply(t)
	sameOverlay(t, "the apply after the verb recorded a different overlay", byVerb,
		mustReadFile(t, overlay))
}

// UNDER `own`, A RESET OF ONE SURFACE LEAVES ITS PACK'S OTHER SURFACES ALONE: the re-render is
// RenderHostPack's one-surface filter, not the pack. Without the filter a reset of claude/settings
// rewrote ~/.claude.json too (MEASURED: {"mine":true} became {"mine":true,"mcpServers":{}}) while
// the line said it had reset claude/settings.
func TestOwnedHostResetOfOneSurfaceLeavesItsSiblingsAlone(t *testing.T) {
	home, _, _ := hostResetFixture(t, "own")
	sibling := filepath.Join(home, ".claude.json")
	writeFile(t, sibling, `{"mine":true}`)
	before := mustReadFile(t, sibling)
	var out, errw bytes.Buffer
	if rc := configReset(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "re-rendered") {
		t.Fatalf("fixture: the reset did not re-render claude/settings:\n%s", out.String())
	}
	if after := mustReadFile(t, sibling); after != before {
		t.Errorf("a reset of claude/settings rewrote %s:\nbefore %s\nafter  %s", sibling, before, after)
	}
}

// claudeForkWithFailingSettingsDerive is a copy of the shipped claude pack, configured by path,
// whose claude/settings derive raises when the host render calls it: a composition that resolves
// (its registration loads) and a one-surface render that comes back refused.
func claudeForkWithFailingSettingsDerive(t *testing.T) string {
	t.Helper()
	var claude *packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "claude" {
			claude = p
		}
	}
	fork := filepath.Join(t.TempDir(), "claude")
	if err := os.CopyFS(fork, os.DirFS(claude.Root)); err != nil {
		t.Fatal(err)
	}
	derive := filepath.Join(fork, "derive.lua")
	raw := mustReadFile(t, derive)
	const head = `yolo.derive("claude", "settings", function(ctx)`
	if strings.Count(raw, head) != 1 {
		t.Fatalf("fixture: the claude/settings derive is not where this test edits it")
	}
	if err := os.Chmod(derive, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, derive, strings.Replace(raw, head, head+` error("this derive fails at the host")`, 1))
	return fork
}

// WHEN THE ONE-SURFACE RE-RENDER IS REFUSED, THE RESET SAYS SO, and does not claim it re-rendered:
// the file holds the declared layers the truncation wrote, and the trailer names the apply that
// lands the rest. A refused render read "re-rendered it from its layers" without the warning.
func TestOwnedHostResetSaysWhenTheReRenderIsRefused(t *testing.T) {
	home, _, _ := hostResetFixture(t, "own")
	fork := claudeForkWithFailingSettingsDerive(t)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["`+fork+`"],"host_management":"own"}`)
	var out, errw bytes.Buffer
	if rc := configReset(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String()
	if !strings.Contains(report, "reset to its declared layers only") ||
		!strings.Contains(report, "its derive failed at the host") {
		t.Errorf("the refused re-render is not named:\n%s", report)
	}
	if strings.Contains(report, "re-rendered") || strings.Contains(report, "finds nothing to change") {
		t.Errorf("the reset claims a re-render that was refused:\n%s", report)
	}
	if !strings.Contains(report, "The next `yolo host apply --assert` re-renders these surfaces") {
		t.Errorf("the trailer does not name the apply that lands the rest:\n%s", report)
	}
}

// AND THE OWNED CAPTURE REFUSES WHAT THE APPLY REFUSES, recording nothing: a surface the apply
// will not compose is one whose edits nothing would record, and saying "captured" there would be
// the verb's own answer again.
func TestOwnedCaptureVerbRefusesWhatTheApplyRefuses(t *testing.T) {
	home, store, surfacePath := hostResetFixture(t, "own")
	fork := claudeForkWithFailingSettingsDerive(t)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["`+fork+`"],"host_management":"own"}`)
	writeFile(t, surfacePath, `{"theme":"yolo's","myEdit":"present"}`)
	overlay := filepath.Join(store, "claude-settings.overlay.json")
	before := mustReadFile(t, overlay)
	var out, errw bytes.Buffer
	if rc := configCapture(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 1 {
		t.Fatalf("capture of a surface the apply refuses: rc=%d, want 1\n%s%s", rc, out.String(),
			errw.String())
	}
	if report := out.String(); !strings.Contains(report, "not captured") ||
		!strings.Contains(report, "yolo host apply --assert") {
		t.Errorf("the refusal does not say why or what next:\n%s", report)
	}
	if after := mustReadFile(t, overlay); after != before {
		t.Errorf("a refused capture wrote the overlay:\nbefore %s\nafter  %s", before, after)
	}
}

// AND THE OWNED CAPTURE TAKES THE APPLY'S LOCK, as reset does: an apply writes the file and then
// the baseline, and a capture reading between the two would record yolo's own new values as
// edits of yours.
func TestOwnedCaptureVerbRefusesWhileAnApplyHoldsTheLock(t *testing.T) {
	_, store, surfacePath := hostResetFixture(t, "own")
	writeFile(t, surfacePath, `{"theme":"yolo's","myEdit":"present"}`)
	held := tryHostApplyLock(os.Getenv("HOME"))
	if held == nil {
		t.Fatal("fixture: could not take the lock")
	}
	defer held.Close()
	overlay := filepath.Join(store, "claude-settings.overlay.json")
	before := mustReadFile(t, overlay)
	var out, errw bytes.Buffer
	if rc := configCapture(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 1 {
		t.Fatalf("capture under a held lock: rc=%d, want 1\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(errw.String(), "again once it has finished") {
		t.Errorf("the refusal does not name the next step:\n%s", errw.String())
	}
	if after := mustReadFile(t, overlay); after != before {
		t.Errorf("a refused capture wrote the overlay")
	}
}

// WITH NO BASELINE, OR NO FILE, THE OWNED CAPTURE RECORDS NOTHING. With no baseline the apply's
// render ADOPTS the file, and adoption is the apply's to perform, with its one-time archive; the
// verb has never acted without one, because it cannot tell an edit from yolo's own output. With no
// file there is no edit to fold.
func TestOwnedCaptureVerbNeitherAdoptsNorCapturesAnAbsentFile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(store, surfacePath string) string
	}{
		{"no baseline", func(store, _ string) string {
			return filepath.Join(store, "claude-settings.last_render")
		}},
		{"no file", func(_, surfacePath string) string { return surfacePath }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, store, surfacePath := hostResetFixture(t, "own")
			writeFile(t, surfacePath, `{"theme":"yolo's","myEdit":"present"}`)
			if err := os.Remove(tc.remove(store, surfacePath)); err != nil {
				t.Fatal(err)
			}
			overlay := filepath.Join(store, "claude-settings.overlay.json")
			before := mustReadFile(t, overlay)
			var out, errw bytes.Buffer
			if rc := configCapture(hostTargetForTest(), []string{"claude/settings"}, &out, &errw,
				false); rc != 0 {
				t.Fatalf("capture: rc=%d\n%s%s", rc, out.String(), errw.String())
			}
			if !strings.Contains(out.String(), "never rendered here — nothing to capture") {
				t.Errorf("the capture does not say there was nothing to capture:\n%s", out.String())
			}
			if after := mustReadFile(t, overlay); after != before {
				t.Errorf("the capture wrote the overlay:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}

// AND A REPAIR THE COMPOSITION MAKES TO YOUR CAPTURED EDITS IS ANNOUNCED. pi rejects
// `theme: "system"` (agentcfg/rejectedvalues.go), and the apply's composition removes it from the
// capture overlay; the verb persists that overlay, so it says so, as the apply does.
func TestOwnedCaptureVerbAnnouncesARepairToYourCapturedEdits(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["pi"],"host_management":"own"}`)
	ownedApply(t)
	editJSONAt(t, home, ".pi/agent/settings.json", func(doc map[string]any) {
		doc["theme"] = "system"
	})
	var out, errw bytes.Buffer
	if rc := configCapture(hostTargetForTest(), []string{"pi/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("capture: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), `removed theme = "system" from your captured edits`) {
		t.Errorf("the repair to your captured edits is not announced:\n%s", out.String())
	}
	overlay := filepath.Join(render.Host(home, nil, render.OwnershipOwn).SidecarDir(),
		"pi-settings.overlay.json")
	if got := mustReadFile(t, overlay); strings.Contains(got, "system") {
		t.Errorf("the rejected value was recorded: %s", got)
	}
}

// A `host_files` ENTRY IS CAPTURED AS THE APPLY RENDERS IT: at the host its inline content folds
// into the layer its mode names (entrypoint.HostFileSurfaceAtHost), so a key a `copy` entry's
// content declares is managed there and the apply narrows an edit of it out. The verb lowered
// the entry the jail's way, kept the edit, and disagreed with the apply that followed.
func TestOwnedCaptureVerbCapturesAHostFileAsTheApplyRendersIt(t *testing.T) {
	home := hostComputedHome(t, `{"host_management":"own","host_files":[
		{"path":".config/tool/conf.json","mode":"copy","content":"{\"pinned\":\"yolo\"}"}]}`)
	ownedApply(t)
	rel := ".config/tool/conf.json"
	if readJSONAt(t, home, rel)["pinned"] != "yolo" {
		t.Fatalf("fixture: the apply did not render the entry: %s", mustReadFile(t, filepath.Join(home, rel)))
	}
	editJSONAt(t, home, rel, func(doc map[string]any) {
		doc["pinned"] = "mine"
		doc["extra"] = "mine too"
	})
	var out, errw bytes.Buffer
	if rc := configCapture(hostTargetForTest(), []string{"user"}, &out, &errw, false); rc != 0 {
		t.Fatalf("capture user: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	store := render.Host(home, nil, render.OwnershipOwn).SidecarDir()
	matches, _ := filepath.Glob(filepath.Join(store, "user-*.overlay.json"))
	if len(matches) != 1 {
		t.Fatalf("fixture: want one user overlay in %s, have %v\n%s", store, matches, out.String())
	}
	byVerb := mustReadFile(t, matches[0])
	if strings.Contains(byVerb, "pinned") || !strings.Contains(byVerb, "extra") {
		t.Errorf("the verb's capture is not the apply's (a managed key recorded, or the edit "+
			"missed):\n%s\n%s", byVerb, out.String())
	}
	ownedApply(t)
	sameOverlay(t, "the apply after the verb recorded a different overlay", byVerb,
		mustReadFile(t, matches[0]))
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
// by packs/claude/pack.json's `autonomous` block and by nothing else. The `guarded` block
// declares the defaultMode and skipDangerousModePermissionPrompt values wanted below, and no
// additionalDirectories, which at the host is the user's own list (hostguardeddirs_test.go).
func TestHostSideResetTruncatesToTheGuardedPosture(t *testing.T) {
	_, _, surfacePath := hostResetFixture(t, "own")

	var out, errw bytes.Buffer
	if rc := configReset(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
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
	if rc := configReset(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset under `own`: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	// The command reset's own trailer names, at the notch it named it for.
	if _, err := entrypoint.RenderHostPack(claude, home, render.OwnershipOwn, false, nil, nil); err != nil {
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

// ON AN OWNED HOST, `diff` REPORTS EXACTLY WHAT `reset` DISCARDS. This is §2.3 F3 closed, and
// the red state it replaces was measured on 2026-09-16: `reset` resolved its two sidecars
// through render.Target and acted on the host capture store, while `diff` read
// `prismOverlayPath` — the cwd's workspace tree — unconditionally. So the shipped
// inspect-then-undo pair was broken in the direction that matters. `diff` reported
//
//	No captured in-jail edits for claude surface settings.
//
// at rc 0, and `reset` then discarded an edit the user was never shown.
//
// The fix is that the store comes off the ONE resolved target
// ([OQ-CR3](docs/reference/config-target-resolution.md#oq-cr3)) — not that the readers learned
// about ownership. `hostOwnsSurfaces` was consulted by the write guard, by reset's paths, by
// the re-render trailer, by the baseline's mode and by the truncation, and by no read path at
// all; there is no second resolution left to leave out.
//
// It asserts the PAIR rather than either half, because either alone passes with the defect in
// place: `diff` naming the key is what the user is shown, and `reset` removing it from the
// store `diff` read is what makes them the same subject.
func TestOwnedHostDiffReportsExactlyWhatResetDiscards(t *testing.T) {
	_, store, _ := hostResetFixture(t, "own")

	var out, errw bytes.Buffer
	if rc := configDiff(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("diff under `own`: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	shown := out.String()
	if !strings.Contains(shown, "theme") {
		t.Fatalf("diff on an owned host did not name the captured key that `reset` is about to "+
			"discard from %s. A negative here is the F3 defect: the user is shown nothing and "+
			"then loses an edit.\n%s", store, shown)
	}
	if strings.Contains(shown, "No captured in-jail edits") {
		t.Errorf("diff read a different store than reset acts on:\n%s", shown)
	}

	out.Reset()
	errw.Reset()
	if rc := configReset(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset under `own`: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	out.Reset()
	errw.Reset()
	if rc := configDiff(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("diff after reset: rc=%d\n%s", rc, errw.String())
	}
	if strings.Contains(out.String(), "theme") {
		t.Errorf("the key `diff` showed survived the `reset` that was supposed to discard it:\n%s",
			out.String())
	}
}

// capturedKeysAt is how many captured keys the overlay sidecar at path holds: 0 for an absent
// file or an empty overlay.
func capturedKeysAt(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	var captured map[string]any
	if err := json.Unmarshal(data, &captured); err != nil {
		t.Fatalf("decode the overlay %s: %v\n%s", path, err, data)
	}
	return len(captured)
}

// ownedPiHome is a real home under `own` with pi, the codex profile and one MCP server of the
// user's, after one `yolo host apply --assert`, and a captured edit in pi's settings since.
func ownedPiHome(t *testing.T) (home, store string) {
	t.Helper()
	home = hostComputedHome(t, ownedPiConfig)
	writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"theme":"dark"}`)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	settings := readJSONAt(t, home, ".pi/agent/settings.json")
	if settings["defaultProvider"] != "openai-codex" {
		t.Fatalf("fixture: the apply did not select the codex profile: %v", settings)
	}
	settings["theme"] = "light" // the edit the reset discards
	raw, _ := json.Marshal(settings)
	writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), string(raw))
	return home, render.Host(home, nil, render.OwnershipOwn).SidecarDir()
}

const ownedPiConfig = `{"packs":["pi"],"host_management":"own","profile":{"pi":"codex"},
	"mcp_servers":{"tavily":{"command":"npx","args":["-y","tavily-mcp"]}}}`

// UNDER `own`, A RESET LANDS WHAT THE NEXT APPLY LANDS: the profile's selection, the user's MCP
// server, every layer the apply composes — not the declared layers alone. MEASURED before
// 2026-10-04: `config reset pi` truncated settings.json to two keys and emptied mcp.json while
// saying "Cleared", and the next dry run then had both to re-render.
func TestOwnedHostResetKeepsTheComputedLayer(t *testing.T) {
	home, store := ownedPiHome(t)
	var out, errw bytes.Buffer
	if rc := configReset(hostTargetForTest(), []string{"pi"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset pi under `own`: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	settings := readJSONAt(t, home, ".pi/agent/settings.json")
	if settings["defaultProvider"] != "openai-codex" || settings["defaultModel"] == nil {
		t.Errorf("the reset dropped your profile's selection: %v\n%s", settings, out.String())
	}
	if settings["theme"] == "light" {
		t.Errorf("the captured edit survived the reset: %v", settings)
	}
	servers, _ := readJSONAt(t, home, ".pi/agent/mcp.json")["mcpServers"].(map[string]any)
	if _, ok := servers["tavily"]; !ok {
		t.Errorf("the reset emptied your MCP servers out of mcp.json: %v", servers)
	}
	for _, surface := range []struct{ rel, sidecar string }{
		{".pi/agent/settings.json", "pi-settings.last_render"},
		{".pi/agent/mcp.json", "pi-mcp.last_render"},
	} {
		file, _ := os.ReadFile(filepath.Join(home, surface.rel))
		baseline, err := os.ReadFile(filepath.Join(store, surface.sidecar))
		if err != nil || string(baseline) != string(file) {
			t.Errorf("%s: the baseline is not the file the reset left (err=%v):\n baseline %q\n file     %q",
				surface.rel, err, baseline, file)
		}
	}
	if !strings.Contains(out.String(), "re-rendered") || strings.Contains(out.String(), "Cleared") {
		t.Errorf("the reset line does not say it re-rendered (or still says Cleared):\n%s", out.String())
	}
	// Every surface re-rendered, so the trailer says the apply has nothing left here, not that it
	// still has to run — the dry run below is what makes that true.
	if !strings.Contains(out.String(), "finds nothing to change here") {
		t.Errorf("a reset that re-rendered every surface does not say the apply has nothing left:\n%s",
			out.String())
	}
	// The next dry run finds nothing to change: the reset wrote the apply's bytes.
	out.Reset()
	errw.Reset()
	hostMain([]string{"apply"}, &out, &errw, false, strings.NewReader(""))
	if report := out.String() + errw.String(); !strings.Contains(report, "this home is up to date") {
		t.Errorf("the dry run after a reset still has something to apply:\n%s", report)
	}
}

// WHEN THE APPLY'S COMPOSITION FAILS, the reset still discards and truncates to the declared
// layers — and says what is missing and how to land it, rather than reading as a full reset.
func TestOwnedHostResetSaysWhenItCannotComposeTheRest(t *testing.T) {
	home, _ := ownedPiHome(t)
	// A config every launch refuses on its provider section: the retired `use_profiles`.
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["pi"],"host_management":"own","use_profiles":{"pi":"codex"}}`)
	var out, errw bytes.Buffer
	if rc := configReset(hostTargetForTest(), []string{"pi/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String()
	if !strings.Contains(report, "declared layers only") || !strings.Contains(report, "yolo host apply --assert") {
		t.Errorf("the fallback is not named with its next step:\n%s", report)
	}
	if settings := readJSONAt(t, home, ".pi/agent/settings.json"); settings["theme"] == "light" {
		t.Errorf("the captured edit survived a reset that could not compose the rest: %v", settings)
	}
}

// AND RESET TAKES THE HOST APPLY'S LOCK: a reset while another process is applying the home is
// refused with the way forward, before anything is discarded.
func TestOwnedHostResetRefusesWhileAnApplyHoldsTheLock(t *testing.T) {
	_, store, surfacePath := hostResetFixture(t, "own")
	held := tryHostApplyLock(os.Getenv("HOME"))
	if held == nil {
		t.Fatal("fixture: could not take the lock")
	}
	defer held.Close()
	var out, errw bytes.Buffer
	if rc := configReset(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 1 {
		t.Fatalf("reset under a held lock: rc=%d, want 1\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(errw.String(), "again once it has finished") {
		t.Errorf("the refusal does not name the next step:\n%s", errw.String())
	}
	if capturedKeysAt(t, filepath.Join(store, "claude-settings.overlay.json")) == 0 {
		t.Errorf("a refused reset discarded the captured edit")
	}
	if data, _ := os.ReadFile(surfacePath); !strings.Contains(string(data), "the user's edit") {
		t.Errorf("a refused reset truncated the file:\n%s", data)
	}
}

// THE TRUNCATION COMPOSES THE CONFIGURED PACK'S DECLARATION, not the shipped one of the same
// name: a fork of claude configured by path declares its own guarded posture, and the reset —
// here on its declared-layers path, the composition refused — lands the fork's value. It read
// the shipped manifest until 2026-10-04, so a fork's reset wrote the pack it replaced.
func TestOwnedHostResetTruncatesToTheConfiguredPacksDeclaration(t *testing.T) {
	_, _, surfacePath := hostResetFixture(t, "own")
	var claude *packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "claude" {
			claude = p
		}
	}
	fork := filepath.Join(t.TempDir(), "claude")
	if err := os.CopyFS(fork, os.DirFS(claude.Root)); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(fork, "pack.json")
	raw := mustReadFile(t, manifest)
	const shipped = `"defaultMode": "default"`
	if strings.Count(raw, shipped) != 1 {
		t.Fatalf("fixture: the shipped guarded defaultMode is not where this test edits it")
	}
	if err := os.Chmod(manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, manifest, strings.Replace(raw, shipped, `"defaultMode": "plan"`, 1))
	// The retired key refuses the composition, so the reset stops at the truncation this pins.
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["`+fork+`"],"host_management":"own","use_profiles":{"claude":"x"}}`)

	var out, errw bytes.Buffer
	if rc := configReset(hostTargetForTest(), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	var got struct {
		Permissions struct {
			DefaultMode string `json:"defaultMode"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(mustReadFile(t, surfacePath)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Permissions.DefaultMode != "plan" {
		t.Errorf("permissions.defaultMode = %q, want the configured fork's %q — the reset composed "+
			"the shipped pack instead of the one you configured:\n%s", got.Permissions.DefaultMode,
			"plan", out.String())
	}
}
