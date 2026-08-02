package integration

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	goruntime "runtime"
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
// THE CHECK. Ask the two sides what they were built from and compare:
//
//   - what the SOURCE wants — `nix eval .#installPrefix.outPath`. installPrefix
//     is the derivation that bakes the four shipped binaries plus the flake
//     bundle into the image, so its store path is a hash of exactly the inputs
//     that decide the image's yolo-jail content: the goSrc fileset (go.mod/sum,
//     vendor/, cmd/, internal/, bundled_loopholes/, packs/) and flake.nix. It is
//     an EVAL, not a build: ~0.3s, so it can run on every suite start (the
//     constraint that the suite must not rebuild the image every run is about the
//     multi-minute `nix build`, which this deliberately avoids).
//   - what the LOADED IMAGE has — `readlink /bin/yolo-entrypoint` inside it. The
//     flake points that symlink at <installPrefix>/opt/yolo-jail/bin/... (see the
//     shadow-hardening note on installPrefix in flake.nix), so one ~0.15s
//     container run recovers the store path the image actually carries.
//
// WHY NOT REUSE AutoLoadImage's NOTION. AutoLoadImage compares the built store
// path against `build/last-load-<runtime>`, which is an LRU of the last TEN
// loaded paths — a set, not a record of what is loaded RIGHT NOW. Only one image
// can hold the `localhost/yolo-jail:latest` tag, so after worktree A loads PA and
// worktree B loads PB, going back to A finds PA still in the LRU, concludes
// "already loaded", and runs B's image. That is the flip-flop above, and it means
// the sentinel cannot be trusted as the freshness oracle here. Asking the image
// itself is ground truth and needs no bookkeeping to stay honest.
//
// KNOWN BLIND SPOT (verified, and the reason the failure message ends with a
// git-add note): nix evaluates a git flake from TRACKED files only, so a brand-new
// UNTRACKED file under cmd/ or internal/ moves neither side of the comparison —
// the check reports "matches" while the image genuinely lacks the new code. `git
// add` makes it visible to both. This is the same trap that already governs
// nested-jail verification, so the check inherits the repo's existing rule rather
// than inventing a second, conflicting notion of "what the source tree is".
//
// WHY installPrefix AND NOT ociImage. `.#ociImage.outPath` also folds in the
// package set — the `packages:` lib-farm tests build --impure per-workspace
// images, and CI loads the ociImageMinimal variant — so it would report skew for
// image variants that carry the identical yolo-jail code. installPrefix is
// invariant across both (verified: same path with and without
// YOLO_EXTRA_PACKAGES, and shared by the full and minimal variants), and is
// docs-insensitive, so it fires on source drift and only on source drift.
const (
	// skewEnv downgrades or disables the check: "fail" (default), "warn", "off".
	skewEnv = "YOLO_TEST_IMAGE_SKEW"
	// rebuildEnv forces a rebuild+reload before the suite, bypassing the
	// image-already-present short-circuit. This is the documented one-command fix
	// for a skew failure.
	rebuildEnv = "YOLO_TEST_REBUILD_IMAGE"
)

// entrypointLinkSuffix is the tail of the /bin/yolo-entrypoint symlink the flake
// bakes into every jail image:
//
//	/bin/yolo-entrypoint -> <installPrefix>/opt/yolo-jail/bin/yolo-entrypoint
//
// Cutting it off a live readlink yields the installPrefix store path the loaded
// image was built from. yolo-entrypoint is the right probe target of the four
// shipped binaries because it is the one that runs INSIDE the container as pid1 —
// the binary whose staleness produced the `unknown field "tier"` failures.
const entrypointLinkSuffix = "/opt/yolo-jail/bin/yolo-entrypoint"

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

// effectiveSkewMode downgrades skewFail to skewWarn where a mismatch cannot be
// attributed with confidence, returning the reason for the downgrade ("" when
// none happened).
//
// darwin is the one such case. There, installPrefix is a DARWIN derivation that
// cross-compiles Linux binaries, so a locally built image matches a local
// `.#installPrefix` eval — but the macOS nightly loads an image built on an
// ubuntu runner, whose installPrefix is the x86_64-LINUX one and will never
// match a darwin eval even at the identical commit. The harness cannot tell the
// two provenances apart from here, and a false "stale image" that reds the
// nightly is worse than a missed one, so on darwin the finding is reported and
// the suite proceeds.
func effectiveSkewMode(mode skewMode, goos string) (skewMode, string) {
	if mode == skewFail && goos == "darwin" {
		return skewWarn, "on darwin the image may have been built on a Linux runner, " +
			"whose installPrefix legitimately differs from a local eval — reporting instead of failing"
	}
	return mode, ""
}

// installPrefixFromLink extracts the installPrefix store path from a
// /bin/yolo-entrypoint symlink target. Kept pure (no exec) so the parse is
// covered by the -short suite, where no container runs.
func installPrefixFromLink(link string) (string, error) {
	link = strings.TrimSpace(link)
	prefix, ok := strings.CutSuffix(link, entrypointLinkSuffix)
	if !ok || !strings.HasPrefix(prefix, "/nix/store/") {
		return "", fmt.Errorf("unexpected /bin/yolo-entrypoint target %q "+
			"(want <nix store path>%s)", link, entrypointLinkSuffix)
	}
	return prefix, nil
}

