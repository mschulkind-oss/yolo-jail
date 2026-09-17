package packload_test

// packshippedjailbinary_test.go is the EXPERIMENT broker-as-a-pack.md §10's second
// sequencing step asks for, pinned: "a throwaway local pack whose `jail_daemon.cmd` is
// `["{jail_loophole_dir}/bin/hello"]` … converts 'the mechanism appears to exist' into
// 'the mechanism works'". The pack is packs/hello-daemon; its README records what the
// experiment measured and this file is what keeps the measurement true.
//
// # The result these tests encode
//
// A pack CAN ship the executable its jail-side daemon runs — through the CONFIGURED
// route (a pack selected by path, staged by internal/packstage, which carries the exec
// bit). It CANNOT through the EMBEDDED route, and that half is pinned too, as a negative,
// because it is the finding.
//
// # Why the assertions run the real pipeline instead of a fixture
//
// §3's inventory is a list of things that are "already built", and every row of it was
// true before anything had exercised them together. So each test below is chosen to fail
// if its CALL SITE is deleted, not merely if its function misbehaves:
//
//	the module-dir `-v`      internal/loopholes' runtimeArgsFor, gated on JailDaemon != nil
//	the token substitution   internal/loopholes' resolve, jail_daemon.Cmd
//	the exec bit             internal/packstage' copyFile, the 0o111 carry
//	the spawn                internal/supervisor' ParseEnv, over the argv payload
//
// Deleting any one of them leaves the pack loading fine, the footprint rendering fine and
// `yolo loopholes list` showing the loophole — and the daemon never running.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/supervisor"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// helloPackName is the pack's slug, which is also its directory name under packs/ and
// (because the manifest's `name` must equal its module dir's basename) the loophole's.
const helloPackName = "hello-daemon"

// helloProgramRel is the pack-relative path of the program the loophole runs. It is the
// tail of `{jail_loophole_dir}/bin/hello`, and spelling it once is what lets the tests
// below check the manifest's argv against the file that has to be there.
const helloProgramRel = "bin/hello"

// stageHelloPackByPath stages packs/hello-daemon the way a launch stages a pack the user
// selected BY PATH — `"packs": ["/abs/path/to/packs/hello-daemon"]`, which lowers to a
// file:// entry and reaches packstage.Stage — and returns the staged loophole MODULE dir.
//
// Out of the WORKTREE deliberately, where the other embedded-pack tests read packs.FS:
// the exec bit is the subject here and the embed channel does not carry one, so reading
// it from the binary would test the wrong artifact. That asymmetry is the finding.
func stageHelloPackByPath(t *testing.T) string {
	t.Helper()
	src := filepath.Join(repoRootFor(t), "packs", helloPackName)
	dest := filepath.Join(t.TempDir(), helloPackName)
	res, err := packstage.Stage(packstage.Spec{Root: src, Dest: dest})
	if err != nil {
		t.Fatalf("staging %s: %v", src, err)
	}
	if len(res.Staged) == 0 {
		t.Fatalf("staging %s copied nothing", src)
	}
	p, problems := packload.LoadDir(dest, helloPackName)
	if len(problems) > 0 {
		t.Fatalf("loading the staged pack: %v", problems)
	}
	granted, refused := p.HonoredLoopholes()
	if len(refused) > 0 {
		t.Fatalf("the pack's loophole contribution was refused: %v", refused)
	}
	if len(granted) != 1 {
		t.Fatalf("got %d loophole modules, want exactly 1", len(granted))
	}
	return granted[0].Dir
}

// enabledHelloSet discovers the staged module the way a launch does — through the pack
// module record, with the user's `loopholes.hello-daemon.enabled` override laid over the
// manifest's `default_enabled: false`.
//
// Both halves are the real thing: the record carries the origin gate (a Set built without
// one refuses every SourcePack crossing, by construction), and the override is the same
// config shape a user writes, because "how do I turn this on" is part of what the
// experiment is proving.
func enabledHelloSet(t *testing.T, moduleDir string) loopholes.Set {
	t.Helper()
	spec := jsonx.NewOrderedMap()
	spec.Set("enabled", true)
	cfg := jsonx.NewOrderedMap()
	cfg.Set(helloPackName, spec)
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		LoopholesConfig: cfg,
		PackModules:     []loopholes.PackModule{{Dir: moduleDir, HostExecApproved: true}},
	})
	lp, ok := set.Lookup(helloPackName)
	if !ok {
		t.Fatalf("the staged loophole was not discovered from %s", moduleDir)
	}
	if !lp.Active() {
		reason, _ := lp.InactiveReason()
		t.Fatalf("the loophole is not active: %s", reason)
	}
	return set
}

