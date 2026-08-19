package run

// audiopack_test.go is the LAUNCH-PATH half of the `audio` pack's proof
// (docs/design/loophole-packaging.md §7, OQ-LP11): the pre-flight it must survive, and the
// two reports that describe it on a machine where it does nothing.
//
// packload/audiopack_test.go pins the pack's CONTENT (claims, subset, destinations). What
// only this package can pin is what the RUN PIPELINE does with it — the fourth pre-flight
// reads the composed reserved set (which packload cannot name: loopholes → config →
// packload is a cycle), and the inert report reads the backend.

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// shippedAudioPack returns the embedded `audio` pack, materialized.
//
// Through packload.Embedded() (which the run package's own tests already use) so this reads
// the same process-wide materialization the launch path does.
func shippedAudioPack(t *testing.T) *packload.Pack {
	t.Helper()
	for _, p := range packload.Embedded() {
		if p.Name == "audio" {
			return p
		}
	}
	t.Skip("no embedded packs registered in this test binary")
	return nil
}

// THE R5 ASSERTION at the launch face: the shipped pack survives the fourth pre-flight.
//
// IT USED TO ALSO ASSERT THAT "audio" WAS NOT IN loopholes.ReservedLoopholeNames(). That
// function is gone: every name yolo reserved has become a pack's own, `claude-oauth-broker`
// last (docs/design/broker-as-a-pack.md §10 step 5), so the reserved namespace was deleted
// with its final entry. The property the absence-check protected is unchanged and is now
// carried entirely by the call below — PackLoopholeNameConflicts is the thing that would
// refuse the launch, so asserting over IT rather than over the set it consulted is the
// stronger of the two spellings and the one that survives the mechanism's removal.
//
// The history is worth keeping because it is the shape of the trap: a pack shipping
// `loopholes/audio` while `bundled_loopholes/audio` existed was refused FATALLY, so every
// jail selecting the pack failed to start. That is why a reservation has to leave in the
// SAME commit as the manifest.
func TestShippedAudioPackDoesNotClaimAReservedName(t *testing.T) {
	p := shippedAudioPack(t)
	decls := packLoopholeDecls([]*packload.Pack{p})
	if len(decls) != 1 {
		t.Fatalf("the audio pack must declare exactly one loophole, got %d", len(decls))
	}
	if conflicts := PackLoopholeNameConflicts(decls); len(conflicts) > 0 {
		t.Errorf("the SHIPPED audio pack is refused by the loophole-name pre-flight, so every "+
			"jail selecting it would fail to launch:\n%s", strings.Join(conflicts, "\n"))
	}
}

// The pack REPLACED the bundled loophole rather than sitting beside it, and discovery
// sees exactly one `audio`.
//
// This test used to assert the opposite — that both were present and neither shadowed the
// other — because the pack shipped only the ALSA half under a second name while the
// bundled copy did the real work. The merge on 2026-08-18 made that framing wrong in the
// dangerous direction: two loopholes both claiming a bind on one destination is a jail
// that REFUSES TO START (podman's "duplicate mount destination", measured), so "additive"
// stopped being the safety property and "exactly one" became it.
//
// There is no `IncludeBundled` to pass any more (the channel is retired, OQ-BP4), which
// makes the "exactly one" assertion stricter rather than weaker: the pack module is the
// only input, so a second `audio` could only come from the pack itself.
func TestShippedAudioPackIsTheOnlyAudioLoophole(t *testing.T) {
	p := shippedAudioPack(t)
	mods := packLoopholeModules([]*packload.Pack{p})
	if len(mods) != 1 {
		t.Fatalf("modules = %d, want 1", len(mods))
	}
	// HostExecApproved comes from the pack's own MayAccessHost, which is true for an
	// embedded pack: selecting a pack that shipped in the binary you ran IS the approval,
	// so there is no lockfile entry to miss.
	if !mods[0].HostExecApproved {
		t.Error("an embedded pack's loophole module must be host-exec approved — the origin " +
			"gate for an embedded pack is the user's own authority, and a false here would " +
			"make the loophole's doctor_cmd unrunnable and its record refused")
	}

	found := map[string]string{}
	for _, lp := range loopholes.Discover(loopholes.DiscoverOptions{
		IncludeDisabled: true,
		PackModules:     mods,
	}) {
		found[lp.Name] = lp.Source
	}
	if got := found["audio"]; got != loopholes.SourcePack {
		t.Errorf("`audio` must now be discovered with source %q, got %q — the bundled copy "+
			"is deleted and the pack carries the whole loophole",
			loopholes.SourcePack, got)
	}
	if _, ok := found["audio-alsa"]; ok {
		t.Error("`audio-alsa` is still discovered. It was the pack's ALSA-only half, named " +
			"that ONLY because `audio` was reserved; both halves merged under the plain " +
			"name, and two audio loopholes claiming binds would be a jail that refuses to " +
			"start rather than one that logs a warning")
	}
	if found[mods[0].Dir] != "" { // sanity: keys are names, not dirs
		t.Error("discovery keyed a loophole by dir rather than name")
	}
}

