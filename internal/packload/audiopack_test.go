package packload_test

// audiopack_test.go pins the OFFICIAL `audio` pack — the dogfood for the 15th
// contribution kind (docs/design/loophole-packaging.md §7, OQ-LP11 "RULED YES, and it
// ships with this change").
//
// # Why these assertions are over the SHIPPED pack and not a fixture
//
// loopholesource_test.go already pins the claim enumeration over synthetic manifests, and
// that is the right place for the RULES. What it cannot pin is that a real pack, embedded
// in the binary, actually goes through them — which is the whole content of §7's promise
// that the example "now exercises the approval path too". Every fact below was measured
// by running the real code against the real pack, and three of them were WRONG on the
// first attempt (see the pack's README for the two the subset forced and the one `pack
// lint` defect the dogfood found), which is the argument for keeping them.
//
// # The claim COUNT is asserted exactly, and that is deliberate
//
// A claim-free crossing is the defect the enumeration exists to prevent (`packMayAccessHost`
// reads an empty claim set as consent), so a test that merely asserted "at least one
// claim" would pass on the day someone drops the bind and keeps the pack. Pinning the
// exact set means adding a crossing to this pack REQUIRES editing this test, which is
// where the reviewer's attention belongs.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// audioPackName is the pack's slug, which is also its directory name under packs/.
const audioPackName = "audio"

// audioLoopholeName is the loophole the pack ships.
//
// It was `audio-alsa` until 2026-08-18 — `audio` was a RESERVED name (the bundled
// loophole owned it) and the launch pre-flight refuses a pack claiming a reserved name,
// fatally. Deleting the bundled copy freed the name, so the pack took it back:
// `loopholes.audio.enabled` is the config key users have always written, and a pack
// whose loophole answered to a different name would have stranded it.
// TestAudioPackTakesTheFormerlyReservedName is the assertion; this constant exists so
// the rename cannot be done halfway.
const audioLoopholeName = "audio"