// A pack-shipped jail binary reaches the container argv — the mount, the resolved argv,
// and a file the kernel will actually execute.
//
// This is §10's experiment minus the launch. Everything below happens on the HOST, so it
// is all reachable from a unit test; what it cannot do is start a container, which is why
// the pack's README names the one command that closes the gap.
func TestAPackShippedJailBinaryReachesTheContainerArgv(t *testing.T) {
	moduleDir := stageHelloPackByPath(t)
	set := enabledHelloSet(t, moduleDir)

	args := set.RuntimeArgsFor(set.Enabled(), "podman")

	// 1. THE MOUNT. runtimeArgsFor emits this only because the manifest declares a
	// jail_daemon at all — delete that arm and the module dir, binary included, never
	// crosses into the jail while everything else about the pack keeps working.
	containerDir := loopholes.JailLoopholeDir(helloPackName)
	wantMount := moduleDir + ":" + containerDir + ":ro"
	if !hasFlagValue(args, "-v", wantMount) {
		t.Fatalf("the module dir is not mounted into the jail:\n  want -v %s\n  got  %v",
			wantMount, args)
	}
	// `:ro` AND NOTHING ELSE is what makes this work at all: §3's "the mount to permit
	// execution — satisfied by omission". A `noexec` appended here would be invisible to
	// every other test and would break exactly this mechanism.
	if strings.Contains(wantMount, "noexec") {
		t.Fatalf("the module-dir mount carries noexec, which forbids the very thing a "+
			"pack-shipped jail binary needs: %s", wantMount)
	}

	// 2. THE ARGV. The payload the in-jail supervisor reads is parsed back with the
	// supervisor's own parser rather than with a JSON literal, so this fails if the two
	// ever disagree about the shape.
	payload, ok := flagValue(args, "-e", "YOLO_JAIL_DAEMONS=")
	if !ok {
		t.Fatalf("no YOLO_JAIL_DAEMONS in the argv: %v", args)
	}
	specs := supervisor.ParseEnv(payload)
	if len(specs) != 1 {
		t.Fatalf("got %d supervised daemons from %q, want 1", len(specs), payload)
	}
	wantCmd := containerDir + "/" + helloProgramRel
	if len(specs[0].Cmd) != 1 || specs[0].Cmd[0] != wantCmd {
		t.Fatalf("the supervisor would run %v, want [%s] — %s resolves to the module dir's "+
			"CONTAINER mount point", specs[0].Cmd, wantCmd, loopholedecl.TokenJailLoopholeDir)
	}
	if strings.Contains(payload, loopholedecl.TokenJailLoopholeDir) {
		t.Fatalf("the token reached the supervisor unsubstituted: %s", payload)
	}

	// 3. THE FILE. The argv names a path under the container mount point; rebasing it
	// onto the staged dir is the same file the jail would exec. Running it is the only
	// assertion here that proves "executable" rather than "declared".
	hostProgram := filepath.Join(moduleDir, filepath.FromSlash(
		strings.TrimPrefix(wantCmd, containerDir+"/")))
	fi, err := os.Stat(hostProgram)
	if err != nil {
		t.Fatalf("the argv names %s, which the staged module dir does not contain: %v",
			wantCmd, err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("%s staged as %04o — a pack-shipped jail binary needs the exec bit, and "+
			"packstage.copyFile is what carries it (0o111 from the source)",
			hostProgram, fi.Mode().Perm())
	}
	out, err := exec.Command(hostProgram).CombinedOutput()
	if err != nil {
		t.Fatalf("running the staged program failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "hello") {
		t.Fatalf("the staged program ran but said %q", strings.TrimSpace(string(out)))
	}
}

// The pack-shipped subset accepts a loophole whose ONLY daemon is a jail-side one.
//
// §3's last inventory row — "the pack-shipped subset … never restricted `jail_daemon`" —
// asserted over the real manifest rather than over the list of rules, because the subset
// is a set of refusals and "there is no rule for this field" is exactly the kind of claim
// that stops being true when someone adds a rule for a neighbouring field. The
// `publishes` refusal is the one that would catch it: it fires on a DEFAULTED value too,
// and is skipped here only because there is no host_daemon to default.
func TestTheSubsetAcceptsAJailOnlyLoophole(t *testing.T) {
	moduleDir := stageHelloPackByPath(t)
	m, err := loopholedecl.LoadDirPackShipped(moduleDir)
	if err != nil {
		t.Fatalf("the pack-shipped subset refused a jail-daemon-only manifest: %v", err)
	}
	if m.HostDaemon != nil {
		t.Fatal("the experiment's manifest grew a host_daemon — it is supposed to cross " +
			"nothing, and with one it no longer isolates the jail-side mechanism")
	}
	if m.JailDaemon == nil || len(m.JailDaemon.Cmd) == 0 {
		t.Fatal("the experiment's manifest declares no jail_daemon, which is the whole of it")
	}
	if !strings.HasPrefix(m.JailDaemon.Cmd[0], loopholedecl.TokenJailLoopholeDir) {
		t.Fatalf("jail_daemon.cmd[0] = %q, want it to start with %s — the token is what §10 "+
			"asks the experiment to exercise", m.JailDaemon.Cmd[0], loopholedecl.TokenJailLoopholeDir)
	}
	if m.Transport != loopholedecl.TransportNone {
		t.Fatalf("transport = %q; a loophole with no host daemon has no transport, and %q is "+
			"the enum's word for that", m.Transport, loopholedecl.TransportNone)
	}
}

// THE NEGATIVE HALF, and it is the finding: the EMBEDDED channel cannot deliver an
// executable, so `packs: ["hello-daemon"]` — the bare-name spelling — stages a program
// the jail cannot run.
//
// Two independent causes, asserted separately because fixing either one alone changes
// nothing:
//
//	embed.FS reports 0444 for every file, whatever its mode on disk. Nothing downstream
//	can recover a bit that was never carried.
//
//	packload.copyEmbeddedTree then writes 0o644 unconditionally. Its comment says
//	"packstage enforces the same rule for configured packs" — packstage does the
//	OPPOSITE now (its copyFile carries 0o111 deliberately), and internal/cli/run's own
//	copyTree carries it too, so this is the last stripper standing.
//
// ⚠ IF THIS TEST FAILS BECAUSE THE BITS NOW SURVIVE, that is the gap closing rather than
// a regression: update packs/hello-daemon/README.md's result table, which currently says
// the embedded route cannot work.
func TestTheEmbeddedChannelCannotDeliverAnExecutable(t *testing.T) {
	embeddedProgram := helloPackName + "/loopholes/" + helloPackName + "/" + helloProgramRel
	fi, err := packs.FS.Open(embeddedProgram)
	if err != nil {
		t.Fatalf("%s is not embedded — extend the //go:embed directive in packs/embed.go "+
			"(and check the goSrc fileset in flake.nix): %v", embeddedProgram, err)
	}
	defer fi.Close()
	info, err := fi.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("embed.FS reported %04o for %s — it used to report 0444 for every file, and "+
			"packs/hello-daemon/README.md's result table depends on that",
			info.Mode().Perm(), embeddedProgram)
	}

	loaded, problems := packload.MaterializeEmbedded(packs.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	var root string
	for _, p := range loaded {
		if p.Name == helloPackName {
			root = p.Root
		}
	}
	if root == "" {
		t.Fatalf("the %q pack is not embedded — extend the //go:embed directive in "+
			"packs/embed.go", helloPackName)
	}
	materialized := filepath.Join(root, "loopholes", helloPackName,
		filepath.FromSlash(helloProgramRel))
	mi, err := os.Stat(materialized)
	if err != nil {
		t.Fatal(err)
	}
	if mi.Mode().Perm()&0o111 != 0 {
		t.Fatalf("%s materialized as %04o — the embedded route now carries the exec bit, so "+
			"packs/hello-daemon/README.md's result table is out of date (and the bare-name "+
			"selection may now work)", materialized, mi.Mode().Perm())
	}
}

// hasFlagValue reports whether args carries `flag value` as an adjacent pair.
func hasFlagValue(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

// flagValue returns the remainder of the first `flag <prefix>…` pair in args.
func flagValue(args []string, flag, prefix string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && strings.HasPrefix(args[i+1], prefix) {
			return strings.TrimPrefix(args[i+1], prefix), true
		}
	}
	return "", false
}