// THE R4 ASSERTION: the platform/inert report fires for the pack on a machine it does not
// support, and stays silent on one it does.
//
// The pack's loophole declares `platforms: ["linux"]`, so this is the report a macOS user
// gets — by name, once, with the platforms it does support, and explicitly saying nothing
// is missing. That last clause is the point of the field existing: a Linux-only loophole
// reported as an unmet `requires` sends the reader after something to install.
//
// Driven through loopholedecl (the schema's own matcher) rather than through the run
// pipeline's printer, for the reason loopholeinert.go states: two matchers over one
// declaration is how a report and a gate come to disagree. So this asserts the note the
// report renders FROM, over the real shipped manifest.
func TestShippedAudioPackIsReportedInertOnDarwin(t *testing.T) {
	p := shippedAudioPack(t)
	decls := packLoopholeDecls([]*packload.Pack{p})
	m, _, err := loopholedecl.LoadDirTolerant(decls[0].Dir)
	if err != nil {
		t.Fatalf("reading the shipped manifest: %v", err)
	}
	if !m.PlatformsSet {
		t.Fatal("the audio pack's loophole must DECLARE its platforms — without the " +
			"declaration a macOS user gets no inert line at all, and the ALSA routing it " +
			"ships is a Linux kernel interface")
	}

	reason := m.PlatformsUnsupportedReason("darwin", "arm64")
	if reason == "" {
		t.Fatal("an ALSA loophole must be reported unsupported on darwin")
	}
	for _, want := range []string{"darwin/arm64", "linux", "Nothing is missing"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the inert reason must contain %q; got: %s", want, reason)
		}
	}
	// Rendered through the ONE mechanism, so the line a user sees carries the axis.
	note := loopholes.InertNote{
		Name: decls[0].Name, Axis: loopholes.AxisPlatform, Reason: reason,
	}
	if line := note.Line(); !strings.Contains(line, decls[0].Name) {
		t.Errorf("the inert line must name the loophole: %s", line)
	}

	// And SILENT where it runs: linux/amd64 and linux/arm64 both, since the declaration
	// names the GOOS only and must not accidentally pin an architecture.
	for _, arch := range []string{"amd64", "arm64"} {
		if r := m.PlatformsUnsupportedReason("linux", arch); r != "" {
			t.Errorf("linux/%s must be supported, got: %s", arch, r)
		}
	}
}