// embeddedAudioPack materializes the EMBEDDED pack set and returns the audio pack, so
// every assertion below reads what the binary actually carries rather than the worktree.
// That distinction is the one the goSrc/embed traps are about.
func embeddedAudioPack(t *testing.T) *packload.Pack {
	t.Helper()
	loaded, problems := packload.MaterializeEmbedded(packs.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	for _, p := range loaded {
		if p.Name == audioPackName {
			return p
		}
	}
	t.Fatalf("the %q pack is not embedded — extend the //go:embed directive in packs/embed.go "+
		"(and check the goSrc fileset in flake.nix)", audioPackName)
	return nil
}

// The pack is EMBEDDED and carries yolo's own authority, which is what makes it an
// "official" pack in the one sense that matters (packs/embed.go's package doc).
//
// MayAccessHost is the load-bearing half for this pack specifically: it is the gate
// packLoopholeModules reuses to decide whether a loophole's host access is approved, so an
// embedded pack shipping a loophole needs no lockfile entry and no prompt — the user's
// selection IS the approval, because the content shipped with the binary they ran.
func TestAudioPackIsEmbeddedWithHostAuthority(t *testing.T) {
	p := embeddedAudioPack(t)
	if !p.MayAccessHost {
		t.Error("an embedded pack must carry host authority (packload.MaterializeEmbedded " +
			"passes mayAccessHost=true); without it the loophole's module would be " +
			"origin-gated against a lockfile that can never have an entry for a pack " +
			"nobody installs")
	}
	if _, err := os.Stat(filepath.Join(p.Root, packdecl.ManifestName)); err != nil {
		t.Errorf("pack.json not materialized: %v", err)
	}
}

// THE R3 ASSERTION: the claim enumeration over the shipped pack, exact.
//
// One `loophole` contribution + one `env` contribution → six claims, and each one is
// pinned by TARGET (the footprint key) rather than by prose, because the target is what
// the approval lockfile compares and what a second declaration would collide on.
//
// It was THREE until the two audio loopholes merged on 2026-08-18. The pack now carries
// the whole loophole — two host sockets, the ALSA fragment and /dev/snd — which is three
// new crossings, and every one of them has to be separately approvable or the
// enumeration is not total.
//
// ALL THREE BINDS LAND IN THE MOUNT CLASS, AND TWO OF THEM ARE SOCKETS. That is the one
// thing here worth reading twice. `bindIsIPC` splits on the manifest's own `readonly`
// bit plus a socket-shaped basename, and the pack-shipped subset refuses
// `readonly: false` — so the sockets, which are named `native` and `pipewire-0`, cannot
// reach the IPC class at all. Nothing is UNDERSTATED (the mount class's text carries the
// socket caveat verbatim), but the discriminator is coarser than the design's "a socket
// bind is its own claim class" wanted. The precise fix is a declared socket bit in the
// schema, which bindIsIPC's own comment names.
func TestAudioPackClaimTargetsAreExact(t *testing.T) {
	fp := packload.FootprintOf(embeddedAudioPack(t))
	got := map[string]packload.Claim{}
	var targets []string
	for _, c := range fp.Claims {
		key := string(c.Kind) + " " + c.Target
		got[key] = c
		targets = append(targets, key)
	}
	sort.Strings(targets)

	want := []string{
		"env PIPEWIRE_REMOTE",
		"env PULSE_SERVER",
		"loophole " + audioLoopholeName + ":device:/dev/snd",
		"loophole " + audioLoopholeName + ":mount:/etc/alsa/conf.d/50-yolo-audio-alsa.conf",
		"loophole " + audioLoopholeName + ":mount:/run/pipewire/pipewire-0",
		"loophole " + audioLoopholeName + ":mount:/run/pulse/native",
	}
	if strings.Join(targets, "\n") != strings.Join(want, "\n") {
		t.Fatalf("claim targets:\n got: %v\nwant: %v", targets, want)
	}

	// Every loophole claim is REVIEW-WORTHY (the enumeration only emits one for
	// something that crosses), and NONE of them runs host code — this pack declares no
	// `host_daemon` and no `doctor_cmd`. Both halves matter for WHEN the launch
	// discloses them: disclosureClassOfClaim degrades a non-exec loophole claim to the
	// read block, so pinning RunsHostCode=false is pinning that the pack does not print
	// in the pre-spawn "runs pack code on your machine" block and cry wolf.
	for _, key := range want {
		if !strings.HasPrefix(key, "loophole ") {
			continue
		}
		c := got[key]
		if !c.ReviewWorthy {
			t.Errorf("%s is not review-worthy — every loophole claim crosses the host "+
				"boundary", key)
		}
		if c.RunsHostCode {
			t.Errorf("%s says it runs host code; this pack declares no host_daemon and no "+
				"doctor_cmd, and a true RunsHostCode would put it in the pre-spawn exec "+
				"block", key)
		}
	}

	// The socket caveat, asserted on the two claims that need it: a reader approving
	// `/run/pulse/native` has to be told that `:ro` buys nothing for a socket, because
	// the class's own name no longer tells them.
	for _, key := range []string{
		"loophole " + audioLoopholeName + ":mount:/run/pulse/native",
		"loophole " + audioLoopholeName + ":mount:/run/pipewire/pipewire-0",
	} {
		if !strings.Contains(got[key].Detail, "AF_UNIX SOCKET here is read-write host IPC") {
			t.Errorf("%s does not disclose that a bound socket is read-write regardless of "+
				"`:ro`; detail = %q", key, got[key].Detail)
		}
	}
}

// The approval strings are what `pack install` records and the launch gate compares, so
// they are pinned separately from the footprint targets: the two renderings are
// deliberately not the same string (the footprint's Detail may abbreviate; an approval
// string may not), and a change that collapsed them would be invisible to the test above.
//
// RAW, with `{loophole_dir}` UNEXPANDED. That is the G2a rule and it is load-bearing: an
// expanded claim carries a staging path that differs per launch and per machine, so it
// could never match a recorded approval, would re-prompt forever, and — since promptYesNo
// fails closed on a non-TTY — would refuse the loophole permanently.
func TestAudioPackApprovalClaimsAreRawAndSpecific(t *testing.T) {
	claims := embeddedAudioPack(t).HostAccessClaims()
	if len(claims) != 4 {
		t.Fatalf("host-access claims = %v; want the three loophole binds plus the device "+
			"(env is static and ungated, so it is not an approval claim)", claims)
	}
	joined := strings.Join(claims, "\n")
	for _, want := range []string{
		loopholedecl.TokenLoopholeDir + "/asound.conf",
		"/etc/alsa/conf.d/50-yolo-audio-alsa.conf",
		// RAW, with the variable UNEXPANDED — this is the G2a rule doing the work that
		// OQ-LP14's ruling leans on: the approval records the DECLARATION, so it stays
		// machine-independent even though the resolved path is /run/user/<uid>/....
		"${XDG_RUNTIME_DIR}/pulse/native",
		"${XDG_RUNTIME_DIR}/pipewire-0",
		"/dev/snd",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("approval claims must mention %q:\n%s", want, joined)
		}
	}
	// The resolved spelling must NOT appear: an expanded claim carries a per-machine
	// path, could never match a recorded approval, and — since promptYesNo fails closed
	// on a non-TTY — would refuse the loophole permanently.
	if strings.Contains(joined, "/run/user/") {
		t.Errorf("an approval claim expanded ${XDG_RUNTIME_DIR}:\n%s", joined)
	}
	if strings.Contains(joined, "/tmp/") || strings.Contains(joined, packsRootHint) {
		t.Errorf("approval claim leaked a staging path — it must stay machine-independent "+
			"(G2a), or it can never match a recorded approval:\n%s", joined)
	}
}

