package run

import (
	"encoding/json"
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostFileIn builds a minimal assembleInput carrying the given host_files entries.
func hostFileIn(t *testing.T, entries ...config.HostFileEntry) *assembleInput {
	t.Helper()
	return &assembleInput{
		wsState:      filepath.Join(t.TempDir(), "home"),
		mountTargets: map[string]struct{}{},
		hostFiles:    entries,
	}
}

// TestHostFilesEnvOnlyWhenPresent is the golden-argv guard: a jail with no
// host_files must emit no YOLO_HOST_FILES at all (not an empty one), so the frozen
// argv of every existing jail is unchanged by this feature.
func TestHostFilesEnvOnlyWhenPresent(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	if got := o.hostFilesEnv(hostFileIn(t)); got != nil {
		t.Errorf("no entries emitted env %v, want nil", got)
	}
	in := hostFileIn(t, config.HostFileEntry{
		Path: ".config/a.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce,
	})
	got := o.hostFilesEnv(in)
	if len(got) != 2 || got[0] != "-e" || !strings.HasPrefix(got[1], "YOLO_HOST_FILES=") {
		t.Fatalf("hostFilesEnv = %v, want [-e YOLO_HOST_FILES=<json>]", got)
	}
	// The wire form must round-trip: the entrypoint decodes exactly this string.
	wire := strings.TrimPrefix(got[1], "YOLO_HOST_FILES=")
	back, err := config.UnmarshalHostFiles(wire)
	if err != nil {
		t.Fatalf("emitted env does not decode: %v", err)
	}
	if len(back) != 1 || back[0].Path != ".config/a.json" {
		t.Errorf("round-trip lost the entry: %+v", back)
	}
}

// TestHostUserFileArgsMountsSourcesOnly: only a source-bearing entry gets a :ro
// /ctx/host-user/<slug> mount, and the slug must be the one the entrypoint
// derives — that agreement is the whole contract between the two halves.
func TestHostUserFileArgsMountsSourcesOnly(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "config.json")
	if err := os.WriteFile(src, []byte(`{"k":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceBearing := config.HostFileEntry{
		Path: ".config/mytool/config.json", Source: src, Codec: "json",
		Mode: config.HostFileModeReadonly,
	}
	sourceLess := config.HostFileEntry{
		Path: ".config/seed.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce,
	}

	o := goldenOptions("/ws", t.TempDir())
	args := strings.Join(o.hostUserFileArgs(hostFileIn(t, sourceBearing, sourceLess)), " ")
	want := src + ":/ctx/host-user/" + sourceBearing.Slug() + ":ro"
	if !strings.Contains(args, want) {
		t.Errorf("hostUserFileArgs = %q, want a mount %q", args, want)
	}
	if strings.Contains(args, sourceLess.Slug()) {
		t.Errorf("source-less entry got a /ctx mount (it has no host file to cross): %q", args)
	}
}

// TestHostUserFileArgsSkipsMissingSource: podman kills the whole container on a
// missing bind source ("statfs …: no such file or directory"), and a host dotfile
// the user has not created is a normal state — so an absent source must be
// skipped, letting the surface fall back to its defaults layer.
func TestHostUserFileArgsSkipsMissingSource(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	in := hostFileIn(t, config.HostFileEntry{
		Path: ".config/absent.json", Source: filepath.Join(t.TempDir(), "nope.json"),
		Codec: "json", Mode: config.HostFileModeReadonly,
	})
	if got := o.hostUserFileArgs(in); got != nil {
		t.Errorf("missing source emitted %v, want no mount", got)
	}
}

// TestHostUserFileArgsDirSource: a directory entry binds its tree directly (the
// single-file deref exists for file binds only).
func TestHostUserFileArgsDirSource(t *testing.T) {
	srcDir := filepath.Join(t.TempDir(), "themes")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := config.HostFileEntry{
		Path: ".pi/agent/themes", Source: srcDir, IsDir: true, Mode: config.HostFileModeCopy,
	}
	o := goldenOptions("/ws", t.TempDir())
	args := strings.Join(o.hostUserFileArgs(hostFileIn(t, entry)), " ")
	if !strings.Contains(args, srcDir+":/ctx/host-user/"+entry.Slug()+":ro") {
		t.Errorf("dir entry args = %q, want a :ro tree mount", args)
	}
}

// TestHostFileWritableDirArgs: only a destination that needs a new writable
// subtree gets one, deduped, with nested paths dropped (a duplicate or nested -v
// for one destination is a hard podman error / a shadowed tree).
func TestHostFileWritableDirArgs(t *testing.T) {
	entries := []config.HostFileEntry{
		// Already writable via the .config bind — must NOT be staged.
		{Path: ".config/mytool/a.json", Codec: "json", HasContent: true},
		// Two entries sharing one new top-level parent — one bind.
		{Path: "foo/a.json", Codec: "json", HasContent: true},
		{Path: "foo/b.json", Codec: "json", HasContent: true},
		// A deeper path under the same root — nested, so dropped.
		{Path: "foo/deep/c.json", Codec: "json", HasContent: true},
	}
	got := hostFileWritableDirs(entries, nil, nil)
	if len(got) != 1 || got[0] != "foo" {
		t.Fatalf("hostFileWritableDirs = %v, want exactly [foo]", got)
	}

	o := goldenOptions("/ws", t.TempDir())
	in := hostFileIn(t, entries...)
	args := o.hostFileWritableDirArgs(in)
	if len(args) != 2 {
		t.Fatalf("writable-dir args = %v, want one -v pair", args)
	}
	if !strings.HasSuffix(args[1], ":/home/agent/foo") {
		t.Errorf("writable-dir bind = %q, want it to land at /home/agent/foo", args[1])
	}
	if !strings.Contains(args[1], filepath.Join(config.WritableHomeBackingSubdir, "foo")) {
		t.Errorf("writable-dir bind %q must be backed by the wsState writable-home subdir", args[1])
	}
}

// stageHostFilesForTest runs both host_files provisioning steps the fresh-launch path runs
// (runContainer): the workspace-overlay half and the home skeleton, returning the skeleton.
func stageHostFilesForTest(t *testing.T, wsState string, entries []config.HostFileEntry) string {
	t.Helper()
	prepareHostFiles(wsState, entries, nil, nil)
	sk, err := buildHomeSkeleton(paths.HomeSkeletonRoot("yolo-hostfiles-test"), nil, nil, entries)
	if err != nil {
		t.Fatalf("buildHomeSkeleton: %v", err)
	}
	if len(sk.warnings) != 0 {
		t.Fatalf("buildHomeSkeleton warned: %v", sk.warnings)
	}
	return sk.dir
}

// TestPrepareHostFilesStagesSymlinkDangling is the load-bearing one for the
// ~/.npmrc case: the skeleton symlink must be created RELATIVE and left
// DANGLING, because HostFileModeOnce seeds only a file it cannot stat. A
// pre-created target would suppress the seed forever.
func TestPrepareHostFilesStagesSymlinkDangling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wsState := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}

	entry := config.HostFileEntry{
		Path: ".npmrc", Source: "/host/.npmrc", Codec: "raw", Mode: config.HostFileModeReadonly,
	}
	skeleton := stageHostFilesForTest(t, wsState, []config.HostFileEntry{entry})

	// The shared base home is no longer where the link goes: created there, one workspace's
	// host_files link showed up in every podman jail on the machine.
	if _, err := os.Lstat(filepath.Join(paths.GlobalHome(), ".npmrc")); err == nil {
		t.Error("the ~/.npmrc link was created in the machine store (<state>/home), which every " +
			"podman jail used to mount; it belongs in this jail's skeleton only")
	}
	link := filepath.Join(skeleton, ".npmrc")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("no symlink staged for a home-root destination: %v", err)
	}
	if filepath.IsAbs(target) {
		t.Errorf("symlink target %q is absolute; it must be relative so it resolves through the container mount table", target)
	}
	if target != entry.SymlinkTarget() {
		t.Errorf("symlink target = %q, want %q", target, entry.SymlinkTarget())
	}
	// DANGLING: Stat (which follows the link) must fail, or `once` never seeds.
	if _, err := os.Stat(link); !os.IsNotExist(err) {
		t.Errorf("staged symlink resolves (err=%v) — `once` would skip the seed forever", err)
	}
	// The overlay dir holding the targets must exist, so the entrypoint's write
	// through the link lands in a real directory.
	if st, err := os.Stat(filepath.Join(wsState, "config", "yolo-home")); err != nil || !st.IsDir() {
		t.Errorf("wsState config/yolo-home not created: %v", err)
	}
}

// TestPrepareHostFilesStagesWritableDirBothEnds: the writable_home_dirs recipe
// needs BOTH the backing dir (a missing bind source kills the container) and the
// skeleton mountpoint (deterministic mode/ownership under the :ro home root).
func TestPrepareHostFilesStagesWritableDirBothEnds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wsState := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}

	skeleton := stageHostFilesForTest(t, wsState, []config.HostFileEntry{
		{Path: "foo/bar.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce},
	})

	backing := filepath.Join(wsState, config.WritableHomeBackingSubdir, "foo")
	if st, err := os.Stat(backing); err != nil || !st.IsDir() {
		t.Errorf("backing dir %s not created (podman would fail the container): %v", backing, err)
	}
	mountpoint := filepath.Join(skeleton, "foo")
	if st, err := os.Stat(mountpoint); err != nil || !st.IsDir() {
		t.Errorf("skeleton mountpoint %s not created: %v", mountpoint, err)
	}
	if _, err := os.Lstat(filepath.Join(paths.GlobalHome(), "foo")); err == nil {
		t.Error("the ~/foo mountpoint was created in the machine store (<state>/home), which " +
			"every podman jail used to mount; it belongs in this jail's skeleton only")
	}
}

// TestPrepareHostFilesLeavesWritableDestsAlone: a destination already under a rw
// bind must get NO staging — provisioning there would shadow a yolo mount.
func TestPrepareHostFilesLeavesWritableDestsAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wsState := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}

	skeleton := stageHostFilesForTest(t, wsState, []config.HostFileEntry{
		{Path: ".config/mytool/c.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce},
	})

	if _, err := os.Stat(filepath.Join(wsState, config.WritableHomeBackingSubdir)); !os.IsNotExist(err) {
		t.Errorf("staged a writable-home backing dir for an already-writable destination (err=%v)", err)
	}
	if entries, _ := os.ReadDir(paths.GlobalHome()); len(entries) != 0 {
		t.Errorf("touched GlobalHome for an already-writable destination: %v", entries)
	}
	// ~/.config is a core bind; the skeleton needs its mountpoint and nothing under it.
	if _, err := os.Lstat(filepath.Join(skeleton, ".config", "mytool")); err == nil {
		t.Error("the skeleton got a mountpoint for a destination already under the ~/.config bind")
	}
}

// TestHostFilesWireCarriesEveryFieldTheEntrypointNeeds: the env is the ONLY
// channel to the entrypoint, so a field dropped in transit silently changes
// behavior in the jail (a lost Mode would fall through to `copy`, discarding
// in-jail edits).
func TestHostFilesWireCarriesEveryFieldTheEntrypointNeeds(t *testing.T) {
	entry := config.HostFileEntry{
		Path:     ".config/mytool/settings.json",
		Source:   "/host/settings.json",
		Codec:    "json",
		Managed:  map[string]any{"telemetry": false},
		Defaults: map[string]any{"theme": "dark"},
		Mode:     config.HostFileModeCapture,
	}
	o := goldenOptions("/ws", t.TempDir())
	got := o.hostFilesEnv(hostFileIn(t, entry))
	wire := strings.TrimPrefix(got[1], "YOLO_HOST_FILES=")

	var raw []map[string]any
	if err := json.Unmarshal([]byte(wire), &raw); err != nil {
		t.Fatalf("wire is not decodable JSON: %v", err)
	}
	for _, key := range []string{"path", "source", "codec", "managed", "defaults", "mode"} {
		if _, ok := raw[0][key]; !ok {
			t.Errorf("wire form dropped %q: %s", key, wire)
		}
	}
	back, err := config.UnmarshalHostFiles(wire)
	if err != nil {
		t.Fatal(err)
	}
	if back[0].Mode != config.HostFileModeCapture {
		t.Errorf("Mode did not survive: %q (a lost mode falls through to copy)", back[0].Mode)
	}
	if back[0].Slug() != entry.Slug() {
		t.Errorf("slug diverged across the wire: %q vs %q — the /ctx mount would not match",
			back[0].Slug(), entry.Slug())
	}
}

// TestAHostFilesEntryUnderAnUnselectedPacksDirIsStaged is the run side of the OQ-BH14 ruling
// (docs/design/base-home-legacy-state.md#28-reservation-is-a-rule-about-config-names-not-about-directories):
// an unselected pack's directory is an ordinary path. `~/.codex/prompts/review.md` in a claude-only
// jail used to be judged "already under a rw bind" because codex SHIPS, so it got no staging,
// the skeleton had no ~/.codex, and the entrypoint's write failed EROFS. Now it is staged like
// any new top-level dir; once codex is selected, codex's own bind covers it and nothing else
// may land there.
//
// Driven through the three production consumers of one selection — prepareHostFiles (the
// backing dir), buildHomeSkeleton (the mountpoint) and assembleRunCmd (the bind) — so it fails
// if any of them stops passing the selected packs through.
func TestAHostFilesEntryUnderAnUnselectedPacksDirIsStaged(t *testing.T) {
	entries := []config.HostFileEntry{{
		Path: ".codex/prompts/review.md", Codec: "raw", HasContent: true, Mode: config.HostFileModeOnce,
	}}
	for _, tc := range []struct {
		name       string
		packs      []string
		wantStaged bool
	}{
		{"claude-only jail", []string{"claude"}, true},
		{"codex selected", []string{"claude", "codex"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := goldenOptions("/ws", home)
			o.Stdout, o.Stderr = discardBuf(), discardBuf()
			packs := packsFixture(t, tc.packs...)
			wsState := filepath.Join(t.TempDir(), "home")
			if err := os.MkdirAll(wsState, 0o755); err != nil {
				t.Fatal(err)
			}

			prepareHostFiles(wsState, entries, packs, nil)
			in := relocationInput(t, "podman", wsState, nil)
			in.packs, in.hostFiles = packs, entries
			in.homeSkeleton = buildSkeletonForTest(t, in.cname, packs, in.cfg, entries)
			argv := o.assembleRunCmd(in)
			assertEveryHomeBindHasASkeletonEntry(t, argv, in.homeSkeleton)

			bindsAt := func(dest string) []string {
				var sources []string
				for i := 0; i+1 < len(argv); i++ {
					if argv[i] == "-v" && strings.HasSuffix(argv[i+1], ":"+dest) {
						sources = append(sources, strings.TrimSuffix(argv[i+1], ":"+dest))
					}
				}
				return sources
			}
			// The writable_home_dirs recipe stages the destination's PARENT.
			staged := entries[0].WritableParent()
			backing := filepath.Join(wsState, config.WritableHomeBackingSubdir, staged)
			_, backingErr := os.Stat(backing)
			if tc.wantStaged {
				if got := bindsAt("/home/agent/" + staged); len(got) != 1 || got[0] != backing {
					t.Errorf("binds at /home/agent/%s from %v, want exactly one, from the "+
						"writable-home backing dir %s", staged, got, backing)
				}
				if got := bindsAt("/home/agent/.codex"); len(got) != 0 {
					t.Errorf("a claude-only jail binds /home/agent/.codex from %v; codex is not selected", got)
				}
				if backingErr != nil {
					t.Errorf("prepareHostFiles did not create the backing dir: %v", backingErr)
				}
				if fi, err := os.Lstat(filepath.Join(in.homeSkeleton, filepath.FromSlash(staged))); err != nil || !fi.IsDir() {
					t.Errorf("the skeleton has no ~/%s mountpoint (%v); the bind above would fail "+
						"at `podman run`", staged, err)
				}
				return
			}
			// codex's own bind covers the destination, so nothing is staged under it: a second
			// bind there would shadow part of codex's state dir, and one AT ~/.codex is a hard
			// podman error ("duplicate mount destination").
			if want := filepath.Join(wsState, "codex"); len(bindsAt("/home/agent/.codex")) != 1 ||
				bindsAt("/home/agent/.codex")[0] != want {
				t.Errorf("binds at /home/agent/.codex from %v, want exactly codex's own, from %s",
					bindsAt("/home/agent/.codex"), want)
			}
			if got := bindsAt("/home/agent/" + staged); len(got) != 0 {
				t.Errorf("binds at /home/agent/%s from %v, inside codex's own bind", staged, got)
			}
			if backingErr == nil {
				t.Errorf("prepareHostFiles created %s for a destination codex's own bind covers", backing)
			}
		})
	}
}

// TestRunContainerHandsTheSelectionToTheReservationReaders pins the arguments no behavioral
// test reaches: runContainer passes the SAME selected packs to prepareWsState, to
// prepareHostFiles and to every config.WritableHomeDirs as it puts in the assembleInput's
// `packs`, which the argv and the skeleton read; prepareHostFiles gets the writable_home_dirs
// entries the argv binds (a WritableHomeDirs call, like the literal's); and prepareWsState
// gets runContainer's own `rt`.
//
// Behaviorally each of these is quiet: validation has already refused a writable_home_dirs
// entry a selected pack declares, a host_files backing dir created for a destination a bind
// covers is an empty directory, and prepareWsState with the wrong backend only lays wsState out
// for the other one, which the unit gate cannot boot. So without this pin, the readers of one
// selection could drift apart with the suite green — the disagreement hostFileWritableDirs'
// own comment says they "cannot" have — and an Apple Container launch could go back to the
// podman seed path (the design's §3 defect) with only a Mac run to notice.
func TestRunContainerHandsTheSelectionToTheReservationReaders(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	hasRT := false
	for _, field := range fd.Type.Params.List {
		for _, name := range field.Names {
			hasRT = hasRT || name.Name == "rt"
		}
	}
	if !hasRT {
		t.Fatal("runContainer no longer takes an `rt` parameter — this pin's anchor moved; re-anchor it")
	}
	packsIdent := ""
	ast.Inspect(fd, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || skelIdent(lit.Type) != "assembleInput" {
			return true
		}
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok && skelIdent(kv.Key) == "packs" {
				packsIdent = skelIdent(kv.Value)
			}
		}
		return true
	})
	if packsIdent == "" {
		t.Fatal("runContainer's assembleInput literal no longer sets `packs:` from a plain " +
			"identifier — this pin's anchor moved; re-anchor it, do not delete it")
	}
	if skelRebinds(fd.Body, "rt") {
		t.Error("runContainer reassigns `rt`; prepareWsState must get the backend this launch runs")
	}

	isWritableHomeDirsCall := func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		return ok && skelCallee(call) == "WritableHomeDirs"
	}
	found := map[string]bool{}
	ast.Inspect(fd, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := skelCallee(call)
		// want maps an argument index to the identifier it must be.
		var want map[int]string
		switch name {
		case "prepareWsState":
			want = map[int]string{1: packsIdent, 2: "rt"}
		case "prepareHostFiles":
			want = map[int]string{2: packsIdent}
			if len(call.Args) <= 3 || !isWritableHomeDirsCall(call.Args[3]) {
				t.Errorf("runContainer calls prepareHostFiles without config.WritableHomeDirs(…) " +
					"as its writable_home_dirs argument; the backing dirs it stages would then " +
					"disagree with the binds the argv emits")
			}
		case "WritableHomeDirs":
			want = map[int]string{1: packsIdent}
		default:
			return true
		}
		found[name] = true
		for idx, ident := range want {
			if len(call.Args) <= idx || skelIdent(call.Args[idx]) != ident {
				t.Errorf("runContainer calls %s without %s as argument %d; one launch would then "+
					"reserve, stage or lay out wsState against two different answers", name, ident, idx)
			}
		}
		return true
	})
	for _, name := range []string{"prepareWsState", "prepareHostFiles", "WritableHomeDirs"} {
		if !found[name] {
			t.Errorf("runContainer no longer calls %s — this pin's anchors moved; re-anchor it", name)
		}
	}
}

// bindSourcesAt returns the source of every -v in argv whose destination is dest.
func bindSourcesAt(argv []string, dest string) []string {
	var sources []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" {
			continue
		}
		parts := strings.Split(argv[i+1], ":")
		if len(parts) >= 2 && parts[1] == dest {
			sources = append(sources, parts[0])
		}
	}
	return sources
}

// assertUniqueBindDests fails on any -v destination the argv binds twice: podman refuses the
// whole container with "duplicate mount destination", naming neither config key.
func assertUniqueBindDests(t *testing.T, argv []string) {
	t.Helper()
	seen := map[string]string{}
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" {
			continue
		}
		parts := strings.Split(argv[i+1], ":")
		if len(parts) < 2 {
			continue
		}
		if prev, dup := seen[parts[1]]; dup {
			t.Errorf("%s is bound twice, from %s and from %s: podman refuses the launch with "+
				"\"duplicate mount destination\"", parts[1], prev, parts[0])
		}
		seen[parts[1]] = parts[0]
	}
}

// hostFilesLaunch runs every host-side step a fresh podman launch takes for host_files and
// writable_home_dirs (prepareWsState, prepareHostFiles, buildHomeSkeleton, assembleRunCmd),
// with the writable_home_dirs value and entries as given, and returns the argv, wsState and
// the skeleton.
func hostFilesLaunch(t *testing.T, packNames []string, writableHomeDirs []any, entries []config.HostFileEntry) (argv []string, wsState, skeleton string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	packs := packsFixture(t, packNames...)
	ws := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}

	in := relocationInput(t, "podman", "", nil)
	in.cfg.Set("writable_home_dirs", writableHomeDirs)
	in.wsState = ws.prepareWsState(in.cfg, packs, "podman")
	in.packs, in.hostFiles = packs, entries
	in.writableHomeDirs = config.WritableHomeDirs(in.cfg, packs)
	prepareHostFiles(in.wsState, entries, packs, in.writableHomeDirs)
	in.homeSkeleton = buildSkeletonForTest(t, in.cname, packs, in.cfg, entries)

	o := goldenOptions("/ws", home)
	o.Stdout, o.Stderr = discardBuf(), discardBuf()
	argv = o.assembleRunCmd(in)
	assertEveryHomeBindHasASkeletonEntry(t, argv, in.homeSkeleton)
	return argv, in.wsState, in.homeSkeleton
}

// A HOST_FILES ENTRY UNDER A WRITABLE_HOME_DIRS ENTRY IS ALREADY WRITABLE. Both keys stage the
// same backing layout, <wsState>/writable-home/<path>, so the writable_home_dirs bind already
// covers the destination; staging it again emitted a second, identical -v at one destination,
// which podman refuses. OQ-BH14 widened this: `.codex` is now a legal writable_home_dirs entry
// in a workspace that does not select codex, where it used to be refused at validation.
func TestAWritableHomeDirCoversAHostFilesEntryUnderIt(t *testing.T) {
	for _, tc := range []struct {
		name, dir, path string
	}{
		{"an unselected pack's dir (OQ-BH14)", ".codex", ".codex/x.json"},
		{"a new dir, at its root", ".foo", ".foo/x.json"},
		{"a new dir, deeper", ".foo", ".foo/deep/x.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := []config.HostFileEntry{{
				Path: tc.path, Codec: "json", HasContent: true, Mode: config.HostFileModeOnce,
			}}
			argv, wsState, skeleton := hostFilesLaunch(t, []string{"claude"}, []any{tc.dir}, entries)
			assertUniqueBindDests(t, argv)

			want := filepath.Join(wsState, config.WritableHomeBackingSubdir, tc.dir)
			if got := bindSourcesAt(argv, "/home/agent/"+tc.dir); len(got) != 1 || got[0] != want {
				t.Errorf("binds at /home/agent/%s from %v, want exactly the writable_home_dirs one, "+
					"from %s", tc.dir, got, want)
			}
			if parent := entries[0].WritableParent(); parent != tc.dir {
				if got := bindSourcesAt(argv, "/home/agent/"+parent); len(got) != 0 {
					t.Errorf("binds at /home/agent/%s from %v, inside the writable_home_dirs bind "+
						"that already covers it", parent, got)
				}
				// The other two readers of the same answer: neither the skeleton nor the
				// backing tree gains a staging dir for the covered destination.
				for _, root := range []string{skeleton,
					filepath.Join(wsState, config.WritableHomeBackingSubdir)} {
					if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(parent))); err == nil {
						t.Errorf("%s/%s was created for a destination the writable_home_dirs bind "+
							"covers; the provisioning steps disagree with the argv", root, parent)
					}
				}
			}
		})
	}
}

// A host_files entry under a SELECTED pack's shared dir sits inside that dir's read-write bind
// (the machine store), so it needs no staging. It used to get a writable subtree anyway, a
// second bind at the shared dir's own destination, and podman refused the launch.
func TestAHostFilesEntryUnderASelectedPacksSharedDirIsNotStaged(t *testing.T) {
	entries := []config.HostFileEntry{{
		Path: ".claude-shared-credentials/x.json", Codec: "json", HasContent: true,
		Mode: config.HostFileModeOnce,
	}}
	argv, wsState, _ := hostFilesLaunch(t, []string{"claude"}, []any{}, entries)
	assertUniqueBindDests(t, argv)
	want := filepath.Join(paths.GlobalHome(), ".claude-shared-credentials")
	if got := bindSourcesAt(argv, "/home/agent/.claude-shared-credentials"); len(got) != 1 || got[0] != want {
		t.Errorf("binds at /home/agent/.claude-shared-credentials from %v, want exactly the "+
			"shared dir's own, from %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(wsState, config.WritableHomeBackingSubdir,
		".claude-shared-credentials")); err == nil {
		t.Error("prepareHostFiles staged a writable-home backing dir for a destination the shared " +
			"dir's own bind covers")
	}
}

// APPLE CONTAINER STAGES NOTHING FOR HOST_FILES. It binds wsState read-write, whole, at
// /home/agent, so a new top-level destination is writable as it stands; and nothing on that
// backend's launch path creates a writable-home backing dir (prepareHostFiles and the
// skeleton are podman-only), so an emitted bind named a missing source. OQ-BH14 widened this
// from new top-level dirs (`~/foo/bar.json`) to every unselected pack's dir.
func TestAppleContainerStagesNoHostFilesWritableDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	o.Stdout, o.Stderr = discardBuf(), discardBuf()
	in := relocationInput(t, "container", filepath.Join(t.TempDir(), "home"), nil)
	in.packs = packsFixture(t, "claude")
	in.hostFiles = []config.HostFileEntry{
		{Path: ".codex/prompts/review.md", Codec: "raw", HasContent: true, Mode: config.HostFileModeOnce},
		{Path: "foo/bar.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce},
	}
	// The fixture's premise: on podman both entries WOULD be staged.
	if got := hostFileWritableDirs(in.hostFiles, in.packs, nil); len(got) != 2 {
		t.Fatalf("fixture: hostFileWritableDirs = %v, want both entries staged on podman", got)
	}
	for _, arg := range o.assembleRunCmd(in) {
		if strings.Contains(arg, config.WritableHomeBackingSubdir) {
			t.Errorf("the Apple Container argv carries %q; that backend's home is the rw wsState "+
				"bind, and nothing there creates the source", arg)
		}
	}
}
