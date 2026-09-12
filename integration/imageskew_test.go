package integration

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Image-skew detection: refuse to test a freshly built host CLI against a stale
// baked jail image.
//
// THE PROBLEM. TestMain always `go build`s the `yolo` under test, but the jail
// image is loaded at most once and then reused. So the suite happily runs new
// host-side code against an OLD in-jail yolo-entrypoint, old shims and old baked
// packages. That is not a hypothetical: adding a field to pack.json made ~10
// tests fail with `unknown field "tier"` from the previous entrypoint, which read
// as a regression in brand-new code. Worse, switching worktrees/commits replaces
// the loaded image behind the suite's back, so unrelated tests flip pass/fail
// between runs.
//
// WHAT THE ORACLE MEASURES CHANGED WHEN THE BINARIES LEFT THE IMAGE. It used to
// be `.#installPrefix` versus `readlink /bin/yolo-entrypoint`, because the image
// BAKED the shipped binaries and the flake bundle, so installPrefix's store path
// hashed exactly the inputs that decided the image's yolo-jail content (the goSrc
// fileset plus flake.nix). The install prefix is now BIND-MOUNTED at launch
// (internal/cli/run/jailprefix.go), which retires half the original problem
// outright: the suite's `yolo` and the yolo-entrypoint it runs come from the SAME
// tree by construction, so the `unknown field "tier"` class cannot recur.
//
// What a stale image can still be wrong about is everything the FLAKE decides —
// baked packages, the /lib link farm, /etc, the image env — and that is what this
// now compares.
//
// THE CHECK. Ask the two sides what the IMAGE was built from and compare:
//
//   - what the SOURCE wants — `nix eval --raw .#imageIdentity`. imageIdentity is
//     a sha256 over flake.nix + flake.lock and nothing else, which is exactly the
//     image's input set now that the Go build is not in it. It is an EVAL, not a
//     build — and not even a nixpkgs eval, so ~0.1s — which is what lets it run
//     on every suite start (the constraint that the suite must not rebuild the
//     image every run is about the multi-minute `nix build`).
//   - what the LOADED IMAGE has — `cat /etc/yolo-jail-image-identity` inside it.
//     mkOciImage writes that file with the same value, so one ~0.15s container
//     run recovers what the image actually carries.
//
// IT IS A HASH AND NOT A STORE PATH, AND THAT IS THE WHOLE OF THE DARWIN FIX.
// Until 2026-09-12 both sides were a /nix/store path, because `imageIdentity` was
// a `pkgs.runCommand` evaluated inside `eachDefaultSystem`: identical content,
// one store path per evaluating system. A darwin host therefore could not vouch
// for a Linux-built image from its own commit, and this file papered over that
// with a darwin-only downgrade to a warning. THE DOWNGRADE IS GONE. Its premise
// was that a darwin eval and a Linux eval legitimately disagree; they no longer
// do, so a mismatch on darwin now means exactly what it means everywhere.
// Measured for all four default systems by TestImageIdentityIsSystemInvariant
// (docs/design/darwin-image-provenance.md, OQ-IP1).
//
// WHY NOT REUSE AutoLoadImage's NOTION. Because it answers a different question,
// and it has answered it two different ways.
//
// It used to compare the built store path against the newest entry in
// `build/last-load-<runtime>` — bookkeeping about what yolo last loaded, not
// about what is in the runtime, and only ever an approximation because a single
// `:latest` tag named every image. (An earlier version of this comment said the
// comparison was set MEMBERSHIP across the ten-entry LRU; it was equality against
// the newest entry, which is exactly the fix issue #35 landed.) Since C2 it asks
// the runtime directly — "is `yolo-jail:<sha16-of-store-path>` present?" — which
// is a real answer rather than an approximation.
//
// Neither version is this check's oracle, and the newer one is not either. Both
// answer "does an image built from THIS store path exist", which is a question
// about nix inputs; the staleness this suite guards against is whether the image
// a container will actually run carries this source tree's binaries. Asking the
// image itself is ground truth and needs no bookkeeping to stay honest — and it
// keeps this file independent of the code under test, which is the property that
// makes it a guard rather than a restatement.
//
// KNOWN BLIND SPOT (verified, and the reason the failure message ends with a
// git-add note): nix evaluates a git flake from TRACKED files only, so a brand-new
// UNTRACKED file under cmd/ or internal/ moves neither side of the comparison —
// the check reports "matches" while the image genuinely lacks the new code. `git
// add` makes it visible to both. This is the same trap that already governs
// nested-jail verification, so the check inherits the repo's existing rule rather
// than inventing a second, conflicting notion of "what the source tree is".
//
// WHY imageIdentity AND NOT ociImage. `.#ociImage.outPath` also folds in the
// package set — the `packages:` lib-farm tests build --impure per-workspace
// images, and CI loads the ociImageMinimal variant — so it would report skew for
// image variants built from the identical flake. imageIdentity is invariant
// across both (it reads neither YOLO_EXTRA_PACKAGES nor the variant flag) and is
// docs-insensitive, so it fires on flake drift and only on flake drift. It is
// also invariant across every Go change, which is not a weakening: a Go change
// can no longer make a loaded image stale, because the binaries are mounted.
const (
	// skewEnv downgrades or disables the check: "fail" (default), "warn", "off".
	skewEnv = "YOLO_TEST_IMAGE_SKEW"
	// rebuildEnv forces a rebuild+reload before the suite, bypassing the
	// image-already-present short-circuit. This is the documented one-command fix
	// for a skew failure.
	rebuildEnv = "YOLO_TEST_REBUILD_IMAGE"
)

