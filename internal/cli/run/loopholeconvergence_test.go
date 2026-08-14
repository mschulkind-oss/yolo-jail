package run

// loopholeconvergence_test.go pins the RUN-PATH half of docs/design/loophole-packaging.md
// §5.1 (landing item 5d): the four launch-side census surfaces read the converged loophole
// set, the briefing filters on Active() rather than Enabled(), and a manifest claiming a
// builtin service name gets its silently-skipped daemon PRINTED (§3.1).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	body := `{"name":"` + name + `","description":"` + name + ` capability","enabled":true,` +
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

// TestBuiltinNameSkipIsAudible is §3.1's requirement: make the loopholesruntime.go skip
// PRINT when the name did not come from the builtin.
//
// The silent version is worse than it looks. A manifest named `journal` or `cgroup-delegate`
// loaded, was discovered, had its daemon dropped here without a word — while RuntimeArgsFor
// had ALREADY emitted its --add-host, ca_cert, --device, bind mounts and jail_env into the
// argv. Half a loophole, and the half that DID happen is the half that changes what crosses
// into the jail.
func TestBuiltinNameSkipIsAudible(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	isolatePackModules(t)
	fakeBundled(t)
	// A user-dir loophole named `journal`. A PACK cannot get here any more (the pre-flight
	// refuses the name at staging), which leaves the hand-placed user directory — not
	// refused, because it carries the user's own authority, and therefore exactly the case
	// that has to be said out loud.
	userRoot := t.TempDir()
	orig := loopholes.UserLoopholesDir
	loopholes.UserLoopholesDir = func() string { return userRoot }
	t.Cleanup(func() { loopholes.UserLoopholesDir = orig })
	writeHostDaemonModule(t, userRoot, paths.BuiltinJournalLoopholeName)

	var out bytes.Buffer
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Stdout = &out
	// rt "container" returns before the loop; use podman, and let every probe fail so
	// nothing is actually spawned.
	o.startLoopholes("yolo-test-builtin-skip", "podman", jsonx.NewOrderedMap())

	got := out.String()
	if !strings.Contains(got, paths.BuiltinJournalLoopholeName) {
		t.Errorf("the skip must NAME the loophole; got:\n%s", got)
	}
	for _, want := range []string{"built-in", "NOT started", "jail_env"} {
		if !strings.Contains(got, want) {
			t.Errorf("skip message missing %q — it has to say both halves: the daemon did not "+
				"start AND the manifest's binds/devices/jail_env DID cross. Only saying the "+
				"first is how this became invisible; got:\n%s", want, got)
		}
	}
}

// The builtin skip must stay SILENT for yolo's own builtins, which reach the same branch on
// every launch. A warning printed unconditionally is a warning nobody reads.
func TestBuiltinNameSkipIsSilentForYolosOwn(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	isolatePackModules(t)
	fakeBundled(t)
	userRoot := t.TempDir()
	orig := loopholes.UserLoopholesDir
	loopholes.UserLoopholesDir = func() string { return userRoot }
	t.Cleanup(func() { loopholes.UserLoopholesDir = orig })

	var out bytes.Buffer
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Stdout = &out
	o.startLoopholes("yolo-test-builtin-quiet", "podman", jsonx.NewOrderedMap())

	if strings.Contains(out.String(), "shares a name with yolo's own") {
		t.Errorf("no manifest claimed a builtin name, so nothing must be said; got:\n%s", out.String())
	}
}

// isBuiltinLoopholeName reads paths.BuiltinLoopholeNames rather than comparing the two
// constants inline — which is how `journal` came to be reserved in paths.go and enforced
// nowhere in the first place.
func TestIsBuiltinLoopholeNameReadsTheSharedList(t *testing.T) {
	for _, name := range paths.BuiltinLoopholeNames {
		if !isBuiltinLoopholeName(name) {
			t.Errorf("%q is in paths.BuiltinLoopholeNames but the skip does not recognize it — "+
				"that divergence is the original defect", name)
		}
	}
	if isBuiltinLoopholeName("acme-proxy") {
		t.Error("an ordinary loophole name must not be treated as a builtin")
	}
	if len(paths.BuiltinLoopholeNames) != 2 {
		t.Errorf("paths.BuiltinLoopholeNames has %d entries; if a THIRD builtin was added, check "+
			"that the config validator, this skip and the reserved set all learned about it "+
			"together — the whole point of the shared list", len(paths.BuiltinLoopholeNames))
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
	body := `{"name":"` + name + `","description":"impostor","enabled":true,` +
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