// The BACKEND axis of the same report: on Apple Container and macos-user the pack is inert
// for a reason that has nothing to do with the platform, and backend BEATS platform when
// both apply (an inert backend starts no host service whatever the platform says, and the
// actionable line is "switch backends", not "get a different machine").
//
// Asserted over the real pack because that is the combination a macOS user actually hits:
// darwin AND a container/macos-user backend, where two reasons apply and only one should
// print.
func TestShippedAudioPackInertBackendBeatsPlatform(t *testing.T) {
	p := shippedAudioPack(t)
	name := packLoopholeDecls([]*packload.Pack{p})[0].Name
	for _, rt := range []string{"container", "macos-user"} {
		reason := backendInertReason(rt)
		if reason == "" {
			t.Fatalf("backend %q must report a loophole as inert", rt)
		}
		line := inertLineFor(p.Name, loopholes.InertNote{
			Name: name, Axis: loopholes.AxisBackend, Reason: reason,
		})
		if !strings.Contains(line, "audio: ") || !strings.Contains(line, name) {
			t.Errorf("the inert line must name the pack and the loophole: %s", line)
		}
		if strings.Contains(line, "Nothing is missing") {
			t.Error("the BACKEND line must not carry the PLATFORM reason's wording — backend " +
				"beats platform, and two reasons for one outcome is the B-0 shape")
		}
	}
	// podman, the backend that DOES run loopholes, reports nothing.
	if r := backendInertReason("podman"); r != "" {
		t.Errorf("podman runs loophole host services, so it must report nothing; got: %s", r)
	}
}

// The launch DISCLOSURE puts the pack's claims in the read block, not the pre-spawn exec
// block — and that is the correct answer for a loophole that runs no host code.
//
// It matters because the pre-spawn block's whole value is that it is short: it is the last
// moment at which reading a line can change what the user does. A `:ro` bind and two static
// env vars in that block would dilute it, which is what disclosureClassOfClaim's
// exec-degrades-to-read rule exists to prevent. This is the first SHIPPED pack that
// exercises that rule.
func TestShippedAudioPackDisclosesAsAReadNotAnExec(t *testing.T) {
	p := shippedAudioPack(t)
	if lines := packHostExecClaims([]*packload.Pack{p}); len(lines) != 0 {
		t.Errorf("the audio pack runs NO host code, so it must not print in the pre-spawn "+
			"\"runs pack code on your machine\" block; got %+v", lines)
	}
	reads := disclosedClaims([]*packload.Pack{p}, disclosureRead)
	if len(reads) != 6 {
		t.Fatalf("want 6 read-disclosure lines (three binds + the device + two env vars), "+
			"got %d: %+v", len(reads), reads)
	}
	joined := ""
	for _, l := range reads {
		if l.pack != "audio" {
			t.Errorf("disclosure line attributed to %q, want \"audio\"", l.pack)
		}
		joined += l.claim + "\n"
	}
	for _, want := range []string{
		"PULSE_SERVER", "PIPEWIRE_REMOTE",
		"/etc/alsa/conf.d/50-yolo-audio-alsa.conf",
		// The three crossings the merge brought across. /dev/snd is disclosed as a READ
		// even though a device node is read-write, and that is the class boundary being
		// asserted rather than a claim about the device: disclosureRead is "does not run
		// host code", not "cannot write".
		"/run/pulse/native", "/run/pipewire/pipewire-0", "/dev/snd",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the read disclosure must name %q; got:\n%s", want, joined)
		}
	}
}

// The RESOLVED record's bind host is inside the pack's own staged tree — the property that
// makes the `{loophole_dir}` bind legal for a pack at all.
//
// Resolution is where a claim string's `{loophole_dir}` becomes a real path, so this is the
// one assertion that cannot be made in packload (which deliberately never resolves). It is
// also the regression guard for the token itself: a manifest whose token failed to
// substitute would bind a literal "{loophole_dir}/asound.conf", which podman would
// materialize as an empty DIRECTORY at the destination and quietly break the ALSA routing.
func TestShippedAudioPackResolvesItsModuleDirToken(t *testing.T) {
	p := shippedAudioPack(t)
	dir := packLoopholeDecls([]*packload.Pack{p})[0].Dir
	lp, err := loopholes.LoadPackLoophole(dir)
	if err != nil {
		t.Fatalf("the shipped pack's loophole must load through the PACK-SHIPPED loader "+
			"(which applies the subset): %v", err)
	}
	if len(lp.HostBindMount) != 3 {
		t.Fatalf("bind mounts = %d, want 3 (two sockets + the ALSA fragment)",
			len(lp.HostBindMount))
	}
	// The ALSA fragment is the one carrying the token; the two sockets carry
	// ${XDG_RUNTIME_DIR}, which internal/loopholes expands from the ENVIRONMENT rather
	// than from the module dir, so they are a different resolution with a different
	// failure mode.
	var bm loopholes.HostBindMount
	for _, cand := range lp.HostBindMount {
		if strings.HasSuffix(cand.Container, "50-yolo-audio-alsa.conf") {
			bm = cand
		}
	}
	if bm.Container == "" {
		t.Fatalf("the pack-shipped ALSA fragment is no longer bound: %+v", lp.HostBindMount)
	}
	if strings.Contains(bm.Host, loopholedecl.TokenLoopholeDir) {
		t.Errorf("the %s token did not resolve: %q — podman would materialize that literal "+
			"path as an empty directory at the destination", loopholedecl.TokenLoopholeDir,
			bm.Host)
	}
	if !filepath.IsAbs(bm.Host) || !strings.HasPrefix(bm.Host, filepath.Clean(dir)) {
		t.Errorf("resolved bind host %q must be an absolute path inside the module dir %q",
			bm.Host, dir)
	}
	if !bm.Readonly {
		t.Error("the bind must be read-only — the subset refuses a writable one, so a false " +
			"here means the record and the gate disagree")
	}
}

