package cli

// configverblinks_test.go pins that the host-side `yolo config` verbs never follow a link the
// jail planted in its workspace's state (docs/reference/jail-home.md, "Host code in
// jail-writable state"). At a workspace target, `yolo config diff|ls|reset|capture --force`
// read and write that jail's own files from the HOST, as the host user: the sidecars in
// `<workspace>/.yolo/prism` and the surfaces in the workspace overlay `<workspace>/.yolo/home`.
// The jail can write both through /workspace/.yolo, so it can leave a symbolic link at any of
// those names, or at a directory above them. Through a link, `diff` and `ls` printed a host
// file's content, `reset` truncated the host file a surface link named and wrote a baseline
// beside it, and `capture --force` read a host file into the overlay the jail reads and wrote
// capture JSON through a sidecar link into the host file it named.
//
// Each case plants the link, runs the verb through configRunW (the production dispatch, which
// resolves the target from the cwd), and checks both the host side and that the verb named
// what it refused.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// verbLinkWorld is a host-side workspace target: HOME is a scratch dir, the process is not in
// a jail, the runtime is podman (so ~/.claude/settings.json is backed by
// <ws>/.yolo/home/claude/settings.json), and no jail is running for the workspace.
type verbLinkWorld struct{ ws, store string }

func newVerbLinkWorld(t *testing.T) verbLinkWorld {
	t.Helper()
	scratchHostHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_RUNTIME", "podman")
	withJailLiveness(t, "", true)
	ws, store := withWorkspaceCwd(t)
	return verbLinkWorld{ws: ws, store: store}
}

func (w verbLinkWorld) surface() string {
	return filepath.Join(w.ws, ".yolo", "home", "claude", "settings.json")
}

func (w verbLinkWorld) sidecar(suffix string) string {
	return filepath.Join(w.store, "claude-settings"+suffix)
}

// seed writes the surface and its sidecars; an empty string leaves that file out.
func (w verbLinkWorld) seed(t *testing.T, surface, lastRender, overlay, listCapture string) {
	t.Helper()
	for path, body := range map[string]string{
		w.surface():                         surface,
		w.sidecar(".last_render"):           lastRender,
		w.sidecar(".overlay.json"):          overlay,
		w.sidecar(render.ListCaptureSuffix): listCapture,
	} {
		if body != "" {
			writeFile(t, path, body)
		}
	}
}

// verbLinkShape is one shape a planted link takes: plant puts it at link (a host file holding
// body, or nothing at all) and returns the check that the host side was left alone.
type verbLinkShape struct {
	name  string
	plant func(t *testing.T, link, body string) (verify func(t *testing.T))
}

var verbLinkShapes = []verbLinkShape{
	{"a link to an existing host file", func(t *testing.T, link, body string) func(*testing.T) {
		host := filepath.Join(t.TempDir(), "host-file")
		if err := os.WriteFile(host, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		captureSymlink(t, host, link)
		return func(t *testing.T) {
			t.Helper()
			if got, err := os.ReadFile(host); err != nil || string(got) != body {
				t.Errorf("the verb wrote through the jail's link into the host file: %q (err %v)", got, err)
			}
		}
	}},
	{"a dangling link", func(t *testing.T, link, _ string) func(*testing.T) {
		dir := t.TempDir()
		captureSymlink(t, filepath.Join(dir, "created-by-the-follow"), link)
		return func(t *testing.T) {
			t.Helper()
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("the verb created %v in a host directory through the jail's link", entries)
			}
		}
	}},
}

// linkDirToHost moves the real directory at <ws>/rel into a host temp dir, puts secret into
// every settings.json below it, and plants a link at <ws>/rel naming it. It returns the host
// directory and a listing of it, for the before/after compare.
func linkDirToHost(t *testing.T, ws, rel, secret string) (hostDir, before string) {
	t.Helper()
	hostDir = t.TempDir()
	real := filepath.Join(ws, rel)
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
			_ = os.WriteFile(p, []byte(secret), 0o644)
		}
		return nil
	})
	before = treeListing(t, hostDir)
	captureSymlink(t, hostDir, real)
	return hostDir, before
}

