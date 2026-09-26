package broker

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The DERIVATION half of OQ-HD2: the set this package's management verb acts over
// comes from the manifests, so a loophole that declares `host_daemon.scope:
// "host"` tomorrow is manageable tomorrow, with nothing edited.

// stageHostScopedModules writes loophole modules into a temp tree and RECORDS them
// as this process's pack-contributed modules, which is the seam every host-side
// discovery surface reads (loopholes.PackModules). The record is process-wide by
// design, so it is reset afterwards.
func stageHostScopedModules(t *testing.T, manifests map[string]map[string]any) {
	t.Helper()
	root := t.TempDir()
	var mods []loopholes.PackModule
	for name, m := range manifests {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		b, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), b, 0o644); err != nil {
			t.Fatal(err)
		}
		mods = append(mods, loopholes.PackModule{Dir: dir, HostExecApproved: true})
	}
	// SnapshotPackModules restores the record AND its set flag ("nothing recorded
	// yet" is not an empty record), and leaves the lazy resolver's memo alone —
	// that memo answers "what does this machine's pack store hold", which no test
	// should make a later one pay to recompute.
	restore := loopholes.SnapshotPackModules()
	loopholes.SetPackModules(mods)
	t.Cleanup(restore)
}

func hostScopedManifest(name string) map[string]any {
	return map[string]any{
		"name":            name,
		"description":     name + " description",
		"default_enabled": true,
		"transport":       "loopback-tls",
		"host_daemon": map[string]any{
			"cmd":       []any{"yolo", "internal", "daemon", name, "--socket", "{socket}"},
			"publishes": "socket",
			"scope":     "host",
		},
	}
}

// TestEveryHostScopedDaemonIsAddressableByName is the ruling's core property: the
// set is DERIVED from the `scope: "host"` declarations, so every declaring
// loophole is addressable — not one of them, and not a list somebody remembered
// to extend.
//
// The per-jail daemon in the fixture is the control. Scope is compared against
// ScopeHost rather than against ScopeJail everywhere in the tree, because the
// field's zero value is ""; a filter written the other way would sweep every
// ordinary loophole into the host-wide management surface.
func TestEveryHostScopedDaemonIsAddressableByName(t *testing.T) {
	perJail := map[string]any{
		"name":            "yjtest-per-jail",
		"description":     "a per-jail daemon",
		"default_enabled": true,
		"transport":       "loopback-tls",
		"host_daemon": map[string]any{
			"cmd":       []any{"yolo", "internal", "daemon", "yjtest-per-jail", "--socket", "{socket}"},
			"publishes": "socket",
		},
	}
	stageHostScopedModules(t, map[string]map[string]any{
		"yjtest-alpha":    hostScopedManifest("yjtest-alpha"),
		"yjtest-beta":     hostScopedManifest("yjtest-beta"),
		"yjtest-per-jail": perJail,
	})

	got := map[string]Singleton{}
	for _, s := range DeclaredSingletons("") {
		got[s.Name] = s
	}
	for _, want := range []string{"yjtest-alpha", "yjtest-beta"} {
		s, ok := got[want]
		if !ok {
			t.Fatalf("%s declares scope: \"host\" and is not in the derived set (%v) — "+
				"a daemon nothing can name has no management surface at all", want, keysOf(got))
		}
		if !s.Declared {
			t.Errorf("%s is in the set but not marked declared", want)
		}
		// Every path is a function of the NAME, so addressability means each member
		// reaches ITS OWN rendezvous — not one shared with whatever was first.
		deps := CLIDepsFor(s)
		if deps.Life.SocketPath != paths.HostSingletonSocket(want) {
			t.Errorf("%s resolves socket %q, want %q", want, deps.Life.SocketPath,
				paths.HostSingletonSocket(want))
		}
		if deps.Life.PIDFilePath != paths.HostSingletonPIDFile(want) {
			t.Errorf("%s resolves pid file %q, want %q", want, deps.Life.PIDFilePath,
				paths.HostSingletonPIDFile(want))
		}
		if deps.LogPath != SingletonLogPath(want) {
			t.Errorf("%s resolves log %q, want %q", want, deps.LogPath, SingletonLogPath(want))
		}
		// And it is SPAWNABLE: the argv is the manifest's with {socket} resolved, so
		// `restart` can actually respawn it rather than refusing.
		wantArgv := []string{"internal", "daemon", want, "--socket", paths.HostSingletonSocket(want)}
		if len(s.Argv) != len(wantArgv)+1 || !reflect.DeepEqual(s.Argv[1:], wantArgv) {
			t.Errorf("%s resolves argv %v, want the running yolo plus %v", want, s.Argv, wantArgv)
		}
	}
	if _, ok := got["yjtest-per-jail"]; ok {
		t.Error("a PER-JAIL daemon entered the host-wide management set — it is spawned and " +
			"reaped with the jail that asked for it, and stopping it here would mean nothing")
	}
}

