package run

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
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

// A TARGET MADE NOW IS SAID AT ONCE, by the arm and before the backend: anything between here
// and the backend's own per-launch line (the approval prompt, a precondition, the account-home
// hold, the nix build) may still end the launch, and a folder yolo made and opened to another
// account must not be left behind unsaid. The stub backend prints nothing, so the line is the
// arm's. Fails if the arm's disclosure is deleted.
func TestTheMacosUserArmSaysATargetItCreatesAtOnce(t *testing.T) {
	target := filepath.Join(floortest.ResolvedTemp(t), "hf")
	relocLaunchHome(t, target)
	_, out := runMacosUserCapturingCtx(t, floortest.ResolvedTemp(t), func(o *Options) { grantRecorder(o, 0, "") })
	want := "Cache relocation: created " + target + " and opened it to " + macosuser.SandboxUser +
		" for ~/.cache/huggingface"
	if !strings.Contains(out, want) {
		t.Errorf("the arm did not say %q:\n%s", want, out)
	}
	// A target that was already there was not made now, and is not said to be.
	existing := floortest.ResolvedTemp(t)
	relocLaunchHome(t, existing)
	_, out = runMacosUserCapturingCtx(t, floortest.ResolvedTemp(t), func(o *Options) { grantRecorder(o, 0, "") })
	if strings.Contains(out, "Cache relocation: created") {
		t.Errorf("an existing target was said to be created:\n%s", out)
	}
}

// A GRANT THAT FAILS IS SAID, NOT FATAL (CR-D4's gates are the backend's DAC preflight and write
// probe): on a volume that takes no access entries — an exFAT or FAT drive — `yolo
// macos-fix-permissions` runs the same `chmod +a` and fails the same way, so naming it would be
// a wrong fix, and such a volume usually ignores ownership, so the sandbox can write the folder
// anyway. The backend is handed the failure, which is what its refusal reads if a probe fails.
func TestAnUngrantableCreatedTargetIsSaidAndHandedToTheBackend(t *testing.T) {
	target := filepath.Join(floortest.ResolvedTemp(t), "hf")
	relocLaunchHome(t, target)
	ctx, out := runMacosUserCapturingCtx(t, floortest.ResolvedTemp(t), func(o *Options) {
		grantRecorder(o, 1, "chmod: Operation not supported")
	})
	if len(ctx.Relocations) != 1 || !ctx.Relocations[0].Created ||
		!strings.Contains(ctx.Relocations[0].GrantFailure, "Operation not supported") {
		t.Fatalf("the backend was handed %+v, want the created target with its grant failure\n%s", ctx.Relocations, out)
	}
	for _, want := range []string{"Warning: created " + target, "Operation not supported",
		"does not support them", "stops if it cannot"} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "macos-fix-permissions") {
		t.Errorf("a grant that failed named `yolo macos-fix-permissions`, which fails the same way:\n%s", out)
	}
}

// A TARGET THAT IS A FILE refuses at the siting, with its next steps, so a dry run refuses too.
func TestARelocationTargetThatIsAFileRefusesADryRunToo(t *testing.T) {
	target := filepath.Join(floortest.ResolvedTemp(t), "hf")
	if err := os.WriteFile(target, []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	relocLaunchHome(t, target)
	for _, dry := range []bool{true, false} {
		var grants *[][]string
		out := runMacosUserExpectingRefusal(t, floortest.ResolvedTemp(t), func(o *Options) {
			o.DryRun = dry
			grants = grantRecorder(o, 0, "")
		})
		for _, want := range []string{"cannot deliver every cache_relocations entry",
			"~/.cache/huggingface → " + target + ": it is a file, not a folder",
			"~/.config/yolo-jail/config.jsonc", "remove it"} {
			if !strings.Contains(out, want) {
				t.Errorf("dry run %v: the refusal does not say %q:\n%s", dry, want, out)
			}
		}
		if len(*grants) != 0 {
			t.Errorf("dry run %v: a refused target was granted access: %v", dry, *grants)
		}
	}
}

// relocTwoLaunchHome writes a user config relocating aaa to a and bbb to b (applied in key order).
func relocTwoLaunchHome(t *testing.T, a, b string) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	ctxLaunchHome(t, `, "cache_relocations": {"aaa": "`+a+`", "bbb": "`+b+`"}`)
}