// packsRootHint is a fragment of the materialization temp-dir prefix, used only to catch a
// leaked absolute path in a claim string.
const packsRootHint = "yolo-embedded-"

// The pack ships NO absolute or $VAR bind host, NO writable bind, NO jail_env and NO
// self-publishing daemon — i.e. it is inside the pack-shipped subset
// (internal/loopholedecl/packshipped.go), which is what makes it LOADABLE at all.
//
// This is the assertion that would have caught the naive migration. A straight copy of
// bundled_loopholes/audio/manifest.jsonc fails four of these rules at once, and
// TestBundledAudioIsOutsideThePackShippedSubset (loopholedecl) pins that it still does —
// the two tests together are the evidence for the README's finding that the subset is too
// tight for the real loophole, rather than a claim about it.
func TestAudioPackLoopholeIsInsideThePackShippedSubset(t *testing.T) {
	p := embeddedAudioPack(t)
	mods, refusals, warnings := p.LoopholeModules()
	if len(refusals) > 0 || len(warnings) > 0 {
		t.Fatalf("the shipped pack must resolve cleanly: refusals=%v warnings=%v",
			refusals, warnings)
	}
	if len(mods) != 1 {
		t.Fatalf("modules = %d; want exactly one loophole module", len(mods))
	}
	mod := mods[0]
	if mod.Name != audioLoopholeName {
		t.Errorf("module name = %q, want %q", mod.Name, audioLoopholeName)
	}
	if mod.Decl == nil {
		t.Fatalf("manifest did not decode: %s", mod.Problem)
	}
	// FALSE, and the value FLIPPED when the two audio loopholes merged
	// (docs/design/loophole-activation.md R4, and the entry in docs/RELEASE-NOTES.md).
	//
	// The `audio-alsa` sibling shipped `default_enabled: true` on an argument that was
	// sound for what it was: R4's subject is HOST ACCESS, and an ALSA config fragment
	// the pack itself ships reaches none. That argument does not survive the merge —
	// this loophole now binds two host sockets and passes /dev/snd through, which is
	// host access and nothing else. Selecting the pack is no longer sufficient consent
	// for what the pack does.
	//
	// Asserted through the EMBEDDED pack, so the value the binary carries is what is
	// pinned — the same reason every other assertion in this file reads the embed.
	if mod.Decl.DefaultEnabled {
		t.Errorf("default_enabled = true; %q binds the host's audio sockets and passes "+
			"/dev/snd through, and R4 is that host access is never on by default. The "+
			"switch is `\"loopholes\": {\"audio\": {\"enabled\": true}}`.", audioLoopholeName)
	}
	if probs := mod.Decl.PackShippedProblems(loopholedecl.ManifestPath(mod.Dir)); len(probs) > 0 {
		t.Errorf("the SHIPPED pack's loophole is outside the pack-shipped subset, so a "+
			"launch would refuse it:\n%s", strings.Join(probs, "\n"))
	}
	// The STRICT authoring read too, which is what `yolo pack lint` runs: a manifest key
	// this build does not know would pass the tolerant read above and still be a typo.
	if probs := p.LoopholeDeclProblems(); len(probs) > 0 {
		t.Errorf("strict decode of the shipped manifest reports problems:\n%s",
			strings.Join(probs, "\n"))
	}
}

