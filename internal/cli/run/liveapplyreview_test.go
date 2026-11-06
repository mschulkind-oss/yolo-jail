package run

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ctxinject"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// liveapplyreview_test.go pins the review's findings on `yolo config apply`: the plan comes from
// the config the gate approved, a changed mount is removed before it is added again, each step
// is bounded by its own deadline, and a mount change the host cannot see still has a line.

// THE MOUNTS COME FROM THE CONFIG THE GATE APPROVED (B1). The apply's own load and the gate's
// read are two reads of an agent-writable file: one that names a mount when yolo loads it and
// something harmless when the gate reads it must not get the mount injected under an approval
// of the harmless one. The apply refuses, names the rerun, and injects nothing; an attach starts
// the session all the same.
func TestConfigApplyRefusesAConfigThatChangedBetweenItsReads(t *testing.T) {
	for _, attach := range []bool{false, true} {
		name := "command"
		if attach {
			name = "attach"
		}
		t.Run(name, func(t *testing.T) {
			evil := t.TempDir()
			o, buf := applyFixture(t, `{}`, `{"mounts":["`+evil+`"]}`, applyJail{running: true})
			liveMounts(t)
			injected := 0
			swapSeam(t, &injectCtxMountSeam, func(*Options, string, string, configCtxMount, time.Time) error {
				injected++
				return nil
			})
			// The flip, between the early load and the gate's read.
			swapSeam(t, &readWorkspaceForGateSeam, func(ws string) (*config.WorkspaceRead, error) {
				writeWorkspaceConfig(t, ws, `{"packages":["jq"]}`)
				return config.ReadWorkspaceForGate(ws)
			})
			answer(o, "y")
			var rc int
			if attach {
				merged, ok := o.loadAndValidateConfig()
				if !ok {
					t.Fatal(buf.String())
				}
				o.applyAtAttach("yolo-ws", "podman", merged, nil)
			} else {
				rc = o.configApply(false)
			}
			out := buf.String()
			if injected != 0 {
				t.Fatalf("a mount the gate never showed was injected:\n%s", out)
			}
			if !attach && rc != 1 {
				t.Errorf("rc = %d, want 1\n%s", rc, out)
			}
			if !strings.Contains(out, "changed while yolo was reading it") ||
				!strings.Contains(out, "yolo config apply") {
				t.Errorf("no refusal naming the rerun:\n%s", out)
			}
			if _, drifted, _, _ := config.WorkspaceConfigDrift(o.Workspace, "podman-self"); !drifted {
				t.Error("a refused apply folded a key")
			}
		})
	}
}

// An unchanged config passes the same check: the read the gate approves composes to the
// config the apply loaded, so the mount is injected.
func TestConfigApplyAppliesWhenBothReadsAgree(t *testing.T) {
	src := t.TempDir()
	o, buf := applyFixture(t, `{}`, `{"mounts":["`+src+`"]}`, applyJail{running: true})
	liveMounts(t)
	injected := 0
	swapSeam(t, &injectCtxMountSeam, func(*Options, string, string, configCtxMount, time.Time) error {
		injected++
		return nil
	})
	answer(o, "y")
	if rc := o.configApply(false); rc != 0 || injected != 1 {
		t.Fatalf("rc = %d, injected %d; want 0 and 1\n%s", rc, injected, buf.String())
	}
}