// identityFilePath is the file mkOciImage writes the image's identity into.
// Reading it inside a loaded image yields what that image was built from.
//
// It replaced `readlink /bin/yolo-entrypoint`, which is no longer an oracle for
// anything: that link now points at /opt/yolo-jail/bin/yolo-entrypoint — a fixed
// string naming the launch's bind mount — so it reads the same in every image
// ever built and would report "matches" always.
const identityFilePath = "/etc/yolo-jail-image-identity"

// identityPrefix tags the digest with its algorithm, so a `cat` of the baked
// file is self-describing and so a value that is not an identity at all can be
// REJECTED rather than compared. flake.nix writes the same prefix.
const identityPrefix = "sha256:"

// identityAbsent is what the in-image probe prints when the file is not there at
// all — distinct from an empty read, which is a file that exists and is empty.
const identityAbsent = "ABSENT"

// identityReadScript reads the identity out of an image and ALWAYS SUCCEEDS.
//
// That is deliberate, and it is the difference between a check and a check-
// shaped hole. A probe that exits nonzero reaches checkImageSkew as "the oracle
// is unavailable", which degrades and runs the suite anyway — the right answer
// for an absent runtime and precisely the wrong one for an image whose identity
// is missing or malformed, which is evidence of staleness rather than of a
// harness limitation. So every outcome comes back as a STRING and the comparison
// decides.
//
// The `readlink` rung is DIAGNOSIS, NOT ACCEPTANCE (OQ-IP3 ruled out a
// compatibility window). On an image built before 2026-09-12 the path is a
// symlink to a directory, so `cat` fails; recovering the store path it names is
// what lets identityHint say "this image predates content addressing" instead of
// leaving the reader with a bare failed probe on the one commit where every
// image mismatches.
const identityReadScript = "cat " + identityFilePath + " 2>/dev/null" +
	" || readlink " + identityFilePath + " 2>/dev/null" +
	" || echo " + identityAbsent

