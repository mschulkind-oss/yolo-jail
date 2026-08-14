package loopholes

// convergence_test.go asserts docs/design/loophole-packaging.md §5.1's requirement
// (landing item 5d): "the pack-aware, lock-gated loophole set is ONE constructed value,
// produced once on the host and passed to every consumer — not seven independent
// DiscoverOptions assemblies. Assert the convergence in a test."
//
// THE CENSUS, re-derived 2026-08-14 against HEAD rather than taken from the doc. Six
// Discover callers plus one independent walker = SEVEN surfaces, which matches:
//
//	1  internal/cli/run/prepare.go            the briefing
//	2  internal/cli/run/assemble_parts.go     brokerLoopholeActive
//	3  internal/cli/run/assemble_parts.go     loopholesRuntimeArgs (container argv)
//	4  internal/cli/run/loopholesruntime.go   startLoopholes (the host daemon spawn)
//	5  internal/loopholes/loopholescmd.go     `yolo loopholes list` / `status`
//	6  internal/loopholes/resolver.go         config.LoopholeResolver.Known() -> validateLoopholes
//	7  internal/loopholes/discover.go         ValidateLoopholes, via internal/cli/check
//
// Sites 1-5 now go through NewHostSet; 6 reads the same recorded modules; 7 is the walker,
// which cannot use Discover at all (it needs the error channel Discover throws away) and so
// reads the recorded modules directly. TestEveryDiscoverCallSiteIsConverged pins that,
// structurally, over the source.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeModule writes a minimal valid loophole module dir and returns its path. doctorCmd
// empty means the manifest declares no self-check.
func writeModule(t *testing.T, parent, name string, doctorCmd []string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"` + name + `","description":"` + name + ` desc","enabled":true,` +
		`"transport":"none","lifecycle":"external"`
	if len(doctorCmd) > 0 {
		body += `,"doctor_cmd":["` + strings.Join(doctorCmd, `","`) + `"]`
	}
	body += "}"
	if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// isolateModules clears the process-wide pack-module record around a test, so one test's
// recording cannot leak into another's (the record is deliberately process-wide — it is the
// convergence point — which makes the cleanup mandatory rather than optional).
func isolateModules(t *testing.T) {
	t.Helper()
	prevResolver := packModuleResolver
	t.Cleanup(func() {
		ResetPackModules()
		SetPackModuleResolver(prevResolver)
	})
	// No resolver and no record: the fail-safe empty state, so a test that records nothing
	// sees nothing rather than whatever this machine's real packs happen to contribute.
	SetPackModuleResolver(nil)
	ResetPackModules()
}

// The LAZY FALLBACK exists for an ordering fact a record alone cannot cover: on the launch
// path config validation (census site 6) runs BEFORE pack staging, so at the moment
// config.LoopholeResolver.Known() is consulted the staged record is still empty.
//
// Without it, a `loopholes.<pack-loophole>.enabled` entry takes the unknown-name path and
// warns "no loophole named 'x' is installed on this machine" at EVERY launch — the same
// sentence a user gets when a pack genuinely failed to stage
// (docs/design/loophole-packaging.md §5.2's prerequisite).
func TestLazyResolverCoversTheSurfacesThatNeverStage(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	mod := writeModule(t, t.TempDir(), "from-the-store", nil)
	calls := 0
	SetPackModuleResolver(func() []PackModule {
		calls++
		return []PackModule{{Dir: mod, HostExecApproved: true}}
	})

	if _, ok := NewHostSet(nil).Lookup("from-the-store"); !ok {
		t.Fatal("a surface that never staged must still see the configured packs' loopholes, " +
			"or `yolo check` / `loopholes list` / the pre-staging config validator each report a " +
			"machine that differs from the one the launch builds")
	}
	// MEMOIZED: resolution reads the pack store and every pack's manifest, and the
	// surfaces that need it ask more than once per process.
	_ = NewHostSet(nil)
	_ = NewHostSet(nil)
	if calls != 1 {
		t.Errorf("resolver called %d times, want 1 — it reads the pack store and every "+
			"manifest, so per-lookup resolution would make discovery cost scale with the "+
			"number of surfaces", calls)
	}
}

// The STAGED record SUPERSEDES the lazy resolver, and it must: staging is the authoritative
// view — it is what the jail actually mounts, `only`/`exclude` filters included — so a
// launch must never validate against one set and mount another.
func TestStagedRecordSupersedesTheLazyResolver(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	parent := t.TempDir()
	fromStore := writeModule(t, parent, "store-only", nil)
	fromStaging := writeModule(t, parent, "staged-only", nil)
	SetPackModuleResolver(func() []PackModule {
		return []PackModule{{Dir: fromStore, HostExecApproved: true}}
	})
	SetPackModules([]PackModule{{Dir: fromStaging, HostExecApproved: true}})

	set := NewHostSet(nil)
	if _, ok := set.Lookup("staged-only"); !ok {
		t.Error("the staged record must win — it is what the jail will mount")
	}
	if _, ok := set.Lookup("store-only"); ok {
		t.Error("the lazy resolver's answer must not MERGE with the staged record: an " +
			"only/exclude filter that removed a module dir is visible to staging and not to " +
			"the store read, so merging would mount a loophole the filter excluded")
	}
}

// Neither a record nor a resolver = no pack loopholes. The fail-safe state, and it has to be
// this direction: a missing pack loophole in `loopholes list` is a visible omission, while an
// unaudited daemon self-check executing under a read-only preflight would not be.
func TestNoRecordAndNoResolverMeansNoPackLoopholes(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	if got := PackModules(); len(got) != 0 {
		t.Errorf("PackModules() = %v, want empty — a process that resolved nothing must not "+
			"discover, let alone EXECUTE, anything a pack shipped", got)
	}
}

// A pack-contributed module dir is DISCOVERED, labelled SourcePack, and carries its
// description — so `yolo loopholes list` shows it, which is the whole point of the fourth
// Source label (§5.1: "SourcePack, beside SourceBundled|User|Config … is what
// `yolo loopholes list` prints").
func TestPackModuleIsDiscoveredAsSourcePack(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	staged := t.TempDir()
	mod := writeModule(t, staged, "acme-proxy", nil)

	set := NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: mod, HostExecApproved: true}}})
	lp, ok := set.Lookup("acme-proxy")
	if !ok {
		t.Fatal("a pack-contributed module dir must be discovered — the caller passes PATHS in " +
			"and internal/loopholes never learns what a pack is")
	}
	if lp.Source != SourcePack {
		t.Errorf("Source = %q, want %q — the label is what `loopholes list` prints, and a pack "+
			"loophole indistinguishable from a bundled one is a pack loophole nobody can audit",
			lp.Source, SourcePack)
	}
	if lp.Description != "acme-proxy desc" {
		t.Errorf("Description = %q; the manifest is read by the SAME loader all four sources use",
			lp.Description)
	}
}

// PRECEDENCE, exactly as §5.1 corrects it: `user` overrides `pack`, and nothing here
// reintroduces draft 1's deleted "bundled < pack" line — a pack-vs-reserved clash is
// refused by the launch pre-flight and never reaches an ordering at all.
func TestUserDirOverridesPackModule(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	staged, userRoot := t.TempDir(), t.TempDir()
	mod := writeModule(t, staged, "shared", nil)
	writeModule(t, userRoot, "shared", nil)

	set := NewSet(DiscoverOptions{
		Root: userRoot, RootSet: true,
		PackModules: []PackModule{{Dir: mod, HostExecApproved: true}},
	})
	lp, ok := set.Lookup("shared")
	if !ok {
		t.Fatal("shared not discovered")
	}
	if lp.Source != SourceUser {
		t.Errorf("Source = %q, want %q — a HAND-PLACED user directory carries the user's own "+
			"authority, the same reason a file:// pack does, so it keeps its last-wins override",
			lp.Source, SourceUser)
	}
}

// And a config entry still overrides a PACK loophole's `enabled`, which is §5.2's
// prerequisite made real: the toggle writes loopholes.<name>.enabled in the user config,
// and applyWorkspaceOverrides has to find the pack record to apply it to.
func TestConfigOverrideAppliesToAPackModule(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	mod := writeModule(t, t.TempDir(), "acme-proxy", nil)
	cfg := orderedFromPairs("acme-proxy", map[string]any{"enabled": false})

	set := NewSet(DiscoverOptions{
		LoopholesConfig: cfg,
		PackModules:     []PackModule{{Dir: mod, HostExecApproved: true}},
	})
	lp, ok := set.Lookup("acme-proxy")
	if !ok {
		t.Fatal("acme-proxy vanished; a config override must PATCH the record, not replace it")
	}
	if lp.Enabled {
		t.Error("loopholes.acme-proxy.enabled=false must disable a PACK-shipped loophole — " +
			"otherwise §5.2's toggle has nowhere to write")
	}
	if lp.Source != SourcePack {
		t.Errorf("Source = %q, want %q: an override patches the record in place; taking the "+
			"unknown-name fallback instead is what warned 'no loophole named x is installed' at "+
			"EVERY launch — the same sentence a user gets when a pack genuinely failed to stage",
			lp.Source, SourcePack)
	}
}

// The Resolver — census site 6 — sees the recorded pack modules, which is what makes the
// §5.2 prerequisite hold at the site that actually validates the config entry.
func TestResolverKnowsPackModules(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	mod := writeModule(t, t.TempDir(), "acme-proxy", nil)
	SetPackModules([]PackModule{{Dir: mod, HostExecApproved: true}})

	known, ok := (&Resolver{Root: t.TempDir()}).Known()
	if !ok {
		t.Fatal("Known() must never report failure — resolver.go's invariant, relied on at " +
			"every call site, and adding a source must not reverse it")
	}
	if _, isKnown := known["acme-proxy"]; !isKnown {
		t.Error("config.LoopholeResolver.Known() must see a pack-shipped loophole, or a " +
			"`loopholes.acme-proxy.enabled` entry takes the override-shaped FALLBACK and warns " +
			"'no loophole named acme-proxy is installed on this machine' at every launch")
	}
}

// ValidateLoopholes — census site 7, `yolo check`'s independent walker — reads the same
// recorded modules. It is a walker rather than a Discover call because it needs the error
// channel Discover throws away by contract, so the convergence here is over the INPUT, not
// the loader.
func TestValidateLoopholesSeesPackModules(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	mod := writeModule(t, t.TempDir(), "acme-proxy", nil)
	SetPackModules([]PackModule{{Dir: mod, HostExecApproved: true}})

	entries := ValidateLoopholes(t.TempDir(), true, false)
	var found *ValidateEntry
	for i := range entries {
		if entries[i].Loophole != nil && entries[i].Loophole.Name == "acme-proxy" {
			found = &entries[i]
		}
	}
	if found == nil {
		t.Fatal("`yolo check` must see a pack-shipped loophole; a preflight blind to a source " +
			"the launch honors reports a green machine that launches differently")
	}
	if found.Loophole.Source != SourcePack {
		t.Errorf("Source = %q, want %q", found.Loophole.Source, SourcePack)
	}
}

// A pack module dir that VANISHED after staging is REPORTED by the walker, not skipped. The
// pack layer's own refusal (a `from` naming a directory the pack does not contain) is a
// pack.json check and does not cover a tree removed afterwards.
func TestValidateLoopholesReportsAMissingPackModule(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	SetPackModules([]PackModule{{Dir: filepath.Join(t.TempDir(), "gone"), HostExecApproved: true}})

	entries := ValidateLoopholes(t.TempDir(), true, false)
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Path, "gone") && e.Err != "" {
			found = true
		}
	}
	if !found {
		t.Error("a missing pack module dir must be reported by name — this function's whole " +
			"contract is that a broken source is visible rather than absent")
	}
}

// THE GATE, and it is the requirement §5.1 states as a hard floor: "until it exists,
// RunDoctorChecks must take only loopholes whose origin gate has been evaluated."
//
// Enforced in the CALLEE, because two of the doctor call sites — `yolo check` and
// `yolo loopholes status` — are commands users and AGENTS.md treat as read-only preflight
// and neither has pack resolution, a lockfile, or packMayAccessHost anywhere in reach. A
// rule they were merely asked to follow is a rule the next call site will not know about.
func TestUnapprovedPackDoctorCmdIsNeverExecuted(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	sentinel := filepath.Join(t.TempDir(), "ran")
	mod := writeModule(t, t.TempDir(), "evil", []string{"/bin/sh", "-c", "touch " + sentinel})

	set := NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: mod, HostExecApproved: false}}})
	lp, ok := set.Lookup("evil")
	if !ok {
		t.Fatal("an UNAPPROVED pack loophole must still be DISCOVERED — 'installed but not " +
			"approved' has to be visible; it is the EXECUTION that is refused")
	}

	results := set.RunDoctorChecks([]*Loophole{lp}, 2*time.Second)
	if len(results) != 1 {
		t.Fatalf("want one result, got %d", len(results))
	}
	if results[0].RC != nil {
		t.Errorf("rc = %d; an unapproved pack's doctor_cmd must not run at all", *results[0].RC)
	}
	if !strings.Contains(results[0].Output, "not approved") {
		t.Errorf("the refusal must SAY it was withheld (%q) — silence is indistinguishable from "+
			"`no-check`, which reads as 'declares no self-check'", results[0].Output)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("THE DOCTOR_CMD RAN. `yolo check` and `yolo loopholes status` are read-only " +
			"preflight; running an unapproved fetched pack's host code from them is the fork " +
			"§5.1 refuses to leave open")
	}
}

// The package-level RunDoctorChecks — the one two call sites outside this package use, and
// which carries no gate at all — must also refuse a SourcePack record. This is the door
// nailed shut rather than documented: a slice cannot carry a gate, so the only place the
// check cannot be forgotten is inside the function that spawns the process.
func TestPackageLevelRunDoctorChecksRefusesAPackRecord(t *testing.T) {
	unsetJail(t)
	sentinel := filepath.Join(t.TempDir(), "ran")
	lp := &Loophole{
		Name: "evil", Source: SourcePack, Enabled: true,
		DoctorCmd: []string{"/bin/sh", "-c", "touch " + sentinel}, DoctorCmdSet: true,
	}
	results := RunDoctorChecks([]*Loophole{lp}, 2*time.Second)
	if results[0].RC != nil {
		t.Errorf("rc = %d, want none", *results[0].RC)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("a SourcePack record reached the ungated RunDoctorChecks and EXECUTED — the " +
			"gate has to hold for a caller that never heard of it")
	}
}

// An APPROVED pack's doctor_cmd does run: the gate is a gate, not a ban. Without this the
// fail-safe direction would be indistinguishable from the feature being broken.
func TestApprovedPackDoctorCmdRuns(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	mod := writeModule(t, t.TempDir(), "friendly", []string{"/bin/sh", "-c", "exit 0"})

	set := NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: mod, HostExecApproved: true}}})
	lp, _ := set.Lookup("friendly")
	results := set.RunDoctorChecks([]*Loophole{lp}, 5*time.Second)
	if results[0].RC == nil || *results[0].RC != 0 {
		t.Errorf("an APPROVED pack loophole's self-check must run and report: got rc=%v out=%q",
			results[0].RC, results[0].Output)
	}
}

// A bundled/user/config record needs no origin decision (all three carry the user's own
// authority by construction), so the gate must not accidentally withhold them — which
// would break every bundled loophole's self-check.
func TestNonPackRecordsAreAlwaysAllowedToRunHostCode(t *testing.T) {
	unsetJail(t)
	set := SetOf([]*Loophole{
		{Name: "b", Source: SourceBundled},
		{Name: "u", Source: SourceUser},
		{Name: "c", Source: SourceConfig},
		{Name: "p", Source: SourcePack},
	})
	for _, lp := range set.All() {
		want := lp.Source != SourcePack
		if got := set.MayRunHostCode(lp); got != want {
			t.Errorf("MayRunHostCode(%s/%s) = %v, want %v", lp.Name, lp.Source, got, want)
		}
	}
}

// Active() vs Enabled(): the distinction the briefing path was missing (§5.1's shipped
// bug). An enabled loophole whose `requires` is unmet on this host is NOT a live capability,
// and a briefing built from Enabled() advertised it to the agent as one.
func TestActiveExcludesEnabledButUnmetRequirements(t *testing.T) {
	unsetJail(t)
	dir := t.TempDir()
	body := `{"name":"needy","enabled":true,"transport":"none","lifecycle":"external",` +
		`"requires":{"file_exists":"` + filepath.Join(dir, "definitely-absent") + `"}}`
	mod := filepath.Join(dir, "needy")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	set := NewSet(DiscoverOptions{Root: dir, RootSet: true})
	if len(set.Enabled()) != 1 {
		t.Fatalf("Enabled() = %d records, want 1", len(set.Enabled()))
	}
	if got := len(set.Active()); got != 0 {
		t.Errorf("Active() = %d records, want 0 — an enabled-but-inactive loophole advertised "+
			"to the agent as a live capability is how an agent comes to debug the host instead "+
			"of reading one line saying the loophole is inactive here", got)
	}
}

// NewHostSet is the CONVERGENCE POINT, and its defaults are the ones every hand-built
// DiscoverOptions had to remember: bundled INCLUDED (the zero value is false), the recorded
// pack modules, and the include-disabled superset so the narrow views can be derived rather
// than re-walked.
func TestNewHostSetIncludesBundledAndRecordedPacks(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	bundled := t.TempDir()
	writeModule(t, bundled, "fake-bundled", nil)
	orig := BundledLoopholesDir
	BundledLoopholesDir = func() string { return bundled }
	t.Cleanup(func() { BundledLoopholesDir = orig })

	mod := writeModule(t, t.TempDir(), "from-a-pack", nil)
	SetPackModules([]PackModule{{Dir: mod, HostExecApproved: true}})

	set := NewHostSet(nil)
	if _, ok := set.Lookup("fake-bundled"); !ok {
		t.Error("NewHostSet must include bundled loopholes — the DiscoverOptions zero value is " +
			"IncludeBundled:false, which is exactly the flag every one of the six call sites had " +
			"to remember to set")
	}
	if _, ok := set.Lookup("from-a-pack"); !ok {
		t.Error("NewHostSet must include the recorded pack modules — that is the convergence")
	}
}

// A disabled record survives into the Set (so `loopholes list` can show it) while being
// absent from Enabled()/Active(). One construction, two views, no second filesystem walk —
// which is also what stops the views being built from different inputs.
func TestSetHoldsTheDisabledSupersetAndOffersNarrowViews(t *testing.T) {
	unsetJail(t)
	dir := t.TempDir()
	writeModule(t, dir, "on", nil)
	off := filepath.Join(dir, "off")
	if err := os.MkdirAll(off, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"off","enabled":false,"transport":"none","lifecycle":"external"}`
	if err := os.WriteFile(filepath.Join(off, "manifest.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	set := NewSet(DiscoverOptions{Root: dir, RootSet: true})
	if len(set.All()) != 2 {
		t.Fatalf("All() = %d, want both records (list has to show the disabled one)", len(set.All()))
	}
	if len(set.Enabled()) != 1 || set.Enabled()[0].Name != "on" {
		t.Errorf("Enabled() = %v, want just [on]", names(set.Enabled()))
	}
}
