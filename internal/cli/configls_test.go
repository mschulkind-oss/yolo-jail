package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// withSidecarDir mints a temp WORKSPACE, creates its capture store, and returns both the
// config target a `yolo config` verb standing in that workspace resolves and the store's
// directory.
//
// IT IS THE SEAM THE RETIRED PAIR WAS, collapsed into one. `prismSidecarDir` and
// `surfacesAreLocal` were two independently stubbable predicates, and that is precisely how
// the store and the home came to be resolvable independently
// (docs/reference/config-target-resolution.md#the-config-target): a test could pin one and leave the other
// ambient. A test now constructs the whole ANSWER it means and hands it to the verb the way
// configRunW does. Without the seam an in-jail `go test` would read — and `reset` would
// DELETE — the real /workspace sidecars.
//
// HOST-SIDE by default (local=false), which is what a bare CI runner is: the write guard
// refuses there, so a reset/capture happy path wants withLocalSidecarDir.
func withSidecarDir(t *testing.T) (configTarget, string) {
	t.Helper()
	return sidecarTarget(t, false)
}

// withLocalSidecarDir is withSidecarDir as the jail that OWNS the workspace sees it, so a
// reset/capture happy path runs the in-jail-owner branch regardless of the ambient
// environment. Without it these tests pass in a dev jail (YOLO_VERSION set, /workspace
// mounted) and the Phase-0 guard refuses on a bare CI runner, where neither holds — the
// classic passes-in-jail-fails-in-CI trap. Tests that model the host-side branch (e.g.
// TestResetCaptureRefuseHostSideWithoutForce) must NOT call it.
func withLocalSidecarDir(t *testing.T) (configTarget, string) {
	t.Helper()
	return sidecarTarget(t, true)
}

// sidecarTarget builds the target through jailConfigTarget — the PRODUCTION constructor —
// and overrides only the one ambient fact a bare runner cannot supply. Rebuilding the struct
// by hand here would be a fixture free to disagree with the resolution it stands in for,
// which is the shape this design exists to remove.
func sidecarTarget(t *testing.T, local bool) (configTarget, string) {
	t.Helper()
	// EvalSymlinks where the path is MINTED: on darwin t.TempDir() hands back /var/folders/…,
	// which IS a symlink to /private/var/folders/…, and this design compares resolved
	// workspace paths. Resolving here rather than at each comparison is the fix the next
	// comparison added cannot forget.
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp workspace: %v", err)
	}
	tgt := jailConfigTarget(ws, "the cwd")
	tgt.local = local
	dir := tgt.sidecarDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create the capture store: %v", err)
	}
	return tgt, dir
}

// withWorkspaceCwd mints a temp workspace, MARKS it, chdirs into it, and creates its capture
// store. It is the fixture for a test that drives configRunW, where the RESOLUTION is the
// subject rather than an input.
//
// ⚠ NOT OPTIONAL for any test that reaches configRunW, and not hygiene. The resolution walks
// the CWD, and this package's own directory sits under the live /workspace checkout, which
// carries a yolo-jail.jsonc — so a `reset --force` driven through configRunW without this
// resolves THIS session's own workspace and DELETES its real sidecars. That accident is
// exactly what the retired prismSidecarDir seam existed to prevent, and handing a verb a
// target no longer protects a test that lets configRunW resolve its own.
func withWorkspaceCwd(t *testing.T) (ws, store string) {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp workspace: %v", err)
	}
	// The ruled marker: a workspace config file is enough, and a bare .yolo is not
	// (docs/reference/config-target-resolution.md [OQ-CR2]).
	writeFile(t, filepath.Join(ws, config.WorkspaceConfigName), `{}`)
	store = render.Jail(paths.Home(), ws, nil).SidecarDir()
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatalf("create the capture store: %v", err)
	}
	t.Chdir(ws)
	return ws, store
}

// hostTargetForTest is what a directory resolving no workspace resolves: the invoking
// process's own home, under whatever `host_management` it declares. Call it AFTER any
// t.Setenv("HOME", …) — the contract and the store are read at construction, which is the
// order the real resolution runs in.
func hostTargetForTest() configTarget {
	return hostConfigTarget("the cwd resolving no workspace")
}

