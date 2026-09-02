package run

// hostforwardgate_test.go pins the HOST-SIDE half of the applied-vs-configured network
// mode: the socat spawner, which was the fourth and last spelling of the port gate.
//
// nestedports_test.go holds the CONTAINER-SIDE half (the -p flags, the DNAT sysctl and
// the two env pairs), and a38fe0ab — the commit that moved those onto appliedNetMode —
// recorded in its own message that it had not moved this one: the socat spawner went on
// re-deriving the CONFIGURED mode inline, so the two halves of one feature answered to
// two different modes. They disagree in both directions, and only one of them is the
// harmless one the commit message describes:
//
//   - NESTED podman: the config says bridge, the launch applies host, so the assembler
//     emits neither the socket-dir mount nor YOLO_FORWARD_HOST_PORTS while this site
//     started one socat per declared forward and left a /tmp/yolo-fwd-<cname> dir behind.
//     Harmless (killed at exit; a shared netns needs no forwarding hop) but unwitnessed.
//   - APPLE CONTAINER with an unhonored `network.mode: "host"`: appliedNetMode answers
//     bridge there whatever the key says, so since a38fe0ab the assembler emits
//     `--publish-socket <hostSock>:…` for every declared forward — and this site, reading
//     the configured mode, declined to create those sockets. That direction is not
//     harmless: the jail's forwards are dead rather than merely unmentioned.
//
// Both are held below, and the second is the reason the fix is a fix rather than a
// narrowing: gating on the applied mode ADDS forwards on that backend at the same time
// as it drops them on a nested launch.

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// fakeSocatOnPath puts a `socat` on PATH that creates its UNIX-LISTEN socket file and
// then sleeps, so startHostPortForwarding's condition-poll succeeds immediately and a
// spawned forward is observable as both a live process handle and a socket file.
//
// Shared with TestStartHostPortForwardingSpawnsSocat (network_test.go), which wrote it
// first: the socat argv is a frozen contract (SocatArgv says so), so two hand-written
// fakes parsing it are two things that can come to disagree about it.
func fakeSocatOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	// The argv is `socat UNIX-LISTEN:<sock>,fork,mode=777 TCP:…`; take the path
	// between "UNIX-LISTEN:" and the first "," and touch it.
	script := `#!/bin/sh
arg="$1"
p="${arg#UNIX-LISTEN:}"
p="${p%%,*}"
: > "$p"
sleep 30
`
	if err := os.WriteFile(filepath.Join(bin, "socat"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

// forwardsConfig declares one `forward_host_ports` entry, with `network.mode` set only
// when mode is non-empty — an unset key leaves resolveNetMode answering with o.Network
// ("bridge" in goldenOptions), which is the default every ordinary jail launches under
// and the one a nested launch silently overrides.
func forwardsConfig(mode string) *jsonx.OrderedMap {
	net := jsonx.NewOrderedMap()
	net.Set("forward_host_ports", []any{"9090:5432"})
	if mode != "" {
		net.Set("mode", mode)
	}
	return newConfig("network", net)
}

// TestHostPortForwardingFollowsTheAppliedNetMode drives the production gate into the
// production spawner — o.hostForwardPorts(cfg, rt) then o.startHostPortForwarding(…),
// the two statements runContainer runs — and measures socats rather than a mode string.
// The wiring between them is pinned separately, by
// TestFreshLaunchGatesHostPortForwardingOnTheAppliedMode below; between the two, a
// revert of either the gate or its call site fails.
//
// What is deliberately NOT modelled here: the `rt == "container" || !o.IsMacOS`
// socket-dir condition (macOS podman forwards over a TCP gateway instead, and this
// change does not touch that). The dir is passed explicitly so every row spawns or
// declines for the mode reason alone.
func TestHostPortForwardingFollowsTheAppliedNetMode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rt         string
		configMode string
		nested     bool
		isMacOS    bool
		wantSocats int
	}{
		// The ordinary jail. Nothing about the fix may take its forwards away — the
		// goldens declare no ports, so nothing else would notice.
		{"podman bridge forwards", "podman", "", false, false, 1},
		// The residue a38fe0ab left: config bridge, applied host.
		{"nested podman forwards nothing", "podman", "", true, false, 0},
		// The user's own declaration. Dropped before the fix and after it.
		{"explicit host mode forwards nothing", "podman", "host", false, false, 0},
		// The direction the fix ADDS: appliedNetMode answers bridge on this backend
		// whatever the key says, and the assembler emits --publish-socket for these.
		{"Apple Container forwards despite an unhonored host mode", "container", "host", false, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeSocatOnPath(t)
			home := t.TempDir()
			t.Setenv("HOME", home)

			o := goldenOptions(t.TempDir(), home)
			o.IsMacOS, o.IsLinux = tc.isMacOS, !tc.isMacOS
			o.PathExists = func(p string) bool { return tc.nested && p == "/run/.containerenv" }

			socketDir := filepath.Join(t.TempDir(), "yolo-fwd-test")
			forwards := o.hostForwardPorts(forwardsConfig(tc.configMode), tc.rt)
			procs := o.startHostPortForwarding(forwards, "test", socketDir)
			t.Cleanup(func() { cleanupPortForwarding(procs, socketDir) })

			if len(procs) != tc.wantSocats {
				t.Fatalf("started %d socat(s), want %d (forwards=%v)", len(procs), tc.wantSocats, forwards)
			}
			// SocketPath, not a hand-spelled join: the path is the contract the
			// container-side socat dials, and retyping it here would assert nothing
			// about the spawner.
			sock := SocketPath(socketDir, 9090)
			_, err := os.Stat(sock)
			if tc.wantSocats == 0 {
				if err == nil {
					t.Errorf("a launch that forwards nothing must leave no socket behind, found %s", sock)
				}
				if _, err := os.Stat(socketDir); err == nil {
					t.Errorf("and no socket dir either, found %s", socketDir)
				}
				return
			}
			if err != nil {
				t.Errorf("declared forward left no host socket at %s: %v", sock, err)
			}
		})
	}
}

