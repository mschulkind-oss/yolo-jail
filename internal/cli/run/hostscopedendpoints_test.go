package run

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// hostScopedFixture registers `mods` as THIS process's whole pack-module record and
// returns nothing — the caller drives hostServicesMountArgs and reads the argv.
//
// It takes already-written manifest dirs rather than writing them itself, because the
// tests below need two different SOURCES of manifest: hand-written records (for a
// synthetic fourth loophole nobody has shipped) and the repo's own shipped manifests
// (for the census test, which must not be able to disagree with `packs/`).
func hostScopedFixture(t *testing.T, mods []loopholes.PackModule) {
	t.Helper()
	loopholes.SetPackModuleResolver(nil)
	loopholes.SetPackModules(mods)
	t.Cleanup(func() {
		loopholes.ResetPackModules()
		loopholes.SetPackModuleResolver(resolvePackLoopholeModules)
	})
}

// writeLoopholeManifest writes one pack-shipped manifest and returns its module dir.
//
// `publishes: "socket"` is REQUIRED of a pack-shipped daemon (the default, "endpoint",
// is refused at load and the loophole silently vanishes), and `scope` is the field
// under test, so both are parameters rather than constants.
func writeLoopholeManifest(t *testing.T, root, name, scope string, defaultEnabled bool) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	enabled := "false"
	if defaultEnabled {
		enabled = "true"
	}
	manifest := `{"name":"` + name + `","description":"fixture","version":1,` +
		`"default_enabled":` + enabled + `,"transport":"` + loopholes.TransportLoopbackTLS + `",` +
		`"lifecycle":"spawned",` +
		`"host_daemon":{"cmd":["yolo","internal","daemon","` + name + `","--socket","{socket}"],` +
		`"publishes":"socket","scope":"` + scope + `"}}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// endpointVarsIn returns the loophole names whose endpoint variable the argv carries,
// derived from the emitted `-e` pairs rather than from a list the test wrote down.
func endpointVarsIn(args []string) []string {
	var names []string
	for _, a := range args {
		eq := strings.IndexByte(a, '=')
		if eq < 0 || !strings.HasPrefix(a, "YOLO_SERVICE_") || !strings.Contains(a, "_ENDPOINT=") {
			continue
		}
		// The VALUE names the loophole: <JailHostServicesDir>/<name>.endpoint. Read from
		// there rather than un-slugging the variable, because the slug is lossy (a dash
		// and an underscore both become `_`) and a test that guessed wrong would report a
		// missing endpoint for a loophole that had one.
		val := a[eq+1:]
		base := filepath.Base(val)
		names = append(names, strings.TrimSuffix(base, filepath.Ext(base)))
	}
	sort.Strings(names)
	return names
}

// shippedHostScopedLoopholeDirs walks the repo's own packs and returns the module dir of
// every loophole whose manifest declares `host_daemon.scope: "host"`, plus their names.
//
// ⚠ IT DERIVES THE CENSUS, WHICH IS THE WHOLE POINT. `rg -n '"scope": "host"'
// packs/*/loopholes/*/manifest.jsonc` is the only enumeration of this set anywhere in the
// tree, and the defect this file exists for is what happened when a second, hand-written
// enumeration lived in the run pipeline: `aws-auth` shipped, nobody added the third `if`,
// and its adapter answered ServiceUnreachable for every request while the launch reported
// a healthy jail. A test that listed the names would be that same enumeration a third
// time, and would pass on the day a FOURTH is added and forgotten.
func shippedHostScopedLoopholeDirs(t *testing.T) (dirs []string, names []string) {
	t.Helper()
	root := repoRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, "packs", "*", "loopholes", "*", "manifest.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		body, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		// The same string the census command greps for. A manifest that spells it
		// differently would be invisible here AND to the command AGENTS.md names, which
		// is a problem with the spelling rather than with this test.
		if !strings.Contains(string(body), `"scope": "host"`) {
			continue
		}
		dir := filepath.Dir(m)
		dirs = append(dirs, dir)
		names = append(names, filepath.Base(dir))
	}
	if len(dirs) < 3 {
		t.Fatalf("found %d host-scoped shipped loopholes (%v); the census had three when this "+
			"test was written and the glob or the manifest spelling has moved", len(dirs), names)
	}
	sort.Strings(names)
	return dirs, names
}

