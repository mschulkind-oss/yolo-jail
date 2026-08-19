package run

// loopholeconvergence_test.go pins the RUN-PATH half of docs/design/loophole-packaging.md
// §5.1 (landing item 5d): the four launch-side census surfaces read the converged loophole
// set, and the briefing filters on Active() rather than Enabled().
//
// §3.1's builtin-name rule USED to be pinned here too — a manifest claiming a builtin
// service name had its daemon skipped, and the skip had to be PRINTED. There are no
// builtin service names left (loophole-activation.md OQ-A6), so what is pinned now is the
// inverse: such a manifest is spawned like any other, and the cgroup delegate's own
// in-process start is gated on its loophole record.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// fakeBundled points BundledLoopholesDir at a temp dir and returns it, so a test can define
// the whole bundled set. Without this the real bundled manifests (audio, the broker,
// host-processes) leak into every assertion and make them host-dependent.
func fakeBundled(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := loopholes.BundledLoopholesDir
	loopholes.BundledLoopholesDir = func() string { return dir }
	t.Cleanup(func() { loopholes.BundledLoopholesDir = orig })
	return dir
}

// isolatePackModules clears the process-wide pack-module record around a test.
func isolatePackModules(t *testing.T) {
	t.Helper()
	// This package's init() registers the LAZY resolver, which reads the real user config.
	// Unregister it so a test sees only what it records, and restore it on cleanup — a
	// later test in this package may depend on the real registration being in place.
	t.Cleanup(func() {
		loopholes.ResetPackModules()
		loopholes.SetPackModuleResolver(resolvePackLoopholeModules)
	})
	loopholes.SetPackModuleResolver(nil)
	loopholes.ResetPackModules()
}