// skewFixDest is the `skopeo copy` DESTINATION for the runtime the suite
// detected — the half of the manual fix that is not the same on both backends.
//
// podman's containers-storage IS the load; Apple Container has no equivalent, so
// it gets an OCI archive plus the `container image load` that reads it, which is
// exactly what internal/image's deliverToAppleContainer does. Naming the wrong
// one would hand a Mac user a command that writes into a store nothing reads.
func skewFixDest(rt string) string {
	if rt == "container" {
		return "oci-archive:/tmp/jail-image.oci:" + jailImage +
			" && container image load -i /tmp/jail-image.oci"
	}
	return "containers-storage:localhost/" + jailImage
}

// degraded reports a harness precondition that could not be met. Every early
// return on the image path goes through this: a degraded run may still be worth
// attempting, but it must never be SILENT — a suite that quietly gave up on
// checking (or loading) the image is how stale-image debugging starts. The
// DEGRADED marker is deliberately greppable in CI logs.
//
// CAVEAT, and the reason `fail` is the default rather than `warn`: `go test`
// BUFFERS a test binary's stdout/stderr and discards it when the package passes
// without -v. So this line — and the warn-mode skew report — is guaranteed
// visible only when the package fails (the default skewFail path, via
// log.Fatalf) or under -v (which both CI jobs and the AGENTS.md invocation use).
// A non-fatal notice cannot be made louder than that from inside a test binary,
// which is precisely why the default had to be "abort", not "warn".
func degraded(format string, args ...any) {
	log.Printf("[integration] DEGRADED: "+format, args...)
}

// skewMode is what to do when the loaded image disagrees with the source tree.
type skewMode int

const (
	skewFail skewMode = iota // default: refuse to run the suite
	skewWarn                 // report loudly, run anyway
	skewOff                  // do not even look
)

// parseSkewMode reads YOLO_TEST_IMAGE_SKEW. An unset/empty value is skewFail:
// the default must never be "silently test stale code". An unrecognized value is
// an error rather than a silent fallback — a typo'd "warning" must not read as
// "off" (or as "fail" while the author believes it is off).
func parseSkewMode(v string) (skewMode, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "fail":
		return skewFail, nil
	case "warn":
		return skewWarn, nil
	case "off":
		return skewOff, nil
	}
	return skewFail, fmt.Errorf("%s=%q is not one of fail|warn|off", skewEnv, v)
}

// THERE IS NO PER-PLATFORM DOWNGRADE, AND THAT IS A RULING, NOT AN OMISSION.
// `effectiveSkewMode` used to sit here and turn skewFail into skewWarn on
// darwin, because a darwin eval of the old store-path identity could not be
// compared with a Linux runner's. The identity is content-addressed now
// (flake.nix, `imageIdentity`), so both hosts compute the same string and the
// downgrade's premise is false — keeping it would mean the macOS nightly could
// never fail on a genuinely stale image, which is the one thing the nightly is
// for. docs/design/darwin-image-provenance.md, OQ-IP1.

// parseImageIdentity validates one side's answer as an identity — the algorithm
// tag plus a 64-char lowercase hex digest. Kept pure (no exec) so the parse is
// covered by the -short suite, where no container runs.
//
// It is applied to the SOURCE side (a malformed `nix eval` result is a broken
// oracle, not a stale image) and by TestImageSkewOracleAnswers to the IMAGE side.
// checkImageSkew deliberately does NOT apply it to the image side: see
// identityReadScript — a bad value there is a finding, not a missing oracle.
func parseImageIdentity(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	hex, ok := strings.CutPrefix(id, identityPrefix)
	if !ok {
		return "", fmt.Errorf("%q is not an image identity (want %s<64 hex>)", id, identityPrefix)
	}
	if len(hex) != 64 || strings.TrimLeft(hex, "0123456789abcdef") != "" {
		return "", fmt.Errorf("%q is not an image identity: %q is not a 64-char lowercase hex digest",
			id, hex)
	}
	return id, nil
}

