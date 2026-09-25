package run

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE CLASS BEHIND ISSUE #44, pinned the way machinetierparity_test.go pins #39's and
// hosthometier_test.go pins the pack-`mount` defect's: by comparing what the assembler
// ACTUALLY EMITS against a rule, rather than by reading the source or trusting a comment.
//
// The invariant: **a container's environment may not name a path that only exists on the
// host.** A jail opens what its env vars point at — YOLO_PACK_ROOT is read by the
// entrypoint, which walks it and renders every pack's surfaces — so a var carrying a host
// path is not a degraded jail, it is a jail that finds nothing at that path and boots with
// no agent at all. That is #44, and it is the failure mode the backend README recommends
// for macOS.
//
// WHY THIS TEST EXISTS BESIDE THE CENSUS. backendparity_test.go enumerates every branch on
// the runtime's identity and requires each to be classified. It would mark #44's site
// declared and be satisfied, because the branch IS deliberate: assemble.go's Apple Container
// arm skips the mount and passes the host path instead, on the stated belief that "the AC
// host filesystem is visible". The census cannot check a belief. This test can, because the
// belief is falsifiable from the argv alone: a path with no mount carrying it is not
// visible, on any container backend, and Apple Container runs each container in its own
// Virtualization.framework VM (userguide/guides/macos.md), which is the strongest possible
// version of "not visible".
//
// This is backend-parity.md §4's residue 1 — "a census prevents SILENT; it cannot prevent
// WRONG" — answered for the one shape where wrong is mechanically detectable.
//
// WHAT IT DOES NOT COVER: a host path that reaches the jail some other way (a generated
// script's contents, a file the launch writes into wsState, an argv position that is not
// `-e`); and the opposite error, a JAIL path handed to something host-side. Both are real
// and neither is visible here.
func TestNoJailEnvVarNamesAnUnreachableHostPath(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			env, hostRoots, dests := jailEnvForRuntime(t, rt)
			if len(env) < 10 || len(dests) < 4 {
				t.Fatalf("the %s argv yielded %d env pairs and %d mount destinations — the "+
					"extractor has stopped matching, so this test would pass by finding "+
					"nothing", rt, len(env), len(dests))
			}
			// The fixture must actually exercise the classifier, or a green here means only
			// that nothing was looked at. YOLO_HOST_DIR is the one var that always carries a
			// host path, which is why it is the probe as well as the carve-out.
			if hostRootIn(env["YOLO_HOST_DIR"], hostRoots) == "" {
				t.Fatalf("no env value on the %s argv lies under a host root at all — the "+
					"fixture's HOME/workspace separation has stopped holding and the rule is "+
					"being applied to nothing", rt)
			}

			var offenders []string
			for _, key := range sortedEnvKeys(env) {
				value := env[key]
				if _, ok := hostPathEnvByDesign[key]; ok {
					continue
				}
				root := hostRootIn(value, hostRoots)
				if root == "" || underAnyDest(value, dests) {
					continue
				}
				if reason, known := knownUnreachableHostPathEnv[rt+"/"+key]; known {
					t.Logf("KNOWN DEFECT, still present: %s carries a host path on %s — %s",
						key, rt, reason)
					continue
				}
				offenders = append(offenders, key+"="+value+"  (host root: "+root+")")
			}

			for _, o := range offenders {
				t.Errorf("%s: the jail is given a host path it cannot open:\n    %s\n\n"+
					"Nothing mounts that path into the container, so whatever reads this var "+
					"finds nothing there — which is issue #44 exactly, not a degraded feature "+
					"but a jail that comes up empty.\n\n"+
					"Three ways out, all already used in this package: mount the directory and "+
					"pass the CONTAINER path (what podman does for YOLO_PACK_ROOT); "+
					"acMaterialize a copy into wsState, which Apple Container already binds "+
					"whole at /home/agent, and pass the path it lands at; or, if the value is a "+
					"LABEL the jail never opens, add it to hostPathEnvByDesign with the reason.",
					rt, o)
			}
		})
	}

	// The ratchet's other direction: a known defect that is FIXED must lose its row, or the
	// next one to appear under that key is waved through by a comment describing a bug that
	// no longer exists.
	for key := range knownUnreachableHostPathEnv {
		rt, name, _ := strings.Cut(key, "/")
		env, hostRoots, dests := jailEnvForRuntime(t, rt)
		if v, ok := env[name]; !ok || hostRootIn(v, hostRoots) == "" || underAnyDest(v, dests) {
			t.Errorf("knownUnreachableHostPathEnv still lists %s, and it no longer carries a "+
				"host path on %s. Delete the row — a stale waiver is how the next instance "+
				"gets in without anyone deciding.", name, rt)
		}
	}
}