// withScratchHome points $HOME at a temp dir for tests that RESET a surface.
//
// ⚠ Not optional, and not hygiene. `reset` truncates the surface file to its pure render
// (ruling 1), and `expandHome` resolves that path against the ambient $HOME — so a reset test
// without this rewrites the DEVELOPER'S OWN ~/.claude/settings.json, in the jail this repo is
// developed inside. It survived unnoticed because claude/settings declares `readsHost`, so the
// jail-notch truncation re-reads the file as its own host layer and composes the same bytes
// back; a surface without that declaration would have been emptied. It also makes the reset
// path's own behaviour ambient: whether reset has a file to truncate — and therefore a
// baseline to re-seed — would depend on what the machine happens to have in its home.
func withScratchHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// writeSidecar seeds an overlay (and optionally a last_render baseline).
func writeSidecar(t *testing.T, dir, agent, name, overlayJSON, lastRenderJSON string) {
	t.Helper()
	if overlayJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, agent+"-"+name+".overlay.json"), []byte(overlayJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if lastRenderJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, agent+"-"+name+".last_render"), []byte(lastRenderJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestConfigLsListsSurfacesAndFlagsOverlay: the listing must show every surface's
// construction AND flag the ones carrying captured edits — the whole point, since an
// overlay outranks every layer but `computed` and `managed` with no other user-facing
// view. captureprecedence_test.go pins the footer's wording and that ceiling.
func TestConfigLsListsSurfacesAndFlagsOverlay(t *testing.T) {
	tgt, dir := withSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{"theme":"dark","model":null}`, `{"theme":"light"}`)

	var out, errw bytes.Buffer
	if rc := configLs(tgt, []string{"--all"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configLs rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	for _, want := range []string{"SURFACE", "claude/settings", "~/.claude/settings.json", "capture"} {
		if !strings.Contains(got, want) {
			t.Errorf("listing missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "2 keys ⚠") {
		t.Errorf("overlay not flagged with its key count:\n%s", got)
	}
	if !strings.Contains(got, "yolo config reset") {
		t.Errorf("footer must point at the cure:\n%s", got)
	}
	// A pure-overwrite surface must never be reported as carrying an overlay.
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "copilot/mcp") && strings.Contains(line, "⚠") {
			t.Errorf("a no-sidecar surface was flagged: %q", line)
		}
	}
}

// TestConfigLsMarksUnrenderedSurface: a surface declared as `unrendered` must be listed
// as such rather than implying the jail composes it.
//
// It used to pin claude/config, which was unrendered only because ~/.claude.json had a
// bespoke Go writer that must never wipe it. That writer is gone — the surface is now
// rmw + reconcile, actually rendered — so the test asserts the MECHANISM against a
// synthetic surface instead of a real one that no longer has the property. Keeping it
// pointed at claude/config would have meant re-marking a rendered file "not rendered" to
// satisfy a test.
func TestConfigLsMarksUnrenderedSurface(t *testing.T) {
	s := manifest.Surface{
		Agent: "example", Name: "config", Path: "~/.example/config.json",
		Codec: "json", Mode: manifest.ModeUnrendered,
	}
	if got := surfaceMode(s); got != surfaceModeUnrendered {
		t.Fatalf("surfaceMode = %q, want %q", got, surfaceModeUnrendered)
	}
	row := surfaceRow{Surface: "example/config", Path: s.Path, Codec: s.Codec,
		Mode: surfaceModeUnrendered, Overlay: -1, Reserved: true}
	tgt, _ := withSidecarDir(t)
	var out bytes.Buffer
	writeSurfaceTable(&out, tgt, []surfaceRow{row}, false)
	if !strings.Contains(out.String(), "not rendered at boot") {
		t.Errorf("an unrendered surface must say so in the listing:\n%s", out.String())
	}
}

// TestConfigLsListsRenderedClaudeConfig is the other half, and the reason the test above
// changed: ~/.claude.json IS rendered now (rmw, with its mcpServers table reconciled), so
// the listing must not describe it as reserved.
func TestConfigLsListsRenderedClaudeConfig(t *testing.T) {
	tgt, _ := withSidecarDir(t)
	var out, errw bytes.Buffer
	if rc := configLs(tgt, []string{"--all"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "claude/config") {
			if strings.Contains(line, "not rendered at boot") {
				t.Errorf("claude/config is rendered now; listing still calls it reserved: %q", line)
			}
			return
		}
	}
	t.Error("claude/config row missing from --all listing")
}

// TestConfigLsEveryBuiltinSurfaceHasAMode is the anti-drift guard: a new builtin
// surface with no resolvable mode would list with an empty MODE, silently
// implying it has no posture.
func TestConfigLsEveryBuiltinSurfaceHasAMode(t *testing.T) {
	for _, s := range surfaceManifest().Surfaces() {
		key := s.Agent + "/" + s.Name
		if surfaceMode(s) == "" {
			t.Errorf("surface %s has no resolvable mode — it would list with an empty MODE", key)
		}
	}
}

// TestConfigDiffShowsCapturedKeys: diff must distinguish a REAL edit from a
// redundant capture (same value yolo last wrote) and from a deletion, because the
// audit found most captured keys are noise.
func TestConfigDiffShowsCapturedKeys(t *testing.T) {
	tgt, dir := withSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings",
		`{"theme":"dark","effortLevel":"xhigh","model":null,"added":1}`,
		`{"theme":"light","effortLevel":"xhigh"}`)

	var out, errw bytes.Buffer
	if rc := configDiff(tgt, []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configDiff rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	for _, want := range []string{
		`theme  "dark" (was "light")`, // a real change, with the prior value
		"redundant capture",           // effortLevel matches last_render
		"model  deleted in-jail",      // a captured deletion
		"added  1 (added in-jail)",    // a key yolo never wrote
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diff missing %q:\n%s", want, got)
		}
	}
}

// TestConfigDiffEmptyOverlayIsQuiet: an empty overlay is the normal state and must
// not read as a problem.
func TestConfigDiffEmptyOverlayIsQuiet(t *testing.T) {
	tgt, dir := withSidecarDir(t)
	writeSidecar(t, dir, "pi", "settings", `{}`, `{"theme":"dark"}`)

	var out, errw bytes.Buffer
	if rc := configDiff(tgt, []string{"pi"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "No captured in-jail edits") {
		t.Errorf("empty overlay not reported as clean:\n%s", out.String())
	}
}

// TestConfigResetDiscardsTheOverlayAndReSeedsTheBaseline is the load-bearing reset behavior
// at the JAIL notch: the capture overlay goes, and the baseline is left describing the bytes
// reset itself just wrote.
//
// Both halves are one statement about the NEXT render. A surviving overlay would re-apply the
// edit that was just discarded. A surviving STALE baseline — the pre-reset render — would have
// the next boot diff the truncated file against it and capture the discard as an edit. And no
// baseline at all, which is what this used to assert, has its own cost: a first migration over
// a non-empty file is an ADOPTION, so the next render treated reset's own output as the user's
// file and spent OQ-CO7's one-per-surface archive on a copy of it (§6.3.3, D1).
func TestConfigResetDiscardsTheOverlayAndReSeedsTheBaseline(t *testing.T) {
	home := withScratchHome(t)
	tgt, dir := withLocalSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)
	writeSidecar(t, dir, "pi", "settings", `{"other":true}`, `{"other":false}`)
	// The surface the edit lives in. Without a file there is nothing to truncate and nothing
	// to re-seed, so the baseline half of this test would be vacuous.
	settings := filepath.Join(home, ".claude", "settings.json")
	writeFile(t, settings, `{"theme":"dark"}`)

	var out, errw bytes.Buffer
	if rc := configReset(tgt, []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configReset rc=%d, stderr=%s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "discarded 1 captured key") {
		t.Errorf("reset did not report what it discarded:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-settings.overlay.json")); !os.IsNotExist(err) {
		t.Errorf("the capture overlay survived reset (err=%v) — the discarded edit would be "+
			"re-applied by the next render", err)
	}
	baseline, err := os.ReadFile(filepath.Join(dir, "claude-settings.last_render"))
	if err != nil {
		t.Fatalf("reset truncated %s and left no baseline for those bytes: %v", settings, err)
	}
	rendered, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(baseline) != string(rendered) {
		t.Errorf("the baseline is not the file reset wrote:\n baseline: %q\n file:     %q\n\n"+
			"A baseline that disagrees with the file is the stale one this half exists to "+
			"prevent; one that is ABSENT makes the next render read yolo's own output as the "+
			"user's file (OQ-CO7 D1).", baseline, rendered)
	}
	// A different surface must be untouched.
	if _, err := os.Stat(filepath.Join(dir, "pi-settings.overlay.json")); err != nil {
		t.Errorf("reset of one surface removed another's sidecar: %v", err)
	}
}

// TestConfigResetIsIdempotent: running it twice must not error. The second run finds an empty
// store — this surface has no file, so the first reset had no bytes to re-seed a baseline from
// — and says so rather than reporting a discard it did not make.
func TestConfigResetIsIdempotent(t *testing.T) {
	withScratchHome(t)
	tgt, dir := withLocalSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{"a":1}`, `{}`)
	var out, errw bytes.Buffer
	if rc := configReset(tgt, []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("first reset rc=%d", rc)
	}
	out.Reset()
	if rc := configReset(tgt, []string{"claude/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("second reset rc=%d, stderr=%s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "Nothing to reset") {
		t.Errorf("second reset should be a no-op, got:\n%s", out.String())
	}
}

// TestConfigDiffResetRejectMissingAgent: both need an agent, and an agent with no
// capture surfaces is an error rather than a silent success.
func TestConfigDiffResetRejectMissingAgent(t *testing.T) {
	tgt, _ := withSidecarDir(t)
	for _, fn := range []func([]string, *bytes.Buffer, *bytes.Buffer, bool) int{
		func(a []string, o, e *bytes.Buffer, c bool) int { return configDiff(tgt, a, o, e, c) },
		func(a []string, o, e *bytes.Buffer, c bool) int { return configReset(tgt, a, o, e, c) },
	} {
		var out, errw bytes.Buffer
		if rc := fn(nil, &out, &errw, false); rc != 2 {
			t.Errorf("no agent: rc=%d, want 2", rc)
		}
		out.Reset()
		errw.Reset()
		if rc := fn([]string{"nosuchagent"}, &out, &errw, false); rc != 1 {
			t.Errorf("unknown agent: rc=%d, want 1", rc)
		}
	}
}

// TestConfigResetUserSurfacesFromSidecars: a host_files capture surface is keyed by
// an opaque slug, so `reset user` must discover its surfaces from the sidecar files
// — which also lets it clean up after an entry the user has since removed.
func TestConfigResetUserSurfacesFromSidecars(t *testing.T) {
	withScratchHome(t)
	tgt, dir := withLocalSidecarDir(t)
	writeSidecar(t, dir, "user", ".config_2fmytool_2fx.json", `{"k":"v"}`, `{}`)

	var out, errw bytes.Buffer
	if rc := configReset(tgt, []string{"user"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configReset user rc=%d, stderr=%s", rc, errw.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "user-.config_2fmytool_2fx.json.overlay.json")); !os.IsNotExist(err) {
		t.Errorf("user overlay survived reset: %v", err)
	}
}

// TestUnslugHostFilePath: the diff/ls header must name the FILE, not the escaped
// slug. The slug is a reversible percent-escape, so decoding needs no config read
// and still works for an entry the user has since removed.
func TestUnslugHostFilePath(t *testing.T) {
	for _, path := range []string{
		".config/mytool/config.json",
		".npmrc",
		".config/dir with spaces/x.conf",
		"foo/bar_baz.json", // a literal '_' must survive the round trip
	} {
		slug := (config.HostFileEntry{Path: path}).Slug()
		if got := unslugHostFilePath(slug); got != path {
			t.Errorf("unslugHostFilePath(Slug(%q)) = %q, want round-trip", path, got)
		}
	}
}

// TestSurfacePresenceReadsTheTargetsOwnHome is the inversion of a bug the nested-jail run
// caught, and the point at which it stops being a choice between two wrong answers.
//
// Presence used to be checked against the PROCESS home, so a host-side `config ls` reported
// every jail-rendered file as absent and every host dotfile the jail never wrote as present;
// an in-jail run for a DIFFERENT workspace (a nested jail, and every integration test) did
// the same. The fix was to DECLINE — never claim absence — which cost the existence filter
// (§2.3 F2). With a home root on the resolved target the question is answerable: a workspace
// target's files live at <workspace>/.yolo/home/…, which is reachable from the host.
//
// Both directions are asserted, because a test for the positive alone would still pass if
// presence had simply gone back to reading the invoking user's own home.
func TestSurfacePresenceReadsTheTargetsOwnHome(t *testing.T) {
	home := withScratchHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_RUNTIME", "podman")
	tgt, _ := withSidecarDir(t)

	s, ok := surfaceManifest().Lookup("claude", "settings")
	if !ok {
		t.Fatal("missing claude/settings in the surface manifest")
	}
	// The jail's own copy, where the launch backs it: the leading dot of the first segment is
	// the overlay's name and is stripped (run/prepare.go's prepareWsState).
	writeFile(t, filepath.Join(paths.WorkspaceHomeState(tgt.workspace), "claude", "settings.json"),
		`{"theme":"the jail's"}`)
	if !tgt.surfaceFileExists(s.Path) {
		t.Errorf("a file the jail DID render read as absent host-side. Presence is the "+
			"target's, and a workspace target's home is reachable from the host at %s",
			paths.WorkspaceHomeState(tgt.workspace))
	}

	// And a surface present ONLY in the invoking human's real home must read as absent: that
	// direction is the half that makes this a resolution and not a fallback.
	other, ok := surfaceManifest().Lookup("pi", "settings")
	if !ok {
		t.Fatal("missing pi/settings in the surface manifest")
	}
	writeFile(t, expandHome(other.Path), `{"theme":"the developer's own"}`)
	if tgt.surfaceFileExists(other.Path) {
		t.Errorf("a dotfile in the INVOKING user's home (%s) read as one this jail rendered — "+
			"that is the wrong home, and reporting it is how a host-side listing came to "+
			"describe the developer's own config as a jail's", home)
	}
}

// NOT RESOLVABLE IS NOT ABSENT (§4.1's last row). A surface whose home this workspace does
// not back at all must say so: "absent" sends the reader looking for a render that was never
// going to land in the home they are asking about.
func TestConfigLsSaysNotResolvableRatherThanAbsent(t *testing.T) {
	withScratchHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_RUNTIME", "podman")
	tgt, _ := withSidecarDir(t)
	// The workspace backs ~/.claude — the directory IS the mount source — and nothing else.
	mkdirAllT(t, filepath.Join(paths.WorkspaceHomeState(tgt.workspace), "claude"))

	var out, errw bytes.Buffer
	if rc := configLs(tgt, []string{"--all"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configLs rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "not resolvable at the jail notch") {
		t.Errorf("a surface this workspace backs no home for must be reported as not "+
			"resolvable at this notch, not as absent:\n%s", got)
	}
	// The one home it DOES back yields an ordinary absence, so the distinction is a
	// measurement rather than a blanket label.
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "claude/settings") && !strings.Contains(line, "(absent)") {
			t.Errorf("a missing file in a directory the workspace DOES back is an ordinary "+
				"absence:\n%s", line)
		}
	}
}

// TestConfigLsDoesNotInflateRowsHostSide closes F2's measured symptom: `cd /workspace` listed
// 11 surfaces and `cd /tmp/notaworkspace` listed 15, same jail, same files, only the cwd
// different — because presence became unknowable and the existence filter stopped applying.
// The filter applies at a workspace target now, so the default listing is what the jail has.
func TestConfigLsDoesNotInflateRowsHostSide(t *testing.T) {
	withScratchHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_RUNTIME", "podman")
	tgt, _ := withSidecarDir(t)
	writeFile(t, filepath.Join(paths.WorkspaceHomeState(tgt.workspace), "claude", "settings.json"), `{}`)

	var filtered, all bytes.Buffer
	if rc := configLs(tgt, nil, &filtered, &filtered, false); rc != 0 {
		t.Fatalf("configLs rc=%d", rc)
	}
	if rc := configLs(tgt, []string{"--all"}, &all, &all, false); rc != 0 {
		t.Fatalf("configLs --all rc=%d", rc)
	}
	nFiltered, nAll := countSurfaceRows(filtered.String()), countSurfaceRows(all.String())
	if nFiltered >= nAll {
		t.Errorf("the default listing printed %d rows and --all printed %d: the existence "+
			"filter is not applying, which is F2's four-row inflation — the same jail "+
			"described differently depending on where the user stood:\n%s", nFiltered, nAll,
			filtered.String())
	}
	if nFiltered != 1 {
		t.Errorf("the default listing printed %d rows for a jail home holding ONE surface "+
			"file:\n%s", nFiltered, filtered.String())
	}
}

// countSurfaceRows counts the listing's data rows: every line naming an "<owner>/<name>"
// surface, which excludes the header and the divergence footer.
func countSurfaceRows(table string) int {
	n := 0
	for _, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && strings.Count(fields[0], "/") == 1 {
			n++
		}
	}
	return n
}

// TestWorkspaceRootWalksUp: the target's workspace must resolve from a SUBDIRECTORY of the
// workspace (like git), and must not be hardcoded to /workspace — that shortcut silently read
// another workspace's sidecars, and `reset` would have deleted them.
//
// ⚠ THE FIXTURE IS A MARKER, NOT A `.yolo` DIRECTORY, and that is the ruled change: a bare
// `.yolo/` matches `/home/agent` inside every jail, where it is the generated-script anchor
// (docs/reference/config-target-resolution.md [OQ-CR2]). This used to build the workspace as an
// empty `.yolo/prism` and would now resolve nothing.
func TestWorkspaceRootWalksUp(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, config.WorkspaceConfigName), `{}`)
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	got, ok := resolveWorkspaceRoot()
	if !ok {
		t.Fatalf("resolveWorkspaceRoot() from a subdir of %q found no workspace", root)
	}
	if got, _ = filepath.EvalSymlinks(got); got != root {
		t.Errorf("resolveWorkspaceRoot() from a subdir = %q, want the workspace root %q", got, root)
	}
}