// expectedInstallPrefix evaluates (never builds) the installPrefix store path
// this source tree would bake into the image.
//
// --impure mirrors every other nix invocation in the repo and is not
// load-bearing here: installPrefix does not read YOLO_EXTRA_PACKAGES, so the
// pure and impure evals agree (verified). stderr is dropped on purpose — nix
// emits untrusted-flake-config warnings and a "Git tree is dirty" notice that
// would bury the one line we want.
func expectedInstallPrefix() (string, error) {
	if _, err := exec.LookPath("nix"); err != nil {
		return "", fmt.Errorf("nix is not on PATH")
	}
	if repoRoot == "" {
		return "", fmt.Errorf("module root unresolved")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nix",
		"--extra-experimental-features", "nix-command flakes",
		"eval", "--impure", "--raw", ".#installPrefix.outPath")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("nix eval .#installPrefix failed: %w", err)
	}
	path := strings.TrimSpace(string(out))
	if !strings.HasPrefix(path, "/nix/store/") {
		return "", fmt.Errorf("nix eval returned %q, not a store path", path)
	}
	return path, nil
}

// loadedInstallPrefix asks the loaded image which installPrefix it carries, by
// reading the /bin/yolo-entrypoint symlink inside it.
func loadedInstallPrefix(rt, image string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), jailTimeout())
	defer cancel()
	argv := []string{"run", "--rm"}
	// --network=none: the probe reads one symlink and needs no network, and
	// skipping netavark cuts it from ~0.5s to ~0.15s. Only for podman — Apple
	// Container spells its network flags differently, and darwin is report-only
	// anyway (see effectiveSkewMode).
	if rt == "podman" {
		argv = append(argv, "--network=none")
	}
	argv = append(argv, image, "readlink", "/bin/yolo-entrypoint")
	out, err := exec.CommandContext(ctx, rt, argv...).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", rt, strings.Join(argv, " "), err)
	}
	return installPrefixFromLink(string(out))
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

	want, err := expectedInstallPrefix()
	if err != nil {
		degraded("cannot determine what this source tree would bake into the image "+
			"(%v) — skipping the staleness check", err)
		return
	}
	got, err := loadedInstallPrefix(rt, image)
	if err != nil {
		degraded("cannot read the loaded image's baked yolo-jail version (%v) — "+
			"skipping the staleness check", err)
		return
	}
	if got == want {
		log.Printf("[integration] %s matches this source tree (%s)", image, want)
		return
	}

	mode, downgrade := effectiveSkewMode(mode, goruntime.GOOS)
	if downgrade != "" {
		degraded("stale-image finding downgraded to a warning: %s", downgrade)
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

	want, err := expectedInstallPrefix()
	if err != nil {
		t.Fatalf("source-tree side of the staleness check is broken: %v\n"+
			"Until this works, the suite cannot tell a stale image from a fresh one.", err)
	}
	got, err := loadedInstallPrefix(rt, image)
	if err != nil {
		t.Fatalf("image side of the staleness check is broken: %v\n"+
			"Has flake.nix stopped pointing /bin/yolo-entrypoint at "+
			"<installPrefix>%s?", err, entrypointLinkSuffix)
	}
	t.Logf("source tree wants %s; %s has %s", want, image, got)
}

// skewMessage is the whole point of this file: turn a mystery into an
// instruction. It names both store paths (so the reader can see it is a source
// mismatch and not a flaky test), says what the mismatch invalidates, and gives
// the exact commands that resolve it.
func skewMessage(image, rt, want, got string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "STALE JAIL IMAGE — refusing to run the container suite.\n\n")
	fmt.Fprintf(&b, "  %s was built from a DIFFERENT source tree than the one under test.\n", image)
	fmt.Fprintf(&b, "  TestMain always builds a fresh host `yolo`, so every container test would\n")
	fmt.Fprintf(&b, "  exercise new host-side code against an OLD in-jail yolo-entrypoint, old\n")
	fmt.Fprintf(&b, "  shims and old baked packages: a pass would prove nothing and a failure\n")
	fmt.Fprintf(&b, "  would point at the wrong code.\n\n")
	fmt.Fprintf(&b, "    source tree wants: %s\n", want)
	fmt.Fprintf(&b, "    loaded image has : %s\n\n", got)
	fmt.Fprintf(&b, "  Fix (pick one):\n")
	fmt.Fprintf(&b, "    rebuild + reload, then run the suite:\n")
	fmt.Fprintf(&b, "        %s=1 go test -count=1 -timeout 0 ./integration\n", rebuildEnv)
	fmt.Fprintf(&b, "    rebuild + reload by hand:\n")
	fmt.Fprintf(&b, "        cd %s && nix build --impure .#ociImage && ./result | %s load\n", repoRoot, rt)
	fmt.Fprintf(&b, "    accept the skew for this run (a host-CLI-only change, a bisect, ...):\n")
	fmt.Fprintf(&b, "        %s=warn go test -count=1 -timeout 0 ./integration\n\n", skewEnv)
	fmt.Fprintf(&b, "  Note: nix only sees git-TRACKED files, so `git add` a newly created file\n")
	fmt.Fprintf(&b, "  before rebuilding, or the rebuilt image still won't contain it.\n")
	return b.String()
}