// selectEmbeddedAudio points HOME at a scratch config selecting the embedded `audio` pack,
// and clears the process-wide pack-module record so the LAZY resolver is what answers.
//
// Both halves are needed: without ResetPackModules a staged record from another test would
// satisfy the lookups below and the assertion would pass without the resolver working at all.
func selectEmbeddedAudio(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"),
		[]byte(`{"packs": ["audio"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
}

// THE LAZY RESOLVER MUST SEE AN EMBEDDED PACK'S LOOPHOLE — the regression test for a defect
// this pack made reachable, found by running the shipped binary rather than by reading.
//
// resolvePackLoopholeModules skipped every embedded entry under the comment "an embedded pack
// ships no loophole today". The `audio` pack is the first that does, and the resolver backs
// the three census surfaces that never stage: config validation (which runs BEFORE
// stageRunPacks on the launch path), `yolo loopholes list`/`status`, and `yolo check`.
// Measured symptoms before the fix, with `packs: ["audio"]` selected:
//
//	$ yolo loopholes list          # audio-alsa absent entirely
//	config.loopholes.audio-alsa: no loophole named 'audio-alsa' is installed on this machine
//
// That warning is the exact §5.2 prerequisite this resolver exists to satisfy, and it is the
// same sentence a user gets when a pack genuinely failed to stage — so the one case it fired
// on was the case where nothing was wrong.
func TestLazyResolverSeesAnEmbeddedPackLoophole(t *testing.T) {
	selectEmbeddedAudio(t)
	mods := resolvePackLoopholeModules()
	if len(mods) == 0 {
		t.Fatal("the lazy resolver returned no modules for a selected EMBEDDED pack that " +
			"ships a loophole — `yolo loopholes list` would omit it and config validation " +
			"would warn that it is not installed, at every launch")
	}
	var dirs []string
	for _, m := range mods {
		dirs = append(dirs, m.Dir)
		if !m.HostExecApproved {
			t.Error("an embedded pack's loophole must be host-exec approved: its content " +
				"shipped in the binary the user ran, and there is no lockfile entry that " +
				"could ever record an approval for it")
		}
	}
	found := false
	for _, d := range dirs {
		if filepath.Base(d) == "audio" {
			found = true
		}
	}
	if !found {
		t.Errorf("no module dir named audio among %v", dirs)
	}
}

// The user-visible half of the same fix: `loopholes.audio-alsa.enabled` validates with NO
// unknown-name warning while the pack is selected.
//
// Asserted through ValidateConfig — the surface `yolo check` and the launch preflight both
// call — rather than through the resolver, because the warning is what a user actually reads
// and it is what made the defect visible.
func TestPackLoopholeNameValidatesWithoutAnUnknownNameWarning(t *testing.T) {
	selectEmbeddedAudio(t)
	raw, err := json5.Decode([]byte(
		`{"packs": ["audio"], "loopholes": {"audio": {"enabled": false}}}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		t.Fatal("config fixture did not decode to an object")
	}
	errs, warns := config.ValidateConfig(cfg, t.TempDir(), loopholes.NewResolver())
	if len(errs) != 0 {
		t.Errorf("disabling a pack-shipped loophole must be valid, got errors: %v", errs)
	}
	for _, w := range warns {
		if strings.Contains(w, "no loophole named") {
			t.Errorf("a SELECTED pack's loophole must resolve, so this entry takes the "+
				"override path rather than the unknown-name fallback; got: %s", w)
		}
	}

	// And the resolver reports it as KNOWN, which is the fact the warning is derived from.
	known, _ := loopholes.NewResolver().Known()
	if _, ok := known["audio"]; !ok {
		t.Errorf("the resolver must know audio; knows: %v", knownNames(known))
	}
	// And it is known through the PACK, which is the whole point of this resolver: the
	// bundled copy that used to supply the name unconditionally is gone, so a jail whose
	// config names `audio` without selecting the pack gets the unknown-name path.
	if _, ok := known["audio-alsa"]; ok {
		t.Error("`audio-alsa` is still known — the two audio loopholes merged under the " +
			"plain name, so the old one resolving means something is shipping it twice")
	}
}

