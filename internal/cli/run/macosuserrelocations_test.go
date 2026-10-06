package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// macosuserrelocations_test.go pins the host half of `cache_relocations` on macos-user
// (macosuserrelocations.go): the arm reads the USER config alone, resolves, sites, makes and
// grants each target, and hands the result to the backend in HostContext.Relocations — or
// refuses the launch before the backend is reached.
//
// ⚠ Run(), for macosctxtree_test.go's reason: a unit test of the planner passes with the arm's
// call deleted, and a missing call is exactly the defect, since every other reader of the key is
// below the arm's return. The planner is called directly only where a Run() would be refused
// for an unrelated reason first (a workspace-scope key is a preflight error).

// relocLaunchHome writes a user config selecting claude and relocating huggingface to target,
// and returns the home. YOLO_VERSION is emptied so config.InJail answers "host" when this test
// itself runs inside a jail, where the loader returns nothing on purpose.
func relocLaunchHome(t *testing.T, target string) string {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	return ctxLaunchHome(t, `, "cache_relocations": {"huggingface": "`+target+`"}`)
}

// grantRecorder answers every `chmod +a` the arm runs with rc (and stderr) and records it,
// leaving every other command to the options' own stub.
func grantRecorder(o *Options, rc int, stderr string) *[][]string {
	var rec [][]string
	prev := o.Exec
	o.Exec = func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		if len(argv) >= 2 && argv[1] == "+a" {
			rec = append(rec, append([]string(nil), argv...))
			return ExecResult{Ran: true, RC: rc, Stderr: stderr}
		}
		return prev(argv, dir, env, timeout)
	}
	return &rec
}

// THE CALL SITE: a user-scope relocation reaches the backend resolved, its missing target made
// as you and granted the shared root's two inheriting entries, and recorded as created.
func TestTheMacosUserArmDeliversAUserScopeRelocation(t *testing.T) {
	parent := floortest.ResolvedTemp(t)
	target := filepath.Join(parent, "hf")
	relocLaunchHome(t, target)
	var grants *[][]string
	ctx, out := runMacosUserCapturingCtx(t, floortest.ResolvedTemp(t), func(o *Options) {
		grants = grantRecorder(o, 0, "")
	})
	want := []macosuser.CacheRelocation{{Subdir: "huggingface", Target: target, Named: target, Created: true}}
	if len(ctx.Relocations) != 1 || ctx.Relocations[0] != want[0] {
		t.Fatalf("the backend was handed %+v, want %+v\n%s", ctx.Relocations, want, out)
	}
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		t.Errorf("the missing target %s was not made: %v", target, err)
	}
	aces := macosuser.CacheRelocationACECommands(target)
	if len(*grants) != len(aces) {
		t.Fatalf("the arm ran %d grants, want %d: %v", len(*grants), len(aces), *grants)
	}
	for i, argv := range *grants {
		if strings.Join(argv, "\x00") != strings.Join(aces[i], "\x00") {
			t.Errorf("grant %d = %v, want %v", i, argv, aces[i])
		}
	}
}

// A TARGET THAT WAS ALREADY THERE is delivered as it is: not recorded as created, and not
// granted anything (the backend's preflight asks whether the sandbox can use it).
func TestTheMacosUserArmLeavesAnExistingTargetsAccessAlone(t *testing.T) {
	target := floortest.ResolvedTemp(t)
	relocLaunchHome(t, target)
	var grants *[][]string
	ctx, out := runMacosUserCapturingCtx(t, floortest.ResolvedTemp(t), func(o *Options) {
		grants = grantRecorder(o, 0, "")
	})
	if len(ctx.Relocations) != 1 || ctx.Relocations[0].Created || ctx.Relocations[0].Target != target {
		t.Fatalf("the backend was handed %+v\n%s", ctx.Relocations, out)
	}
	if len(*grants) != 0 {
		t.Errorf("an existing target was granted access: %v", *grants)
	}
}

// A TARGET THROUGH A SYMLINK is handed over RESOLVED, since the profile names it verbatim, and
// keeps the spelling the user wrote for messages.
func TestTheMacosUserArmResolvesARelocationTarget(t *testing.T) {
	real := floortest.ResolvedTemp(t)
	link := filepath.Join(floortest.ResolvedTemp(t), "via")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	relocLaunchHome(t, link)
	ctx, out := runMacosUserCapturingCtx(t, floortest.ResolvedTemp(t), func(o *Options) { grantRecorder(o, 0, "") })
	if len(ctx.Relocations) != 1 || ctx.Relocations[0].Target != real || ctx.Relocations[0].NamedTarget() != link {
		t.Fatalf("the backend was handed %+v, want %s resolved to %s\n%s", ctx.Relocations, link, real, out)
	}
}