// A TARGET yolo CANNOT MAKE refuses naming what to do, and every target made before it is still
// granted and said. ensureMacosRelocationTargets is stubbed to fail the second entry, as a mkdir
// under a folder only an administrator may write does, which a test running as root cannot
// arrange (the test below arranges it for real where it can).
func TestARelocationTargetYoloCannotMakeNamesTheNextStep(t *testing.T) {
	parent := floortest.ResolvedTemp(t)
	a, b := filepath.Join(parent, "a"), filepath.Join(parent, "b")
	relocTwoLaunchHome(t, a, b)
	prev := ensureMacosRelocationTargets
	t.Cleanup(func() { ensureMacosRelocationTargets = prev })
	ensureMacosRelocationTargets = func(rels []config.CacheRelocation) ([]bool, error) {
		created, err := prev(rels[:1])
		if err != nil {
			t.Fatal(err)
		}
		return append(created, make([]bool, len(rels)-1)...),
			fmt.Errorf("cache_relocations.%s: creating target %s: mkdir %s: permission denied", rels[1].Subdir, rels[1].Target, rels[1].Target)
	}
	var grants *[][]string
	out := runMacosUserExpectingRefusal(t, floortest.ResolvedTemp(t), func(o *Options) {
		grants = grantRecorder(o, 0, "")
	})
	for _, want := range []string{"Refusing the macos-user launch: cache_relocations.bbb: creating target " + b,
		"Create the folder yourself", "~/.config/yolo-jail/config.jsonc", "remove the entry",
		"Cache relocation: created " + a + " and opened it to " + macosuser.SandboxUser} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	aces := macosuser.CacheRelocationACECommands(a)
	if len(*grants) != len(aces) || (*grants)[0][len((*grants)[0])-1] != a {
		t.Errorf("the target made before the failure was not granted: %v", *grants)
	}
}

// THE SAME, FOR REAL: a target under a folder its user may not write. Skipped as root, who may
// write any folder; CI's runners and check-macos run it.
func TestARelocationTargetUnderAFolderYouCannotWriteRefusesWithTheNextStep(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may create a folder anywhere")
	}
	ro := filepath.Join(floortest.ResolvedTemp(t), "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	target := filepath.Join(ro, "hf")
	relocLaunchHome(t, target)
	out := runMacosUserExpectingRefusal(t, floortest.ResolvedTemp(t), func(o *Options) { grantRecorder(o, 0, "") })
	for _, want := range []string{"creating target " + target, "Create the folder yourself",
		"~/.config/yolo-jail/config.jsonc"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
}

// A TARGET INSIDE A `mounts` SOURCE refuses at the arm, before the target is made or granted and
// before the nix build: the arm hands the siting the launch's delivered context links, and
// CR-D5's overlap rule refuses on them. Without them the target would be made and `chmod +a`'d
// inside the user's own source folder, pass the preflight, and be refused only by PlanInvariants
// once the nix build had run. Fails if the arm stops passing the links.
func TestARelocationInsideAMountsSourceRefusesBeforeAnythingIsMade(t *testing.T) {
	lib := filepath.Join(floortest.ResolvedTemp(t), "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(lib, "hf")
	t.Setenv("YOLO_VERSION", "")
	ctxLaunchHome(t, `, "mounts": ["`+lib+`:/ctx/lib"], "cache_relocations": {"huggingface": "`+target+`"}`)
	var grants *[][]string
	out := runMacosUserExpectingRefusal(t, floortest.ResolvedTemp(t), func(o *Options) {
		deliveringSiting(t, "")(o)
		grants = grantRecorder(o, 0, "")
	})
	for _, want := range []string{"cannot deliver every cache_relocations entry",
		"source " + lib + " (`mounts`)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if _, err := os.Lstat(target); err == nil {
		t.Errorf("the refused target %s was made inside the mounts source", target)
	}
	if len(*grants) != 0 {
		t.Errorf("a refused target was granted access: %v", *grants)
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