// hostPathEnvByDesign names the vars whose value is a host path ON PURPOSE, because nothing
// in the jail ever opens them. The distinction is the whole test: a LABEL is fine, a
// HANDLE is not.
var hostPathEnvByDesign = map[string]string{
	// The workspace's host path, recorded so the jail can SAY where it came from and so a
	// pack hook can key on it. Read by the shell prompt (entrypoint/shell.go), pack hooks
	// (entrypoint/packhooks.go), `yolo config ls`'s ownership check (cli/configls.go) and
	// the host-side `yolo ps` display (runtime/display.go) — every one of them treats it as
	// a string. Nothing opens it; the workspace itself is at /workspace.
	"YOLO_HOST_DIR": "a label for where the workspace came from, never opened in-jail",
}

// knownUnreachableHostPathEnv is the DEFECT list, not a waiver list: each row is a live bug
// this test has caught and that is not fixed yet. It is empty, and the way it emptied is the
// argument for having it.
//
// It was written with two rows — `container/YOLO_PACK_ROOT` (issue #44: the staged pack tree
// neither mounted nor copied, so the entrypoint rendered no packs at all) and
// `container/YOLO_CAPTURES_DIR` (the same premise, "Apple Container reads host paths
// directly", applied to the install-capture store) — because the fixes live in files this
// change could not touch, and landing a red test blocks every commit. Both were fixed while
// this was being written, and the stale-row check below is what SAID so, by failing on a row
// whose subject had gone. That is the property a waiver list does not have: a row here cannot
// outlive its bug.
//
// Add a row only with an issue number and the reason the fix is not a one-liner. Delete it
// the moment the fix lands — this test will insist.
var knownUnreachableHostPathEnv = map[string]string{}

// jailEnvForRuntime assembles one launch and returns its `-e KEY=VALUE` pairs plus the host
// roots that count as "outside the container" for this fixture.
//
// The fixture deliberately keeps HOME and the workspace in separate temp dirs — the same
// trick hostHomeSourcedMounts uses — so "a host path" is expressible at all; in a real
// launch the workspace usually sits under the home.
func jailEnvForRuntime(t *testing.T, rt string) (env map[string]string, hostRoots, mountDests []string) {
	t.Helper()
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)

	o := goldenOptions(ws, home)
	o.IsMacOS = rt == "container"
	o.IsLinux = !o.IsMacOS
	// goldenOptions stubs PathExists to a flat false so a frozen argv cannot depend on the
	// developer's disk. The capture-store arm is gated on the store existing, so this
	// fixture needs the real stat back or that whole branch is unreachable and the
	// YOLO_CAPTURES_DIR half of this test measures nothing (capturesmount_test.go's
	// fixture does the same, for the same reason).
	o.PathExists = func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}

	wsState := filepath.Join(ws, ".yolo", "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	// A staged pack tree, because the var this test was written for only appears when one
	// exists. Without it the container arm emits nothing and the test passes vacuously.
	packStaging := filepath.Join(home, ".local", "share", "yolo-jail", "packs")
	if err := os.MkdirAll(packStaging, 0o755); err != nil {
		t.Fatal(err)
	}
	// A capture store, for the same reason: the arm that names it only runs when one is
	// on disk.
	capturesDir := filepath.Join(home, ".local", "share", "yolo-jail", "captures")
	if err := os.MkdirAll(capturesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})

	argv := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("security", sec),
		rt:           rt,
		cname:        "yolo-ws-abcd1234",
		packs:        claudePackFixture(t),
		agentsPath:   filepath.Join(ws, "agents"),
		wsState:      wsState,
		packStaging:  packStaging,
		capturesDir:  capturesDir,
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})

	env = map[string]string{}
	for i := 0; i+1 < len(argv); i++ {
		switch argv[i] {
		case "-e", "--env":
			if k, v, ok := strings.Cut(argv[i+1], "="); ok {
				env[k] = v
			}
			i++
		case "-v", "--volume":
			spec := argv[i+1]
			if _, dest, ok := strings.Cut(spec, ":"); ok {
				mountDests = append(mountDests, strings.TrimSuffix(dest, ":ro"))
			}
			i++
		}
	}
	// paths.GlobalStorage() is under HOME in this fixture, but it is named anyway: it is
	// relocatable by env, and a root that silently stops being a root is how a census
	// stops covering the thing it was written for.
	return env, []string{home, ws, paths.GlobalStorage()}, mountDests
}

