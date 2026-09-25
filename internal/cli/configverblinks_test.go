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
// capture JSON through a sidecar link into the host file it named. `yolo config promote`
// declared a host file's keys in a pack manifest and wrote the overlay back into a linked
// store, and `yolo apply --sealed` counted a host file's keys, or sealed over a dangling link.
//
// Each case plants the link, runs the verb through its production dispatch (configRunW, which
// resolves the target from the cwd, or applyMain), and checks both the host side and that the
// verb named what it refused.

import (
	"bytes"
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

// --- promote ------------------------------------------------------------------------------

// promoteLinkWorld is a verbLinkWorld whose configured packs own claude/settings, so a
// captured key there is promotable into the conventional local pack.
func promoteLinkWorld(t *testing.T) verbLinkWorld {
	t.Helper()
	w := newVerbLinkWorld(t)
	t.Setenv("YOLO_USE_PROFILES", "")
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude"]}`)
	return w
}

// localPackManifestPath is where promote declares a key.
func localPackManifestPath() string {
	return filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "local", "pack.json")
}

// `yolo config promote` is a host-side verb over a jail's store: it READS the overlay, the
// baseline and the list capture, and on --accept-promotion it copies the captured keys into a
// pack manifest under ~/.config/yolo-jail and WRITES the overlay back without them. Through a
// link at a sidecar it read a host file and declared its keys in the manifest, which every
// later jail reads.
func TestConfigPromoteNeverReadsThroughALink(t *testing.T) {
	for _, tc := range []struct {
		name, suffix, overlay, host, leak string
	}{
		{"the overlay", ".overlay.json", "", `{"hostOnlyPref":"leaked"}`, "hostOnlyPref"},
		{"the last_render baseline", ".last_render", `{"autoMemoryEnabled":true}`,
			`{"autoMemoryEnabled":true}`, "redundant"},
		{"the list capture", render.ListCaptureSuffix, "",
			`{"/plugins":{"add":["a","b","c","d","e"],"remove":[]}}`, "5 captured list"},
	} {
		for _, shape := range verbLinkShapes {
			t.Run(tc.name+"/"+shape.name, func(t *testing.T) {
				w := promoteLinkWorld(t)
				lastRender := `{"model":"base"}`
				if tc.suffix == ".last_render" {
					lastRender = ""
				}
				w.seed(t, `{"model":"base"}`, lastRender, tc.overlay, "")
				link := w.sidecar(tc.suffix)
				verify := shape.plant(t, link, tc.host)

				rc, out, errw := runConfigVerb(t, "promote", "claude/settings", "--accept-promotion")

				verify(t)
				if strings.Contains(out+errw, tc.leak) {
					t.Errorf("promote reported a host file read through the jail's link at %s (%q):\n%s%s",
						link, tc.leak, out, errw)
				}
				if data, err := os.ReadFile(localPackManifestPath()); err == nil {
					t.Errorf("promote declared keys from a store it refused to read:\n%s", data)
				}
				if rc == 0 {
					t.Errorf("promote exited 0 over a store file it refused to read:\n%s%s", out, errw)
				}
				assertNamedRefusal(t, errw, link)
			})
		}
	}
}

// With `.yolo` or `.yolo/prism` a link to a host directory, promote read that directory's
// overlay, declared its keys, and wrote the overlay back into it.
func TestConfigPromoteNeverFollowsALinkedDirectory(t *testing.T) {
	for _, linked := range storeDirLinks {
		t.Run(linked, func(t *testing.T) {
			w := promoteLinkWorld(t)
			w.seed(t, `{"model":"base"}`, `{"model":"base"}`, `{"hostOnlyPref":"leaked"}`, "")
			hostDir, before := linkDirToHost(t, w.ws, linked, `{"hostOnlyPref":"leaked"}`)

			rc, out, errw := runConfigVerb(t, "promote", "claude/settings", "--accept-promotion")

			if after := treeListing(t, hostDir); after != before {
				t.Errorf("promote wrote into the host directory behind %s:\nbefore:\n%s\nafter:\n%s",
					linked, before, after)
			}
			if strings.Contains(out+errw, "hostOnlyPref") {
				t.Errorf("promote reported the host directory behind the link at %s:\n%s%s", linked, out, errw)
			}
			if data, err := os.ReadFile(localPackManifestPath()); err == nil {
				t.Errorf("promote declared keys read through the link at %s:\n%s", linked, data)
			}
			if rc == 0 {
				t.Errorf("promote exited 0 over a store it refused to read:\n%s%s", out, errw)
			}
			assertNamedRefusal(t, errw, filepath.Join(w.ws, linked))
		})
	}
}

