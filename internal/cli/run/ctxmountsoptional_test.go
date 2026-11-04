package run

// ctxmountsoptional_test.go pins optional `mounts` elements (docs/reference/context-mounts.md
// #optional-mounts) at the launch's own call sites: the container assembler (assembleRunCmd) and
// Run() on the macos-user arm. Each skip is asserted through what the launch hands on — the argv,
// the backend's links — so deleting the skip at its call site fails here, and each has a required
// control whose behavior did not change.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// optionalRO is the object form of a read-only optional element.
func optionalRO(host, at string) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("host", host)
	if at != "" {
		m.Set("at", at)
	}
	m.Set("mode", "ro")
	m.Set("optional", true)
	return m
}

// A MISSING OPTIONAL SOURCE is one plain line and no mount; a missing required one keeps its
// warning (CX-D24).
func TestAnOptionalMountWithNoSourceIsSkippedInOneLine(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	argv, printed := assembleWithMounts(t, "podman", []any{optionalRO(gone, "/ctx/gone"), gone + "2"})
	if got := ctxMountArgs(argv); got != nil {
		t.Errorf("a missing source was mounted: %v", got)
	}
	if want := "skipped optional mount: " + gone + " → /ctx/gone (source does not exist)"; !strings.Contains(printed, want) {
		t.Errorf("no skip line %q:\n%s", want, printed)
	}
	if strings.Count(printed, "Warning:") != 1 || !strings.Contains(printed, gone+"2") {
		t.Errorf("want exactly one warning, for the required element:\n%s", printed)
	}
}

// AN APPLE CONTAINER BELOW THE :ro FLOOR skips an optional read-only element in one line.
func TestAnOptionalMountAppleContainerCannotMakeReadOnlyIsSkippedInOneLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lib")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	argv, printed := assembleWithMounts(t, "container", []any{optionalRO(dir, "")})
	if got := ctxMountArgs(argv); got != nil {
		t.Errorf("Apple Container emitted ctx mounts %v, want none", got)
	}
	if want := "skipped optional mount: " + dir + " → /ctx/lib (this Apple Container ignores read-only binds)"; !strings.Contains(printed, want) {
		t.Errorf("no skip line %q:\n%s", want, printed)
	}
}

// A MACOS PODMAN MACHINE THAT DOES NOT SHARE an optional source: skipped before the argv, so the
// launch's share pre-flight never sees it. The required element stays in the argv, where that
// pre-flight refuses it as before.
func TestAnOptionalMountThePodmanMachineDoesNotShareIsSkipped(t *testing.T) {
	root := t.TempDir()
	opt, req := filepath.Join(root, "opt"), filepath.Join(root, "req")
	for _, d := range []string{opt, req} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	var buf bytes.Buffer
	o.Stderr = &buf
	o.IsMacOS = true
	o.machineShares = &machineSharesProbe{ok: true, shares: runtime.MachineShares{Machine: "m",
		Shares: []runtime.MachineShare{{Source: "/nowhere", Target: "/nowhere"}}}}
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	argv := o.assembleRunCmd(&assembleInput{
		cfg: newConfig("agents", []any{"claude"}, "security", sec, "mounts",
			[]any{optionalRO(opt, "/ctx/opt"), req + ":/ctx/req"}),
		rt: "podman", cname: "yolo-ws-abcd1234", packs: claudePackFixture(t),
		agentsPath: "/agents/yolo-ws-abcd1234", wsState: "/ws/.yolo/home", miseStore: "/mise-store",
		yoloVersion: "9.9.9-test", mountTargets: map[string]struct{}{},
	})
	got := strings.Join(ctxMountArgs(argv), "\n")
	if strings.Contains(got, ":/ctx/opt") {
		t.Errorf("the unshared optional source is still in the argv:\n%s", got)
	}
	if !strings.Contains(got, ":/ctx/req") {
		t.Errorf("the required source left the argv, so the share pre-flight can no longer refuse it:\n%s", got)
	}
	if want := "skipped optional mount: " + opt + " → /ctx/opt (the Podman Machine does not share it)"; !strings.Contains(buf.String(), want) {
		t.Errorf("no skip line %q:\n%s", want, buf.String())
	}
}

