package macosuser

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// pkgOpts builds Options whose config declares `packages`.
func pkgOpts(ws string, packages []any) Options {
	o := newOpts(ws)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("packages", packages)
	o.Config = cfg
	return o
}

// A DECLARED PACKAGE THAT DID NOT BUILD IS FATAL (A2 piece 1). The tree
// warn-and-skipped for a year after the ruling, which masks the two cases that
// matter and that the message cannot tell apart: a typo (an unknown attr is
// "skipped"), and a package with genuinely no darwin build. Either way the user
// asked for a tool and the jail started without it.
func TestRunAbortsOnAPackageWithNoDarwinBuild(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		return &Darwin{System: "aarch64-darwin", Skipped: []string{"strace", "ripgrpe"}}, true, nil
	}

	rc := RunMacosUser(d, pkgOpts("/Users/Shared/yolo/proj", []any{"strace", "ripgrpe"}))

	if rc != 1 {
		t.Errorf("rc = %d, want 1 — a declared package that did not build must not launch", rc)
	}
	if strings.Contains(strings.Join(rec, "\n"), "proxy:") {
		t.Error("launched a jail missing a package the user declared")
	}
	got := buf.String()
	// EVERY name at once: fixing them one launch at a time is the thing the
	// aggregated error exists to avoid.
	for _, want := range []string{"strace", "ripgrpe"} {
		if !strings.Contains(got, want) {
			t.Errorf("the error does not name %q:\n%s", want, got)
		}
	}
	// The system nix actually resolved against, not a hardcoded arch — on an Intel
	// Mac, blaming aarch64 sends the reader after the wrong cause.
	if !strings.Contains(got, "aarch64-darwin") {
		t.Errorf("the error does not name the resolved system:\n%s", got)
	}
	// A typo is the likeliest cause and is indistinguishable from a real absence,
	// so the message has to say so.
	if !strings.Contains(got, "TYPO") {
		t.Errorf("the error does not raise the typo case:\n%s", got)
	}
	// And it must name the escape hatch, or the only way out is deleting the package.
	if !strings.Contains(got, `"platforms": ["linux"]`) {
		t.Errorf("the error does not show the linux-only override:\n%s", got)
	}
}

// A package marked Linux-only is EXPECTED-absent: it never reaches the build (
// EffectivePackages drops it), so it cannot appear in the skip list and the launch
// proceeds. This is the half that makes the hard error usable rather than a wall.
func TestRunLaunchesWithALinuxOnlyPackage(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	var sawPackages []any
	d.MaterializeDarwin = func(_ string, pkgs []any) (*Darwin, bool, error) {
		sawPackages = pkgs
		return &Darwin{System: "aarch64-darwin"}, true, nil
	}

	linuxOnly := jsonx.NewOrderedMap()
	linuxOnly.Set("name", "strace")
	linuxOnly.Set("platforms", []any{"linux"})

	rc := RunMacosUser(d, pkgOpts("/Users/Shared/yolo/proj", []any{"jq", linuxOnly}))

	if rc != 42 {
		t.Errorf("rc = %d, want 42 (the mock proxy's exit code)\n%s", rc, buf.String())
	}
	// The filter runs BEFORE the build, so nix never sees the linux-only entry —
	// which is what lets the error above treat everything still missing as real.
	if len(sawPackages) != 1 {
		t.Errorf("materialize saw %v, want only the darwin-applicable entry", sawPackages)
	}
	if strings.Contains(buf.String(), "strace") {
		t.Errorf("complained about a package explicitly marked Linux-only:\n%s", buf.String())
	}
}