// assertNamedRefusal checks that the verb named the link it refused, and said why.
func assertNamedRefusal(t *testing.T, errw, path string) {
	t.Helper()
	if !strings.Contains(errw, path) || !strings.Contains(errw, "the jail can write") {
		t.Errorf("the verb did not name the link it refused (%s) and say the jail can write it:\n%s",
			path, errw)
	}
}

// The directories above a store file or a surface a jail can replace with a link.
var (
	storeDirLinks = []string{".yolo", filepath.Join(".yolo", "prism")}
	homeDirLinks  = []string{filepath.Join(".yolo", "home"), filepath.Join(".yolo", "home", "claude")}
)

// --- diff ---------------------------------------------------------------------------------

// `yolo config diff` READS the overlay, the baseline and the list capture. Through a link at
// any of them it printed the host file's content as captured edits.
func TestConfigDiffNeverReadsThroughALink(t *testing.T) {
	for _, tc := range []struct {
		name, suffix, lastRender, overlay, host string
	}{
		{"the overlay", ".overlay.json", `{"model":"base"}`, "",
			`{"hostOnlySecret":"leaked"}`},
		{"the last_render baseline", ".last_render", "", `{"hostOnlySecret":"captured"}`,
			`{"hostOnlySecret":"leaked"}`},
		{"the list capture", render.ListCaptureSuffix, `{"model":"base"}`, "",
			`{"/plugins":{"add":["leaked"],"remove":[]}}`},
	} {
		for _, shape := range verbLinkShapes {
			t.Run(tc.name+"/"+shape.name, func(t *testing.T) {
				w := newVerbLinkWorld(t)
				w.seed(t, `{"model":"base"}`, tc.lastRender, tc.overlay, "")
				link := w.sidecar(tc.suffix)
				verify := shape.plant(t, link, tc.host)

				rc, out, errw := runConfigVerb(t, "diff", "claude/settings")

				verify(t)
				if strings.Contains(out+errw, "leaked") {
					t.Errorf("diff printed a host file read through the jail's link at %s:\n%s%s", link, out, errw)
				}
				if rc == 0 {
					t.Errorf("diff exited 0 over a store file it refused to read:\n%s%s", out, errw)
				}
				assertNamedRefusal(t, errw, link)
			})
		}
	}
}

func TestConfigDiffNeverFollowsALinkedDirectory(t *testing.T) {
	for _, linked := range storeDirLinks {
		t.Run(linked, func(t *testing.T) {
			w := newVerbLinkWorld(t)
			w.seed(t, `{"model":"base"}`, `{"model":"base"}`, `{"hostOnlySecret":"leaked"}`, "")
			hostDir, before := linkDirToHost(t, w.ws, linked, `{"hostOnlySecret":"leaked"}`)

			rc, out, errw := runConfigVerb(t, "diff", "claude/settings")

			if after := treeListing(t, hostDir); after != before {
				t.Errorf("diff wrote into the host directory behind %s:\n%s", linked, after)
			}
			if strings.Contains(out+errw, "leaked") {
				t.Errorf("diff printed the host directory behind the link at %s:\n%s%s", linked, out, errw)
			}
			if rc == 0 {
				t.Errorf("diff exited 0 over a store it refused to read:\n%s%s", out, errw)
			}
			assertNamedRefusal(t, errw, filepath.Join(w.ws, linked))
		})
	}
}

// --- ls -----------------------------------------------------------------------------------