// MACOS-USER'S SITING, the motivating case: an optional source inside a home is skipped in one
// line and never reaches the backend, and the launch goes on.
func TestMacosUserSkipsAnOptionalMountInsideAHome(t *testing.T) {
	users := floortest.ResolvedTemp(t)
	logs := filepath.Join(users, "alice", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	ctxLaunchHome(t, `, "mounts": [{"host": "`+logs+`", "at": "/ctx/host-yolo-logs", "mode": "ro", "optional": true}]`)

	ctx, out := runMacosUserCapturingCtx(t, t.TempDir(), func(o *Options) {
		s := sitingWritable(t, "")
		s.UsersRoot, s.UsersRootAliases = users, nil
		o.macosCtxSiting = s
	})
	if want := "skipped optional mount: " + logs + " → /ctx/host-yolo-logs (macos-user can't reach a folder inside a home)"; !strings.Contains(out, want) {
		t.Errorf("no skip line %q:\n%s", want, out)
	}
	if strings.Contains(out, "Refusing the macos-user launch") {
		t.Errorf("an optional mount refused the launch:\n%s", out)
	}
	if len(ctx.Links) != 0 {
		t.Errorf("the skipped mount reached the backend: %+v", ctx.Links)
	}
}

// DROPPING AN OPTIONAL LINK RE-SITES THE REST: a required mount refused only for nesting with the
// optional one is delivered once the optional one is skipped.
func TestMacosUserDeliversARequiredMountOnceTheOptionalOneItNestedWithIsSkipped(t *testing.T) {
	users := floortest.ResolvedTemp(t)
	inHome := filepath.Join(users, "alice", "notes")
	lib := filepath.Join(floortest.ResolvedTemp(t), "lib")
	for _, d := range []string{inHome, lib} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctxLaunchHome(t, `, "mounts": [{"host": "`+inHome+`", "at": "/ctx/a", "mode": "ro", "optional": true}, "`+lib+`:/ctx/a/lib"]`)

	ctx, out := runMacosUserCapturingCtx(t, t.TempDir(), func(o *Options) {
		s := sitingWritable(t, "")
		s.UsersRoot, s.UsersRootAliases = users, nil
		o.macosCtxSiting = s
	})
	if len(ctx.Links) != 1 || ctx.Links[0].Dest != "/ctx/a/lib" {
		t.Fatalf("Links = %+v, want only the required /ctx/a/lib\n%s", ctx.Links, out)
	}
}

// OPTIONAL NEVER SKIPS A SAFETY REFUSAL: a source overlapping yolo's state directory refuses
// whether or not it is marked optional, and the refusal offers no "mark it optional" for it.
func TestMacosUserStillRefusesAnOptionalMountInYolosStateDir(t *testing.T) {
	state := floortest.ResolvedTemp(t)
	src := filepath.Join(state, "ctx")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	ctxLaunchHome(t, `, "mounts": [{"host": "`+src+`", "mode": "ro", "optional": true}]`)

	out := runMacosUserExpectingRefusal(t, t.TempDir(), func(o *Options) {
		s := sitingWritable(t, "")
		s.StateDirs = []string{state}
		o.macosCtxSiting = s
	})
	if !strings.Contains(out, "is inside yolo's state directory") {
		t.Errorf("the refusal did not give the safety reason:\n%s", out)
	}
	if strings.Contains(out, `"optional": true`) {
		t.Errorf("the refusal offered to mark an unsafe mount optional:\n%s", out)
	}
}

// IN THE RUN-AS-YOU MODE A MOUNT IS JUDGED BY WHERE IT RESOLVES: a source that is a link to your
// home is delivered as the home, which the profile would then open whole, so it refuses, marked
// optional or not. Fails if the siting stops applying its home rule to the resolved source.
func TestMacosUserSelfModeRefusesAMountResolvingToTheHome(t *testing.T) {
	users := floortest.ResolvedTemp(t)
	home := filepath.Join(users, "you")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(floortest.ResolvedTemp(t), "notes")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	ctxLaunchHome(t, `, "mounts": [{"host": "`+link+`", "at": "/ctx/notes", "mode": "ro", "optional": true}]`)

	out := runMacosUserExpectingRefusal(t, t.TempDir(), func(o *Options) {
		s := macosuser.DarwinContextSitingFor(macosuser.ModeSelf, home)
		s.UsersRoot, s.UsersRootAliases, s.WritableRoots = users, nil, nil
		o.macosCtxSiting = &s
	})
	if !strings.Contains(out, link+" → /ctx/notes (read-only, `mounts`): it is or contains your home") {
		t.Errorf("the refusal did not name the home:\n%s", out)
	}
}

// THE REFUSAL'S SHAPE: one headline, a blank line, the mounts, a blank line, then the fixes,
// "mark it optional" with the exact JSON to paste for the element as written.
func TestMacosUserRefusalOffersTheOptionalSpelling(t *testing.T) {
	users := floortest.ResolvedTemp(t)
	notes := filepath.Join(users, "alice", "notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	ctxLaunchHome(t, `, "mounts": ["`+notes+`", "`+notes+`:/ctx/n2"]`)

	out := runMacosUserExpectingRefusal(t, t.TempDir(), func(o *Options) {
		s := sitingWritable(t, "")
		s.UsersRoot, s.UsersRootAliases = users, nil
		o.macosCtxSiting = s
	})
	for _, want := range []string{
		"Refusing the macos-user launch: it can't deliver 2 context mounts.\n\n  " + notes + " → /ctx/notes",
		"\n\nFix one:\n",
		`{"host": "` + notes + `", "mode": "ro", "optional": true}`,
		`{"host": "` + notes + `", "at": "/ctx/n2", "mode": "ro", "optional": true}`,
		"copy the folder under " + macosuser.SharedRootDefault(),
		"use a container setup: yolo --setup podman-self -- claude",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal did not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "root-owned link") {
		t.Errorf("the refusal still carries the old prose paragraph:\n%s", out)
	}
}

// THE BRIEFING AGREES WITH THE DELIVERY: a skipped optional mount is not listed.
func TestMacosUserBriefsNoSkippedOptionalMount(t *testing.T) {
	users := floortest.ResolvedTemp(t)
	logs := filepath.Join(users, "alice", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := newConfig("mounts", []any{optionalRO(logs, "/ctx/logs")})
	o := &Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}
	s := sitingWritable(t, "")
	s.UsersRoot, s.UsersRootAliases = users, nil
	o.macosCtxSiting = s
	if got := o.briefedCtxMounts("macos-user", "/var/yolo-jail/ctx/x", cfg, nil); len(got) != 0 {
		t.Errorf("briefed %+v, want nothing: the optional mount was skipped", got)
	}
}

// A DELIVERED OPTIONAL MOUNT IS BRIEFED AS "optional; may be absent": on macos-user the sandbox
// account's access check runs after the briefing is written and may still skip it. Through the
// briefing's decider and the renderer both, so deleting either half fails here.
func TestTheBriefingMarksAnOptionalMountMayBeAbsent(t *testing.T) {
	lib := filepath.Join(floortest.ResolvedTemp(t), "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := newConfig("mounts", []any{optionalRO(lib, "/ctx/lib")})
	o := &Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}
	o.macosCtxSiting = sitingWritable(t, "")
	const dir = "/var/yolo-jail/ctx/x"
	got := o.briefedCtxMounts("macos-user", dir, cfg, nil)
	if len(got) != 1 || !got[0].Optional {
		t.Fatalf("briefed %+v, want one optional entry", got)
	}
	body := jailcontent.BriefingContent(jailcontent.BriefingInput{Workspace: "/Users/Shared/yolo/p",
		Mechanism: "macos-user", ContextDir: dir, ContextMounts: got})
	if want := "- `" + dir + "/lib` (optional; may be absent; read-only;"; !strings.Contains(body, want) {
		t.Errorf("the briefing lacks %q:\n%s", want, body)
	}
}

// THE REVIEW'S REPRODUCTIONS, through Run(): an optional entry refused first for a delivery reason
// (an `at` outside /ctx; a Data-volume spelling) but unsafe underneath still refuses the launch.
func TestMacosUserRefusesAnOptionalUnsafeMountWhicheverRuleSeesItFirst(t *testing.T) {
	t.Run("the users root at an at outside /ctx", func(t *testing.T) {
		users := floortest.ResolvedTemp(t)
		ctxLaunchHome(t, `, "mounts": [{"host": "`+users+`", "at": "/data", "mode": "ro", "optional": true}]`)
		out := runMacosUserExpectingRefusal(t, t.TempDir(), func(o *Options) {
			s := sitingWritable(t, "")
			s.UsersRoot, s.UsersRootAliases = users, nil
			o.macosCtxSiting = s
		})
		if !strings.Contains(out, "it contains every home on this Mac") {
			t.Errorf("not refused for containing every home:\n%s", out)
		}
	})
	t.Run("the state dir spelled through the Data volume", func(t *testing.T) {
		data := floortest.ResolvedTemp(t)
		const state = "/Users/Shared/yolo-staged"
		src := filepath.Join(data, state)
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		ctxLaunchHome(t, `, "mounts": [{"host": "`+src+`", "at": "/ctx/staged", "mode": "ro", "optional": true}]`)
		out := runMacosUserExpectingRefusal(t, t.TempDir(), func(o *Options) {
			s := sitingWritable(t, "")
			s.DataVolume, s.StateDirs = data, []string{state, data + state}
			o.macosCtxSiting = s
		})
		if !strings.Contains(out, "yolo's state directory") {
			t.Errorf("not refused for overlapping yolo's state directory:\n%s", out)
		}
	})
}