// A DRY RUN MAKES AND GRANTS NOTHING, and its plan still names the relocation.
func TestAMacosUserDryRunMakesNoRelocationTarget(t *testing.T) {
	target := filepath.Join(floortest.ResolvedTemp(t), "hf")
	relocLaunchHome(t, target)
	var grants *[][]string
	ctx, out := runMacosUserCapturingCtx(t, floortest.ResolvedTemp(t), func(o *Options) {
		o.DryRun = true
		grants = grantRecorder(o, 0, "")
	})
	if len(ctx.Relocations) != 1 || ctx.Relocations[0].Created {
		t.Fatalf("the dry run handed %+v\n%s", ctx.Relocations, out)
	}
	if _, err := os.Lstat(target); err == nil {
		t.Errorf("a dry run made the target %s", target)
	}
	if len(*grants) != 0 {
		t.Errorf("a dry run granted access: %v", *grants)
	}
}

// A RELOCATION THE SITING REFUSES ends the launch before the backend, naming why and the next
// steps, and makes no directory where it was refused.
func TestTheMacosUserArmRefusesARelocationIntoAHome(t *testing.T) {
	users := floortest.ResolvedTemp(t)
	target := filepath.Join(users, "alice", "hf")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	relocLaunchHome(t, target)
	out := runMacosUserExpectingRefusal(t, floortest.ResolvedTemp(t), func(o *Options) {
		s := sitingWritable(t, "")
		s.UsersRoot, s.UsersRootAliases = users, nil
		o.macosCtxSiting = s
		grantRecorder(o, 0, "")
	})
	for _, want := range []string{"cannot deliver every cache_relocations entry",
		"~/.cache/huggingface → " + target, "inside the home folder " + filepath.Join(users, "alice"),
		"`runtime: \"podman\"`", "~/.config/yolo-jail/config.jsonc"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if _, err := os.Lstat(target); err == nil {
		t.Errorf("the refused target %s was made", target)
	}
}

// A GRANT THAT FAILS refuses the launch naming the command that applies it by hand.
func TestTheMacosUserArmRefusesWhenTheTargetCannotBeGranted(t *testing.T) {
	target := filepath.Join(floortest.ResolvedTemp(t), "hf")
	relocLaunchHome(t, target)
	out := runMacosUserExpectingRefusal(t, floortest.ResolvedTemp(t), func(o *Options) {
		grantRecorder(o, 1, "chmod: Operation not supported")
	})
	for _, want := range []string{"could not be opened to the sandbox account", "Operation not supported",
		"yolo macos-fix-permissions " + target} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
}

// A WORKSPACE-SCOPE KEY NEVER REACHES THE BACKEND: the planner reads the user config alone, so
// an entry in yolo-jail.jsonc — the agent's to write — relocates nothing (the preflight also
// refuses it, which is why this asks the planner and not Run()).
func TestAWorkspaceScopeRelocationNeverReachesTheMacosUserArm(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	ctxLaunchHome(t, "")
	ws := floortest.ResolvedTemp(t)
	target := filepath.Join(floortest.ResolvedTemp(t), "hf")
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"),
		[]byte(`{"cache_relocations": {"huggingface": "`+target+`"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	grants := grantRecorder(o, 0, "")
	relocs, ok := o.planMacosUserCacheRelocations(nil)
	if !ok || len(relocs) != 0 || len(*grants) != 0 {
		t.Fatalf("a workspace-scope entry was delivered: %+v (ok %v, grants %v)\n%s", relocs, ok, *grants, stderr.String())
	}
	if _, err := os.Lstat(target); err == nil {
		t.Errorf("a workspace-scope entry made its target %s", target)
	}
}

// UNDER THE SEAL there are none, whatever the user config says (seal.go).
func TestASealedMacosUserLaunchRelocatesNothing(t *testing.T) {
	target := filepath.Join(floortest.ResolvedTemp(t), "hf")
	relocLaunchHome(t, target)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, floortest.ResolvedTemp(t), "macos-user", &stdout, &stderr, nil)
	o.Sealed = true
	if relocs, ok := o.planMacosUserCacheRelocations(nil); !ok || len(relocs) != 0 {
		t.Fatalf("a sealed launch delivered %+v (ok %v)", relocs, ok)
	}
	if _, err := os.Lstat(target); err == nil {
		t.Errorf("a sealed launch made the target %s", target)
	}
}
