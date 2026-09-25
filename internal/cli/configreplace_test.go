package cli

// configreplace_test.go pins which capture-state writes REPLACE the file (a temp file beside
// it, then a rename over it) and which rewrite it IN PLACE, keeping its inode.
//
//   - The SIDECARS are replaced, so a crash mid-write cannot leave a truncated overlay or
//     baseline: the overlay and list-capture sidecars `yolo config capture --force` and
//     capture-on-terminate write (both through captureSurfaceAt), and the last_render baseline
//     `yolo config reset` re-seeds. No mount names a file in either store. `yolo config
//     promote`'s write-back is pinned the same way in configverblinks_test.go.
//   - The SURFACE `yolo config reset` truncates to its pure render is rewritten in place, in
//     every form: it is a mount-visible file (a `host_files` source, a single-file bind target
//     in-jail, or a single-file bind source in the workspace store), and a rename would leave
//     a running jail's bind on the old inode (docs/reference/jail-home.md, "No rename-writes
//     to mount-visible files").
//
// A crash cannot be staged in a unit test, so each case uses the observation that tells the
// two writes apart: a SECOND HARD-LINK NAME for the file's inode. A rename leaves that name on
// the old inode, holding the old content; an in-place truncate rewrites it too.
//
// The file modes are pinned beside it, because a rename takes the temp file's mode where a
// truncate kept the old file's: a sidecar gets its store's mode (render.Target.SidecarFileMode,
// 0600 in a real home), and a surface keeps its own.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// pinSecondName gives path a second hard-link name under dir and returns the check that the
// second name still holds what path held: that the write replaced path rather than rewriting
// its inode.
func pinSecondName(t *testing.T, dir, path string) (verify func(t *testing.T)) {
	t.Helper()
	second := filepath.Join(dir, "second-name-"+filepath.Base(path))
	if err := os.Link(path, second); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	return func(t *testing.T) {
		t.Helper()
		if got, _ := os.ReadFile(second); string(got) != string(before) {
			t.Errorf("%s was truncated and rewritten in place, not replaced: its second name "+
				"now holds %q, want the old %q", path, got, before)
		}
	}
}

// pinSameInode gives path a second hard-link name under dir and returns the check that the
// second name now holds what path holds: that the write rewrote path's inode in place.
func pinSameInode(t *testing.T, dir, path string) (verify func(t *testing.T)) {
	t.Helper()
	second := filepath.Join(dir, "second-name-"+filepath.Base(path))
	if err := os.Link(path, second); err != nil {
		t.Fatal(err)
	}
	return func(t *testing.T) {
		t.Helper()
		got, _ := os.ReadFile(second)
		want, _ := os.ReadFile(path)
		if string(got) != string(want) {
			t.Errorf("%s was replaced by a rename, not rewritten in place: its second name "+
				"still holds %q, want the new %q; a jail binding the file would keep the old inode",
				path, got, want)
		}
	}
}

// replacedSidecars are the two sidecars a capture WRITES, each seeded with a prior version so
// there is an inode to link: the surface edit adds want to it.
var replacedSidecars = []struct {
	name, suffix, baseline, current, prior, want string
}{
	{"the overlay", ".overlay.json", `{"model":"base"}`, `{"model":"base","myEdit":"present"}`,
		`{"older":"edit"}`, "myEdit"},
	{"the list capture", render.ListCaptureSuffix, `{"plugins":["owner"]}`,
		`{"plugins":["owner","mine"]}`, `{"/plugins":{"add":[],"remove":[]}}`, "mine"},
}

// `yolo config capture --force` at a workspace target, host-side.
func TestConfigCaptureReplacesTheSidecarsItWrites(t *testing.T) {
	for _, tc := range replacedSidecars {
		t.Run(tc.name, func(t *testing.T) {
			w := newVerbLinkWorld(t)
			w.seed(t, tc.current, tc.baseline, "", "")
			sidecar := w.sidecar(tc.suffix)
			writeFile(t, sidecar, tc.prior)
			verify := pinSecondName(t, w.ws, sidecar)

			rc, out, errw := runConfigVerb(t, "capture", "claude/settings", "--force")

			if rc != 0 {
				t.Fatalf("capture rc=%d:\n%s%s", rc, out, errw)
			}
			if got, _ := os.ReadFile(sidecar); !strings.Contains(string(got), tc.want) {
				t.Fatalf("capture did not rewrite %s, so the check proves nothing: %q", sidecar, got)
			}
			verify(t)
		})
	}
}

// Capture-on-terminate, which writes the same two sidecars on the host at jail exit.
func TestCaptureOnTerminateReplacesTheSidecarsItWrites(t *testing.T) {
	for _, tc := range replacedSidecars {
		t.Run(tc.name, func(t *testing.T) {
			ws := terminateWorkspace(t, filepath.Join("claude", "settings.json"), tc.baseline, tc.current)
			sidecar := filepath.Join(ws, ".yolo", "prism", "claude-settings"+tc.suffix)
			writeFile(t, sidecar, tc.prior)
			verify := pinSecondName(t, ws, sidecar)

			var warnings []string
			captureOnTerminate(ws, "podman", func(m string) { warnings = append(warnings, m) })

			if got, _ := os.ReadFile(sidecar); !strings.Contains(string(got), tc.want) {
				t.Fatalf("the capture did not rewrite %s (warnings %v), so the check proves "+
					"nothing: %q", sidecar, warnings, got)
			}
			verify(t)
			if fi, err := os.Stat(sidecar); err != nil || fi.Mode().Perm() != 0o644 {
				t.Errorf("the workspace store's sidecar is %v (err %v), want 0644", fi.Mode(), err)
			}
		})
	}
}

