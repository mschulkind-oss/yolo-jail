package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// DP-L1: the host bytes a `/ctx` mount carries on every other backend now cross on
// macos-user too, by COPY into a tree the launch stages root-owned.
//
// ⚠ EVERY TEST HERE DRIVES A REAL Run(), and that is the point rather than thoroughness.
// The composer could be unit-tested directly and would pass with the call deleted from the
// macos-user arm — the shape AGENTS.md says this repo has shipped five times, and the shape
// this backend has been bitten by three separate times (pack staging, launch flags, the
// profile channel, each a B-0). The arm returns above runContainer, so anything the
// container path does implicitly has to be re-pinned here explicitly.

// ctxLaunchHome sets up a fake host home with the claude pack selected and returns it.
func ctxLaunchHome(t *testing.T, extraConfigKeys string) string {
	t.Helper()
	home := packHome(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"packs": ["claude"]` + extraConfigKeys + `}`
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

// writeHostFileAt writes a host-side file, creating parents.
func writeHostFileAt(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// runMacosUserCapturingCtx drives Run() on the macos-user arm with a stub backend and
// returns the HostContext the handler was handed, plus the launch's whole output.
func runMacosUserCapturingCtx(t *testing.T, ws string, tweak func(*Options)) (macosuser.HostContext, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	if tweak != nil {
		tweak(o)
	}
	var got macosuser.HostContext
	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string,
		hostCtx macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		got, reached = hostCtx, true
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !reached {
		t.Fatalf("Run() never reached the macos-user handler\nstderr:\n%s", stderr.String())
	}
	return got, stdout.String() + stderr.String()
}

// THE CELL ITSELF: a pack `reads-host` grant. The claude pack's settings surface declares
// `readsHost`, so the human's ~/.claude/settings.json is what the agent's own settings.json
// must be composed from — and on this backend it was composed from DEFAULTS instead,
// silently, for as long as the backend has existed (DP-B row in §5.1).
//
// It asserts the BYTES, not a path: a tree computed and never written is exactly as empty
// to the bootstrap as no tree at all, which is the lesson
// TestPacksAreStagedBeforeBackendDispatch was written from.
func TestMacosUserLaunchDeliversAPackReadsHostGrant(t *testing.T) {
	home := ctxLaunchHome(t, "")
	writeHostFileAt(t, filepath.Join(home, ".claude", "settings.json"),
		`{"theme":"the human's own"}`, 0o644)

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	if ctx.Tree == "" {
		t.Fatalf("the macos-user arm composed no context tree, so the claude pack's " +
			"reads-host grant did not cross and the agent's settings.json is a default")
	}
	// The layout IS the manifest: the jail opens $YOLO_CTX_ROOT/<the /ctx path minus /ctx>.
	staged := filepath.Join(ctx.Tree, "host-claude", "settings.json")
	body, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("the grant's bytes are not at %s: %v", staged, err)
	}
	if !strings.Contains(string(body), "the human's own") {
		t.Errorf("staged file holds %q, not the human's file", string(body))
	}
	// And the launcher RECORDED it, which is the half the jail's fail-closed read needs:
	// without the record a wrong-path delivery composes from defaults in silence, which is
	// the 2026-09-05 bug OQ-CO10 closed.
	want := packload.CtxRoot + "/host-claude/settings.json"
	if !containsString(ctx.Delivered, want) {
		t.Errorf("Delivered = %v, want it to name %s — the jail cannot tell a file that "+
			"was never delivered from one that did not arrive", ctx.Delivered, want)
	}
}

// THE OTHER CELL: a source-bearing `host_files` entry. It used to be FILTERED OUT of the
// wire entirely (macosuser.sourceLessHostFilesWire), so a user who pointed at a host file
// by path got no file at that path at all.
//
// Both halves are asserted because either alone is silently useless: bytes in the tree the
// wire never names are never read, and an entry in the wire whose bytes are absent renders
// the user's declared file from its defaults layer.
func TestMacosUserLaunchDeliversASourceBearingHostFilesEntry(t *testing.T) {
	home := ctxLaunchHome(t, `, "host_files": [{"path": ".npmrc", "source": "~/.npmrc"}]`)
	writeHostFileAt(t, filepath.Join(home, ".npmrc"), "registry=https://example.invalid\n", 0o644)
	ws := t.TempDir()

	ctx, _ := runMacosUserCapturingCtx(t, ws, nil)

	if len(ctx.HostFiles) != 1 || ctx.HostFiles[0].Path != ".npmrc" {
		t.Fatalf("HostFiles = %+v, want the one source-bearing entry — without it the "+
			"bootstrap is never told the file exists", ctx.HostFiles)
	}
	staged := filepath.Join(ctx.Tree, "host-user", ctx.HostFiles[0].Slug())
	body, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("the entry's bytes are not at %s: %v", staged, err)
	}
	if !strings.Contains(string(body), "example.invalid") {
		t.Errorf("staged file holds %q, not the user's ~/.npmrc", string(body))
	}
}

// A SOURCE THAT IS NOT THERE IS NOT A FAILURE, and must not be RECORDED as delivered.
// Most users have never written a ~/.claude/settings.json, the surface correctly composes
// from its lower layers, and this is the one case where nothing arriving is right. Claiming
// it would refuse the launch, because the jail's read fails closed on a path the launcher
// said it delivered.
func TestMacosUserLaunchClaimsNothingForAnAbsentHostFile(t *testing.T) {
	ctxLaunchHome(t, "") // no ~/.claude/settings.json written
	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	if len(ctx.Delivered) != 0 {
		t.Errorf("Delivered = %v for a home with no settings.json; the jail would refuse "+
			"the launch, because a delivered path it cannot read is the ONE disposition "+
			"that fails closed", ctx.Delivered)
	}
	if ctx.Tree != "" {
		t.Errorf("composed a tree at %s with nothing to put in it — a launch that carried "+
			"no host bytes must say so by absence, or the host-layer report claims a "+
			"delivery mechanism it did not use", ctx.Tree)
	}
}

// DP-D15, not DP-L1: a `host_files` entry whose source is a DIRECTORY names an arbitrary
// user tree, and a copy does not scale to one ("we can't do /ctx by copying, some of these
// directories are huge"). It is left undelivered and NAMED — the one thing this backend's
// host-byte warning still has to say.
func TestMacosUserLaunchNamesADirectoryHostFileItCannotCarry(t *testing.T) {
	// The trailing slashes are what DECLARE it a directory entry — config.checkHostFiles
	// reads the shape off the declaration, never off a stat, so a dir entry is a thing the
	// user wrote rather than a thing yolo inferred.
	home := ctxLaunchHome(t, `, "host_files": [{"path": ".config/big/", "source": "~/big/"}]`)
	if err := os.MkdirAll(filepath.Join(home, "big"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeHostFileAt(t, filepath.Join(home, "big", "a.txt"), "x\n", 0o644)

	ctx, out := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	if len(ctx.HostFiles) != 0 {
		t.Errorf("a directory entry reached the wire (%+v); the bootstrap would look for "+
			"bytes nothing staged", ctx.HostFiles)
	}
	if !strings.Contains(out, ".config/big") {
		t.Errorf("the launch did not name the directory entry it could not carry — the user "+
			"declared a path and gets nothing there with no reason given:\n%s", out)
	}
}

// ⚠ THE INVERSION. Two warnings said pack `reads-host` grants "do not cross on macos-user"
// and that source-bearing `host_files` entries "are dropped". Both gaps are CLOSED, and a
// warning describing a closed gap teaches the reader to distrust the ones still true —
// the rule loopholeinert.go is already written under, applied to itself.
//
// It replaces TestMacosUserNotesHostByteGaps, which asserted the opposite. This is the
// NEGATIVE half only; the positive halves are the two delivery tests above.
func TestMacosUserLaunchNoLongerWarnsThatHostBytesCannotCross(t *testing.T) {
	home := ctxLaunchHome(t, `, "host_files": [{"path": ".npmrc", "source": "~/.npmrc"}]`)
	writeHostFileAt(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1}`, 0o644)
	writeHostFileAt(t, filepath.Join(home, ".npmrc"), "registry=https://example.invalid\n", 0o644)

	_, out := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	for _, retired := range []string{
		"reads-host grants do not cross",
		"are dropped on macos-user",
		"renders from its DEFAULTS layer",
	} {
		if strings.Contains(out, retired) {
			t.Errorf("the launch still says %q, but the bytes now cross by copy into the "+
				"staged context tree:\n%s", retired, out)
		}
	}
}

