package run

// ctxmountsrw_test.go pins the read-write form of `mounts` on the container backends
// (docs/design/context-mounts.md §4 step 1), each at the call site the design's §4 table
// names: the argv from Run's assembler, the launch stream, the refusal from Run(), and the
// briefing from prepare.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// rwMountHome is a fixture home whose USER config declares `mounts` (the only scope a
// read-write element may come from, OQ-WT1's rule D) and returns it with a resolved source
// directory named `name` outside both the home and the workspace.
func rwMountHome(t *testing.T, mountsJSON func(src string) string) (home, src string) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "") // the host-only halves of validation run
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src = filepath.Join(root, "datasets")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"mounts": ` + mountsJSON(src) + `}`
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, src
}

// assembleUserMounts runs Run's assembler with the merged config a launch would hand it
// (the user config's `mounts` in cfg too — the assembler must take the rw element from the
// user scope, and the ro ones from cfg), returning the argv and the launch stream.
func assembleUserMounts(t *testing.T, o *Options, rt string, cfg *jsonx.OrderedMap) ([]string, string) {
	t.Helper()
	emptyLoopholeDirs(t)
	var buf bytes.Buffer
	o.Stderr = &buf
	o.Stdout = &buf
	argv := o.assembleRunCmd(&assembleInput{
		cfg:          cfg,
		rt:           rt,
		cname:        "yolo-ws-abcd1234",
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})
	return argv, buf.String()
}

// mergedMounts is the merged config's `mounts` for a fixture: the same elements the user
// config holds, as LoadConfig would merge them.
func mergedMounts(t *testing.T, mountsJSON string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(mountsJSON))
	if err != nil {
		t.Fatal(err)
	}
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	return newConfig("agents", []any{"claude"}, "security", sec, "mounts", v)
}

// STEP 1'S ARGV PIN: a read-write element binds `src:dest` with NO mode, every read-only one
// keeps `:ro`, and the read-write one comes from the user scope — delete the LoadRWMounts
// read from configCtxMounts, or its call from the assembler, and the rw bind disappears.
func TestAssemblerBindsAReadWriteMountWithoutROAndEveryOtherWithIt(t *testing.T) {
	var elems string
	_, src := rwMountHome(t, func(src string) string {
		elems = `["` + filepath.Dir(src) + `:/ctx/parent", {"host": "` + src + `", "mode": "rw", "at": "/ctx/data"},` +
			` {"host": "` + src + `", "mode": "ro", "at": "/ctx/data-ro"}]`
		return elems
	})
	o := goldenOptions("/ws", os.Getenv("HOME"))

	argv, _ := assembleUserMounts(t, o, "podman", mergedMounts(t, elems))

	got := ctxMountArgs(argv)
	want := []string{
		filepath.Dir(src) + ":/ctx/parent:ro",
		src + ":/ctx/data-ro:ro",
		src + ":/ctx/data",
	}
	if !slices.Equal(got, want) {
		t.Errorf("ctx mounts = %q\nwant       %q", got, want)
	}
}

// THE TRUST PREDICATE'S BUILDER HALF: a read-write element present ONLY in the merged view
// (as a workspace element would be, had validation not refused it first) is never bound —
// the assembler reads read-write elements from the user scope alone, never from the merge.
func TestAssemblerNeverTakesAReadWriteElementFromTheMergedView(t *testing.T) {
	_, src := rwMountHome(t, func(string) string { return `[]` })
	o := goldenOptions("/ws", os.Getenv("HOME"))

	argv, _ := assembleUserMounts(t, o, "podman",
		mergedMounts(t, `[{"host": "`+src+`", "mode": "rw", "at": "/ctx/data"}]`))

	if got := ctxMountArgs(argv); len(got) != 0 {
		t.Errorf("a read-write element the user scope never declared was bound: %q", got)
	}
}

// §2.4's DISCLOSURE, on the launch stream, WITH YOLO_NO_BANNER=1: the one hatch there is
// covers the version line and nothing else (OQ-RO3), so the line naming the host path, the
// jail path and the injection sentence prints anyway.
func TestAReadWriteMountIsDisclosedEvenUnderYoloNoBanner(t *testing.T) {
	var elems string
	_, src := rwMountHome(t, func(src string) string {
		elems = `[{"host": "` + src + `", "mode": "rw", "at": "/ctx/data"}]`
		return elems
	})
	o := goldenOptions("/ws", os.Getenv("HOME"))
	o.Getenv = func(k string) string {
		if k == "YOLO_NO_BANNER" {
			return "1"
		}
		return ""
	}

	_, stream := assembleUserMounts(t, o, "podman", mergedMounts(t, elems))

	for _, want := range []string{
		"Read-write mount:", src + " → /ctx/data",
		"anything on this machine that later reads", "including a symlink the jail planted",
	} {
		if !strings.Contains(stream, want) {
			t.Errorf("the launch stream lacks %q:\n%s", want, stream)
		}
	}
	// Rootless or unknown: no ownership sentence (it is rootful podman's alone, CX-D6).
	if strings.Contains(stream, "ROOTFUL") {
		t.Errorf("a podman that never said it was rootful got the rootful sentence:\n%s", stream)
	}
}

// CX-D6: on a podman that ANSWERED it is rootful, the same line adds the ownership
// consequence, and the mount is still bound — disclosed, not refused.
func TestARootfulPodmanDisclosesWhoOwnsTheWrites(t *testing.T) {
	var elems string
	rwMountHome(t, func(src string) string {
		elems = `[{"host": "` + src + `", "mode": "rw"}]`
		return elems
	})
	o := goldenOptions("/ws", os.Getenv("HOME"))
	facts := &podmanFacts{json: `{"host":{"security":{"rootless":false}}}`}
	if err := json.Unmarshal([]byte(facts.json), &facts.info); err != nil {
		t.Fatal(err)
	}
	facts.parsed = true
	o.podmanFacts = facts

	argv, stream := assembleUserMounts(t, o, "podman", mergedMounts(t, elems))

	if len(ctxMountArgs(argv)) != 1 {
		t.Fatalf("a rootful podman did not bind the read-write mount: %q", ctxMountArgs(argv))
	}
	if !strings.Contains(stream, "owned by root on the host") {
		t.Errorf("the rootful ownership consequence is missing:\n%s", stream)
	}
}

// CX-D12's other half: a NESTED podman reports rootful only because the launch forces it onto
// `--userns host`, and its writes land under the OUTER jail's mapping, not as host root — so
// the same answer adds no ownership sentence there, and the mount is still bound and named.
func TestANestedPodmanReportingRootfulGetsNoOwnershipSentence(t *testing.T) {
	var elems string
	rwMountHome(t, func(src string) string {
		elems = `[{"host": "` + src + `", "mode": "rw"}]`
		return elems
	})
	o := goldenOptions("/ws", os.Getenv("HOME"))
	o.PathExists = func(p string) bool { return p == "/run/.containerenv" }
	facts := &podmanFacts{json: `{"host":{"security":{"rootless":false}}}`}
	if err := json.Unmarshal([]byte(facts.json), &facts.info); err != nil {
		t.Fatal(err)
	}
	facts.parsed = true
	o.podmanFacts = facts

	argv, stream := assembleUserMounts(t, o, "podman", mergedMounts(t, elems))

	if len(ctxMountArgs(argv)) != 1 || !strings.Contains(stream, "Read-write mount:") {
		t.Fatalf("a nested launch did not bind and name the read-write mount: %q\n%s",
			ctxMountArgs(argv), stream)
	}
	if strings.Contains(stream, "owned by root on the host") {
		t.Errorf("a nested podman's forced rootful answer produced the host-root sentence:\n%s", stream)
	}
}

// §2.9: read-write is NOT gated by Apple Container's `:ro` floor. On a `container` whose
// version cannot be read (the fixture's), every read-only element is skipped with its
// reason — never bound writable in its place — and the read-write one is bound.
func TestAppleContainerBindsAReadWriteMountBelowTheROFloor(t *testing.T) {
	var elems string
	_, src := rwMountHome(t, func(src string) string {
		elems = `["` + src + `:/ctx/ro", {"host": "` + src + `", "mode": "rw", "at": "/ctx/rw"}]`
		return elems
	})
	o := goldenOptions("/ws", os.Getenv("HOME"))

	argv, stream := assembleUserMounts(t, o, "container", mergedMounts(t, elems))

	if got := ctxMountArgs(argv); !slices.Equal(got, []string{src + ":/ctx/rw"}) {
		t.Errorf("Apple Container ctx mounts = %q, want only the read-write one", got)
	}
	if !strings.Contains(stream, "Skipping mount "+src+" → /ctx/ro") {
		t.Errorf("the read-only element's skip was not said:\n%s", stream)
	}
}

// EACH REFUSAL FIRES FROM Run(), on a fixture $HOME, before anything starts: an rw element
// in the workspace config (the trust predicate), and an rw source that trips each clause of
// the refusal set.
func TestRunRefusesAReadWriteMountItMayNotGrant(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, home, ws, src string)
		want  string
	}{
		{"declared in the workspace config", func(t *testing.T, home, ws, src string) {
			writeWorkspaceConfig(t, ws, `{"mounts": [{"host": "`+src+`", "mode": "rw"}]}`)
		}, "user-scope only"},
		{"the home", func(t *testing.T, home, ws, src string) {
			writeUserConfig(t, home, `{"mounts": [{"host": "~", "mode": "rw"}]}`)
		}, "IS your home directory"},
		{"yolo's state dir", func(t *testing.T, home, ws, src string) {
			writeUserConfig(t, home, `{"mounts": [{"host": "~/.local/share/yolo-jail", "mode": "rw"}]}`)
		}, "yolo's own state directory"},
		{"the workspace's parent", func(t *testing.T, home, ws, src string) {
			writeUserConfig(t, home, `{"mounts": [{"host": "`+filepath.Dir(ws)+`", "mode": "rw"}]}`)
		}, "contains the workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("YOLO_VERSION", "")
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			wsRoot, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ws := filepath.Join(wsRoot, "proj", "ws")
			src := filepath.Join(wsRoot, "datasets")
			for _, d := range []string{ws, src, filepath.Join(home, ".local", "share", "yolo-jail")} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			tc.setup(t, home, ws, src)

			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
			if rc := Run(*o); rc == 0 {
				t.Fatalf("Run() = 0: the launch went ahead\n%s%s", stdout.String(), stderr.String())
			}
			out := stdout.String() + stderr.String()
			if !strings.Contains(out, "Invalid jail config") || !strings.Contains(out, tc.want) {
				t.Errorf("the refusal does not say %q:\n%s", tc.want, out)
			}
		})
	}
}

// STEP 1'S BRIEFING PIN: the briefing from prepare labels each entry — read-write and
// read-only — names the context dir's variable, and says a read-write mount is the host's
// own directory. Delete briefedCtxMounts' call from prepare and the section is gone.
func TestTheBriefingLabelsEachContextMountsMode(t *testing.T) {
	var elems string
	home, src := rwMountHome(t, func(src string) string {
		elems = `["` + filepath.Dir(src) + `:/ctx/parent", {"host": "` + src + `", "mode": "rw", "at": "/ctx/data"}]`
		return elems
	})
	o := appliedOptions(t, t.TempDir(), home, false)
	emptyLoopholeDirs(t)

	got := appliedBriefing(t, o, "podman", mergedMounts(t, elems))

	for _, want := range []string{
		"## Additional Context Mounts",
		"`$YOLO_CONTEXT_DIR` is `/ctx` here.",
		"- `/ctx/data` (read-write; host `" + src + "`)",
		"- `/ctx/parent` (read-only; host `" + filepath.Dir(src) + "`)",
		"what you write there",
		"read-only unless marked read-write",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the briefing lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Additional Context Mounts (read-only)") {
		t.Errorf("the heading still claims every mount is read-only:\n%s", got)
	}
}

// §3.8: a pack's `mount` grant is listed too, with its pack — before, an agent learned a
// pack mount's path only from the pack's own prose.
func TestTheBriefingListsAPackMountWithItsPack(t *testing.T) {
	home := packHome(t)
	if err := os.MkdirAll(filepath.Join(home, "datasets", "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	acme := mountPackFixture(t, `{"kind":"mount","host":"datasets/acme","into":"acme"}`)
	o := appliedOptions(t, t.TempDir(), home, false)
	emptyLoopholeDirs(t)
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", appliedTestConfig(), "podman",
		stagedPacks{packs: append(claudePackFixture(t), acme...)}, "")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(staging, briefingStagingName(claudeBriefingDest)))
	if err != nil {
		t.Fatal(err)
	}
	if want := "- `/ctx/acme` (read-only; host `" + filepath.Join(home, "datasets", "acme") +
		"`; from pack `acme`)"; !strings.Contains(string(body), want) {
		t.Errorf("the briefing lacks the pack mount line %q:\n%s", want, body)
	}
}