// expectedImageIdentity evaluates (never builds) the identity this source tree
// would bake into the image.
//
// `.#imageIdentity` is a bare flake attribute, not `packages.<system>.…`, which
// is how it carries no system; nix falls back to a bare attribute after trying
// both per-system prefixes, so the spelling is the same on every host.
//
// --impure mirrors every other nix invocation in the repo and is not
// load-bearing here: imageIdentity reads neither YOLO_EXTRA_PACKAGES nor
// nixpkgs, so the pure and impure evals agree. stderr is dropped on purpose —
// nix emits untrusted-flake-config warnings and a "Git tree is dirty" notice
// that would bury the one line we want.
func expectedImageIdentity() (string, error) {
	return evalImageIdentity()
}

// evalImageIdentity is expectedImageIdentity with optional extra nix flags, so
// TestImageIdentityIsSystemInvariant can ask the same question once per
// `--system` without a second copy of the invocation.
func evalImageIdentity(extraFlags ...string) (string, error) {
	if _, err := exec.LookPath("nix"); err != nil {
		return "", fmt.Errorf("nix is not on PATH")
	}
	if repoRoot == "" {
		return "", fmt.Errorf("module root unresolved")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	argv := []string{"--extra-experimental-features", "nix-command flakes"}
	argv = append(argv, extraFlags...)
	argv = append(argv, "eval", "--impure", "--raw", ".#imageIdentity")
	cmd := exec.CommandContext(ctx, "nix", argv...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("nix %s failed: %w", strings.Join(argv, " "), err)
	}
	return parseImageIdentity(string(out))
}

// loadedImageIdentity asks the loaded image what it was built from, by reading
// the identity file inside it.
//
// It returns the image's answer VERBATIM (trimmed), valid or not — an error here
// means the container did not run at all. See identityReadScript for why the
// difference matters: "the image answered with something that is not an
// identity" must reach the comparison as a finding, never as a skipped check.
func loadedImageIdentity(rt, image string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), jailTimeout())
	defer cancel()
	argv := []string{"run", "--rm"}
	// --network=none: the probe reads one file and needs no network, and
	// skipping netavark cuts it from ~0.5s to ~0.15s. Only for podman — Apple
	// Container spells its network flags differently.
	if rt == "podman" {
		argv = append(argv, "--network=none")
	}
	argv = append(argv, image, "sh", "-c", identityReadScript)
	out, err := exec.CommandContext(ctx, rt, argv...).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", rt, strings.Join(argv, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// checkImageSkew compares the loaded image against the source tree and, by
// default, aborts the suite when they disagree.
//
// A probe that cannot run is reported as degraded and the suite proceeds: an
// unavailable oracle is a harness limitation, not evidence of stale code, and
// failing on it would make the suite unrunnable wherever nix is absent.
func checkImageSkew(rt, image string) {
	mode, err := parseSkewMode(os.Getenv(skewEnv))
	if err != nil {
		log.Fatalf("integration: %v", err)
	}
	if mode == skewOff {
		degraded("%s=off — NOT checking whether %s matches this source tree; "+
			"container results may reflect stale baked code", skewEnv, image)
		return
	}

	want, err := expectedImageIdentity()
	if err != nil {
		degraded("cannot determine what this source tree would bake into the image "+
			"(%v) — skipping the staleness check", err)
		return
	}
	got, err := loadedImageIdentity(rt, image)
	if err != nil {
		degraded("cannot read the loaded image's baked identity (%v) — "+
			"skipping the staleness check", err)
		return
	}
	if got == want {
		log.Printf("[integration] %s matches this source tree (%s)", image, want)
		return
	}

	msg := skewMessage(image, rt, want, got)
	if mode == skewWarn {
		log.Printf("[integration] WARNING (proceeding anyway):\n%s", msg)
		return
	}
	log.Fatalf("integration: %s", msg)
}

