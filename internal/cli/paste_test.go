package cli

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/clipimage"
)

func pastePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(1, 1, color.RGBA{200, 10, 10, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pasteWorkspace(t *testing.T) string {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

func isolatePasteHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "") // the host, whatever jail runs the suite
	t.Setenv("YOLO_SETUP", "")
	t.Setenv("YOLO_RUNTIME", "")
}

// TestPasteThroughTheRegistry runs the verb as `yolo paste` does, with its production host:
// the clipboard tools are stub scripts first on PATH (wl-paste and wl-copy on Linux, osascript
// and pbcopy on macOS), so no real clipboard is read or written. It fails if the registry row,
// the verb's call into clipimage, or the clipboard write-back is deleted.
func TestPasteThroughTheRegistry(t *testing.T) {
	isolatePasteHome(t)
	ws := pasteWorkspace(t)
	stubs := t.TempDir()
	img := pastePNG(t)
	write := func(name, body string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(stubs, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("img.png", string(img), 0o644)
	write("img.hex", "«data PNGf"+strings.ToUpper(hex.EncodeToString(img))+"»\n", 0o644)
	write("wl-paste", "#!/bin/sh\nif [ \"$1\" = --list-types ]; then echo text/plain; echo image/png; exit 0; fi\ncat '"+stubs+"/img.png'\n", 0o755)
	write("osascript", "#!/bin/sh\ncat '"+stubs+"/img.hex'\n", 0o755)
	for _, w := range []string{"wl-copy", "pbcopy"} {
		write(w, "#!/bin/sh\ncat > '"+stubs+"/copied'\n", 0o755)
	}
	t.Setenv("PATH", stubs+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WAYLAND_DISPLAY", "stub-0")
	t.Setenv("DISPLAY", "")
	t.Chdir(ws)

	rc, out, errs := captureStd(t, func() int {
		return registry["paste"]([]string{"paste", "--setup", "podman-self"})
	})
	if rc != 0 {
		t.Fatalf("rc %d, stderr:\n%s", rc, errs)
	}
	path := strings.TrimSpace(out)
	if !regexp.MustCompile(`^/workspace/\.yolo/paste/\d{8}T\d{6}Z-[0-9a-f]{8}\.png$`).MatchString(path) {
		t.Fatalf("stdout %q is not the in-jail path", out)
	}
	saved, err := os.ReadFile(filepath.Join(ws, strings.TrimPrefix(path, "/workspace/")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(saved)); err != nil {
		t.Fatalf("the saved file is not a PNG: %v", err)
	}
	copied, err := os.ReadFile(filepath.Join(stubs, "copied"))
	if err != nil || string(copied) != path {
		t.Fatalf("clipboard got %q (%v), want %q", copied, err, path)
	}
	if !strings.Contains(errs, "paste it into the agent") {
		t.Fatalf("stderr does not say what to do next:\n%s", errs)
	}
}

type fakeClip struct {
	clip    clipimage.Clip
	readErr error
	copyErr error
	reads   int
	copied  []string
}

func (f *fakeClip) Read(context.Context) (clipimage.Clip, error) {
	f.reads++
	return f.clip, f.readErr
}

func (f *fakeClip) CopyText(_ context.Context, s string) error {
	f.copied = append(f.copied, s)
	return f.copyErr
}

func testPasteEnv(clip *fakeClip, ws string) (pasteEnv, *bytes.Buffer, *bytes.Buffer) {
	var out, errw bytes.Buffer
	return pasteEnv{
		clip:      clip,
		inJail:    func() bool { return false },
		workspace: func() (string, bool) { return ws, ws != "" },
		jailWorkspace: func(flag, w string) (string, string, []string, error) {
			return "/workspace", "podman-self", nil, nil
		},
		now:    func() time.Time { return time.Date(2026, 11, 6, 1, 2, 3, 0, time.UTC) },
		rand:   bytes.NewReader([]byte{0xde, 0xad, 0xbe, 0xef}),
		stdout: &out,
		stderr: &errw,
	}, &out, &errw
}

func TestPasteInAJailReadsNothing(t *testing.T) {
	clip := &fakeClip{}
	e, _, errw := testPasteEnv(clip, t.TempDir())
	e.inJail = func() bool { return true }
	if rc := pasteMain(nil, e); rc != 1 {
		t.Fatalf("rc %d", rc)
	}
	if clip.reads != 0 {
		t.Fatal("the clipboard was read from a jail")
	}
	if !strings.Contains(errw.String(), "on the host") {
		t.Fatalf("no next step: %s", errw)
	}
}

func TestPasteOutsideAWorkspaceReadsNothing(t *testing.T) {
	clip := &fakeClip{}
	e, _, errw := testPasteEnv(clip, "")
	if rc := pasteMain(nil, e); rc != 1 || clip.reads != 0 {
		t.Fatalf("rc %d reads %d", rc, clip.reads)
	}
	if !strings.Contains(errw.String(), "inside the workspace") {
		t.Fatalf("no next step: %s", errw)
	}
}

func TestPasteWithNoImageSavesNothing(t *testing.T) {
	ws := pasteWorkspace(t)
	clip := &fakeClip{readErr: errors.New("no image on the clipboard; copy an image, then run `yolo paste` again")}
	e, out, errw := testPasteEnv(clip, ws)
	if rc := pasteMain(nil, e); rc != 1 {
		t.Fatalf("rc %d", rc)
	}
	if out.Len() != 0 || len(clip.copied) != 0 {
		t.Fatalf("stdout %q copied %q", out, clip.copied)
	}
	if !strings.Contains(errw.String(), "copy an image") {
		t.Fatalf("stderr %s", errw)
	}
	if _, err := os.Lstat(clipimage.StoreDir(ws)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused paste made the store")
	}
}

func TestPasteStillPrintsThePathWhenTheClipboardWriteFails(t *testing.T) {
	ws := pasteWorkspace(t)
	clip := &fakeClip{clip: clipimage.Clip{Data: pastePNG(t), Type: "image/png", Tool: "stub"}, copyErr: errors.New("no wl-copy")}
	e, out, errw := testPasteEnv(clip, ws)
	if rc := pasteMain(nil, e); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errw)
	}
	want := "/workspace/.yolo/paste/20261106T010203Z-deadbeef.png\n"
	if out.String() != want {
		t.Fatalf("stdout %q, want %q", out, want)
	}
	if !strings.Contains(errw.String(), "copy the path printed above") {
		t.Fatalf("stderr %s", errw)
	}
}

func TestPasteRefusesAnUnknownArgument(t *testing.T) {
	clip := &fakeClip{}
	e, _, _ := testPasteEnv(clip, t.TempDir())
	if rc := pasteMain([]string{"--bogus"}, e); rc != 2 || clip.reads != 0 {
		t.Fatalf("rc %d reads %d", rc, clip.reads)
	}
}

// TestPasteJailWorkspacePerSetup is the parity table: where each setup's agent sees the store.
func TestPasteJailWorkspacePerSetup(t *testing.T) {
	isolatePasteHome(t)
	ws := "/home/you/code/widgets"
	for setupName, want := range map[string]string{
		"podman-self":           "/workspace",
		"apple-container-self":  "/workspace",
		"seatbelt-self":         ws,
		"seatbelt-yolo-account": ws,
		"host-self":             ws,
	} {
		got, name, _, err := pasteJailWorkspace(setupName, ws)
		if err != nil {
			t.Fatalf("%s: %v", setupName, err)
		}
		if got != want || name != setupName {
			t.Errorf("%s: got %q (%s), want %q", setupName, got, name, want)
		}
	}
}