// At the host notch the store holds the user's own config bytes, so a sidecar capture WRITES is
// 0600 whether it existed or not. A truncate kept an existing file's mode but created a new one
// at 0644; a rename takes the mode it is handed, so it must be handed the store's.
func TestHostSideCaptureWritesTheOverlayAtTheStoreMode(t *testing.T) {
	for _, existing := range []bool{true, false} {
		name := "a new overlay"
		if existing {
			name = "an existing overlay"
		}
		t.Run(name, func(t *testing.T) {
			_, store, surfacePath := hostResetFixture(t, "own")
			writeFile(t, surfacePath, `{"theme":"yolo's","myEdit":"present"}`)
			overlay := filepath.Join(store, "claude-settings.overlay.json")
			if existing {
				if err := os.Chmod(overlay, 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(overlay); err != nil {
				t.Fatal(err)
			}

			var out, errw bytes.Buffer
			if rc := configCapture(hostTargetForTest(), []string{"claude/settings", "--force"},
				&out, &errw, false); rc != 0 {
				t.Fatalf("capture rc=%d:\n%s%s", rc, out.String(), errw.String())
			}
			if got, _ := os.ReadFile(overlay); !strings.Contains(string(got), "myEdit") {
				t.Fatalf("capture did not write the overlay: %q\n%s%s", got, out.String(), errw.String())
			}
			if fi, err := os.Stat(overlay); err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("the overlay in a real home's store is %v (err %v), want 0600: it holds "+
					"the user's own config bytes", fi.Mode(), err)
			}
		})
	}
}

// `yolo config reset` at a workspace target, host-side: the surface it truncates to its pure
// render is rewritten in place, keeping its inode and its own mode.
func TestConfigResetRewritesTheSurfaceInPlace(t *testing.T) {
	w := newVerbLinkWorld(t)
	w.seed(t, `{"theme":"the agents edit"}`, `{"theme":"yolos"}`, `{"theme":"the agents edit"}`, "")
	if err := os.Chmod(w.surface(), 0o600); err != nil {
		t.Fatal(err)
	}
	verify := pinSameInode(t, w.ws, w.surface())

	rc, out, errw := runConfigVerb(t, "reset", "claude/settings")

	if rc != 0 {
		t.Fatalf("reset rc=%d:\n%s%s", rc, out, errw)
	}
	if got, _ := os.ReadFile(w.surface()); strings.Contains(string(got), "the agents edit") {
		t.Fatalf("reset did not rewrite the surface, so the check proves nothing: %q", got)
	}
	verify(t)
	if fi, err := os.Lstat(w.surface()); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
		t.Errorf("the reset surface is %v (err %v), want a regular file keeping its own 0600", fi.Mode(), err)
	}
}

// In the jail that owns the workspace the surface is the process's own file, and reset FOLLOWS
// a link at it (a home-root host_files destination is one, into a writable overlay; a real-home
// surface may be one into a dotfiles repo). The rewrite lands on the file the link names, in
// place, keeping the link, that file's inode and its mode.
func TestLocalConfigResetRewritesTheFileALinkedSurfaceNamesInPlace(t *testing.T) {
	home := withScratchHome(t)
	tgt, dir := withLocalSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)
	real := filepath.Join(home, "elsewhere", "settings.json")
	writeFile(t, real, `{"theme":"dark"}`)
	if err := os.Chmod(real, 0o600); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	captureSymlink(t, filepath.Join("..", "elsewhere", "settings.json"), settings)
	verify := pinSameInode(t, home, real)

	var out, errw bytes.Buffer
	if rc := configReset(tgt, []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configReset rc=%d, stderr=%s", rc, errw.String())
	}
	if fi, err := os.Lstat(settings); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("reset replaced the surface's link with a file (%v, err %v): it must write the "+
			"file the link names", fi.Mode(), err)
	}
	if got, _ := os.ReadFile(real); strings.Contains(string(got), "dark") {
		t.Fatalf("reset did not rewrite the file the link names: %q", got)
	}
	verify(t)
	if fi, err := os.Stat(real); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the reset surface is %v (err %v), want its own 0600 kept", fi.Mode(), err)
	}
}

// The baseline reset re-seeds. Reset removes the old one first, so no inode is there to link
// by the time configReset writes it; the writer is driven directly over one instead. Its call
// site in configReset is pinned by TestConfigResetDiscardsTheOverlayAndReSeedsTheBaseline and
// TestHostSideResetWorksUnderOwn (which pins the 0600).
func TestResetReplacesTheBaselineItReSeeds(t *testing.T) {
	w := newVerbLinkWorld(t)
	w.seed(t, "", `{"theme":"stale"}`, "", "")
	tgt := jailConfigTarget(w.ws, "the cwd")
	verify := pinSecondName(t, w.ws, w.sidecar(".last_render"))

	if err := reseedResetBaseline(tgt, tgt.lastRenderFile("claude", "settings"), []byte(`{"theme":"yolos"}`)); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(w.sidecar(".last_render")); string(got) != `{"theme":"yolos"}` {
		t.Fatalf("the baseline was not re-seeded: %q", got)
	}
	verify(t)
	if fi, err := os.Stat(w.sidecar(".last_render")); err != nil || fi.Mode().Perm() != tgt.sidecarFileMode() {
		t.Errorf("the re-seeded baseline is %v (err %v), want the store's %v", fi.Mode(), err, tgt.sidecarFileMode())
	}
}