// TestImageSkewOracleAnswers is the guard on the guard: it asserts that BOTH
// halves of the check actually produce a store path here.
//
// Without it the check has a silent-failure mode that returns the suite to
// exactly the behavior this file exists to remove: if `nix eval` or the in-image
// readlink ever stops working (a renamed flake attribute, a changed /bin/<name>
// symlink layout, nix off PATH in a new CI job), checkImageSkew degrades to a
// DEGRADED line and every later run happily tests whatever image is loaded. A
// DEGRADED line is the right behavior for a genuinely unavailable oracle, but it
// must not be the SILENT permanent state — so one test asserts the oracle answers
// in the environment the suite actually runs in.
//
// It deliberately does NOT assert the two agree: that is checkImageSkew's job at
// TestMain, and asserting it twice would turn an accepted `YOLO_TEST_IMAGE_SKEW=warn`
// run into a red test.
func TestImageSkewOracleAnswers(t *testing.T) {
	requireJail(t)
	rt := detectRuntime()
	if rt == "" {
		t.Skip("no container runtime")
	}
	image := imageExists(rt)
	if image == "" {
		t.Skip("no jail image loaded")
	}

	want, err := expectedImageIdentity()
	if err != nil {
		t.Fatalf("source-tree side of the staleness check is broken: %v\n"+
			"Until this works, the suite cannot tell a stale image from a fresh one.", err)
	}
	raw, err := loadedImageIdentity(rt, image)
	if err != nil {
		t.Fatalf("image side of the staleness check is broken: %v\n"+
			"Has flake.nix stopped writing %s (mkOciImage's postBuild)?",
			err, identityFilePath)
	}
	// The image side is parsed HERE and nowhere else on the read path: this is
	// the test whose job is "the oracle answers", so a malformed value is its
	// failure. checkImageSkew must keep treating the same value as a finding.
	got, err := parseImageIdentity(raw)
	if err != nil {
		t.Fatalf("image side of the staleness check answered %q: %v\n"+
			"Has flake.nix stopped writing %s (mkOciImage's postBuild)?",
			raw, err, identityFilePath)
	}
	t.Logf("source tree wants %s; %s has %s", want, image, got)
}

// TestImageIdentityIsSystemInvariant is the measurement OQ-IP1 turned into a
// requirement: two hosts of different systems evaluating this commit must agree
// on the image's identity, or the identity is a local cache key wearing one.
//
// It is the guard that makes removing the darwin downgrade safe. The old
// store-path identity fails it outright — measured on THIS Linux host on
// 2026-09-12, `imageIdentity.outPath` evaluated to three different store paths
// for x86_64-linux, aarch64-linux and aarch64-darwin with byte-identical content
// — which is precisely why the macOS nightly could not vouch for an image an
// ubuntu runner had built from the same commit.
//
// WHAT IT DOES AND DOES NOT MEASURE. `--system` changes the system nix evaluates
// FOR, not the machine it evaluates ON, so this proves the expression carries no
// system; it is not a substitute for running the suite on a Mac. That is enough
// for the property at issue, because the defect was entirely in the expression:
// a `pkgs.runCommand` reaches the evaluating host's package set, and a
// `builtins.hashFile` over two files has nothing per-host to reach.
func TestImageIdentityIsSystemInvariant(t *testing.T) {
	requireJail(t)
	want, err := expectedImageIdentity()
	if err != nil {
		t.Fatalf("cannot evaluate this tree's image identity: %v", err)
	}
	for _, sys := range []string{
		"x86_64-linux", "aarch64-linux", "x86_64-darwin", "aarch64-darwin",
	} {
		got, err := evalImageIdentity("--system", sys)
		if err != nil {
			// A system whose eval FAILS is the same defect wearing a different
			// face: a host there cannot compute the identity either.
			t.Errorf("--system %s cannot evaluate the identity: %v", sys, err)
			continue
		}
		if got != want {
			t.Errorf("--system %s evaluates the identity as %s, want %s\n"+
				"An identity that varies by evaluating system cannot vouch for an image "+
				"built anywhere else (docs/design/darwin-image-provenance.md, OQ-IP1).",
				sys, got, want)
		}
	}
}