// repoRoot walks up from this package to the checkout root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the repo root from internal/cli/run")
	return ""
}

// TestEveryShippedHostScopedLoopholeGetsItsEndpointVariable is THE test this change is
// for, and it is written so that a FOURTH host-scoped loophole added tomorrow and
// forgotten fails it.
//
// It reads the census out of `packs/` — every manifest declaring
// `host_daemon.scope: "host"` — registers those real manifests as this process's approved
// pack modules, switches each one on, and asserts the argv carries an endpoint variable
// for EVERY name it found. Nothing in the test names a loophole.
//
// The defect it pins is live history: hostServicesMountArgs emitted the variable for
// `claude-oauth-broker` and `openai-auth-broker` with one `if` each, `aws-auth` shipped as
// the third, and its in-jail adapter answered `ServiceUnreachable` for every request while
// the launch reported a healthy jail — the reachability witness walks
// YOLO_SERVICE_*_ENDPOINT, so the one check that would have caught it was blind to a fault
// whose whole shape is a variable that does not exist.
func TestEveryShippedHostScopedLoopholeGetsItsEndpointVariable(t *testing.T) {
	dirs, names := shippedHostScopedLoopholeDirs(t)
	mods := make([]loopholes.PackModule, 0, len(dirs))
	for _, d := range dirs {
		mods = append(mods, loopholes.PackModule{Dir: d, HostExecApproved: true})
	}
	hostScopedFixture(t, mods)

	// The USER's switch, on for every one of them: `default_enabled` is the pack
	// author's answer and several ship OFF on purpose (aws-auth documents why), which
	// is a fact about the default and not about this emission.
	block := jsonx.NewOrderedMap()
	for _, n := range names {
		entry := jsonx.NewOrderedMap()
		entry.Set("enabled", true)
		block.Set(n, entry)
	}
	cfg := jsonx.NewOrderedMap()
	cfg.Set("loopholes", block)

	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return false }
	o.Getenv = func(string) string { return "" }

	got := endpointVarsIn(o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", cfg))
	if strings.Join(got, ",") != strings.Join(names, ",") {
		t.Errorf("the argv wires %v; the shipped host-scoped census is %v.\n"+
			"Every `host_daemon.scope: \"host\"` loophole that is Active and whose pack may "+
			"run host code is owed YOLO_SERVICE_<NAME>_ENDPOINT. A name missing here is a "+
			"jail whose adapter answers ServiceUnreachable for every request while the "+
			"launch reports success — the witness walks the variable, so the fault IS "+
			"invisible to it.", got, names)
	}
}

// TestASyntheticFourthHostScopedLoopholeIsWiredWithoutCodeChanges is the same property
// one step further out: a loophole that exists in NO pack in this repo, under a name the
// run pipeline has never heard, is wired purely because its manifest says
// `scope: "host"`.
//
// The test above could in principle be satisfied by three more hardcoded names. This one
// cannot be satisfied by any number of them.
func TestASyntheticFourthHostScopedLoopholeIsWiredWithoutCodeChanges(t *testing.T) {
	root := t.TempDir()
	dir := writeLoopholeManifest(t, root, "quartermaster", loopholes.ScopeHost, true)
	hostScopedFixture(t, []loopholes.PackModule{{Dir: dir, HostExecApproved: true}})

	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return false }
	o.Getenv = func(string) string { return "" }

	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	want := hostServiceEnvVar("quartermaster") + "=" + hostServiceEndpointPath("quartermaster")
	if !containsStr(args, want) {
		t.Errorf("a host-scoped loophole no line of run/ knows by name was not wired: %v\n"+
			"want %q — the emission is a predicate over `host_daemon.scope`, not a list", args, want)
	}
}

