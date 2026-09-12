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
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	for _, leaf := range []string{"claude-settings.overlay.json", "claude-settings.last_render"} {
		if _, err := os.Stat(filepath.Join(store, leaf)); !os.IsNotExist(err) {
			t.Errorf("%s survived reset in the host capture store (err=%v) — the next apply "+
				"would diff against a stale baseline and re-capture what was just discarded",
				leaf, err)
		}
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