// TestTheBrokerSingletonDoesNotDependOnDiscovery is why `yolo broker` can be an
// alias at all. Discovery is fail-safe-empty — in a jail, in any process that
// resolved no packs, on a host whose `packs` list does not name claude — while the
// daemon it manages is at a path that has not moved.
func TestTheBrokerSingletonDoesNotDependOnDiscovery(t *testing.T) {
	// An explicitly EMPTY record: nothing is discoverable in this process.
	restore := loopholes.SnapshotPackModules()
	loopholes.SetPackModules(nil)
	t.Cleanup(restore)

	if len(DeclaredSingletons("")) != 0 {
		t.Fatal("the fixture is wrong: discovery found something with no modules recorded")
	}
	s := BrokerSingleton()
	if s.Name != BrokerLoopholeName {
		t.Errorf("the alias resolves to %q, want %q", s.Name, BrokerLoopholeName)
	}
	deps := CLIDepsFor(s)
	for _, tc := range []struct{ what, got, want string }{
		{"socket", deps.Life.SocketPath, BrokerSingletonSocket()},
		{"pid file", deps.Life.PIDFilePath, BrokerSingletonPIDFile()},
		{"lock", deps.Life.LockPath, BrokerSingletonLock()},
		{"log", deps.LogPath, BrokerLogPath()},
	} {
		if tc.got != tc.want {
			t.Errorf("the alias's %s is %q, want %q — `yolo broker` and the run pipeline "+
				"would ensure and inspect different files", tc.what, tc.got, tc.want)
		}
	}
	if len(s.Argv) == 0 {
		t.Error("the alias carries no spawn argv, so `yolo broker restart` cannot respawn")
	}
}

// TestCycleCommandNamesTheDaemonItCycles is the unit under the defect: the
// sentence a user is told to retype.
//
// `yolo broker restart` was printed for whichever singleton was broken, which for
// two of the three cycles a different process and leaves the broken one running.
func TestCycleCommandNamesTheDaemonItCycles(t *testing.T) {
	for _, name := range []string{BrokerLoopholeName, "openai-auth-broker", "aws-auth"} {
		got := CycleCommand(name)
		if !strings.HasSuffix(got, " "+name) {
			t.Errorf("CycleCommand(%q) = %q — it does not name the daemon it cycles", name, got)
		}
		if !strings.Contains(got, HostDaemonVerb+" restart") {
			t.Errorf("CycleCommand(%q) = %q — it is not a command a user can retype", name, got)
		}
	}
}

// TestRestartRefusesBeforeTheKillWhenItCannotRespawn pins the ORDER, which is the
// whole of the protection: a daemon nothing declares can be stopped but not
// started, so discovering that after the SIGTERM would leave the user with the
// process gone and no way to bring it back.
func TestRestartRefusesBeforeTheKillWhenItCannotRespawn(t *testing.T) {
	st := &lifeState{alive: map[int]bool{42: true}}
	deps, buf := newDeps(t, st)
	deps.Singleton = Singleton{Name: "yjtest-orphan", NoSpawn: "nothing on this machine declares it"}
	deps.Life.Argv = nil
	_ = os.WriteFile(deps.Life.PIDFilePath, []byte("42\n"), 0o644)

	if rc := Restart(deps); rc != 1 {
		t.Errorf("restart with no argv rc = %d, want 1", rc)
	}
	if len(st.killed) != 0 {
		t.Errorf("it killed %v before finding out it could not respawn", st.killed)
	}
	out := buf.String()
	if !strings.Contains(out, "yjtest-orphan") {
		t.Errorf("the refusal does not name the daemon:\n%s", out)
	}
	if !strings.Contains(out, "nothing on this machine declares it") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
	if !strings.Contains(out, "stop yjtest-orphan") {
		t.Errorf("the refusal does not name the verb that DOES work on it:\n%s", out)
	}
}

