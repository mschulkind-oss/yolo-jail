package run

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// hostnixdelegation_test.go guards the split between the two claims a macOS launch can
// make about /nix: that its VM SHARES the tree (a bind source resolves) and that the store
// it shares is a usable /nix/store FOR A LINUX JAIL. One variable meant both until
// 2026-09-13, and the second claim was never true on the macOS nightly's runner.
//
// THESE ARE ARGV TESTS, NOT PREDICATE TESTS, and that is the whole point of the file.
// shouldMountHostNix is already exercised by TestEnvTruthyMatchesTheOtherOptInDials — but
// a test that pins only the predicate passes with the gate's CALL SITE deleted, because
// assembleRunCmd would then emit the mount regardless of what the predicate returned. That
// is the shape AGENTS.md names ("does it fail if I delete the call site?"), and this repo
// has shipped it five times. So every row below reads the composed container argv.

// macosNixOptions is goldenOptions turned into a Mac whose VM does share /nix: the two host
// paths exist, and the env is whatever the row is testing.
//
// It keeps podman as the runtime deliberately. Apple Container cannot bind a Unix socket at
// all and is refused a step earlier, so it would pass every row here for the wrong reason.
func macosNixOptions(t *testing.T, workspace, home string, env map[string]string) (*Options, *bytes.Buffer) {
	t.Helper()
	o := goldenOptions(workspace, home)
	o.IsMacOS = true
	o.IsLinux = false
	o.PathExists = func(p string) bool { return p == hostNixSocket || p == hostNixStore }
	o.Getenv = func(k string) string { return env[k] }
	var stderr bytes.Buffer
	o.Stderr = &stderr
	return o, &stderr
}

// macosNixArgv composes the container argv for a minimal macOS podman launch.
func macosNixArgv(t *testing.T, o *Options) []string {
	t.Helper()
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	cfg := newConfig("agents", []any{"claude"}, "security", sec)
	return o.assembleRunCmd(&assembleInput{
		cfg:          cfg,
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		imageRef:     goldenImageRef,
		jailPrefix:   goldenJailPrefix,
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})
}

// storeMountArg is the one argv element the whole file is about.
func storeMountArg() string { return hostNixStore + ":" + hostNixStore + ":ro" }

// TestMacosVMShareAloneDoesNotMountTheHostStore is the regression for the 2026-09-13 macOS
// nightly (run 34778464086). Every launch there died as
//
//	yolo-entrypoint: exec: "bash": executable file not found in $PATH
//
// because flake.nix builds the image's /bin as symlinks into the store
// (`ln -s ${imagePkgs.bashInteractive}/bin/bash $out/bin/bash`) and this mount replaced the
// view they point into. The runner's image had been built on an ubuntu job and shipped as a
// tar, so its closure existed only in the image's own layers — nothing in the Mac's store.
//
// The workflow set YOLO_NIX_HOST_DAEMON for prefixUnreachableFromVM, which is a claim about
// reachability and nothing else. This row is that exact configuration.
func TestMacosVMShareAloneDoesNotMountTheHostStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, stderr := macosNixOptions(t, "/ws", home, map[string]string{
		"YOLO_NIX_HOST_DAEMON": "1",
	})

	argv := macosNixArgv(t, o)

	if slices.Contains(argv, storeMountArg()) {
		t.Errorf("argv mounts the host store on a Mac that only claimed to SHARE /nix:\n%v", argv)
	}
	if slices.Contains(argv, hostNixSocket+":"+hostNixSocket) {
		t.Errorf("argv mounts the host nix daemon socket:\n%v", argv)
	}
	if slices.Contains(argv, "NIX_REMOTE=daemon") {
		t.Error("argv sets NIX_REMOTE=daemon with no store mounted — nix would fail on a " +
			"socket that is not there")
	}
	// The narrowed variable says so, or "I set YOLO_NIX_HOST_DAEMON and nothing happened"
	// is a legitimate bug report (hostprobes.go, nixDelegationSkipNotice).
	if !strings.Contains(stderr.String(), nixHostStoreLinuxEnv) {
		t.Errorf("the launch did not name %s as the way back:\n%s", nixHostStoreLinuxEnv, stderr.String())
	}
}

// TestMacosBothClaimsMountTheHostStore is the other half: a Mac whose store really does hold
// the jail's Linux closure (a linux-builder VM, or a substituter that served it) still gets
// nested Nix. Without this row the fix would read as "macOS never mounts", which is the
// option that was considered and NOT taken — it removes a configuration that works.
func TestMacosBothClaimsMountTheHostStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, stderr := macosNixOptions(t, "/ws", home, map[string]string{
		"YOLO_NIX_HOST_DAEMON": "1",
		nixHostStoreLinuxEnv:   "1",
	})

	argv := macosNixArgv(t, o)

	if !slices.Contains(argv, storeMountArg()) {
		t.Errorf("argv does not mount the host store though both claims were made:\n%v", argv)
	}
	if !slices.Contains(argv, "NIX_REMOTE=daemon") {
		t.Errorf("argv does not set NIX_REMOTE=daemon:\n%v", argv)
	}
	// Nothing was skipped, so nothing is explained.
	if strings.Contains(stderr.String(), nixHostStoreLinuxEnv) {
		t.Errorf("the launch explained a skip it did not perform:\n%s", stderr.String())
	}
}

// TestMacosStoreClaimAloneMountsNothing pins the ASYMMETRY. The second claim is an
// additional one, not a replacement: a store full of Linux paths is unreachable from the
// container if the VM does not share /nix, and mounting it would be podman's unattributable
// `statfs …: no such file or directory` at rc 125 — the failure prefixUnreachableFromVM
// exists to convert into a sentence.
func TestMacosStoreClaimAloneMountsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, stderr := macosNixOptions(t, "/ws", home, map[string]string{
		nixHostStoreLinuxEnv: "1",
	})

	argv := macosNixArgv(t, o)

	if slices.Contains(argv, storeMountArg()) {
		t.Errorf("argv mounts a store the VM was never said to share:\n%v", argv)
	}
	// And this launch is told nothing: it never claimed the thing that was narrowed.
	if strings.Contains(stderr.String(), nixHostStoreLinuxEnv) {
		t.Errorf("a launch that never set YOLO_NIX_HOST_DAEMON was lectured about the split:\n%s",
			stderr.String())
	}
}

// TestLinuxStillMountsWithoutAnyDial is the blast-radius check. On Linux the host store IS
// the jail's system and holds the image's closure because that host built it, so nested Nix
// must keep working with no variable set at all — this is the default documented in
// userguide/guides/macos.md's own "exactly as on Linux".
//
// It reads goldenOptions unmodified except for the two nix paths, so a future edit that
// moved the macOS gate up out of the `if !isMacOS` arm fails here rather than in production.
func TestLinuxStillMountsWithoutAnyDial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	o.PathExists = func(p string) bool { return p == hostNixSocket || p == hostNixStore }
	var stderr bytes.Buffer
	o.Stderr = &stderr

	argv := macosNixArgv(t, o)

	if !slices.Contains(argv, storeMountArg()) {
		t.Errorf("a Linux launch stopped mounting the host store:\n%v", argv)
	}
	if strings.Contains(stderr.String(), nixHostStoreLinuxEnv) {
		t.Errorf("a Linux launch was told about a macOS-only dial:\n%s", stderr.String())
	}
}