// writeUserConfigOf writes cfg as the user-scope config under $HOME, so a test that hands the
// apply a merged config hands it the one the gate's read composes to. It adds the user's read
// grant for the fixture's source, gbsrc, unless cfg says what gbsrc's grants are itself (an
// empty list for none): a broker with no read grant stays out of the gate (BB-D110), and every
// fixture here is about a broker whose gate asks.
func writeUserConfigOf(t *testing.T, cfg *jsonx.OrderedMap) {
	t.Helper()
	cfg = withUserReadGrant(cfg)
	s, err := jsonx.DumpsSnapshot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A SOURCE CHANGED AT THE SAME DESTINATION is a removal and an addition (diffCtxMounts), and
// the removal runs first (M1): the destination is the mount's identity in the jail, so an add
// run first finds it taken and the mount is lost. The jail ends with the new source.
func TestConfigApplyMovesAChangedSourceAtTheSameDestination(t *testing.T) {
	oldSrc, newSrc := resolvedRunTemp(t), resolvedRunTemp(t)
	o, buf := applyFixture(t, `{"mounts":["`+oldSrc+`:/ctx/a"]}`, `{"mounts":["`+newSrc+`:/ctx/a"]}`,
		applyJail{running: true})
	rec := liveMounts(t)
	*rec = []liveCtxMount{{Source: oldSrc, Dest: "/ctx/a"}}
	mounted := map[string]string{"/ctx/a": oldSrc}
	swapSeam(t, &injectCtxMountSeam, func(_ *Options, _, _ string, m configCtxMount, _ time.Time) error {
		if _, taken := mounted[m.dest]; taken {
			return errors.New(m.dest + " is, or is under, a mount already; remove that first")
		}
		mounted[m.dest] = m.source
		return nil
	})
	swapSeam(t, &removeCtxMountSeam, func(_ *Options, _, _ string, e liveCtxMount, _ time.Time) error {
		delete(mounted, e.Dest)
		return nil
	})
	answer(o, "y")
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	if mounted["/ctx/a"] != newSrc {
		t.Errorf("/ctx/a holds %q, want the new source %q\n%s", mounted["/ctx/a"], newSrc, buf.String())
	}
	if len(*rec) != 1 || (*rec)[0].Source != newSrc {
		t.Errorf("the host's mount record is %v, want the new source alone", *rec)
	}
}

// EVERY CALL A MOUNT STEP MAKES IS BOUNDED BY THE STEP'S DEADLINE (M2): nothing is left running
// past it in an abandoned goroutine, where it could mount after the record was written without
// the mount. The real driver runs against a fake podman whose verb never finishes.
func TestConfigApplyBoundsEveryCallOfAMountStep(t *testing.T) {
	src := t.TempDir()
	o, buf := applyFixture(t, `{}`, `{"mounts":["`+src+`"]}`, applyJail{running: true})
	liveMounts(t)
	o.PathExists = func(string) bool { return false }
	answeringPodman(o, rootlessInfo)
	swapSeam(t, &applyStepDeadline, 200*time.Millisecond)
	swapSeam(t, &ctxProcessIdentitySeam, func(int) (ctxinject.ProcessIdentity, error) {
		return ctxinject.ProcessIdentity{StartTime: 11, MountNSDevice: 22, MountNSInode: 33}, nil
	})
	inner := o.Exec
	var stepTimeouts []time.Duration
	o.Exec = func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		switch {
		case slices.Contains(argv, ctxAttachVerbName):
			stepTimeouts = append(stepTimeouts, timeout)
			return ExecResult{Ran: true, Timeout: true}
		case len(argv) > 3 && argv[1] == "inspect" && argv[3] == "{{.State.Pid}}":
			stepTimeouts = append(stepTimeouts, timeout)
			return ExecResult{Ran: true, Stdout: "4242\n"}
		}
		return inner(argv, dir, env, timeout)
	}
	answer(o, "y")
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
	}
	if len(stepTimeouts) == 0 {
		t.Fatalf("the step ran nothing:\n%s", buf.String())
	}
	for _, d := range stepTimeouts {
		if d <= 0 || d > 200*time.Millisecond {
			t.Errorf("a step call ran under %v, past the step's 200ms deadline", d)
		}
	}
	if out := buf.String(); !strings.Contains(out, "at next restart") || !strings.Contains(out, "did not finish within") {
		t.Errorf("want at next restart naming the deadline:\n%s", out)
	}
}