// `yolo config ls` READS the overlay and the list capture for its OVERLAY column, and the
// provenance record for its per-key report. Through a link it counted, or listed, a host
// file's keys. The provenance case selects a pack owning acme/settings (writeOverlayFixture),
// and the list-contribution case a `config-list` pack on pi/settings, because the per-key and
// per-list reports read a store file only for a surface a configured pack declares.
func TestConfigLsNeverReadsThroughALink(t *testing.T) {
	for _, tc := range []struct {
		name, file, host, leak string
		packs                  func(t *testing.T)
	}{
		{"the overlay", "claude-settings.overlay.json",
			`{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7}`, "7 keys", nil},
		{"the list capture", "claude-settings" + render.ListCaptureSuffix,
			`{"/plugins":{"add":["a","b","c","d","e"],"remove":[]}}`, "5 list entries", nil},
		{"the provenance record", "acme-settings.provenance",
			"hostOnlySecret\tretired:leakedLayer\n", "hostOnlySecret",
			func(t *testing.T) { writeOverlayFixture(t, map[string]string{"acme": acmeOwnerPackJSON}) }},
		{"the list capture a list contribution reports", "pi-settings" + render.ListCaptureSuffix,
			`{"/packages":{"add":["a","b","c"],"remove":["d"]}}`, "3 added and 1 removed",
			func(t *testing.T) {
				home := os.Getenv("HOME")
				writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
					`{"packs":["pi",`+appendKilo(t, home)+`]}`)
			}},
	} {
		for _, shape := range verbLinkShapes {
			t.Run(tc.name+"/"+shape.name, func(t *testing.T) {
				w := newVerbLinkWorld(t)
				if tc.packs != nil {
					tc.packs(t)
				}
				w.seed(t, `{"model":"base"}`, `{"model":"base"}`, "", "")
				link := filepath.Join(w.store, tc.file)
				verify := shape.plant(t, link, tc.host)

				rc, out, errw := runConfigVerb(t, "ls")

				verify(t)
				if strings.Contains(out+errw, tc.leak) {
					t.Errorf("ls reported a host file read through the jail's link at %s (%q):\n%s%s",
						link, tc.leak, out, errw)
				}
				if rc == 0 {
					t.Errorf("ls exited 0 over a store file it refused to read:\n%s%s", out, errw)
				}
				assertNamedRefusal(t, errw, link)
			})
		}
	}
}

func TestConfigLsNeverFollowsALinkedDirectory(t *testing.T) {
	for _, linked := range append(append([]string{}, storeDirLinks...), homeDirLinks...) {
		t.Run(linked, func(t *testing.T) {
			w := newVerbLinkWorld(t)
			w.seed(t, `{"model":"base"}`, `{"model":"base"}`,
				`{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7}`, "")
			hostDir, before := linkDirToHost(t, w.ws, linked, `{"hostOnlySecret":"leaked"}`)

			rc, out, errw := runConfigVerb(t, "ls")

			if after := treeListing(t, hostDir); after != before {
				t.Errorf("ls wrote into the host directory behind %s:\n%s", linked, after)
			}
			if strings.Contains(out, "7 keys") {
				t.Errorf("ls counted the host directory's overlay behind %s:\n%s", linked, out)
			}
			if rc == 0 {
				t.Errorf("ls exited 0 over a directory it refused to follow:\n%s%s", out, errw)
			}
			assertNamedRefusal(t, errw, filepath.Join(w.ws, linked))
		})
	}
}

// --- reset --------------------------------------------------------------------------------

// `yolo config reset` TRUNCATES the surface to its pure render and writes the baseline back.
// Through a link at the surface it truncated the host file the link named. The refusal comes
// BEFORE the sidecars are discarded: a reset that dropped the captures and then declined the
// surface would leave the next launch to adopt the very edits it was asked to discard.
func TestConfigResetNeverWritesASurfaceThroughALink(t *testing.T) {
	for _, shape := range verbLinkShapes {
		t.Run(shape.name, func(t *testing.T) {
			w := newVerbLinkWorld(t)
			w.seed(t, "", `{"theme":"yolos"}`, `{"theme":"the agents edit"}`, "")
			verify := shape.plant(t, w.surface(), `{"theme":"the agents edit","hostOnlySecret":"kept"}`)

			rc, out, errw := runConfigVerb(t, "reset", "claude/settings")

			verify(t)
			if rc == 0 {
				t.Errorf("reset exited 0 over a surface it refused to write:\n%s%s", out, errw)
			}
			assertNamedRefusal(t, errw, w.surface())
			if _, err := os.Lstat(w.sidecar(".overlay.json")); err != nil {
				t.Errorf("reset discarded the captures and then refused the surface (%v): the next "+
					"launch would adopt the edits it was asked to discard", err)
			}
		})
	}
}