// hostRootIn returns the host root a value lies under, or "" when it names nothing
// host-side. A SUBSTRING test rather than a prefix one, because a var can carry a host path
// inside a list or a JSON document (YOLO_MCP_SERVERS, YOLO_LSP_SERVERS) and a prefix test
// would miss every one of those.
func hostRootIn(value string, roots []string) string {
	for _, r := range roots {
		if r == "" || r == "/" {
			continue
		}
		if strings.Contains(value, r+string(filepath.Separator)) || value == r {
			return r
		}
	}
	return ""
}

// underAnyDest reports whether the value names a path the container can actually open,
// which on this argv means a path at or below some mount DESTINATION.
//
// This is the half that makes the rule self-maintaining, and it is why no allowlist grows
// here: fix #44 by binding the staged tree at /ctx/packs, or by materializing it into
// wsState and naming /home/agent/…, and the value stops being a host path at all — it comes
// out under a dest and passes with nothing added to either map below. The check is kept
// separate from the host-root test rather than folded into it because the two answer
// different questions ("is this outside the container" / "is it nevertheless reachable"),
// and an identity mount — a host path bound at itself — is the one case where both are yes.
func underAnyDest(value string, dests []string) bool {
	for _, d := range dests {
		if d == "" || d == "/" {
			continue
		}
		if value == d || strings.HasPrefix(value, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func sortedEnvKeys(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k := range env {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestTheHostPathRuleWouldHaveCaughtIssue44 is the guard on the guard, and it is here for
// AGENTS.md's rule that a test which passes when its subject is deleted is not a test.
//
// TestNoJailEnvVarNamesAnUnreachableHostPath is GREEN today because both defects it was
// written against were fixed while it was being written. A green test whose subject has been
// repaired proves nothing about the test — weaken hostRootIn, or widen underAnyDest to
// accept anything, and it stays green forever. So the rule is exercised directly here
// against the two values the assembler ACTUALLY EMITTED on the Apple Container arm, before
// and after the fix.
//
// The before values are copied from the argv as it stood on 2026-09-14 (measured, not
// reconstructed): YOLO_PACK_ROOT named the launcher's own staging directory and
// YOLO_CAPTURES_DIR named the machine capture store, each on the belief that "Apple
// Container reads host paths directly". If either shape ever comes back, the argv test
// above fails — and this one proves the argv test can still tell the difference.
func TestTheHostPathRuleWouldHaveCaughtIssue44(t *testing.T) {
	const home = "/Users/someone"
	const ws = "/Users/someone/code/proj"
	hostRoots := []string{home, ws, home + "/.local/share/yolo-jail"}

	// The Apple Container destination set, which is the short list backend-parity.md §2's
	// tell is really about: everything else is host-side and stays there.
	acDests := []string{"/workspace", "/home/agent", "/home/agent/.cache", "/mise",
		"/home/agent/.claude-shared-credentials", "/opt/yolo-jail/bin"}

	for _, c := range []struct {
		name      string
		value     string
		reachable bool
	}{
		{"issue #44, as emitted: the staged pack tree by host path",
			home + "/.local/share/yolo-jail/agents/yolo-ws-abcd1234/packs", false},
		{"issue #44, as fixed: the copy under the wsState bind",
			"/home/agent/.yolo-packs", true},
		{"the capture store by host path, the same premise one key over",
			home + "/.local/share/yolo-jail/captures", false},
		{"the capture store as podman names it",
			"/ctx/captures", false}, // not host-side at all, so the rule never looks
		{"a workspace-relative host path, which /workspace covers only by luck of name",
			ws + "/.yolo/home/packs", false},
		{"an image path the jail owns", "/tmp/mise-cache", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			isHostSide := hostRootIn(c.value, hostRoots) != ""
			flagged := isHostSide && !underAnyDest(c.value, acDests)
			wantFlagged := isHostSide && !c.reachable
			if flagged != wantFlagged {
				t.Fatalf("%q: flagged=%v, want %v (host-side=%v, under a dest=%v).\n\n"+
					"This is the predicate TestNoJailEnvVarNamesAnUnreachableHostPath is "+
					"built on. If it stops separating these, that test goes green on a jail "+
					"that finds nothing at the path it was handed.",
					c.value, flagged, wantFlagged, isHostSide, underAnyDest(c.value, acDests))
			}
		})
	}

	// And the direction that matters most: the pre-fix value must be flagged, full stop.
	pre := home + "/.local/share/yolo-jail/agents/yolo-ws-abcd1234/packs"
	if hostRootIn(pre, hostRoots) == "" || underAnyDest(pre, acDests) {
		t.Error("the exact value issue #44 shipped is not flagged by this rule — whatever " +
			"else this file tests, it does not test #44")
	}
}
