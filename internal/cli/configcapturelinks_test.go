package cli

// configcapturelinks_test.go pins that capture-on-terminate never follows a link the jail
// planted in its own writable state (docs/reference/jail-home.md, "Host code in jail-writable
// state"). captureOnTerminate runs on the HOST, as the host user, when a jail exits, and every
// file it touches is one the jail could write: the capture surfaces live in the workspace
// overlay (`<workspace>/.yolo/home`) and the sidecars in `<workspace>/.yolo/prism`, both
// reachable from the jail at /workspace/.yolo. A link at a surface, or a directory above one,
// had the capture read a host file into the jail's own overlay sidecar; a link at a sidecar had
// it truncate the host file the link named and write capture JSON into it.
//
// Each case plants the link, runs the production call site, and checks the host side.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// captureHostFile is a host file (a stand-in for ~/.claude/settings.json or ~/.bashrc) whose
// content carries a key nothing in the workspace has, so a capture that read it is visible.
func captureHostFile(t *testing.T) (path, body string) {
	t.Helper()
	body = `{"model":"base","hostOnlySecret":"leaked"}`
	path = filepath.Join(t.TempDir(), "host-settings.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, body
}

func captureSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(link)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// captureLinkShape is one shape a planted link at a sidecar takes: plant puts it at link and
// returns the check that the host side behind it was left alone.
type captureLinkShape struct {
	name  string
	plant func(t *testing.T, link string) (verify func(t *testing.T))
}

var captureLinkShapes = []captureLinkShape{
	{"a link to an existing host file", func(t *testing.T, link string) func(*testing.T) {
		p, body := captureHostFile(t)
		captureSymlink(t, p, link)
		return func(t *testing.T) {
			t.Helper()
			if got, err := os.ReadFile(p); err != nil || string(got) != body {
				t.Errorf("the capture wrote through the jail's link into the host file: %q (err %v)", got, err)
			}
		}
	}},
	{"a dangling link", func(t *testing.T, link string) func(*testing.T) {
		dir := t.TempDir()
		captureSymlink(t, filepath.Join(dir, "created-by-the-follow"), link)
		return func(t *testing.T) {
			t.Helper()
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("the capture created %v in a host directory through the jail's link", entries)
			}
		}
	}},
}

// THE OVERLAY SIDECAR the capture WRITES. A link at it had os.WriteFile truncate the host file
// it named (or create a dangling link's target) and write the capture's JSON there. The list
// capture's write half is pinned by the list-capture case of the read test below, which is the
// only shape in which a capture writes one.
func TestCaptureOnTerminateNeverWritesASidecarThroughALink(t *testing.T) {
	for _, sidecar := range []struct {
		name, file, baseline, current string
	}{
		{"the overlay", "claude-settings.overlay.json",
			`{"model":"base"}`, `{"model":"base","myEdit":"present"}`},
	} {
		for _, shape := range captureLinkShapes {
			t.Run(sidecar.name+"/"+shape.name, func(t *testing.T) {
				ws := terminateWorkspace(t, filepath.Join("claude", "settings.json"), sidecar.baseline, sidecar.current)
				link := filepath.Join(ws, ".yolo", "prism", sidecar.file)
				verify := shape.plant(t, link)

				captureOnTerminate(ws, "podman", func(string) {})

				verify(t)
			})
		}
	}
	t.Run("the overlay is still delivered, as a regular file", func(t *testing.T) {
		ws := terminateWorkspace(t, filepath.Join("claude", "settings.json"),
			`{"model":"base"}`, `{"model":"base","myEdit":"present"}`)
		link := filepath.Join(ws, ".yolo", "prism", "claude-settings.overlay.json")
		dir := t.TempDir()
		captureSymlink(t, filepath.Join(dir, "gone"), link)

		var warnings []string
		captureOnTerminate(ws, "podman", func(m string) { warnings = append(warnings, m) })

		if fi, err := os.Lstat(link); err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("the overlay is not a regular file after the capture (%v, %v): the link must be "+
				"replaced, not followed", fi, err)
		}
		if got := readOverlay(t, ws, "claude-settings"); !strings.Contains(got, "myEdit") {
			t.Errorf("the capture was not delivered (warnings %v):\n%s", warnings, got)
		}
	})
}

