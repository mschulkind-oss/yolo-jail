package image

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestNixFlakeFlagsAcceptsFlakeConfig pins the flag itself. Dropping
// --accept-flake-config is silent at runtime — nix warns on stderr and builds
// from source anyway — so the only place it can be caught is here.
func TestNixFlakeFlagsAcceptsFlakeConfig(t *testing.T) {
	flags := NixFlakeFlags()
	if !slices.Contains(flags, "--accept-flake-config") {
		t.Fatalf("NixFlakeFlags() missing --accept-flake-config: %v", flags)
	}
	i := slices.Index(flags, "--extra-experimental-features")
	if i < 0 || i+1 >= len(flags) || flags[i+1] != "nix-command flakes" {
		t.Fatalf("NixFlakeFlags() missing experimental-features pair: %v", flags)
	}
}

// TestFlakeInvocationsCarryAcceptFlakeConfig drives the REAL builders and reads
// back what the child process was actually handed.
//
// Asserting on ociBuildArgv instead would be a tautology dressed as a table.
// ociBuildArgv is a helper, not a call site, and the defect b7f2ade fixed was not
// "the helper is wrong" — the helper did not exist. It was that each call site
// spelled its own `[]string{"nix", "--extra-experimental-features", …}`, and any
// one of them can regress to that in a single edit while every assertion against
// the helper stays green. Measured 2026-08-17: reverting either builder to an
// inline argv left `go test -short ./...` entirely green.
//
// So the seam under test is the SUBPROCESS. A recording `nix` first on PATH
// captures the argv, which is the one artifact that cannot be produced by a
// builder that bypassed the helper.
//
// check's dry-run probe is the third flake-evaluating invocation; it is covered
// the same way by the sibling test in internal/cli/check, which is where that
// builder lives and where its injectable Exec seam is.
func TestFlakeInvocationsCarryAcceptFlakeConfig(t *testing.T) {
	cases := []struct {
		name string
		run  func(repoRoot, outLink string)
	}{
		{"run path (buildImageStorePathArgs)", func(repoRoot, outLink string) {
			_, _ = buildImageStorePathArgs(repoRoot, nil, outLink, io.Discard, nil, nil)
		}},
		{"run path with builder offload", func(repoRoot, outLink string) {
			_, _ = buildImageStorePathArgs(repoRoot, nil, outLink, io.Discard,
				[]string{"--builders", "ssh://b"}, nil)
		}},
		{"check preflight (BuildOCIImage)", func(repoRoot, _ string) {
			_, _ = BuildOCIImage(repoRoot, nil)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorded := fakeNix(t)
			dir := t.TempDir()
			tc.run(dir, filepath.Join(dir, "result"))
			assertFlakeArgv(t, recorded())
		})
	}
}

// fakeNix installs a recording `nix` first on PATH and returns a reader for the
// argv it was called with.
//
// The recorder exits NON-ZERO on purpose. Both builders treat a failed nix as an
// ordinary build failure — they collect the stderr tail and return ("", tail) —
// so the probe finishes in milliseconds without either one going on to read an
// out-link that a real build would have created. A zero exit would send them
// looking for that symlink instead, which is work this test has no opinion about.
//
// The argv file is passed through the environment rather than baked into the
// script so the path never has to survive shell quoting: TMPDIR is not this
// test's to choose. Both builders hand the child os.Environ(), so it arrives.
func fakeNix(t *testing.T) func() []string {
	t.Helper()
	bin := t.TempDir()
	argvFile := filepath.Join(bin, "recorded-argv")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >>\"$YOLO_TEST_NIX_ARGV\"; done\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_TEST_NIX_ARGV", argvFile)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	return func() []string {
		t.Helper()
		data, err := os.ReadFile(argvFile)
		if err != nil {
			t.Fatalf("the recording nix was never executed (%v) — the builder did not "+
				"run `nix` at all, so this test proved nothing", err)
		}
		// One argument per line: `nix-command flakes` is a single argv element that
		// contains a space, so any whitespace split would silently pass a test that
		// should fail.
		args := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		return append([]string{"nix"}, args...)
	}
}

// assertFlakeArgv is the shared assertion: this really is a build of the flake
// attr, and both nix-level flags precede the subcommand (nix rejects
// --accept-flake-config after it). check's own test mirrors it for the dry-run
// probe.
func assertFlakeArgv(t *testing.T, argv []string) {
	t.Helper()
	if len(argv) == 0 || argv[0] != "nix" {
		t.Fatalf("argv does not invoke nix: %v", argv)
	}
	sub := slices.Index(argv, "build")
	if sub < 0 {
		t.Fatalf("argv has no `build` subcommand: %v", argv)
	}
	if !slices.Contains(argv, ".#ociImage") {
		t.Fatalf("argv does not evaluate .#ociImage, so it is not the invocation this "+
			"test is about: %v", argv)
	}
	nixLevel := argv[1:sub]
	for _, want := range NixFlakeFlags() {
		if !slices.Contains(nixLevel, want) {
			t.Errorf("nix-level flags %v missing %q (full argv: %v)", nixLevel, want, argv)
		}
	}
}

// TestOCIBuildArgvShape keeps the extracted builder honest about the argv the
// two call sites used to spell inline, so the refactor that unified them is not
// free to change what nix is actually asked to do.
func TestOCIBuildArgvShape(t *testing.T) {
	got := ociBuildArgv("/tmp/link", []string{"--builders", "ssh://b"})
	want := []string{
		"nix",
		"--extra-experimental-features", "nix-command flakes",
		"--accept-flake-config",
		"build", ".#ociImage", "--impure",
		"--out-link", "/tmp/link",
		"--print-build-logs",
		"--builders", "ssh://b",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ociBuildArgv:\n got %v\nwant %v", got, want)
	}
}
