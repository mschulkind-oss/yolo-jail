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
// This is the test that decides the "one name or two" question with the REAL reserved set
// rather than a literal — `loopholes.ReservedLoopholeNames()` composes paths.Builtin*,
// broker.BrokerLoopholeName and the bundled directory names off the embed.FS, so a future
// bundled loophole extends the reservation and this test starts covering it for free.
//
// It is the assertion that would have caught the naive answer. A pack shipping
// `loopholes/audio` while bundled_loopholes/audio exists is refused FATALLY here, which
// means every jail selecting the pack fails to start — the exact "leaves both and quietly
// fails the pre-flight" outcome the design forbids.
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

	// And the reservation it is avoiding is real, asserted positively so the test cannot
	// pass by the reserved set having quietly emptied.
	reserved := map[string]bool{}
	for _, r := range loopholes.ReservedLoopholeNames() {
		reserved[r.Name] = true
	}
	if !reserved["audio"] {
		t.Error(`"audio" is no longer a reserved loophole name. If the BUNDLED audio loophole ` +
			`was removed, this pack may take the plain name — but that is a deliberate ` +
			`decision to make here, in this test, not a silent consequence.`)
	}
	if reserved[decls[0].Name] {
		t.Errorf("the pack's loophole name %q is reserved", decls[0].Name)
	}
}

// The pack and the BUNDLED loophole coexist: both are present, and neither refuses the
// other. That is the regression-safety requirement stated as a test — audio is a shipped
// capability people use (the jail briefing advertises /voice, sox, ffmpeg), and the pack
// must be additive.
func TestShippedAudioPackCoexistsWithTheBundledLoophole(t *testing.T) {
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

	// Discovery with BOTH sources present yields both names, with neither shadowing the
	// other. Pack modules sit BETWEEN bundled and user in precedence, so a name collision
	// here would silently drop one — which is exactly what the distinct names prevent.
	found := map[string]string{}
	for _, lp := range loopholes.Discover(loopholes.DiscoverOptions{
		IncludeBundled:  true,
		IncludeDisabled: true,
		Root:            t.TempDir(), // an empty user dir, so only bundled + pack contribute
		RootSet:         true,
		PackModules:     mods,
	}) {
		found[lp.Name] = lp.Source
	}
	if found["audio"] != loopholes.SourceBundled {
		t.Errorf("the bundled `audio` loophole must still be discovered as bundled, got %q — "+
			"the pack must be ADDITIVE, not a replacement", found["audio"])
	}
	if found[mods[0].Dir] != "" { // sanity: keys are names, not dirs
		t.Error("discovery keyed a loophole by dir rather than name")
	}
	if got := found["audio-alsa"]; got != loopholes.SourcePack {
		t.Errorf("the pack's loophole must be discovered with source %q, got %q",
			loopholes.SourcePack, got)
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
	if len(reads) != 3 {
		t.Fatalf("want 3 read-disclosure lines (the bind + two env vars), got %d: %+v",
			len(reads), reads)
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
	if len(lp.HostBindMount) != 1 {
		t.Fatalf("bind mounts = %d, want 1", len(lp.HostBindMount))
	}
	bm := lp.HostBindMount[0]
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
		if filepath.Base(d) == "audio-alsa" {
			found = true
		}
	}
	if !found {
		t.Errorf("no module dir named audio-alsa among %v", dirs)
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
		`{"packs": ["audio"], "loopholes": {"audio-alsa": {"enabled": false}}}`))
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
	if _, ok := known["audio-alsa"]; !ok {
		t.Errorf("the resolver must know audio-alsa; knows: %v", knownNames(known))
	}
	// The bundled loophole is still known too — the pack is additive.
	if _, ok := known["audio"]; !ok {
		t.Error("the BUNDLED audio loophole must still be known")
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
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")

	p := shippedAudioPack(t)
	dir := packLoopholeDecls([]*packload.Pack{p})[0].Dir
	lp, err := loopholes.LoadPackLoophole(dir)
	if err != nil {
		t.Fatalf("loading the shipped loophole: %v", err)
	}
	lp.Source = loopholes.SourcePack
	if !lp.Active() {
		t.Fatalf("the loophole must be ACTIVE on a host: enabled=%v supportedHere=%v "+
			"requirementsMet=%v — it declares no `requires`, so the only way this fails is a "+
			"platform mismatch", lp.Enabled, lp.SupportedHere(), lp.RequirementsMet())
	}

	args := loopholes.RuntimeArgsFor([]*loopholes.Loophole{lp}, "podman")
	joined := strings.Join(args, " ")

	// The bind: `-v <resolved module path>/asound.conf:/etc/alsa/conf.d/…:ro`. Built from
	// the record rather than matched loosely, so a changed destination fails here.
	want := lp.HostBindMount[0].Host + ":" + lp.HostBindMount[0].Container + ":ro"
	if !strings.Contains(joined, want) {
		t.Errorf("the container argv must carry the read-only bind %q; got:\n%s", want, joined)
	}
	if strings.Contains(joined, ":/etc/asound.conf") {
		t.Error("the pack must NOT bind /etc/asound.conf — the bundled audio loophole claims " +
			"that destination, and podman refuses two binds on one destination whose " +
			"sources differ, so a jail with both would refuse to start")
	}
	// No device, no --add-host, no jail_env: this loophole crosses with one bind and
	// nothing else, which is what makes it additive.
	for _, unwanted := range []string{"--device", "--add-host", "PULSE_SERVER"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("the pack's loophole must not emit %s (the bundled loophole owns the "+
				"device, and the env goes through the `env` contribution kind); got:\n%s",
				unwanted, joined)
		}
	}
}