// THE FILES the capture READS: the surface, and the three sidecars. A link at any of them had
// a host file read into the capture; the surface's content, or the host file's keys as
// tombstones against a linked baseline, then landed in the overlay the jail reads.
func TestCaptureOnTerminateNeverReadsThroughALink(t *testing.T) {
	for _, tc := range []struct{ name, link string }{
		{"the surface", filepath.Join("home", "claude", "settings.json")},
		{"the last_render baseline", filepath.Join("prism", "claude-settings.last_render")},
		{"the overlay", filepath.Join("prism", "claude-settings.overlay.json")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := terminateWorkspace(t, filepath.Join("claude", "settings.json"),
				`{"model":"base"}`, `{"model":"base","myEdit":"present"}`)
			host, _ := captureHostFile(t)
			captureSymlink(t, host, filepath.Join(ws, ".yolo", tc.link))

			captureOnTerminate(ws, "podman", func(string) {})

			if got := readOverlayNoFollow(t, ws, "claude-settings"); strings.Contains(got, "hostOnlySecret") {
				t.Errorf("the capture read a host file through the jail's link at %s:\n%s", tc.link, got)
			}
		})
	}
	t.Run("the list capture", func(t *testing.T) {
		ws := terminateWorkspace(t, filepath.Join("claude", "settings.json"),
			`{"plugins":["owner"]}`, `{"plugins":["owner","mine"]}`)
		host := filepath.Join(t.TempDir(), "host.json")
		body := `{"/plugins":{"add":["hostOnlyEntry"],"remove":[]}}`
		if err := os.WriteFile(host, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		captureSymlink(t, host, filepath.Join(ws, ".yolo", "prism", "claude-settings"+render.ListCaptureSuffix))

		captureOnTerminate(ws, "podman", func(string) {})

		if got, _ := os.ReadFile(host); string(got) != body {
			t.Errorf("the capture rewrote the host file behind the jail's list-capture link: %s", got)
		}
	})
}

// A LINKED DIRECTORY on the way: the overlay (`.yolo/home`), a dir in it (`.yolo/home/claude`),
// the sidecar dir (`.yolo/prism`) or `.yolo` itself. Through any of them the plain path landed
// in a host directory: the surface was read from it, or the sidecars written into it.
func TestCaptureOnTerminateNeverFollowsALinkedDirectory(t *testing.T) {
	for _, linked := range []string{
		".yolo",
		filepath.Join(".yolo", "home"),
		filepath.Join(".yolo", "home", "claude"),
		filepath.Join(".yolo", "prism"),
	} {
		t.Run(linked, func(t *testing.T) {
			ws := terminateWorkspace(t, filepath.Join("claude", "settings.json"),
				`{"model":"base"}`, `{"model":"base","myEdit":"present"}`)
			// The host directory the link names holds a copy of what was there, with the
			// host's secret in the surface, so a capture through it has something to read
			// and a baseline to write beside.
			hostDir := t.TempDir()
			real := filepath.Join(ws, linked)
			if err := os.Rename(real, hostDir+".src"); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(hostDir); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(hostDir+".src", hostDir); err != nil {
				t.Fatal(err)
			}
			_ = filepath.WalkDir(hostDir, func(p string, d os.DirEntry, err error) error {
				if err == nil && d.Type().IsRegular() && filepath.Base(p) == "settings.json" {
					_ = os.WriteFile(p, []byte(`{"model":"base","hostOnlySecret":"leaked"}`), 0o644)
				}
				return nil
			})
			before := treeListing(t, hostDir)
			captureSymlink(t, hostDir, real)

			captureOnTerminate(ws, "podman", func(string) {})

			if after := treeListing(t, hostDir); after != before {
				t.Errorf("the capture wrote into the host directory behind the link at %s:\nbefore:\n%s\nafter:\n%s",
					linked, before, after)
			}
			if got := readOverlayNoFollow(t, ws, "claude-settings"); strings.Contains(got, "hostOnlySecret") {
				t.Errorf("the capture read the host directory behind the link at %s:\n%s", linked, got)
			}
		})
	}
}

// readOverlayNoFollow is readOverlay that reads only a REGULAR overlay file beneath a real
// `.yolo/prism`: a test must not itself follow the link it planted and report the host file's
// content as the capture's.
func readOverlayNoFollow(t *testing.T, ws, name string) string {
	t.Helper()
	for _, p := range []string{filepath.Join(ws, ".yolo"), filepath.Join(ws, ".yolo", "prism")} {
		if fi, err := os.Lstat(p); err != nil || !fi.IsDir() {
			return ""
		}
	}
	p := filepath.Join(ws, ".yolo", "prism", name+".overlay.json")
	if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	data, _ := os.ReadFile(p)
	return string(data)
}

// treeListing is every path below dir with its size and content, for a before/after compare.
func treeListing(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		b.WriteString(rel)
		if d.Type().IsRegular() {
			data, _ := os.ReadFile(p)
			b.WriteString(" = " + string(data))
		}
		b.WriteString("\n")
		return nil
	})
	return b.String()
}