// THE DISCLOSURE MUST TRAVEL WITH THE BYTES. A pack now reads the human's home on this
// backend exactly as it does on every other one, and the banner is the entire trust
// boundary — packhostgrants.go: "the boundary today is DISCLOSURE, not consent", and
// OQ-TP9 deleted the approval gate only because the disclosure survives it. The container
// path prints this inside runContainer, below the macos-user return, so the arm prints its
// own; deleting that call ships a host-byte read nothing announces.
func TestMacosUserLaunchDisclosesWhatThePackReadsFromTheHost(t *testing.T) {
	home := ctxLaunchHome(t, "")
	writeHostFileAt(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1}`, 0o644)

	_, out := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	for _, want := range []string{"claude", "settings.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch did not disclose that the claude pack reads the human's "+
				"settings.json (missing %q). The disclosure is the whole trust boundary for "+
				"a host-file grant:\n%s", want, out)
		}
	}
}

// REBUILT, NEVER ACCUMULATED. A grant the user REVOKED — a pack dropped from `packs`, an
// entry deleted from their config — must stop being delivered, and a tree that only ever
// grew would keep composing a host file into a surface whose declaration is gone.
func TestMacosUserCtxTreeIsRebuiltFromScratchEachLaunch(t *testing.T) {
	home := ctxLaunchHome(t, "")
	writeHostFileAt(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1}`, 0o644)
	ws := t.TempDir()

	ctx, _ := runMacosUserCapturingCtx(t, ws, nil)
	if ctx.Tree == "" {
		t.Fatal("no tree composed on the first launch")
	}
	stale := filepath.Join(ctx.Tree, "host-gone", "settings.json")
	writeHostFileAt(t, stale, `{"revoked":true}`, 0o644)

	ctx2, _ := runMacosUserCapturingCtx(t, ws, nil)
	if ctx2.Tree != ctx.Tree {
		t.Fatalf("the second launch composed a different tree (%s vs %s)", ctx2.Tree, ctx.Tree)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Errorf("a previous launch's file survived at %s; a revoked grant would keep "+
			"composing into the agent's config forever", stale)
	}
}