// knownNames is a sorted name list for a failure message.
func knownNames(known map[string]config.LoopholeInfo) []string {
	var out []string
	for n := range known {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// THE R6 ASSERTION, at the only place it can be made in-jail: the CONTAINER ARGV.
//
// A nested-jail launch cannot select this pack from inside this jail — `packs` is user-scope
// only and ~/.config/yolo-jail/config.jsonc is bind-mounted READ-ONLY into a jail — so the
// argv the launch would emit is the closest verifiable thing, and it is the thing that
// actually decides whether audio reaches the jail. RuntimeArgsFor is the exact function the
// assembler calls (assemble.go's loopholesRuntimeArgs).
//
// It also pins the two facts a bare "does it appear" test would miss: the bind is emitted
// with `:ro`, and the destination is the conf.d fragment rather than /etc/asound.conf — the
// difference between an additive pack and a jail that will not start.
//
// YOLO_VERSION IS CLEARED, and the reason is a real behaviour worth recording rather than a
// test convenience. Inside a jail, `RequirementsMet` short-circuits to `inJailActive`, which
// asks whether the CONTAINER path already exists — i.e. "did my host wire this for me?" —
// so this loophole reads as inactive in a jail that has no /etc/alsa/conf.d, and
// `yolo loopholes list` says exactly that ("host-side wiring not visible in this jail").
// That is correct for a report and wrong for building the HOST's argv, which is the role
// this test is exercising.
func TestShippedAudioPackEmitsAReadOnlyBindIntoTheContainerArgv(t *testing.T) {
	// The audio loophole is declared platforms: ["linux"] (ALSA is a Linux kernel
	// interface and the sockets are a Linux session layout), so on darwin it is
	// correctly inert and there is no host argv to assert.
	if runtime.GOOS != "linux" {
		t.Skipf("audio is declared platforms: [linux]; no host argv on %s", runtime.GOOS)
	}

	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")

	p := shippedAudioPack(t)
	dir := packLoopholeDecls([]*packload.Pack{p})[0].Dir

	// Through the GATED form, not the package-level one. A pack-shipped record hands its
	// crossings to the argv only when the caller recorded that the pack's host access is
	// approved — the origin gate is enforced INSIDE RuntimeArgsFor precisely because a
	// slice carries no gate and a filter the caller must remember is a filter the next
	// caller omits. audio is an EMBEDDED pack, so it is approved by origin (embedded and
	// local packs are yolo's own code and the user's own files respectively); this test
	// therefore has to say so, which is the same thing the run pipeline says at staging.
	// NewSet, not SetOf: the gate is a map keyed by module path, built from PackModules —
	// so SetOf (which carries no gate) would withhold every crossing and this test would
	// assert the withholding rather than the argv. Building it the way the run pipeline
	// does is also the only spelling that proves the pack's own module reaches the gate.
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		PackModules: []loopholes.PackModule{{Dir: dir, HostExecApproved: true}},
	})
	lp, ok := set.Lookup("audio")
	if !ok {
		t.Fatalf("the shipped loophole was not discovered from its module dir %s", dir)
	}
	// ENABLED BY HAND, because the manifest ships `default_enabled: false` since the
	// merge (R4: host access is never on by default, and this loophole is now nothing
	// but host access). The argv is what is under test, not the switch — and the switch
	// has its own test in loopholedecl. Setting it here rather than through a config
	// fixture keeps this test about RuntimeArgsFor.
	lp.Enabled = true
	if !lp.Active() {
		t.Fatalf("the loophole must be ACTIVE on a host: enabled=%v supportedHere=%v "+
			"requirementsMet=%v", lp.Enabled, lp.SupportedHere(), lp.RequirementsMet())
	}
	args := set.RuntimeArgsFor([]*loopholes.Loophole{lp}, "podman")
	joined := strings.Join(args, " ")

	// The bind: `-v <resolved module path>/asound.conf:/etc/alsa/conf.d/…:ro`. Built from
	// the record rather than matched loosely, so a changed destination fails here.
	//
	// The LAST bind, not the first: the merge put the two host sockets ahead of the ALSA
	// fragment, and those two are skipped on a host with no audio ("skipping bind mount,
	// host source missing"), so indexing [0] would make this test pass or fail on whether
	// the machine running it has PipeWire.
	alsa := lp.HostBindMount[len(lp.HostBindMount)-1]
	want := alsa.Host + ":" + alsa.Container + ":ro"
	if !strings.Contains(joined, want) {
		t.Errorf("the container argv must carry the read-only bind %q; got:\n%s", want, joined)
	}
	if strings.Contains(joined, ":/etc/asound.conf") {
		t.Error("the pack binds /etc/asound.conf. The destination is free now that the " +
			"bundled loophole is gone, so this is no longer fatal — but the conf.d " +
			"fragment is the spelling measured working with a real libasound client, and " +
			"anything else claiming /etc/asound.conf would make a jail refuse to start " +
			"(podman: duplicate mount destination). Re-measure before taking it.")
	}
	// NO --add-host and NO jail_env: this loophole intercepts nothing, and the two
	// environment variables travel as the pack's `env` contribution because `jail_env`
	// is refused for a pack-shipped loophole (which is what makes them UNCONDITIONAL —
	// OQ-LP5's named cost, paid here).
	for _, unwanted := range []string{"--add-host", "PULSE_SERVER"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("the pack's loophole must not emit %s (the env goes through the `env` "+
				"contribution kind); got:\n%s", unwanted, joined)
		}
	}
	// --device /dev/snd IS expected now, and it is the one declaration in this manifest
	// no verification loop in this repo can observe: RuntimeArgsFor skips device
	// passthrough whenever the LAUNCHER is itself in a jail ("devices cannot nest under
	// rootless podman"), and nested-jail verification is the mandated loop. So the argv
	// is asserted only when the passthrough is reachable at all, and the skip is asserted
	// otherwise — a bare "contains --device" would fail in this repo's own jail.
	if inJailLauncher() {
		if strings.Contains(joined, "--device") {
			t.Errorf("device passthrough must be skipped when the launcher is in a jail; "+
				"got:\n%s", joined)
		}
		return
	}
	if !strings.Contains(joined, "--device /dev/snd") {
		t.Errorf("the merged loophole passes /dev/snd through (ALSA-seq MIDI has no "+
			"userspace shim, so rtmidi and gomidi open /dev/snd/seq directly); got:\n%s",
			joined)
	}
}

// inJailLauncher reports whether THIS process is running inside a jail, which is the
// exact condition RuntimeArgsFor uses to skip device passthrough. Read the same way the
// production code reads it, so the test and the code cannot disagree about the answer.
func inJailLauncher() bool {
	// PRESENCE, not a non-empty value: loopholes.inJail uses os.LookupEnv, so an empty
	// YOLO_VERSION still counts as "in a jail". A test helper that read it the other way
	// would assert the opposite branch from the one the code takes.
	_, ok := os.LookupEnv("YOLO_VERSION")
	return ok
}