// Between the plan and the write the jail can swap the overlay, or the store it is in, for a
// link. The write-back replaces a link at the overlay with a regular file, and refuses a linked
// store, abandoning the promotion; it never writes into the host file or directory a link names.
func TestConfigPromoteNeverWritesTheOverlayThroughALink(t *testing.T) {
	for _, tc := range []struct {
		name string
		swap func(t *testing.T, w verbLinkWorld) (verify func(t *testing.T))
		ok   bool
	}{
		{"a link at the overlay", func(t *testing.T, w verbLinkWorld) func(*testing.T) {
			host := filepath.Join(t.TempDir(), "host-file")
			writeFile(t, host, `{"hostOnlyPref":"kept"}`)
			captureSymlink(t, host, w.sidecar(".overlay.json"))
			return func(t *testing.T) {
				t.Helper()
				if got, _ := os.ReadFile(host); string(got) != `{"hostOnlyPref":"kept"}` {
					t.Errorf("promote wrote the overlay through the jail's link into the host file: %q", got)
				}
				fi, err := os.Lstat(w.sidecar(".overlay.json"))
				if err != nil || !fi.Mode().IsRegular() {
					t.Fatalf("the rewritten overlay is not a regular file (err %v)", err)
				}
				if got := readOverlayNoFollow(t, w.ws, "claude-settings"); !strings.Contains(got, "kept") ||
					strings.Contains(got, "autoMemoryEnabled") {
					t.Errorf("the rewritten overlay = %q, want only the unpromoted key", got)
				}
			}
		}, true},
		{"a link at .yolo/prism", func(t *testing.T, w verbLinkWorld) func(*testing.T) {
			hostDir, before := linkDirToHost(t, w.ws, filepath.Join(".yolo", "prism"), "")
			return func(t *testing.T) {
				t.Helper()
				if after := treeListing(t, hostDir); after != before {
					t.Errorf("promote wrote into the host directory behind .yolo/prism:\nbefore:\n%s\nafter:\n%s",
						before, after)
				}
				if _, err := os.Lstat(localPackManifestPath()); !os.IsNotExist(err) {
					t.Errorf("the manifest survived a promotion whose overlay reset was refused (err %v)", err)
				}
			}
		}, false},
		// Not a jail's link: a second name for the overlay's inode, which tells a rename from
		// an in-place truncate. The write-back must stay atomic beneath its root, so a crash
		// mid-write cannot leave a truncated overlay, and a truncate would rewrite this name too.
		{"a hard link to the overlay (the rewrite is a rename)", func(t *testing.T, w verbLinkWorld) func(*testing.T) {
			second := filepath.Join(w.ws, "second-name")
			if err := os.Link(w.sidecar(".overlay.json"), second); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(second)
			return func(t *testing.T) {
				t.Helper()
				if got, _ := os.ReadFile(second); string(got) != string(before) {
					t.Errorf("the overlay was truncated and rewritten in place, not replaced: %q", got)
				}
			}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := promoteLinkWorld(t)
			w.seed(t, `{"model":"base"}`, `{"model":"base"}`, `{"autoMemoryEnabled":true,"kept":1}`, "")

			var verify func(*testing.T)
			real := promoteWriteFile
			promoteWriteFile = func(dst captureFile, data []byte) error {
				if verify == nil && strings.HasSuffix(dst.path(), ".overlay.json") {
					verify = tc.swap(t, w)
				}
				return real(dst, data)
			}
			t.Cleanup(func() { promoteWriteFile = real })

			rc, out, errw := runConfigVerb(t, "promote", "claude/settings", "--keys", "autoMemoryEnabled",
				"--accept-promotion")
			if verify == nil {
				t.Fatalf("promote never wrote the overlay (rc=%d):\n%s%s", rc, out, errw)
			}
			verify(t)
			if tc.ok && rc != 0 {
				t.Errorf("rc=%d:\n%s%s", rc, out, errw)
			}
			if !tc.ok {
				if rc == 0 {
					t.Errorf("promote exited 0 over an overlay reset it refused:\n%s%s", out, errw)
				}
				assertNamedRefusal(t, errw, filepath.Join(w.ws, ".yolo", "prism"))
			}
		})
	}
}

// --- apply --sealed -----------------------------------------------------------------------

// sealedLinkWorld is a verbLinkWorld in which `yolo apply --sealed` has exactly one question
// left: the workspace declares packs, and the user declares host_management.
func sealedLinkWorld(t *testing.T) verbLinkWorld {
	t.Helper()
	w := newVerbLinkWorld(t)
	writeFile(t, filepath.Join(w.ws, "yolo-jail.jsonc"), `{"packs":["claude"]}`)
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc"),
		`{"host_management":"assert"}`)
	return w
}

func runApplySealed(t *testing.T) (rc int, stdout, stderr string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc = applyMain([]string{"--sealed"}, &out, &errw, false, nil)
	return rc, out.String(), errw.String()
}

// `yolo apply --sealed` counts each surface's captured keys in the workspace store, host-side.
// Through a link it counted a host file's keys, and through a dangling one it counted none and
// reported the environment sealed: a store it did not read, certified as holding nothing.
func TestApplySealedNeverReadsThroughALink(t *testing.T) {
	for _, shape := range verbLinkShapes {
		t.Run(shape.name, func(t *testing.T) {
			w := sealedLinkWorld(t)
			link := w.sidecar(".overlay.json")
			verify := shape.plant(t, link, `{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7}`)

			rc, out, errw := runApplySealed(t)

			verify(t)
			if strings.Contains(out, "has 7 captured") {
				t.Errorf("apply --sealed counted a host file read through the jail's link at %s:\n%s", link, out)
			}
			if rc == 0 {
				t.Errorf("apply --sealed exited 0 over a store file it refused to read:\n%s%s", out, errw)
			}
			assertNamedRefusal(t, out, link)
		})
	}
}

func TestApplySealedNeverFollowsALinkedDirectory(t *testing.T) {
	for _, linked := range storeDirLinks {
		t.Run(linked, func(t *testing.T) {
			w := sealedLinkWorld(t)
			w.seed(t, "", "", `{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7}`, "")
			hostDir, before := linkDirToHost(t, w.ws, linked, "")

			rc, out, errw := runApplySealed(t)

			if after := treeListing(t, hostDir); after != before {
				t.Errorf("apply --sealed changed the host directory behind %s:\n%s", linked, after)
			}
			if strings.Contains(out, "has 7 captured") {
				t.Errorf("apply --sealed counted the host directory's overlay behind %s:\n%s", linked, out)
			}
			if rc == 0 {
				t.Errorf("apply --sealed exited 0 over a directory it refused to follow:\n%s%s", out, errw)
			}
			assertNamedRefusal(t, out, filepath.Join(w.ws, linked))
		})
	}
}