// TestAJailScopedLoopholeGetsNoHostServiceEndpoint is the discrimination control: the
// predicate reads SCOPE, not "declares a host_daemon".
//
// Without it the change would pass by emitting a variable for every daemon-declaring
// loophole — which would promise a host-wide endpoint path for a PER-JAIL daemon whose
// variable is emitted elsewhere entirely (RuntimeArgsFor), i.e. the same `-e` twice with
// one of them wrong.
func TestAJailScopedLoopholeGetsNoHostServiceEndpoint(t *testing.T) {
	root := t.TempDir()
	hostDir := writeLoopholeManifest(t, root, "quartermaster", loopholes.ScopeHost, true)
	jailDir := writeLoopholeManifest(t, root, "purser", loopholes.ScopeJail, true)
	hostScopedFixture(t, []loopholes.PackModule{
		{Dir: hostDir, HostExecApproved: true},
		{Dir: jailDir, HostExecApproved: true},
	})

	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return false }
	o.Getenv = func(string) string { return "" }

	got := endpointVarsIn(o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap()))
	if strings.Join(got, ",") != "quartermaster" {
		t.Errorf("wired %v, want only the host-scoped record: a jail-scoped daemon's endpoint "+
			"variable is emitted by the runtime-args path, and a second copy from here would "+
			"name the wrong publisher", got)
	}
}

// TestAnInactiveHostScopedLoopholeIsNotWired is the other control, and it is the one the
// witness makes expensive to get wrong: an endpoint variable for a loophole nothing
// starts is faultUnpublished, which can refuse the whole launch (OQ-R4, OQ-R5).
func TestAnInactiveHostScopedLoopholeIsNotWired(t *testing.T) {
	root := t.TempDir()
	dir := writeLoopholeManifest(t, root, "quartermaster", loopholes.ScopeHost, false)
	hostScopedFixture(t, []loopholes.PackModule{{Dir: dir, HostExecApproved: true}})

	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return false }
	o.Getenv = func(string) string { return "" }

	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	if got := endpointVarsIn(args); len(got) != 0 {
		t.Errorf("a disabled host-scoped loophole was wired anyway: %v", got)
	}
	// The directory mount is unconditional on this backend and still there — the
	// predicate must govern the env vars only.
	if len(args) != 2 || args[0] != "-v" {
		t.Errorf("want exactly the host-services mount, got %v", args)
	}
}

// TestAnUnapprovedPacksHostScopedLoopholeIsNotWired is the ORIGIN GATE, at a name that is
// not the broker's.
//
// TestBrokerEnvSuppressedForAnUnapprovedPack pins the same gate for `claude-oauth-broker`
// alone, which was exactly as much as the by-name emission could be wrong about. Now that
// the set is derived, the gate has to ride the predicate rather than one branch of it.
func TestAnUnapprovedPacksHostScopedLoopholeIsNotWired(t *testing.T) {
	root := t.TempDir()
	dir := writeLoopholeManifest(t, root, "quartermaster", loopholes.ScopeHost, true)
	hostScopedFixture(t, []loopholes.PackModule{{Dir: dir, HostExecApproved: false}})

	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return true }
	o.Getenv = func(string) string { return "" }

	if got := endpointVarsIn(o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())); len(got) != 0 {
		t.Errorf("the endpoint of a pack whose host access nobody approved was wired: %v — "+
			"Active() is not enough, and MayRunHostCode is what says the pack may touch the "+
			"host", got)
	}
}