// writeLoopholeModule writes a module dir. requiresFile, when non-empty, makes the loophole
// enabled-but-INACTIVE (a `requires.file_exists` that is not there).
func writeLoopholeModule(t *testing.T, parent, name, requiresFile string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"` + name + `","description":"` + name + ` capability","default_enabled":true,` +
		`"transport":"none","lifecycle":"external"`
	if requiresFile != "" {
		body += `,"requires":{"file_exists":"` + requiresFile + `"}`
	}
	body += "}"
	if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestBriefingAdvertisesOnlyActiveLoopholes is §5.1's "one shipped bug to fix while here":
// the briefing path filtered on Enabled only, not Active(), so an enabled-but-inactive
// loophole was advertised to the agent as a live capability.
//
// It matters because the briefing is INSTRUCTIONS THE AGENT ACTS ON. An agent told audio is
// available when the PipeWire sockets never crossed goes and debugs ALSA; an agent told the
// loophole is not active here reads one line and moves on.
func TestBriefingAdvertisesOnlyActiveLoopholes(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")
	isolatePackModules(t)
	bundled := fakeBundled(t)
	writeLoopholeModule(t, bundled, "live-one", "")
	writeLoopholeModule(t, bundled, "inactive-one", filepath.Join(t.TempDir(), "definitely-absent"))

	set := loopholes.NewHostSet(nil)
	// Preconditions: both are ENABLED, only one is ACTIVE. Without this the assertion
	// below could pass because discovery found nothing at all.
	if got := len(set.Enabled()); got != 2 {
		t.Fatalf("Enabled() = %d records, want both (the fixture is wrong, not the code)", got)
	}
	active := set.Active()
	if len(active) != 1 || active[0].Name != "live-one" {
		t.Fatalf("Active() = %v, want just [live-one]", loopholeNames(active))
	}

	// The BRIEFING'S OWN projection — the function refreshJailBriefings calls, not a
	// retyped copy of its body, which would assert nothing about the launch path.
	var advertised []string
	for _, lo := range briefingLoopholes(nil) {
		advertised = append(advertised, lo.Name)
	}
	for _, name := range advertised {
		if name == "inactive-one" {
			t.Error("the briefing advertises an enabled-but-INACTIVE loophole as a live " +
				"capability. The agent acts on this file: it will go debug the host wiring " +
				"instead of reading one line saying the loophole is inactive here (§5.1)")
		}
	}
	if len(advertised) != 1 {
		t.Errorf("briefing loopholes = %v, want just the active one", advertised)
	}
}

// A pack-contributed loophole reaches the briefing, the container argv and the daemon spawn
// through ONE recorded value — the convergence, observed from the run package.
func TestRunPathSurfacesSeeARecordedPackLoophole(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	isolatePackModules(t)
	fakeBundled(t)
	mod := writeLoopholeModule(t, t.TempDir(), "acme-proxy", "")
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: mod, HostExecApproved: true}})

	set := loopholes.NewHostSet(nil)
	lp, ok := set.Lookup("acme-proxy")
	if !ok {
		t.Fatal("a recorded pack loophole must be visible to the run path's converged set — " +
			"that is the whole of the convergence; a pack whose loophole the launch does not see " +
			"is a pack that staged a directory and did nothing")
	}
	if lp.Source != loopholes.SourcePack {
		t.Errorf("Source = %q, want %q", lp.Source, loopholes.SourcePack)
	}
	// The container-argv surface reads the SAME set (Enabled view).
	found := false
	for _, e := range set.Enabled() {
		if e.Name == "acme-proxy" {
			found = true
		}
	}
	if !found {
		t.Error("the container-argv surface must see it too, or the argv and the briefing " +
			"disagree about what this jail contains")
	}
}

// TestBrokerLookupIsUnshadowable: brokerLoopholeActive looks up a RESERVED name, so it can
// only ever find yolo's own bundled record. That is what closes §5.1's half-the-broker
// hazard — this predicate evaluating a PACK's Active() to decide the terminator/CA/endpoint
// wiring while loopholesruntime.go special-cased the NAME and ran yolo's own broker argv.
//
// Pinned at the pre-flight, which is where the guarantee actually lives.
func TestBrokerLookupIsUnshadowable(t *testing.T) {
	got := PackLoopholeNameConflicts([]PackLoopholeDecl{
		decl("sneaky", "loopholes/"+broker.BrokerLoopholeName),
	})
	if len(got) != 1 {
		t.Fatalf("a pack shipping loopholes/%s must be refused at staging, or brokerLoopholeActive "+
			"would evaluate the PACK's Active() while startLoopholes still ran yolo's own broker "+
			"argv — half the broker from one manifest, no message", broker.BrokerLoopholeName)
	}
}

// TestFormerBuiltinNamesAreSpawnedNotSkipped is what THREE tests used to be, inverted.
//
// They pinned the builtin-name skip: `paths.BuiltinLoopholeNames` was read by
// `isBuiltinLoopholeName`, a manifest claiming one of those names had its host daemon
// dropped, and §3.1's requirement was that the drop be AUDIBLE — because RuntimeArgsFor
// had already emitted that manifest's --add-host, ca_cert, --device, bind mounts and
// jail_env into the argv. Half a loophole, and the half that happened was the half that
// changes what crosses into the jail.
//
// The list, the predicate and the skip are all GONE (2026-08-18): `journal` and
// `cgroup-delegate` are pack-shipped manifests now, so there is no builtin name for the
// branch to fire on. What has to be pinned is the OPPOSITE property, and it is the one
// that breaks users rather than merely puzzling them: a loophole under either name must
// have its daemon STARTED. Keeping the skip would have inverted the original defect —
// refusing to run the very daemon the manifest exists to declare — which is what "do not
// recreate that shape in reverse" means.
//
// Evidenced by the readiness warning, which only a daemon that was actually spawned can
// produce: the fixture's cmd is /bin/true, so it exits immediately and never publishes.
//
// `journal` rather than `cgroup-delegate` for the fixture, deliberately: the delegate
// has an IN-PROCESS branch that this call would also reach, and on a cgroup-v2 host that
// binds a real socket and leaks a goroutine the test cannot stop. Its own gate is pinned
// by TestCgroupDelegateNeedsItsLoophole.
func TestFormerBuiltinNamesAreSpawnedNotSkipped(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	isolatePackModules(t)
	bundledRoot := fakeBundled(t)
	writeHostDaemonModule(t, bundledRoot, "journal")

	var out bytes.Buffer
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Stdout = &out
	o.ServiceReadyTimeout = 200 * time.Millisecond
	o.startLoopholes("yolo-test-former-builtin", "podman", jsonx.NewOrderedMap())

	got := out.String()
	if strings.Contains(got, "shares a name with yolo's own") {
		t.Errorf("the builtin-name skip fired for %q — that name belongs to an official "+
			"PACK now, so skipping it drops the daemon the manifest exists to declare while "+
			"its binds/devices/jail_env still cross; got:\n%s", "journal", got)
	}
	if !strings.Contains(got, "host service 'journal'") {
		t.Errorf("the daemon was never spawned: nothing reported on it at all. A former "+
			"builtin name must take the ORDINARY path now; got:\n%s", got)
	}
}

// TestCgroupDelegateNeedsItsLoophole is the OQ-A4 ruling, pinned at the one place it can
// be observed without a cgroup-v2 host: the delegate's gate.
//
// It used to start because the platform allowed it — "Linux only, cgroup v2 only", no
// config key anywhere — which is the presence-activation R1 deletes, in a host-side
// service, and the last one left. Now it starts only when a loophole record named
// `cgroup-delegate` is enabled, active and origin-approved.
//
// A gate is a hard thing to test from the positive side here (the in-process start needs
// a real cgroup-v2 delegation), so what is pinned is the predicate itself over three
// sets: no record at all, a disabled record, an enabled one.
func TestCgroupDelegateNeedsItsLoophole(t *testing.T) {
	o := goldenOptions(t.TempDir(), t.TempDir())

	if o.cgroupDelegateHonored(loopholes.SetOf(nil)) {
		t.Error("the delegate is honored with NO loophole record at all — that is the " +
			"presence activation OQ-A4 deletes, and it is how the delegate behaved for " +
			"every launch before 2026-08-18")
	}

	disabled := &loopholes.Loophole{
		Name: paths.BuiltinCgroupLoopholeName, Enabled: false, Source: loopholes.SourceBundled,
	}
	if o.cgroupDelegateHonored(loopholes.SetOf([]*loopholes.Loophole{disabled})) {
		t.Error("a DISABLED cgroup-delegate record still honors the delegate — the user's " +
			"switch has to reach it, or `default_enabled: false` means nothing")
	}

	enabled := &loopholes.Loophole{
		Name: paths.BuiltinCgroupLoopholeName, Enabled: true, Source: loopholes.SourceBundled,
	}
	if !o.cgroupDelegateHonored(loopholes.SetOf([]*loopholes.Loophole{enabled})) {
		t.Error("an ENABLED, active, non-pack record does not honor the delegate — the " +
			"switch is unreachable, so `yolo-cglimit` can never be turned back on")
	}

	// AND THE ORIGIN GATE BITES. A SourcePack record in a Set assembled by hand carries
	// no gate, so MayRunHostCode is false — the fail-safe direction, and the reason this
	// predicate is Honored-shaped rather than a bare Active(). Starting a host-side
	// listener on the strength of a PACK's record is exactly the crossing that gate
	// governs, and the broker's equivalent may skip it only because its record is
	// bundled under a name no pack may claim.
	fromPack := &loopholes.Loophole{
		Name: paths.BuiltinCgroupLoopholeName, Enabled: true, Source: loopholes.SourcePack,
		Path: "/nowhere/cgroup-delegate",
	}
	if o.cgroupDelegateHonored(loopholes.SetOf([]*loopholes.Loophole{fromPack})) {
		t.Error("an UNGATED pack record honors the delegate — Active() alone is not the " +
			"predicate here: the record comes from a pack now, so the origin gate applies")
	}
}

// TestLazyResolverReadsTheStoreAndGates is the run-package half of §5.2's prerequisite: the
// resolver registered by this package's init() resolves a CONFIGURED pack's loophole modules
// from the store, so the three surfaces that never stage — the pre-staging config validator,
// `yolo loopholes list`/`status`, `yolo check` — are pack-aware AND gated.
//
// A `file://` pack is used because its origin carries the user's own authority, which is what
// makes the gate's TRUE branch observable without a lockfile.
func TestLazyResolverReadsTheStoreAndGates(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	home := packHome(t)
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	pack := loopholePackDir(t, "hasloop", "loopholes/acme-proxy")
	writeUserPacks(t, home, `["file://`+pack+`"]`)

	got := resolvePackLoopholeModules()
	// This used to t.Skip on len(got) == 0, because before the kind landed packdecl.Decode
	// refused the unknown kind and the projection legitimately found nothing. The kind
	// landed, so an empty answer is now the DEFECT this test exists to catch — a resolver
	// that silently resolves nothing leaves the three never-staging surfaces (the
	// pre-staging config validator, `loopholes list`/`status`, `yolo check`) pack-blind,
	// which is the fork §5.1 refuses to leave open. A skip here would report that as ok.
	if len(got) != 1 {
		t.Fatalf("want one module, got %d: %+v", len(got), got)
	}
	if filepath.Base(got[0].Dir) != "acme-proxy" {
		t.Errorf("Dir = %q, want the acme-proxy module dir", got[0].Dir)
	}
	if !got[0].HostExecApproved {
		t.Error("a file:// pack's origin carries the user's own authority, so its loophole must " +
			"be approved for host execution — the same gate packMayAccessHost applies at launch")
	}
}

// The resolver is SILENT-AND-EMPTY on every failure, which is the opposite of stagePacks'
// fail-closed contract and deliberate: it runs behind read-only commands and behind a config
// validator, where the honest answer to "I cannot resolve your packs" is "I know of no pack
// loopholes" — never a refused preflight, and never a loophole treated as approved.
func TestLazyResolverIsSilentAndEmptyOnAnUnresolvablePack(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	home := packHome(t)
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	// A git pack that was never fetched: packRoot fails, and `yolo check` must not.
	writeUserPacks(t, home, `["git+ssh://git@example.invalid/org/repo?ref=main"]`)

	if got := resolvePackLoopholeModules(); len(got) != 0 {
		t.Errorf("an unresolvable pack must contribute nothing, got %+v", got)
	}
}

// writeHostDaemonModule writes a module dir declaring a host_daemon, so it reaches
// startLoopholes' spawn loop (a loophole with no host_daemon never enters `order`).
func writeHostDaemonModule(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"` + name + `","description":"impostor","default_enabled":true,` +
		`"transport":"loopback-tls","lifecycle":"spawned",` +
		`"host_daemon":{"cmd":["/bin/true"],"publishes":"socket"}}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// loopholeNames renders a record slice for error messages.
func loopholeNames(lps []*loopholes.Loophole) []string {
	out := make([]string, len(lps))
	for i, lp := range lps {
		out[i] = lp.Name
	}
	return out
}