// A READ-WRITE MOUNT CHANGE WITH NO RECORD STILL HAS A LINE (M3). Read-write elements come from
// the user scope, never the workspace config, so without the host's record of the jail's mounts
// (Apple Container, a jail launched before the record existed) nothing else can say they moved:
// the apply prints one line for them, at next restart with the reason, and exits 3.
func TestConfigApplyNamesUserScopeMountsTheRecordCannotVouchFor(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{}`, applyJail{running: true})
	rw := t.TempDir()
	writeUserConfig(t, os.Getenv("HOME"), `{"mounts":[{"host":"`+rw+`","mode":"rw"}]}`)
	swapSeam(t, &ctxMountsLiveSeam, func(*Options, string, string, []string) (bool, string) {
		return false, "this jail started before live mounts existed"
	})
	swapSeam(t, &readCtxMountRecordSeam, func(string, []string) ([]liveCtxMount, bool) { return nil, false })
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
	}
	if out := buf.String(); !strings.Contains(out,
		"~ mounts (user config): at next restart (yolo stop, then yolo) — this jail started before live mounts existed") {
		t.Errorf("no user-scope mounts line:\n%s", out)
	}
}

// A non-interactive attach's notice counts a user-scope mount change the record shows: the
// workspace config and its approval can both be unchanged while a read-write mount moved.
func TestAttachNoticeCountsAUserScopeMountChange(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{}`, applyJail{running: true})
	rw := t.TempDir()
	writeUserConfig(t, os.Getenv("HOME"), `{"mounts":[{"host":"`+rw+`","mode":"rw"}]}`)
	swapSeam(t, &readCtxMountRecordSeam, func(string, []string) ([]liveCtxMount, bool) { return nil, true })
	merged, ok := o.loadAndValidateConfig()
	if !ok {
		t.Fatal(buf.String())
	}
	buf.Reset()
	o.applyAtAttach("yolo-ws", "podman", merged, nil)
	if !strings.Contains(buf.String(), "yolo config apply") {
		t.Errorf("a read-write mount the jail lacks printed no notice:\n%s", buf.String())
	}
}

// A MOUNT THE INJECTOR CANNOT MAKE WAITS FOR A RESTART, saying why, rather than claiming
// `applied now` and failing: a destination outside /ctx (the injector's only target) and a
// source that is a file (the injector mounts on a directory it makes).
func TestThePlanWaitsForAMountTheInjectorCannotMake(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	boot, cur := mapOf(t, `{}`), mapOf(t, `{"mounts":["x"]}`)
	plan := buildApplyPlan(applyPlanInput{rt: "podman", running: true, bootOK: true, boot: boot, current: cur,
		mountsLive: true, mountRecordOK: true, mountRefusal: liveMountRefusal,
		mountAdds: []configCtxMount{{source: dir, dest: "/opt/elsewhere"}, {source: file, dest: "/ctx/f.txt"},
			{source: dir, dest: "/ctx/ok"}},
		mountRemoves: []liveCtxMount{{Source: dir, Dest: "/srv/old"}},
	})
	joined := strings.Join(renderApplyChanges(plan), "\n")
	for _, want := range []string{
		"+ /opt/elsewhere (read-only): at next restart (yolo stop, then yolo) — a mount outside /ctx/",
		"+ /ctx/f.txt (read-only): at next restart (yolo stop, then yolo) — a file",
		"- /srv/old (read-only): at next restart (yolo stop, then yolo) — a mount outside /ctx/",
		"+ /ctx/ok (read-only): applied now",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the plan has no %q:\n%s", want, joined)
		}
	}
}