// THE R5 ASSERTION, INVERTED BY THE CONVERSION: the pack DOES claim the name `audio`,
// and that is now correct rather than fatal.
//
// This test used to assert the opposite. The name-exclusivity pre-flight
// (run.PackLoopholeNameConflicts) refuses a pack claiming a reserved name FATALLY, and
// the bundled loophole directory names were part of the reserved set — read off the same
// embed.FS the loader materializes. So while `bundled_loopholes/audio/` existed, a pack
// shipping `loopholes/audio` was a launch that did not start, and the pack shipped
// `audio-alsa` for exactly that reason.
//
// Deleting the bundled copy retired the reservation in the same commit, because the
// reservation was DERIVED from the directory rather than listed beside it. Taking the
// name back is not cosmetic: `loopholes.audio.enabled` is the config key the release
// notes tell users to write, and a loophole answering to `audio-alsa` would have made
// that key name nothing.
//
// Asserted HERE, in packload, as a property of the pack's CONTENT; the run-package half
// (over the real composed reserved set) is TestShippedAudioPackDoesNotClaimAReservedName.
func TestAudioPackTakesTheFormerlyReservedName(t *testing.T) {
	var got []string
	for _, from := range embeddedAudioPack(t).Decl.LoopholeSources() {
		got = append(got, filepath.Base(from))
	}
	if len(got) != 1 || got[0] != audioLoopholeName {
		t.Errorf("the pack declares loophole module basenames %v, want [%q] — the config "+
			"key users write is `loopholes.audio.enabled`, so the module directory has to "+
			"be `audio`", got, audioLoopholeName)
	}
}

// THE DESTINATION ASSERTION, RETARGETED: nothing else claims the pack's bind
// destinations, and the /etc/asound.conf spelling stays deliberately unused.
//
// MEASURED, and it is why the pack delivers a conf.d fragment rather than
// /etc/asound.conf:
//
//	$ podman run -v A:/x.txt:ro -v B:/x.txt:ro alpine cat /x.txt
//	Error: /x.txt: duplicate mount destination
//
// Two binds on one destination with DIFFERENT sources is refused by podman, so a jail
// with two things claiming /etc/asound.conf REFUSES TO START — worse than either of them
// not existing. That is why the fragment path was chosen, and it is why the choice
// SURVIVES the bundled loophole's deletion even though the collision it avoided is gone:
// alsa-lib loads /etc/alsa/conf.d before /etc/asound.conf (its own alsa.conf include
// list), the fragment is the spelling measured working in this repo's jail with sox, and
// moving back to the freed path would be an unmeasured edit made for tidiness.
//
// The bundled comparison this test used to make went with the bundled manifest. What
// replaced it is the property that actually protects a user: the destinations are pinned,
// so a change to them has to be made here.
func TestAudioPackDoesNotCollideWithTheBundledAudioDestinations(t *testing.T) {
	mods, _, _ := embeddedAudioPack(t).LoopholeModules()
	if len(mods) != 1 || mods[0].Decl == nil {
		t.Fatalf("expected one decodable module, got %+v", mods)
	}
	var dests []string
	for _, bm := range mods[0].Decl.HostBindMounts {
		dests = append(dests, bm.Container)
		if bm.Container == "/etc/asound.conf" {
			t.Error("the pack binds /etc/asound.conf. The destination is FREE now that the " +
				"bundled loophole is gone, so this is no longer fatal — but the conf.d " +
				"fragment is the spelling measured working with a real libasound client, " +
				"and podman refuses two binds on one destination whose sources differ, so " +
				"anything else that writes /etc/asound.conf would make a jail refuse to " +
				"start. Re-measure before taking it.")
		}
	}
	sort.Strings(dests)
	want := []string{
		"/etc/alsa/conf.d/50-yolo-audio-alsa.conf",
		"/run/pipewire/pipewire-0",
		"/run/pulse/native",
	}
	if strings.Join(dests, "\n") != strings.Join(want, "\n") {
		t.Errorf("bind destinations:\n got: %v\nwant: %v", dests, want)
	}
}

