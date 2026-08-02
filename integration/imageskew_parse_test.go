package integration

import (
	"strings"
	"testing"
)

// These run under -short (no container): they cover the image-skew check's pure
// decision logic — the parts that decide whether the suite runs at all — so a
// mistake there is caught by the pre-commit gate rather than by a confusing
// integration run. The exec'ing halves (nix eval, the in-image readlink) are
// exercised by every real integration run.

// TestParseSkewMode pins the DEFAULT: an unset knob must mean "fail", because the
// whole point is that the suite never silently tests stale code. An unrecognized
// value must be an error, not a silent fallback — "warning" must not read as
// "off" (nor as "fail" while the author believes it is off).
func TestParseSkewMode(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want skewMode
	}{
		{"", skewFail}, // unset → the safe default
		{"fail", skewFail},
		{"FAIL", skewFail}, // case-insensitive
		{"warn", skewWarn},
		{" warn ", skewWarn}, // tolerate stray whitespace
		{"off", skewOff},
	} {
		got, err := parseSkewMode(tc.in)
		if err != nil {
			t.Errorf("parseSkewMode(%q) errored: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseSkewMode(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"warning", "true", "1", "no"} {
		if _, err := parseSkewMode(bad); err == nil {
			t.Errorf("parseSkewMode(%q) = nil error; a typo must not silently pick a mode", bad)
		}
	}
}

// TestEffectiveSkewMode: darwin can't attribute a mismatch (the image may have
// been built on a Linux runner, whose installPrefix legitimately differs from a
// darwin eval), so a hard failure is downgraded to a warning there — a false
// "stale image" reddening the macOS nightly is worse than a missed one. Linux
// keeps the hard failure, and warn/off are never UPgraded.
func TestEffectiveSkewMode(t *testing.T) {
	if mode, why := effectiveSkewMode(skewFail, "linux"); mode != skewFail || why != "" {
		t.Errorf("linux fail → (%v, %q), want (skewFail, \"\")", mode, why)
	}
	mode, why := effectiveSkewMode(skewFail, "darwin")
	if mode != skewWarn {
		t.Errorf("darwin fail → %v, want skewWarn", mode)
	}
	if why == "" {
		t.Error("a downgrade must explain itself — silence is the bug this file fixes")
	}
	// An explicit warn/off is the user's choice; the platform must not tighten it.
	if mode, _ := effectiveSkewMode(skewWarn, "darwin"); mode != skewWarn {
		t.Errorf("darwin warn → %v, want skewWarn", mode)
	}
	if mode, _ := effectiveSkewMode(skewOff, "darwin"); mode != skewOff {
		t.Errorf("darwin off → %v, want skewOff", mode)
	}
}

// TestInstallPrefixFromLink locks the parse of the /bin/yolo-entrypoint symlink
// the flake bakes into the image. If flake.nix ever stops pointing /bin/<name> at
// <installPrefix>/opt/yolo-jail/bin/<name> (the shadow-hardening layout), this
// test still passes but the real probe starts erroring — which surfaces as a
// DEGRADED line, not a false "stale image". That is the intended failure mode.
func TestInstallPrefixFromLink(t *testing.T) {
	const prefix = "/nix/store/bh2wnsa9rmbacx6lciwcfind54n1b5pj-yolo-jail-install-prefix"
	got, err := installPrefixFromLink(prefix + entrypointLinkSuffix + "\n")
	if err != nil {
		t.Fatalf("valid link errored: %v", err)
	}
	if got != prefix {
		t.Errorf("got %q, want %q", got, prefix)
	}
	for _, bad := range []string{
		"",
		"/bin/yolo-entrypoint",                 // not a store path
		"/nix/store/abc-x/bin/yolo-entrypoint", // wrong layout (no /opt/yolo-jail)
		"relative/opt/yolo-jail/bin/yolo-entrypoint",
	} {
		if _, err := installPrefixFromLink(bad); err == nil {
			t.Errorf("installPrefixFromLink(%q) = nil error, want a parse failure", bad)
		}
	}
}

// TestSkewMessageIsActionable: the message is the deliverable — a mystery turned
// into an instruction. It must name BOTH store paths (so the reader can see this
// is a source mismatch, not a flaky test) and hand over the commands that fix it,
// including the git-add caveat (nix only sees tracked files, so a rebuild without
// it produces a still-stale image and a second round of confusion).
func TestSkewMessageIsActionable(t *testing.T) {
	msg := skewMessage("localhost/yolo-jail:latest", "podman",
		"/nix/store/aaa-yolo-jail-install-prefix", "/nix/store/bbb-yolo-jail-install-prefix")
	for _, want := range []string{
		"/nix/store/aaa-yolo-jail-install-prefix", // what the source wants
		"/nix/store/bbb-yolo-jail-install-prefix", // what is loaded
		rebuildEnv + "=1",                         // the one-command fix
		skewEnv + "=warn",                         // the documented escape hatch
		"nix build --impure .#ociImage",           // the manual fix
		"podman load",                             // ...for the detected runtime
		"git add",                                 // the tracked-files trap
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("skew message is missing %q:\n%s", want, msg)
		}
	}
}