// TestAppleContainerWithholdsEveryUnpublishableHostScopedEndpoint widens the AC arm's
// pin past the one name it used to cover.
//
// The shipped AC test asserts the CLAUDE broker is withheld there. That was the whole of
// the exposure while two names were emitted; with the set derived, a third, fourth and
// synthetic host-scoped loophole would each reach that backend — where startLoopholes'
// per-runtime allow list admits `openai-auth-broker` alone, so nothing publishes for them
// and the fatal witness turns the promise into a refused launch.
//
// The OpenAI endpoint is asserted PRESENT in the same argv, so the test cannot pass by
// suppressing host services wholesale.
func TestAppleContainerWithholdsEveryUnpublishableHostScopedEndpoint(t *testing.T) {
	root := t.TempDir()
	mods := []loopholes.PackModule{
		{Dir: writeLoopholeManifest(t, root, openAIAuthBrokerName, loopholes.ScopeHost, true), HostExecApproved: true},
		{Dir: writeLoopholeManifest(t, root, "quartermaster", loopholes.ScopeHost, true), HostExecApproved: true},
		{Dir: writeLoopholeManifest(t, root, "purser", loopholes.ScopeHost, true), HostExecApproved: true},
	}
	hostScopedFixture(t, mods)

	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return true }
	o.Getenv = func(string) string { return "" }

	got := endpointVarsIn(o.hostServicesMountArgs("container", "yolo-ws-abcd1234", jsonx.NewOrderedMap()))
	if strings.Join(got, ",") != openAIAuthBrokerName {
		t.Errorf("Apple Container wired %v, want only %q. Nothing on an AC launch publishes "+
			"for any other host-scoped loophole — the singleton ensure is gated on "+
			"`rt != \"container\"` and the loophole allow list admits openai-auth-broker "+
			"alone — so the jail dials files that never appear and the fatal witness may "+
			"refuse the launch outright", got, openAIAuthBrokerName)
	}

	// And podman is unchanged: same fixture, same options, one runtime over, all three.
	podman := endpointVarsIn(o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap()))
	if len(podman) != 3 {
		t.Errorf("podman wired %v, want all three: without this row the assertion above "+
			"passes for a fix that simply stopped emitting the variables everywhere", podman)
	}
}

// TestAppleContainerMountIsSkippedWhenNothingPublishesThere pins the generalised early
// return, which was the same defect wearing a different hat: it read
// `rt == "container" && !openAIAuthLoopholeActive(cfg)`, one hardcoded name standing in
// for "will anything publish into this directory on this backend?".
//
// Both rows are needed. A host-scoped loophole AC cannot back must not conjure the mount
// (it would name a directory nothing writes), and the one AC does back must still get it.
func TestAppleContainerMountIsSkippedWhenNothingPublishesThere(t *testing.T) {
	root := t.TempDir()
	dir := writeLoopholeManifest(t, root, "quartermaster", loopholes.ScopeHost, true)
	hostScopedFixture(t, []loopholes.PackModule{{Dir: dir, HostExecApproved: true}})

	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return true }
	o.Getenv = func(string) string { return "" }

	if args := o.hostServicesMountArgs("container", "yolo-ws-abcd1234", jsonx.NewOrderedMap()); len(args) != 0 {
		t.Errorf("Apple Container got host-services args for a backend that publishes "+
			"nothing this launch: %v", args)
	}
	// Every other backend keeps the unconditional mount: the same directory carries
	// JAIL-scoped daemons' endpoint files, whose variables are emitted elsewhere.
	if args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap()); len(args) < 2 || args[0] != "-v" {
		t.Errorf("podman lost the unconditional host-services mount: %v", args)
	}
}

// TestAppleContainerAllowListHasOneSpelling ties the two copies of ONE backend fact
// together, and it is a source assertion because the two live in different functions with
// no value passed between them.
//
// hostScopedEndpointIsUnpublishable exempts `openai-auth-broker` on `container`;
// startLoopholes' per-runtime allow list admits `openai-auth-broker` on `container`. If
// the allow list widens and the exemption does not, AC gains a daemon whose endpoint the
// jail is never told about; if the exemption widens and the allow list does not, AC
// promises an endpoint nothing publishes — which the fatal witness turns into a refused
// launch. Neither direction has a test that can see the other half, so this is it.
func TestAppleContainerAllowListHasOneSpelling(t *testing.T) {
	body, err := os.ReadFile("loopholesruntime.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `allow = func(name string) bool { return name == openAIAuthBrokerName }`) {
		t.Error("startLoopholes' Apple Container allow list is no longer the single-member " +
			"list hostScopedEndpointIsUnpublishable's `name != openAIAuthBrokerName` mirrors " +
			"(assemble_parts.go). Change both together, or AC either promises an endpoint " +
			"nothing publishes (faultUnpublished, a refused launch) or starts a daemon the " +
			"jail is never told about")
	}
}