// THE R3 ASSERTION over an AUDIO-SHAPED manifest: §7's four review-worthy claims, with
// host IPC marked distinguishably from a host read.
//
// This manifest is the one a FETCHED copy of the bundled audio loophole would carry, and
// it is UNSHIPPABLE as a pack (the subset refuses its $VAR bind hosts and its writable
// binds — see TestAudioShapedManifestIsRefusedByTheSubset). The claim producer runs before
// that gate, on purpose: a footprint reports what a pack WANTS, so the enumeration has to
// be total for manifests the launch would go on to refuse. Testing it here is what makes
// §7's "would prompt" verifiable without shipping a manifest that cannot load.
//
// FOUR claims: three binds + one device. Two of the binds are the IPC class (the manifest
// declares them bidirectional) and one is the mount class, which is exactly the
// "distinguishably marked" requirement — the classes have different TARGETS (`:ipc:` vs
// `:mount:`), so they are distinguishable by key and not merely by prose.
func TestAudioShapedManifestEnumeratesEveryCrossingClass(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"audio-shaped": audioShapedManifest})
	claims := loadPack(t, root).LoopholeHostAccessClaims()
	if len(claims) != 4 {
		t.Fatalf("audio-shaped manifest emits %d claims, want 4 (three binds + /dev/snd):\n%s",
			len(claims), strings.Join(claims, "\n"))
	}
	// The two sockets are host IPC, and the claim SAYS so — a claim calling a bound socket
	// "read-only" would be false (the kernel exempts non-REG/DIR/LNK inodes from the
	// read-only check, so a `:ro` socket is fully connectable and bidirectional).
	//
	// KEYED ON THE VERB "CONNECTS", not on the phrase "read-write host IPC", and that is a
	// finding rather than a style choice: the MOUNT class's text ends with the caveat "an
	// AF_UNIX SOCKET here is read-write host IPC regardless of `:ro`", so that phrase
	// appears in BOTH classes and a substring test on it cannot tell them apart. (Measured:
	// this test failed on that assertion first.) What actually distinguishes them is the
	// verb — CONNECTS vs MOUNTS — and the target discriminator, `:ipc:` vs `:mount:`.
	for _, sock := range []string{"pulse/native", "pipewire-0"} {
		if !hasClaimContaining(claims, sock, "CONNECTS the jail to the host socket",
			"read-write host IPC") {
			t.Errorf("no read-write-host-IPC claim naming %q:\n%s", sock,
				strings.Join(claims, "\n"))
		}
	}
	// The regular file is the MOUNT class, and NOT the IPC class: the split is what makes
	// "host IPC marked distinguishably from a host read" mean something.
	if !hasClaimContaining(claims, "asound.conf", "MOUNTS") {
		t.Errorf("no mount claim for the shipped asound.conf:\n%s", strings.Join(claims, "\n"))
	}
	if hasClaimContaining(claims, "asound.conf", "CONNECTS the jail to the host socket") {
		t.Error("a :ro bind of a REGULAR FILE must not be claimed with the IPC class's " +
			"CONNECTS verb — that would collapse the two classes the design separates")
	}
	// The device, which is NOT weaker than a read-write bind: audio's own manifest
	// describes --device as passing a node "so the cgroup device-allow rules permit
	// reads/writes".
	if !hasClaimContaining(claims, "/dev/snd", "PASSES THROUGH") {
		t.Errorf("no device-passthrough claim for /dev/snd:\n%s", strings.Join(claims, "\n"))
	}
	// NO claim-free crossing: every declaration above produced a claim, so the count check
	// at the top is an equality rather than a floor.
	for _, c := range claims {
		if strings.TrimSpace(c) == "" {
			t.Error("an empty claim string is a claim-free crossing")
		}
	}
}