// writeWorkspaceConfig writes a fixture workspace's yolo-jail.jsonc.
func writeWorkspaceConfig(t *testing.T, ws, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// EVERY /ctx BIND YOLO MAKES ITSELF IS ON THE RESERVED LIST that the `mounts` duplicate check
// refuses (paths.ReservedContextPaths, context-mounts.md §3.2). The argv below carries every
// one of them — the staged pack tree, the capture store, a directory and a file `host_files`
// source, and the host nvim config — and no user-declared context mount, so each /ctx
// destination in it is yolo's. A new bind added without a reservation fails here, instead of
// surfacing as podman's "duplicate mount destination" on the launch of whoever mounts a
// directory of the same name.
func TestEveryContextBindYoloMakesItselfIsReserved(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	ws := t.TempDir()
	wsState := filepath.Join(ws, ".yolo", "home")
	packStaging := filepath.Join(home, "pack-staging")
	captures := filepath.Join(home, "captures-store")
	certs := filepath.Join(home, "certs")
	for _, d := range []string{wsState, packStaging, captures, certs, filepath.Join(home, ".config", "nvim")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	npmrc := filepath.Join(home, "npmrc")
	if err := os.WriteFile(npmrc, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := goldenOptions(ws, home)
	o.PathExists = func(p string) bool { _, err := os.Stat(p); return err == nil }
	o.Stdout, o.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})

	argv := o.assembleRunCmd(&assembleInput{
		cfg: newConfig("security", sec), rt: "podman", cname: "yolo-ws-abcd1234",
		packs: claudePackFixture(t), packStaging: packStaging, capturesDir: captures,
		hostFiles: []config.HostFileEntry{
			{Path: "certs", Source: certs, IsDir: true, Mode: config.HostFileModeReadonly},
			{Path: ".npmrc", Source: npmrc, Mode: config.HostFileModeReadonly},
		},
		agentsPath: filepath.Join(ws, "agents"), wsState: wsState,
		miseStore: "/mise-store", yoloVersion: "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})

	seen := map[string]bool{}
	for _, spec := range ctxMountArgs(argv) {
		_, dest, _ := strings.Cut(strings.TrimSuffix(spec, ":ro"), ":")
		reserved := false
		for _, r := range paths.ReservedContextPaths() {
			if dest == r.Path || strings.HasPrefix(dest, r.Path+"/") {
				reserved, seen[r.Path] = true, true
			}
		}
		if !reserved {
			t.Errorf("yolo binds %s itself, and no entry of paths.ReservedContextPaths covers it, "+
				"so a `mounts` element there passes `yolo check` and fails at podman", dest)
		}
	}
	// The fixture is only a census if it reached every reserved bind.
	for _, r := range paths.ReservedContextPaths() {
		if !seen[r.Path] {
			t.Errorf("the fixture argv never bound %s, so this test says nothing about it:\n%s",
				r.Path, strings.Join(ctxMountArgs(argv), "\n"))
		}
	}
}