// TestSetStatusNamesEveryMemberAndFailsIfAnyIsUnhealthy pins the set-wide report:
// it covers every member and its exit code is the whole machine's verdict.
func TestSetStatusNamesEveryMemberAndFailsIfAnyIsUnhealthy(t *testing.T) {
	healthy := &lifeState{alive: map[int]bool{7: true}, reachOK: true}
	sick := &lifeState{}
	healthyDeps, _ := newDeps(t, healthy)
	sickDeps, _ := newDeps(t, sick)
	_ = os.WriteFile(healthyDeps.Life.PIDFilePath, []byte("7\n"), 0o644)
	_ = os.WriteFile(healthyDeps.Life.SocketPath, nil, 0o644)

	set := []Singleton{{Name: "yjtest-up", Declared: true}, {Name: "yjtest-down", Declared: true}}
	var buf bytes.Buffer
	rc := PrintSetStatus(SetDeps{
		Out: &buf, Err: &buf,
		Set: set,
		For: func(s Singleton) CLIDeps {
			d := healthyDeps
			if s.Name == "yjtest-down" {
				d = sickDeps
			}
			d.Singleton = s
			return d
		},
	})
	if rc != 1 {
		t.Errorf("set status rc = %d, want 1 — one member is not healthy", rc)
	}
	out := buf.String()
	for _, name := range []string{"yjtest-up", "yjtest-down"} {
		if !strings.Contains(out, "Host-wide daemon: "+name) {
			t.Errorf("the set report never names %s:\n%s", name, out)
		}
	}
}

// TestSetStatusReportsAnEmptySetRatherThanInventingOne: discovery is
// fail-safe-empty, so "nothing declares one and nothing is running" is a real
// answer — and it exits 0, because nothing is broken.
func TestSetStatusReportsAnEmptySetRatherThanInventingOne(t *testing.T) {
	var buf bytes.Buffer
	rc := PrintSetStatus(SetDeps{Out: &buf, Err: &buf, For: CLIDepsFor})
	if rc != 0 {
		t.Errorf("empty-set rc = %d, want 0", rc)
	}
	if !strings.Contains(buf.String(), "No host-wide daemon") {
		t.Errorf("an empty set says nothing:\n%s", buf.String())
	}
}

// TestARunningDaemonIsManageableWithoutADeclaration is mode 5 of
// host-daemon-ownership.md §6 made actionable: a singleton survives the pack being
// deselected and the loophole being disabled, so a set derived ONLY from this
// machine's current declarations would refuse to stop the daemon a user is trying
// to get rid of.
func TestARunningDaemonIsManageableWithoutADeclaration(t *testing.T) {
	restore := loopholes.SnapshotPackModules()
	loopholes.SetPackModules(nil)
	t.Cleanup(restore)

	const name = "yjtest-undeclared"
	lock := paths.HostSingletonLock(name)
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Skipf("cannot write a fixture rendezvous at %s: %v", lock, err)
	}
	t.Cleanup(func() { _ = os.Remove(lock) })

	var found *Singleton
	for _, s := range Singletons("") {
		if s.Name == name {
			c := s
			found = &c
		}
	}
	if found == nil {
		t.Fatalf("a daemon with state at %s is not manageable — nothing but kill(1) can "+
			"reach it", lock)
	}
	if found.Declared {
		t.Error("an undeclared daemon is reported as declared")
	}
	if len(found.Argv) != 0 {
		t.Error("yolo claims a spawn argv for a daemon nothing declares")
	}
	if found.NoSpawn == "" {
		t.Error("nothing says why it cannot be restarted, so `restart` would refuse mutely")
	}
}

// TestUnknownSingletonListsTheSet: a name the set does not hold is refused BY
// NAME, with this machine's actual membership — which only this process can know,
// since the set is derived per machine.
func TestUnknownSingletonListsTheSet(t *testing.T) {
	msg := UnknownSingleton("nope", []Singleton{{Name: "yjtest-a"}, {Name: "yjtest-b"}})
	for _, want := range []string{"nope", "yjtest-a", "yjtest-b"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal is missing %q:\n%s", want, msg)
		}
	}
	if empty := UnknownSingleton("nope", nil); !strings.Contains(empty, "nope") {
		t.Errorf("the empty-set refusal does not name the daemon:\n%s", empty)
	}
}