// Reset REMOVES the sidecars, which never follows a link, but it READS the overlay and the list
// capture first to count what it discards. Through a link it counted a host file's keys.
func TestConfigResetNeverReadsASidecarThroughALink(t *testing.T) {
	for _, tc := range []struct{ name, suffix, host, leak string }{
		{"the overlay", ".overlay.json", `{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7}`, "7 captured"},
		{"the list capture", render.ListCaptureSuffix,
			`{"/plugins":{"add":["a","b","c","d","e"],"remove":[]}}`, "5 captured list"},
	} {
		for _, shape := range verbLinkShapes {
			t.Run(tc.name+"/"+shape.name, func(t *testing.T) {
				w := newVerbLinkWorld(t)
				w.seed(t, `{"theme":"yolos"}`, `{"theme":"yolos"}`, "", "")
				link := w.sidecar(tc.suffix)
				verify := shape.plant(t, link, tc.host)

				_, out, errw := runConfigVerb(t, "reset", "claude/settings")

				verify(t)
				if strings.Contains(out, tc.leak) {
					t.Errorf("reset counted a host file read through the jail's link at %s:\n%s", link, out)
				}
				assertNamedRefusal(t, errw, link)
				if _, err := os.Lstat(link); !os.IsNotExist(err) {
					t.Errorf("reset left the jail's link at %s (err %v)", link, err)
				}
			})
		}
	}
}

// The baseline reset writes back goes to a regular file in the store: a link the jail left at
// last_render is removed with the other sidecars, and the re-seed never lands behind it.
func TestConfigResetNeverWritesTheBaselineThroughALink(t *testing.T) {
	for _, shape := range verbLinkShapes {
		t.Run(shape.name, func(t *testing.T) {
			w := newVerbLinkWorld(t)
			w.seed(t, `{"theme":"the agents edit"}`, "", `{"theme":"the agents edit"}`, "")
			verify := shape.plant(t, w.sidecar(".last_render"), `{"hostOnlySecret":"kept"}`)

			runConfigVerb(t, "reset", "claude/settings")

			verify(t)
			if fi, err := os.Lstat(w.sidecar(".last_render")); err == nil && !fi.Mode().IsRegular() {
				t.Errorf("the re-seeded baseline is not a regular file: %v", fi.Mode())
			}
		})
	}
}

func TestConfigResetNeverFollowsALinkedDirectory(t *testing.T) {
	for _, linked := range append(append([]string{}, storeDirLinks...), homeDirLinks...) {
		t.Run(linked, func(t *testing.T) {
			w := newVerbLinkWorld(t)
			w.seed(t, `{"theme":"the agents edit"}`, `{"theme":"yolos"}`,
				`{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7}`, "")
			hostDir, before := linkDirToHost(t, w.ws, linked, `{"theme":"yours","hostOnlySecret":"kept"}`)

			rc, out, errw := runConfigVerb(t, "reset", "claude/settings")

			if after := treeListing(t, hostDir); after != before {
				t.Errorf("reset changed the host directory behind %s:\nbefore:\n%s\nafter:\n%s",
					linked, before, after)
			}
			if rc == 0 {
				t.Errorf("reset exited 0 over a directory it refused to follow:\n%s%s", out, errw)
			}
			assertNamedRefusal(t, errw, filepath.Join(w.ws, linked))
		})
	}
}

// --- capture --force ----------------------------------------------------------------------