// TestFreshLaunchGatesHostPortForwardingOnTheAppliedMode is the CALL-SITE pin, and the
// gate above is worth nothing without it: runContainer starts a real container, so no
// unit test can reach the spawn, and the whole defect this change fixes was a gate that
// existed elsewhere while THIS site derived its own answer inline. Reading the source is
// this package's standing answer to that shape (TestFreshLaunchCallsTheConfigArtifactWriter,
// TestFreshLaunchChecksProviderCredentialsOnTheAssembledEnv), and an AST walk rather than
// a substring search keeps the match off a mention in a comment.
//
// Three claims, which together make the pre-fix code unrepresentable:
//
//  1. runContainer calls hostForwardPorts and BINDS the result;
//  2. that same identifier is what it hands startHostPortForwarding — a gate whose answer
//     the spawner does not receive is not a gate;
//  3. runContainer names no "forward_host_ports" literal of its own. Any re-derivation
//     has to read that config key by name, so its absence is what forbids the inline copy
//     coming back beside the call rather than instead of it.
//
// Statement ORDER needs no assertion of its own here: Go's scoping already forbids using
// the binding before the assignment that creates it.
func TestFreshLaunchGatesHostPortForwardingOnTheAppliedMode(t *testing.T) {
	const (
		gate    = "hostForwardPorts"
		spawner = "startHostPortForwarding"
		key     = `"forward_host_ports"`
	)
	fn := methodDecl(t, "run.go", "runContainer")

	// The identifier the gate's result is bound to, and the first argument the spawner
	// is handed. Both are collected at their FIRST occurrence: a later re-call must not
	// be what satisfies the assertions.
	gateBinding, spawnerArg := "", ""
	sawSpawner := false
	ast.Inspect(fn, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			// `x := o.hostForwardPorts(…)` / `x = o.hostForwardPorts(…)`
			if gateBinding != "" || len(node.Lhs) != 1 || len(node.Rhs) != 1 {
				return true
			}
			if !subtreeCalls(node.Rhs[0], gate) {
				return true
			}
			if id, ok := node.Lhs[0].(*ast.Ident); ok {
				gateBinding = id.Name
			}
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != spawner || sawSpawner {
				return true
			}
			sawSpawner = true
			if len(node.Args) > 0 {
				if id, ok := node.Args[0].(*ast.Ident); ok {
					spawnerArg = id.Name
				}
			}
		}
		return true
	})

	if gateBinding == "" {
		t.Fatalf("runContainer no longer binds the result of %s. The gate still exists and its "+
			"own test still passes, so the socat spawner would answer to whatever this site "+
			"derives instead — which for the whole life of this code was the CONFIGURED network "+
			"mode, the defect a38fe0ab fixed for the -p flags and left here. If the gate moved, "+
			"move this check with it rather than deleting it.", gate)
	}
	if !sawSpawner {
		t.Fatalf("runContainer no longer calls %s — a larger regression than the one this test "+
			"was written for", spawner)
	}
	if spawnerArg != gateBinding {
		t.Errorf("runContainer passes %q to %s, not the %q that %s returned: a gate whose answer "+
			"the spawner never sees is not a gate", spawnerArg, spawner, gateBinding, gate)
	}

	// (3) no inline re-derivation beside the call.
	ast.Inspect(fn, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || lit.Value != key {
			return true
		}
		t.Errorf("runContainer reads %s out of the config itself. That key is read in exactly "+
			"two places — the assembler's publish gate and %s — and both answer to the APPLIED "+
			"network mode; a third reading here is how the host half and the container half of "+
			"one feature came to disagree.", key, gate)
		return true
	})
}