// The exec bit has a READER downstream: entrypoint.hostSourceIsExecutable stats the staged
// copy to decide the rendered mode, so a host script arriving without its bit renders
// non-executable and the agent told to run it gets EACCES. `host_files` gained executable
// delivery deliberately; dropping it here would take it away on this backend alone.
func TestMacosUserCtxTreeCarriesTheExecBit(t *testing.T) {
	home := ctxLaunchHome(t, `, "host_files": [{"path": ".local/bin/hook", "source": "~/hook.sh"}]`)
	writeHostFileAt(t, filepath.Join(home, "hook.sh"), "#!/bin/sh\nexit 0\n", 0o755)

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)
	if len(ctx.HostFiles) != 1 {
		t.Fatalf("HostFiles = %+v, want the one entry", ctx.HostFiles)
	}
	staged := filepath.Join(ctx.Tree, "host-user", ctx.HostFiles[0].Slug())
	info, err := os.Stat(staged)
	if err != nil {
		t.Fatalf("stat %s: %v", staged, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("staged copy is %v, not executable; the rendered file would be "+
			"non-executable and running it gives EACCES", info.Mode().Perm())
	}
}

// --dry-run composes too, for buildMacosHomeOverlay's reason: the plan's job is to
// describe the launch, and a plan whose stage list omitted the context tree would show a
// launch nobody runs. Composing writes only into the host-side staging dir; RunMacosUser
// still returns before executing a single staged command.
func TestMacosUserDryRunStillComposesTheContextTree(t *testing.T) {
	home := ctxLaunchHome(t, "")
	writeHostFileAt(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1}`, 0o644)

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), func(o *Options) { o.DryRun = true })
	if ctx.Tree == "" {
		t.Error("--dry-run composed no context tree, so the plan it prints omits the " +
			"staging a real launch performs")
	}
}

// THE TWO /ctx DECLARATIONS A COPY CANNOT CARRY, and the human's half of them. Both were
// accepted, validated and then dropped in SILENCE on this backend: the config `mounts` loop
// and hostMountArgs are the only readers either has, and both sit below the macos-user
// return, so nothing on this arm mentioned either key. appliedCtxMounts kept the mounts out
// of the AGENT's briefing while its own parity marker claimed a `Warned` disposition — a
// disposition is `Warned` only when the launch says so.
//
// ⚠ Run(), for macosctxtree_test.go's own reason and one sharper: the printer is a pure
// function of the config and a pack list, so a unit test of it passes with the call deleted —
// and a missing call is EXACTLY the defect, since every other reader of these two keys is
// below the arm's return.
func TestMacosUserNamesTheConfigMountsItCannotBind(t *testing.T) {
	home := ctxLaunchHome(t, `, "mounts": ["~/code/ref-repo", "`+t.TempDir()+`:/ctx/logs"]`)
	// The source EXISTS, so config validation's own "host path does not exist and will be
	// skipped" line cannot be what the assertions below are reading.
	if err := os.MkdirAll(filepath.Join(home, "code", "ref-repo"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, out := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	for _, want := range []string{
		"`mounts` is not honored on macos-user",
		"/ctx/ref-repo", // the bare-path entry, at the destination it would have taken
		"/ctx/logs",     // the host:container entry, at the one it named
		"COPY",          // the reason, which is this backend's own and not the AC :ro rule
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch never mentioned %q — the user declared a context mount and "+
				"gets nothing at that path with no reason given:\n%s", want, out)
		}
	}
	// Not the container path's sentence borrowed: Apple Container refuses a `:ro` bind it
	// would otherwise make, and this backend makes no bind at all. Asserting the absence
	// keeps a later "just reuse the existing string" from stating the wrong reason.
	if strings.Contains(out, "read-only (:ro)") {
		t.Errorf("the macos-user notice borrowed Apple Container's `:ro` reason:\n%s", out)
	}
}

// A PACK `mount` GRANT IS THE SAME DROP, and it is named the same way — the matched half of
// the pair above. No pack yolo ships declares one, so this drives a fetched (file://) pack,
// which is also the case where silence costs most: that grant was approved by a human
// against a sentence about reading their home.
func TestMacosUserNamesAPackMountGrantItCannotBind(t *testing.T) {
	home := packHome(t)
	src := filepath.Join(t.TempDir(), "acme")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeHostFileAt(t, filepath.Join(src, "pack.json"),
		`{"name":"acme","contributes":[{"kind":"mount","host":"datasets/acme","into":"acme"}]}`,
		0o644)
	writeUserPacks(t, home, `["file://`+src+`"]`)
	if err := os.MkdirAll(filepath.Join(home, "datasets", "acme"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, out := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	for _, want := range []string{
		"pack `mount` grant is not honored on macos-user",
		"~/datasets/acme",
		"/ctx/acme",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch never mentioned %q. The banner on this same arm discloses "+
				"the grant as a host READ, so without this line the launch's only word on "+
				"the subject is the one that overclaims:\n%s", want, out)
		}
	}
}

// THE CONTROL, and it is the difference between a disclosure and the warning OQ-BP-3 says
// people learn to skip: a launch that declared no context mount says nothing about one.
func TestMacosUserSaysNothingAboutContextMountsNobodyDeclared(t *testing.T) {
	ctxLaunchHome(t, "")

	_, out := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	for _, unwanted := range []string{"`mounts` is not honored", "pack `mount` grant"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a launch with no `mounts` and no pack grant still printed %q:\n%s",
				unwanted, out)
		}
	}
}

// containsString is a local membership test — the run package has no generic helper and
// one comparison does not earn an import.
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// PARITY IN THE STATE NOBODY LOOKS AT: a source-bearing entry whose host file does not
// EXIST yet still reaches the wire, so the destination renders from its `defaults`/
// `content` layers. That is what the container path does — hostUserFileArgs emits the
// entry and skips only the bind — and gating the wire on the copy would leave this backend
// with the pre-DP-L1 answer (no file at that path at all) in exactly the case a reader
// would assume was covered by the delivery test above.
func TestMacosUserLaunchStillWiresAnEntryWhoseSourceIsAbsent(t *testing.T) {
	// `defaults`, not `content`: the two are mutually exclusive with `source` on one side
	// and not the other, and `defaults` is the layer a missing host file falls back to.
	ctxLaunchHome(t, `, "host_files": [{"path": ".npmrc.json", "source": "~/.npmrc.json", `+
		`"defaults": {"registry": "https://default.invalid"}}]`)
	// ~/.npmrc deliberately NOT written.

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	if len(ctx.HostFiles) != 1 || ctx.HostFiles[0].Path != ".npmrc.json" {
		t.Fatalf("HostFiles = %+v; an entry whose source does not exist yet must still "+
			"cross, or nothing renders at ~/.npmrc.json and the user's declared file "+
			"is not there", ctx.HostFiles)
	}
	if ctx.Tree != "" {
		t.Errorf("composed a tree at %s with no bytes to put in it", ctx.Tree)
	}
}