// `yolo config capture --force` READS the surface and all three sidecars, and WRITES the
// overlay and the list capture. Through a link it folded a host file into the overlay the jail
// reads, or wrote capture JSON into the host file the link named.
func TestConfigCaptureNeverFollowsALink(t *testing.T) {
	for _, tc := range []struct {
		name, link, baseline, current, host string
	}{
		{"the surface", "surface", `{"model":"base"}`, "",
			`{"model":"base","hostOnlySecret":"leaked"}`},
		{"the last_render baseline", ".last_render", "", `{"model":"base","myEdit":"present"}`,
			`{"model":"base","hostOnlySecret":"leaked"}`},
		{"the overlay", ".overlay.json", `{"model":"base"}`, `{"model":"base","myEdit":"present"}`,
			`{"hostOnlySecret":"leaked"}`},
		{"the list capture", render.ListCaptureSuffix, `{"plugins":["owner"]}`,
			`{"plugins":["owner","mine"]}`, `{"/plugins":{"add":["hostOnlySecret"],"remove":[]}}`},
	} {
		for _, shape := range verbLinkShapes {
			t.Run(tc.name+"/"+shape.name, func(t *testing.T) {
				w := newVerbLinkWorld(t)
				w.seed(t, tc.current, tc.baseline, "", "")
				link := w.surface()
				if tc.link != "surface" {
					link = w.sidecar(tc.link)
				}
				verify := shape.plant(t, link, tc.host)

				_, out, errw := runConfigVerb(t, "capture", "claude/settings", "--force")

				verify(t)
				if got := readOverlayNoFollow(t, w.ws, "claude-settings"); strings.Contains(got, "hostOnlySecret") {
					t.Errorf("capture read a host file through the jail's link at %s:\n%s", link, got)
				}
				assertNamedRefusal(t, out+errw, link)
			})
		}
	}
}

// A `user` surface is discovered by LISTING the store (userSidecarSurfaces). Through a link at
// `.yolo/prism` the listing named the files in the host directory the link pointed at, and
// capture printed each one back as a surface ("never rendered here").
func TestConfigCaptureNeverListsALinkedStore(t *testing.T) {
	w := newVerbLinkWorld(t)
	w.seed(t, `{"model":"base"}`, `{"model":"base"}`, "", "")
	writeFile(t, filepath.Join(w.store, "user-hostOnlySlug.overlay.json"), `{"k":1}`)
	linked := filepath.Join(".yolo", "prism")
	hostDir, before := linkDirToHost(t, w.ws, linked, "")

	_, out, errw := runConfigVerb(t, "capture", "user", "--force")

	if after := treeListing(t, hostDir); after != before {
		t.Errorf("capture changed the host directory behind %s:\n%s", linked, after)
	}
	if strings.Contains(out+errw, "hostOnlySlug") {
		t.Errorf("capture listed the host directory behind the link at %s:\n%s%s", linked, out, errw)
	}
	assertNamedRefusal(t, errw, filepath.Join(w.ws, linked))
}

func TestConfigCaptureNeverFollowsALinkedDirectory(t *testing.T) {
	for _, linked := range append(append([]string{}, storeDirLinks...), homeDirLinks...) {
		t.Run(linked, func(t *testing.T) {
			w := newVerbLinkWorld(t)
			w.seed(t, `{"model":"base","myEdit":"present"}`, `{"model":"base"}`, "", "")
			hostDir, before := linkDirToHost(t, w.ws, linked, `{"model":"base","hostOnlySecret":"leaked"}`)

			_, _, errw := runConfigVerb(t, "capture", "claude/settings", "--force")

			if after := treeListing(t, hostDir); after != before {
				t.Errorf("capture wrote into the host directory behind %s:\nbefore:\n%s\nafter:\n%s",
					linked, before, after)
			}
			if got := readOverlayNoFollow(t, w.ws, "claude-settings"); strings.Contains(got, "hostOnlySecret") {
				t.Errorf("capture read the host directory behind the link at %s:\n%s", linked, got)
			}
			assertNamedRefusal(t, errw, filepath.Join(w.ws, linked))
		})
	}
}