// The postcheck remains useful, but is supplementary to the pre-mutation process and namespace
// pin: a jail that changes after the injector has joined its pinned namespace is still reported.
func TestInjectCtxMountReportsAPidThatChangedAfterTheStep(t *testing.T) {
	f := newCtxDriveFixture(t)
	f.o.acceptPodmanFacts(rootlessInfo)
	inner := f.o.Exec
	inspects := 0
	f.o.Exec = func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		if len(argv) > 3 && argv[1] == "inspect" && argv[3] == "{{.State.Pid}}" {
			inspects++
			if inspects > 2 {
				return ExecResult{Ran: true, Stdout: "5151\n"}
			}
		}
		return inner(argv, dir, env, timeout)
	}
	err := f.o.injectCtxMount("podman", "yolo-ws-abcd1234", ctxWant("/h/src", "/ctx/src", false), time.Time{})
	if err == nil || !strings.Contains(err.Error(), "pid 4242, now 5151") {
		t.Errorf("err %v; want the changed pid named", err)
	}
	if argv, _ := f.verbCall(); argv == nil {
		t.Error("the verb did not run before the supplementary postcheck")
	}
}

// A REMOVAL IS JUDGED BY ITS DESTINATION ALONE: the detach never reads the source, so a recorded
// mount whose host source has since become a file still leaves the jail now.
func TestAMountRemovalIgnoresWhatItsSourceBecame(t *testing.T) {
	file := filepath.Join(t.TempDir(), "now-a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := buildApplyPlan(applyPlanInput{rt: "podman", running: true, bootOK: true,
		boot: mapOf(t, `{"mounts":["x"]}`), current: mapOf(t, `{}`), mountsLive: true, mountRecordOK: true,
		mountRefusal: liveMountRefusal, mountRemoves: []liveCtxMount{{Source: file, Dest: "/ctx/gone"}}})
	joined := strings.Join(renderApplyChanges(plan), "\n")
	if !strings.Contains(joined, "- /ctx/gone (read-only): applied now") {
		t.Errorf("the removal waits for its old source:\n%s", joined)
	}
}

// THE UNREADABLE-RECORD LINE IS PODMAN'S ALONE: Apple Container never writes a record and binds
// its shares at VM boot, so a line there would stand on every apply and attach, restarts
// included; macos-user has no mounts.
func TestUserScopeMountsUnknownOnlyOnPodman(t *testing.T) {
	rw := []configCtxMount{{source: "/h/rw", dest: "/ctx/rw", rw: true}}
	ro := []configCtxMount{{source: "/h/ro", dest: "/ctx/ro"}}
	for _, c := range []struct {
		rt     string
		mounts []configCtxMount
		want   bool
	}{
		{"podman", rw, true},
		{"podman", ro, false},
		{"container", rw, false},
		{"macos-user", rw, false},
	} {
		if got := userScopeMountsUnknown(c.rt, c.mounts); got != c.want {
			t.Errorf("userScopeMountsUnknown(%s, rw=%v) = %v, want %v", c.rt, c.mounts[0].rw, got, c.want)
		}
	}
}

// withUserReadGrant is cfg with `brokered.gbsrc.grants` holding the user's read grant for every
// workspace (BB-D107), where cfg names no grants for gbsrc. cfg itself is not changed.
func withUserReadGrant(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, k := range cfg.Keys() {
		v, _ := cfg.Get(k)
		out.Set(k, v)
	}
	sources := jsonx.NewOrderedMap()
	if v, ok := out.Get("brokered"); ok {
		if m, ok := v.(*jsonx.OrderedMap); ok {
			for _, k := range m.Keys() {
				sv, _ := m.Get(k)
				sources.Set(k, sv)
			}
		}
	}
	src := jsonx.NewOrderedMap()
	if v, ok := sources.Get("gbsrc"); ok {
		if m, ok := v.(*jsonx.OrderedMap); ok {
			if _, has := m.Get("grants"); has {
				return cfg
			}
			for _, k := range m.Keys() {
				sv, _ := m.Get(k)
				src.Set(k, sv)
			}
		}
	}
	read := jsonx.NewOrderedMap()
	read.Set("kind", "read")
	read.Set("repo", "*")
	src.Set("grants", []any{read})
	sources.Set("gbsrc", src)
	out.Set("brokered", sources)
	return out
}