// TestStopWorksFromTheNameAlone: stop needs no declaration, which is what makes
// the previous test's daemon actually stoppable rather than merely listable.
func TestStopWorksFromTheNameAlone(t *testing.T) {
	st := &lifeState{alive: map[int]bool{31: true}}
	deps, buf := newDeps(t, st)
	deps.Singleton = Singleton{Name: "yjtest-orphan"}
	deps.Life.Argv = nil
	_ = os.WriteFile(deps.Life.PIDFilePath, []byte("31\n"), 0o644)
	if rc := Stop(deps); rc != 0 {
		t.Errorf("stop rc = %d, want 0", rc)
	}
	if len(st.killed) != 1 || st.killed[0] != 31 {
		t.Errorf("expected SIGTERM to 31, got %v", st.killed)
	}
	if !strings.Contains(buf.String(), "Stopped yjtest-orphan.") {
		t.Errorf("the stop line does not name the daemon:\n%s", buf.String())
	}
}

func keysOf(m map[string]Singleton) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestResolveSingletonFillsBothGapsForTheAlias pins the two states in which the
// derived set alone cannot answer for `yolo broker`, because both were measured
// rather than imagined: this repo's own jail carries
// /tmp/yolo-claude-oauth-broker.pid with no claude pack resolved in the test
// process, which is the second case exactly.
func TestResolveSingletonFillsBothGapsForTheAlias(t *testing.T) {
	// GAP 1: not in the set at all.
	got, ok := ResolveSingleton(nil, BrokerLoopholeName)
	if !ok {
		t.Fatal("`yolo broker` resolves to nothing when discovery is empty — the alias " +
			"goes dark exactly where a user still needs it")
	}
	if len(got.Argv) == 0 {
		t.Error("the alias resolved to a record with no argv, so `yolo broker restart` refuses")
	}

	// GAP 2: present but unspawnable — a member derived from a PID file, with no
	// manifest behind it.
	rendezvousOnly := []Singleton{{Name: BrokerLoopholeName, NoSpawn: "nothing declares it"}}
	got, ok = ResolveSingleton(rendezvousOnly, BrokerLoopholeName)
	if !ok || len(got.Argv) == 0 {
		t.Errorf("with the broker known only from its rendezvous, the alias resolves to "+
			"%+v — `yolo broker restart` would refuse to restart the broker on the very "+
			"machine it is running on", got)
	}

	// AND NOTHING ELSE GETS A FALLBACK: an unknown name is refused, which is what
	// makes the refusal path reachable at all.
	if _, ok := ResolveSingleton(nil, "yjtest-nobody"); ok {
		t.Error("an unknown name resolved to something")
	}
	// A DECLARED member is returned as itself, not replaced.
	declared := []Singleton{{Name: BrokerLoopholeName, Declared: true, Argv: []string{"from-the-manifest"}}}
	if got, _ := ResolveSingleton(declared, BrokerLoopholeName); got.Argv[0] != "from-the-manifest" {
		t.Errorf("a declared record was overwritten by the built-in one: %+v", got)
	}
}

// TestTheRendezvousScanIgnoresForeignPIDFiles is a regression pin with a measured
// origin: the scan globbed `/tmp/yolo-*.pid` and picked up `jaild` — the in-jail
// SUPERVISOR's pid file, whose namespace overlaps the singleton rendezvous exactly
// (`internal/entrypoint`'s supervisorPIDFile, plus its legacy
// `/tmp/yolo-jail-supervisor.pid`). `yolo host-daemon stop jaild` would have killed
// the jail's own supervisor, and the name round-tripped through the derivation, so
// the round-trip check did not catch it.
//
// The lock file has exactly one writer, which is why the scan globs that instead.
func TestTheRendezvousScanIgnoresForeignPIDFiles(t *testing.T) {
	const foreign = "yjtest-fake-supervisor"
	pidFile := paths.HostSingletonPIDFile(foreign)
	if err := os.WriteFile(pidFile, []byte("1\n"), 0o600); err != nil {
		t.Skipf("cannot write the fixture at %s: %v", pidFile, err)
	}
	t.Cleanup(func() { _ = os.Remove(pidFile) })

	for _, name := range RendezvousSingletonNames() {
		if name == foreign {
			t.Fatalf("a bare PID file at %s became a manageable daemon — `stop` would "+
				"signal whatever process wrote it", pidFile)
		}
	}
}