// What the pack-shipped subset STILL refuses about an audio-shaped manifest, and what
// it stopped refusing — the second half is the point.
//
// This test used to be the evidence for the README's finding that "the socket half of
// audio is unexpressible for a pack": it pinned SIX refusals, two of them for the
// `${XDG_RUNTIME_DIR}` bind hosts. OQ-LP14 withdrew that rule on 2026-08-17 (it
// admitted `~/.ssh` and refused a pulse socket), so the finding is retired and the
// manifest below draws FOUR.
//
// Each of the four has a fix the shipped `audio` pack actually takes, which is why the
// conversion needed no new vocabulary:
//
//	readonly:false ×2   -> declare `readonly: true`. Measured: a :ro bind of an
//	                       AF_UNIX socket is fully connectable and BIDIRECTIONAL,
//	                       so the refusal costs a socket nothing.
//	jail_env            -> the pack's `env` contribution kind, accepting that it
//	                       becomes unconditional (OQ-LP5).
//	requires.file_exists-> `platforms: ["linux"]`, which answers the question the
//	                       probe was really asking and is not path-scoped.
//
// The count is pinned so a rule quietly narrowing or widening shows up here, in the
// test whose whole subject is how much the subset refuses.
func TestAudioShapedManifestIsRefusedByTheSubset(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"audio-shaped": audioShapedManifest})
	mods, _, _ := loadPack(t, root).LoopholeModules()
	if len(mods) != 1 || mods[0].Decl == nil {
		t.Fatalf("expected one decodable module, got %+v", mods)
	}
	probs := mods[0].Decl.PackShippedProblems(loopholedecl.ManifestPath(mods[0].Dir))
	joined := strings.Join(probs, "\n")
	for _, want := range []string{
		"may not ask for a WRITABLE",  // both readonly:false binds
		"'jail_env' is not available", // PULSE_SERVER / PIPEWIRE_REMOTE
		"'requires.file_exists'",      // the $VAR probe, which stays scoped
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the subset must refuse %q for an audio-shaped pack manifest; got:\n%s",
				want, joined)
		}
	}
	// AND THE WITHDRAWAL, asserted here rather than only in loopholedecl: this is the
	// manifest the rule was measured against, so it is where its absence belongs.
	// PER PROBLEM, not over the joined string: `requires.file_exists` is still
	// path-scoped and its refusal carries the same "expands an environment variable"
	// clause, so a substring test on the join cannot tell the two fields apart.
	for _, prob := range probs {
		if strings.Contains(prob, "host_bind_mounts") &&
			strings.Contains(prob, "expands an environment variable") {
			t.Errorf("a ${XDG_RUNTIME_DIR} BIND HOST is refused again — OQ-LP14 withdrew "+
				"that rule, and restoring it makes the shipped `audio` pack unloadable:\n%s",
				prob)
		}
	}
	if len(probs) != 4 {
		t.Errorf("subset refusals = %d, want 4 (2 writable binds + jail_env + $VAR in "+
			"requires.file_exists):\n%s", len(probs), joined)
	}
}

// audioShapedManifest is bundled_loopholes/audio/manifest.jsonc reduced to its
// host-crossing declarations, with the name changed so it is not the reserved one.
//
// A LITERAL rather than a read of the bundled file, and the reason is that the two tests
// above assert opposite things about it (four claims; five refusals) and both must stay
// true of the SAME bytes. Reading the bundled manifest would make this test drift with a
// file it does not own — and the bundled file legitimately changes for reasons that have
// nothing to do with the pack-shipped subset.
const audioShapedManifest = `{
  "name": "audio-shaped",
  "description": "the audio loophole's shape, for claim-enumeration tests",
  "transport": "none",
  "lifecycle": "external",
  "requires": {"file_exists": "${XDG_RUNTIME_DIR}/pulse/native"},
  "host_bind_mounts": [
    {"host": "${XDG_RUNTIME_DIR}/pulse/native", "container": "/run/pulse/native", "readonly": false},
    {"host": "${XDG_RUNTIME_DIR}/pipewire-0", "container": "/run/pipewire/pipewire-0", "readonly": false},
    {"host": "{loophole_dir}/asound.conf", "container": "/etc/asound.conf", "readonly": true}
  ],
  "host_devices": ["/dev/snd"],
  "jail_env": {
    "PULSE_SERVER": "unix:/run/pulse/native",
    "PIPEWIRE_REMOTE": "/run/pipewire/pipewire-0"
  }
}`
