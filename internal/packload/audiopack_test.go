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

// audioLoopholeName is the loophole the pack ships. NOT "audio": that name is RESERVED
// (the bundled loophole owns it) and the launch pre-flight refuses a pack claiming a
// reserved name. TestAudioPackAvoidsTheReservedBundledName is the assertion; this
// constant exists so the rename cannot be done halfway.
const audioLoopholeName = "audio-alsa"

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
// One `loophole` contribution + one `env` contribution → three claims, and each one is
// pinned by TARGET (the footprint key) rather than by prose, because the target is what
// the approval lockfile compares and what a second declaration would collide on.
//
// The bind lands in the MOUNT class, not the IPC class, and that is correct rather than a
// weakening: `bindIsIPC` splits on the manifest's own `readonly` bit plus a socket-shaped
// basename, and asound.conf is a REGULAR FILE bound `:ro`. The class's text still carries
// the socket caveat verbatim, so nothing is understated. See
// TestAudioShapedManifestEnumeratesEveryCrossingClass for the four-claim audio-SHAPED
// case §7 describes, which is a different (and unshippable) manifest.
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
		"loophole " + audioLoopholeName + ":mount:/etc/alsa/conf.d/50-yolo-audio-alsa.conf",
	}
	if strings.Join(targets, "\n") != strings.Join(want, "\n") {
		t.Fatalf("claim targets:\n got: %v\nwant: %v", targets, want)
	}

	// The loophole claim is REVIEW-WORTHY (every loophole claim is: the enumeration only
	// emits one for something that crosses), and it does NOT run host code — this pack
	// declares no `host_daemon` and no `doctor_cmd`. Both halves matter for WHEN the
	// launch discloses it: disclosureClassOfClaim degrades a non-exec loophole claim to
	// the read block, so pinning RunsHostCode=false here is pinning that the pack does
	// not print in the pre-spawn "runs pack code on your machine" block.
	lc := got["loophole "+audioLoopholeName+":mount:/etc/alsa/conf.d/50-yolo-audio-alsa.conf"]
	if !lc.ReviewWorthy {
		t.Error("a loophole claim must be review-worthy — it crosses the host boundary")
	}
	if lc.RunsHostCode {
		t.Error("this pack declares no host_daemon and no doctor_cmd, so nothing it ships " +
			"runs host code; a true RunsHostCode would put it in the pre-spawn exec block " +
			"and cry wolf")
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
	if len(claims) != 1 {
		t.Fatalf("host-access claims = %v; want exactly the one loophole bind (env is "+
			"static and ungated, so it is not an approval claim)", claims)
	}
	claim := claims[0]
	for _, want := range []string{
		"loophole " + audioLoopholeName,
		loopholedecl.TokenLoopholeDir + "/asound.conf",
		"/etc/alsa/conf.d/50-yolo-audio-alsa.conf",
	} {
		if !strings.Contains(claim, want) {
			t.Errorf("approval claim %q must contain %q", claim, want)
		}
	}
	if strings.Contains(claim, "/tmp/") || strings.Contains(claim, packsRootHint) {
		t.Errorf("approval claim %q leaked a staging path — it must stay machine-independent "+
			"(G2a), or it can never match a recorded approval", claim)
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
	// The fourth shipped manifest's enablement default, after the OQ-A9 rename
	// (docs/design/loophole-activation.md). TRUE, unlike its bundled sibling `audio`
	// which R4 flips to false in the same commit, and the asymmetry is the ruling
	// rather than an oversight: R4's subject is HOST ACCESS, and this loophole reaches
	// none — its one crossing is a :ro bind of a file the pack itself ships. The
	// deliberate act R1 wants has also already happened by the time this manifest is
	// read, because a pack-shipped loophole is discovered only when its pack is
	// SELECTED, and `packs: ["audio"]` is user-scope and hand-written (OQ-A7).
	//
	// Asserted through the EMBEDDED pack, so the value the binary carries is what is
	// pinned — the same reason every other assertion in this file reads the embed.
	if !mod.Decl.DefaultEnabled {
		t.Errorf("default_enabled = false; selecting the `audio` pack is already the "+
			"deliberate act, so the pack's own loophole must not need a second one. "+
			"If this was flipped on purpose, R4 is about host access and %q reaches none.",
			audioLoopholeName)
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

// THE R5 ASSERTION, half one: the pack does NOT claim the reserved name `audio`.
//
// The name-exclusivity pre-flight (run.PackLoopholeNameConflicts) refuses a pack claiming
// any reserved name, FATALLY — and the bundled loophole directory names are part of the
// reserved set, read off the same embed.FS the loader materializes. So a pack shipping
// `loopholes/audio` while bundled_loopholes/audio exists is a launch that does not start.
//
// Asserted HERE, in packload, rather than only in the run package: this is a property of
// the pack's CONTENT, and it must fail in the package that owns the content even if the
// pre-flight is ever restructured. The run-package half (over the real reserved set) is
// TestShippedAudioPackDoesNotClaimAReservedName.
func TestAudioPackAvoidsTheReservedBundledName(t *testing.T) {
	for _, from := range embeddedAudioPack(t).Decl.LoopholeSources() {
		if filepath.Base(from) == "audio" {
			t.Errorf("the pack declares `from: %q`, whose basename is the RESERVED loophole "+
				"name \"audio\" (the bundled loophole owns it). The launch pre-flight refuses "+
				"this fatally, so every jail selecting this pack would fail to start. Rename "+
				"the module directory.", from)
		}
	}
}

// THE R5 ASSERTION, half two: the pack's bind DESTINATION does not collide with the
// bundled audio loophole's.
//
// MEASURED, and it is why the pack delivers a conf.d fragment instead of /etc/asound.conf:
//
//	$ podman run -v A:/x.txt:ro -v B:/x.txt:ro alpine cat /x.txt
//	Error: /x.txt: duplicate mount destination
//
// Two binds on one destination with DIFFERENT sources is refused by podman, so a jail with
// both the bundled loophole and this pack would REFUSE TO START — a regression far worse
// than the pack not existing. alsa-lib loads /etc/alsa/conf.d before /etc/asound.conf (its
// own alsa.conf include list), so the fragment routes identically at a destination nothing
// else claims. Measured with sox in this repo's jail image: neither file → "cannot find
// card '0'"; fragment only → reaches the pipewire shim; BOTH together → identical to
// fragment only, with no duplicate-definition error.
func TestAudioPackDoesNotCollideWithTheBundledAudioDestinations(t *testing.T) {
	bundled := bundledAudioManifest(t)
	bundledDests := map[string]bool{}
	for _, bm := range bundled.HostBindMounts {
		bundledDests[bm.Container] = true
	}
	if !bundledDests["/etc/asound.conf"] {
		t.Fatalf("the bundled audio loophole no longer binds /etc/asound.conf (destinations: "+
			"%v) — this test's premise moved, so re-derive the collision analysis rather "+
			"than deleting the test", bundledDests)
	}

	mods, _, _ := embeddedAudioPack(t).LoopholeModules()
	for _, mod := range mods {
		if mod.Decl == nil {
			continue
		}
		for _, bm := range mod.Decl.HostBindMounts {
			if bundledDests[bm.Container] {
				t.Errorf("the pack binds %q, which the BUNDLED audio loophole also binds. "+
					"podman refuses two binds on one destination whose sources differ "+
					"(\"duplicate mount destination\", measured), so a jail with both would "+
					"refuse to start", bm.Container)
			}
		}
		// The same rule for devices: the bundled loophole passes /dev/snd through, and a
		// duplicate --device is only observable OFF a jail host (RuntimeArgsFor skips
		// device passthrough whenever the launcher is itself in a jail), which makes it
		// exactly the kind of regression the mandated verification loop cannot catch.
		for _, dev := range mod.Decl.HostDevices {
			for _, bundledDev := range bundled.HostDevices {
				if dev == bundledDev {
					t.Errorf("the pack passes through %q, which the bundled audio loophole "+
						"already passes through. A duplicate --device is unobservable in a "+
						"nested jail, so this would only break on a real host", dev)
				}
			}
		}
	}
}

// bundledAudioManifest decodes bundled_loopholes/audio/manifest.jsonc off the repo tree.
//
// Read from the TREE rather than the embed.FS because this package cannot import
// internal/loopholes' bundled embed (packload is below it), and TestEmbedMatchesTree
// already pins the two against each other — so reading one reads both.
func bundledAudioManifest(t *testing.T) *loopholedecl.Manifest {
	t.Helper()
	dir := filepath.Join(repoRootForAudioTest(t), "bundled_loopholes", "audio")
	m, _, err := loopholedecl.LoadDirTolerant(dir)
	if err != nil {
		t.Fatalf("decoding the bundled audio manifest at %s: %v", dir, err)
	}
	return m
}

// repoRootForAudioTest walks up to the dir holding go.mod. A separate helper from
// embeddrift_test.go's findRepoRoot only because that one lives in package packload and
// this file is the external test package.
func repoRootForAudioTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
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

// The audio-shaped manifest is REFUSED by the pack-shipped subset, and the refusal names
// every violation at once — which is the evidence for the README's finding.
//
// This is the test that makes "the subset is too tight for a real loophole" a MEASUREMENT
// rather than an opinion: the manifest below is bundled audio with only the name changed,
// and it draws refusals for both `${XDG_RUNTIME_DIR}` bind hosts, both writable binds, and
// its `jail_env`. Nothing about the pack the repo actually ships can fix that; the socket
// half of audio is unexpressible for a pack until the subset gains a vocabulary for a
// runtime-dir socket.
func TestAudioShapedManifestIsRefusedByTheSubset(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"audio-shaped": audioShapedManifest})
	mods, _, _ := loadPack(t, root).LoopholeModules()
	if len(mods) != 1 || mods[0].Decl == nil {
		t.Fatalf("expected one decodable module, got %+v", mods)
	}
	probs := mods[0].Decl.PackShippedProblems(loopholedecl.ManifestPath(mods[0].Dir))
	joined := strings.Join(probs, "\n")
	for _, want := range []string{
		"expands an environment variable", // both ${XDG_RUNTIME_DIR} bind hosts
		"may not ask for a WRITABLE",      // both readonly:false binds
		"'jail_env' is not available",     // PULSE_SERVER / PIPEWIRE_REMOTE
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the subset must refuse %q for an audio-shaped pack manifest; got:\n%s",
				want, joined)
		}
	}
	// SIX refusals, exactly: two $VAR hosts, two writable binds, one jail_env, and the
	// $VAR in `requires.file_exists`. Pinned as a count so a rule quietly narrowing (or
	// widening) shows up here, in the test whose whole subject is how much the subset
	// refuses.
	//
	// It was FIVE when this test was written, and the sixth is not a regression: the
	// path-scope rule was extended to `requires.file_exists` after a verifier measured
	// that an unscoped value there is a host-filesystem probe WITH A READOUT — `yolo
	// loopholes list` prints the resolved path beside the loophole's inactive reason, so
	// a fetched pack could ask "does ~/.ssh/id_ed25519 exist" and read the answer. audio
	// probes ${XDG_RUNTIME_DIR}/pulse/native, which is the legitimate shape of exactly
	// that vocabulary, so it is refused for the same reason its bind hosts are.
	if len(probs) != 6 {
		t.Errorf("subset refusals = %d, want 6 (2 $VAR hosts + 2 writable binds + jail_env "+
			"+ $VAR in requires.file_exists):\n%s", len(probs), joined)
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