// skewMessage is the whole point of this file: turn a mystery into an
// instruction. It names both store paths (so the reader can see it is a source
// mismatch and not a flaky test), says what the mismatch invalidates, and gives
// the exact commands that resolve it.
func skewMessage(image, rt, want, got string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "STALE JAIL IMAGE — refusing to run the container suite.\n\n")
	fmt.Fprintf(&b, "  %s was built from a DIFFERENT flake than the one under test.\n", image)
	fmt.Fprintf(&b, "  The jail's own binaries are MOUNTED from this tree, so those are fresh — but\n")
	fmt.Fprintf(&b, "  everything the flake decides is not: baked packages, the /lib link farm, /etc,\n")
	fmt.Fprintf(&b, "  the image env. A test that touches any of them would pass or fail for a reason\n")
	fmt.Fprintf(&b, "  that is not in the code under test.\n\n")
	fmt.Fprintf(&b, "    source tree wants: %s\n", want)
	fmt.Fprintf(&b, "    loaded image has : %s\n\n", got)
	b.WriteString(identityHint(got))
	fmt.Fprintf(&b, "  Fix (pick one):\n")
	fmt.Fprintf(&b, "    rebuild + reload, then run the suite:\n")
	fmt.Fprintf(&b, "        %s=1 go test -count=1 -timeout 0 ./integration\n", rebuildEnv)
	fmt.Fprintf(&b, "    rebuild + reload by hand:\n")
	fmt.Fprintf(&b, "        cd %s && nix build --impure .#ociImage .#imageCopier && \\\n", repoRoot)
	fmt.Fprintf(&b, "            ./result-1/bin/skopeo --insecure-policy copy \\\n")
	fmt.Fprintf(&b, "            \"nix:$(readlink -f ./result)\" %s\n", skewFixDest(rt))
	fmt.Fprintf(&b, "    accept the skew for this run (a host-CLI-only change, a bisect, ...):\n")
	fmt.Fprintf(&b, "        %s=warn go test -count=1 -timeout 0 ./integration\n\n", skewEnv)
	fmt.Fprintf(&b, "  Note: nix only sees git-TRACKED files, so `git add` a newly created file\n")
	fmt.Fprintf(&b, "  before rebuilding, or the rebuilt image still won't contain it.\n")
	return b.String()
}

// identityHint explains an image-side answer that is not an identity at all, so
// the reader is told what they are looking at instead of comparing a hash to a
// string that is obviously not one.
//
// The first case is the one commit's worth of noise this change creates. Making
// the identity content-addressed moves every image's recorded value exactly once,
// so on the commit that lands it every already-loaded image mismatches and needs
// one rebuild (OQ-IP3, which ruled that cost accepted over a dual-spelling
// window). An unexplained "loaded image has: /nix/store/…" would read as a
// corrupt image; naming it costs four lines and expires on its own, because the
// shape it recognises can never be produced again.
func identityHint(got string) string {
	switch {
	case strings.HasPrefix(got, "/nix/store/"):
		return "  That is a STORE PATH, not an identity. This image predates the identity\n" +
			"  becoming content-addressed (2026-09-12), when the baked value was a symlink\n" +
			"  to a per-system store path — which is why a darwin host could never vouch for\n" +
			"  a Linux-built image. EVERY image built before that commit mismatches exactly\n" +
			"  once, and the rebuild below is the whole fix.\n\n"
	case got == identityAbsent || got == "":
		return "  The image carries NO identity. On a freshly built image that means flake.nix\n" +
			"  has stopped writing " + identityFilePath + " (mkOciImage's postBuild),\n" +
			"  and the staleness check is blind until it does again.\n\n"
	}
	return ""
}
