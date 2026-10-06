package run

import (
	"bytes"
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio/iopriotest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ioPriorityEnv returns the value of every `-e YOLO_IO_PRIORITY=` pair in argv.
func ioPriorityEnv(argv []string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-e" && strings.HasPrefix(argv[i+1], ioprio.EnvVar+"=") {
			out = append(out, strings.TrimPrefix(argv[i+1], ioprio.EnvVar+"="))
		}
	}
	return out
}

// ioPriorityBriefingLine returns the briefing's "- **Disk I/O priority**" line, or "".
func ioPriorityBriefingLine(briefing string) string {
	for _, line := range strings.Split(briefing, "\n") {
		if strings.HasPrefix(line, "- **Disk I/O priority**") {
			return line
		}
	}
	return ""
}

// THE CALL-SITE PIN for the argv and the briefing, one row per backend (IO-D2): the value
// reaches the container environment on podman on Linux, nested included, and nowhere else;
// the briefing states a class exactly where the argv carries one, and never as a limit.
func TestTheIOPriorityReachesTheArgvAndTheBriefingTogether(t *testing.T) {
	obj := jsonx.NewOrderedMap()
	obj.Set("priority", "idle")
	for _, tc := range []struct {
		name    string
		rt      string
		macOS   bool
		nested  bool
		io      any
		want    string // "" = not passed
		inBrief string
	}{
		{"podman on Linux", "podman", false, false, "low", "low", "`low` (best effort, level 7)"},
		{"podman nested in a jail", "podman", false, true, "low", "low", "`low`"},
		{"the object form", "podman", false, false, obj, "idle", "`idle` (idle class)"},
		{"normal declares nothing", "podman", false, false, "normal", "", ""},
		{"an empty object declares nothing", "podman", false, false, jsonx.NewOrderedMap(), "", ""},
		{"unset", "podman", false, false, nil, "", ""},
		{"podman on macOS", "podman", true, false, "low", "", ""},
		{"Apple Container", "container", true, false, "low", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := appliedOptions(t, t.TempDir(), home, tc.nested)
			o.IsMacOS, o.IsLinux = tc.macOS, !tc.macOS
			o.Stderr = discardBuf()
			cfg := appliedTestConfig()
			if tc.io != nil {
				res := jsonx.NewOrderedMap()
				res.Set("io", tc.io)
				cfg.Set("resources", res)
			}
			got := ioPriorityEnv(appliedArgv(t, o, tc.rt, cfg, t.TempDir()))
			if tc.want == "" && len(got) != 0 || tc.want != "" && (len(got) != 1 || got[0] != tc.want) {
				t.Errorf("argv carries %s=%q, want %q", ioprio.EnvVar, got, tc.want)
			}
			line := ioPriorityBriefingLine(appliedBriefing(t, o, tc.rt, cfg))
			if tc.inBrief == "" && line != "" {
				t.Errorf("the briefing states a class the argv never passed: %q", line)
			}
			if tc.inBrief != "" {
				if !strings.Contains(line, tc.inBrief) {
					t.Errorf("briefing line %q, want it to state %q", line, tc.inBrief)
				}
				if strings.Contains(line, "kernel-enforced") || !strings.Contains(line, "Advisory") {
					t.Errorf("the briefing must call the class advisory, never enforced: %q", line)
				}
			}
		})
	}
}

// TestTheMacosUserLaunchBriefsTheDiskPolicyItSets is the run pipeline's half of build step 5
// (io-priority.md §5.5): appliedIOPriority answers the declaration on macos-user, because the
// orchestrator sets it as the launcher's disk policy before the session starts, and the
// briefing the arm writes names that policy, in macOS words and never in Linux ones. The
// macos-user arm's refreshJailBriefings call passing appliedIOPriority is pinned by
// TestTheFreshLaunchNotesTheIOPriority; this row is what fails if the answer it reads is Normal.
func TestTheMacosUserLaunchBriefsTheDiskPolicyItSets(t *testing.T) {
	for _, tc := range []struct {
		io      any
		want    ioprio.Priority
		inBrief string
	}{
		{"low", ioprio.Low, "`low` (IOPOL_UTILITY)"},
		{"idle", ioprio.Idle, "`idle` (IOPOL_THROTTLE)"},
		{"normal", ioprio.Normal, ""},
		{nil, ioprio.Normal, ""},
	} {
		res := jsonx.NewOrderedMap()
		if tc.io != nil {
			res.Set("io", tc.io)
		}
		if got := appliedIOPriority("macos-user", true, res); got != tc.want {
			t.Errorf("io=%v: appliedIOPriority on macos-user = %q, want %q", tc.io, got, tc.want)
		}
		home := t.TempDir()
		t.Setenv("HOME", home)
		emptyLoopholeDirs(t)
		o := appliedOptions(t, t.TempDir(), home, false)
		o.IsMacOS, o.IsLinux = true, false
		o.Stderr = discardBuf()
		cfg := appliedTestConfig()
		if tc.io != nil {
			cfg.Set("resources", res)
		}
		line := ioPriorityBriefingLine(appliedBriefing(t, o, "macos-user", cfg))
		if tc.inBrief == "" {
			if line != "" {
				t.Errorf("io=%v: the briefing states a policy nothing set: %q", tc.io, line)
			}
			continue
		}
		if !strings.Contains(line, tc.inBrief) || !strings.Contains(line, "Advisory") {
			t.Errorf("io=%v: briefing line %q, want it to state %q, advisory", tc.io, line, tc.inBrief)
		}
		for _, never := range []string{"scheduler", "writeback", "kernel-enforced", "best effort"} {
			if strings.Contains(line, never) {
				t.Errorf("io=%v: the macos-user line says %q: %q", tc.io, never, line)
			}
		}
	}
}

// ioNoteOptions is a podman/Linux host whose workspace is wsPath inside the fake root sys.
func ioNoteOptions(t *testing.T, sys *iopriotest.Sys, wsPath string) (*Options, *bytes.Buffer) {
	t.Helper()
	o := goldenOptions(wsPath, t.TempDir())
	o.ioSysRoot = sys.Root
	var stderr bytes.Buffer
	o.Stderr = &stderr
	return o, &stderr
}

// TestNoteIOPriorityNamesTheDiskThatIgnoresIt: the disclosure prints exactly where the
// grading says the value does nothing, names the key, the disk and its scheduler, and
// prints nothing for a disk that honors it or a path that cannot be graded.
func TestNoteIOPriorityNamesTheDiskThatIgnoresIt(t *testing.T) {
	const ws = "/yolo-test-ws/project"
	for _, tc := range []struct {
		name string
		sys  *iopriotest.Sys
		p    ioprio.Priority
		want string // "" = no line
	}{
		{"kyber under LUKS", iopriotest.Kyber(t, ws), ioprio.Low,
			`resources.io.priority "low" has no effect on nvme0n1 (scheduler kyber), the disk under ` + ws},
		{"mq-deadline and low", iopriotest.Scheduler(t, ws, "mq-deadline"), ioprio.Low,
			`"low" has no effect on sda (scheduler mq-deadline)`},
		{"none and idle", iopriotest.Scheduler(t, ws, "none"), ioprio.Idle, `"idle" has no effect on sda (scheduler none)`},
		{"mq-deadline and idle", iopriotest.Scheduler(t, ws, "mq-deadline"), ioprio.Idle, ""},
		{"bfq", iopriotest.Scheduler(t, ws, "bfq"), ioprio.Low, ""},
		{"tmpfs", iopriotest.New(t).Mount(ws, "tmpfs", "tmpfs"), ioprio.Low, ""},
		{"unreadable sysfs", iopriotest.New(t).Mount(ws, "ext4", "/dev/sda1"), ioprio.Low, ""},
		{"normal on kyber", iopriotest.Kyber(t, ws), ioprio.Normal, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, stderr := ioNoteOptions(t, tc.sys, ws)
			o.noteIOPriority("podman", tc.p)
			if tc.want == "" {
				if stderr.Len() != 0 {
					t.Errorf("printed %q, want nothing", stderr)
				}
				return
			}
			if !strings.Contains(stderr.String(), tc.want) || !strings.Contains(stderr.String(), "yolo check") {
				t.Errorf("printed %q, want a line containing %q that points at yolo check", stderr, tc.want)
			}
			if strings.Count(stderr.String(), "\n") != 1 {
				t.Errorf("the disclosure is ONE line: %q", stderr)
			}
		})
	}
}

// TestNoteIOPriorityWarnsOnTheVirtiofsBackends: Apple Container and podman on macOS never
// pass the value, and say so by name, whatever the disk — a line only when declared.
func TestNoteIOPriorityWarnsOnTheVirtiofsBackends(t *testing.T) {
	for _, tc := range []struct{ rt, backend string }{
		{"container", "Apple Container"},
		{"podman", "podman on macOS"},
	} {
		o, stderr := ioNoteOptions(t, iopriotest.New(t), "/yolo-test-ws/project")
		o.IsMacOS = true
		o.noteIOPriority(tc.rt, ioprio.Idle)
		if !strings.Contains(stderr.String(), `resources.io.priority "idle" is NOT applied on `+tc.backend) ||
			!strings.Contains(stderr.String(), "VirtioFS") {
			t.Errorf("%s: printed %q, want the Warned line naming VirtioFS", tc.rt, stderr)
		}
		stderr.Reset()
		o.noteIOPriority(tc.rt, ioprio.Normal)
		if stderr.Len() != 0 {
			t.Errorf("%s: an undeclared priority printed %q", tc.rt, stderr)
		}
	}
}

// argCallee is the name of the function argument i of call calls, or "" when that
// argument is not a call.
func argCallee(call *ast.CallExpr, i int) string {
	if i < 0 || i >= len(call.Args) {
		return ""
	}
	if c, ok := call.Args[i].(*ast.CallExpr); ok {
		return skelCallee(c)
	}
	return ""
}

// TestTheFreshLaunchNotesTheIOPriority pins the fresh-launch call sites, which a unit fixture
// cannot reach (runContainer runs the whole launch):
//
//   - runContainer calls noteIOPriority after the launch banner, so the line is on screen when
//     the container takes the terminal, and about the DECLARED value (ioprio.FromResources).
//     The passed value (appliedIOPriority) is Normal on Apple Container and podman on macOS,
//     where the Warned line is the whole behavior, so handing it that value silences them.
//   - every fresh refreshJailBriefings call — runContainer's and the macos-user arm's in Run —
//     briefs appliedIOPriority, the value the argv passes. appliedBriefing, which the argv and
//     briefing table drives, passes the same expression; this is what ties it to run.go.
func TestTheFreshLaunchNotesTheIOPriority(t *testing.T) {
	fn := methodDecl(t, "run.go", "runContainer")
	var banner, note int
	var noteArg string
	var refreshArgs []string
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch skelCallee(call) {
		case "emitLaunchBanner":
			if banner == 0 {
				banner = int(call.Pos())
			}
		case "noteIOPriority":
			note, noteArg = int(call.Pos()), argCallee(call, 1)
		case "refreshJailBriefings":
			refreshArgs = append(refreshArgs, argCallee(call, 4))
		}
		return true
	})
	if note == 0 {
		t.Fatal("runContainer never calls noteIOPriority: a declared priority the disk ignores " +
			"would launch in silence (docs/design/io-priority.md §5.2)")
	}
	if banner == 0 || note < banner {
		t.Error("noteIOPriority must follow the launch banner, beside warnIfNoPacks")
	}
	if noteArg != "FromResources" {
		t.Errorf("runContainer's noteIOPriority is about %q, want the declaration (ioprio.FromResources): "+
			"the VM backends' Warned line is about a value that is never passed", noteArg)
	}
	fd := funcDecl(t, "run.go", "Run")
	ast.Inspect(fd, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && skelCallee(call) == "refreshJailBriefings" {
			refreshArgs = append(refreshArgs, argCallee(call, 4))
		}
		return true
	})
	if len(refreshArgs) != 2 {
		t.Fatalf("found %d fresh refreshJailBriefings calls in runContainer and Run, want 2; re-anchor this pin", len(refreshArgs))
	}
	for _, a := range refreshArgs {
		if a != "appliedIOPriority" {
			t.Errorf("a fresh refreshJailBriefings briefs %q, want appliedIOPriority, the value the argv passes", a)
		}
	}
}

// ioAttached is what one driven attach left behind: its stderr, and the briefing line it
// staged for the claude pack's destination ("" when the briefing states no priority).
type ioAttached struct {
	stderr, briefing string
}

// ioAttach drives the real attachExisting to its exec, as attachExecArgv does, over a
// running jail on runtime rt whose frozen environment is frozenEnv, with this invocation's
// config cfg and the workspace's disk described by kyber. The staged packs are the claude
// pack, so the attach's briefing refresh writes the file an agent in the jail reads.
func ioAttach(t *testing.T, rt string, macOS bool, cfg *jsonx.OrderedMap, frozenEnv string) ioAttached {
	t.Helper()
	home := packHome(t)
	emptyLoopholeDirs(t)
	o := goldenOptions(t.TempDir(), home)
	o.IsMacOS, o.IsLinux = macOS, !macOS
	// The fake mount table names the RESOLVED workspace, because Resolve evaluates
	// symlinks on the path (a TMPDIR behind a link, as on macOS).
	ws, err := filepath.EvalSymlinks(o.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	o.ioSysRoot = iopriotest.Kyber(t, ws).Root
	var stderr bytes.Buffer
	o.Stdout, o.Stderr = discardBuf(), &stderr
	o.Getenv = func(string) string { return "" }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "inspect" {
			return ExecResult{Ran: true, RC: 0, Stdout: frozenEnv}
		}
		return ExecResult{Ran: false}
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, rt), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	const cname = "yolo-attach-ioprio"
	packs := claudePackFixture(t)
	channel := channelFor(t, o, cfg, packs, nil)
	if rc, _ := o.attachExisting(cname, rt, "true", cfg, stagedPacks{packs: packs}, channel, false, nil); rc != 0 {
		t.Fatalf("the attach did not run through to its exec (rc=%d):\n%s", rc, &stderr)
	}
	body, err := os.ReadFile(filepath.Join(paths.AgentsDir(), cname, briefingStagingName(claudeBriefingDest)))
	if err != nil {
		t.Fatalf("the attach staged no briefing for the claude pack: %v\n%s", err, &stderr)
	}
	return ioAttached{stderr: stderr.String(), briefing: ioPriorityBriefingLine(string(body))}
}

// ioDeclared is a config declaring resources.io = v.
func ioDeclared(v string) *jsonx.OrderedMap {
	res := jsonx.NewOrderedMap()
	res.Set("io", v)
	return newConfig("resources", res)
}

// TestAnAttachRepeatsTheIOPriorityLineForTheLaunchedValue: an attach grades the value the
// jail was LAUNCHED with, read from its frozen environment, because that is the value the
// attached shell's entrypoint applies. So a jail launched with "low" repeats the line even
// when the config no longer declares it, and a jail launched without the variable prints
// nothing even when the config now declares it (the edit waits for a fresh launch).
func TestAnAttachRepeatsTheIOPriorityLineForTheLaunchedValue(t *testing.T) {
	const line = `resources.io.priority "low" has no effect on nvme0n1 (scheduler kyber)`
	if got := ioAttach(t, "podman", false, newConfig(), "YOLO_VERSION=9.9.9-test\n"+ioprio.EnvVar+"=low\n"); !strings.Contains(got.stderr, line) {
		t.Errorf("an attach to a jail launched with low did not repeat the line:\n%s", got.stderr)
	}
	if got := ioAttach(t, "podman", false, ioDeclared("low"), "YOLO_VERSION=9.9.9-test\n"); strings.Contains(got.stderr, "resources.io.priority") {
		t.Errorf("an attach to a jail launched WITHOUT the variable graded the edited config:\n%s", got.stderr)
	}
}

// TestAnAttachBriefsTheIOPriorityTheJailWasLaunchedWith: the briefing an attach re-renders
// states the class the jail's processes hold, which is the one in its frozen environment —
// never the current config's (IO-D2, §5.2: "where it was not passed, the briefing says
// nothing about it"). The attach's stderr line and its briefing must agree.
func TestAnAttachBriefsTheIOPriorityTheJailWasLaunchedWith(t *testing.T) {
	for _, tc := range []struct {
		name, frozen string
		cfg          *jsonx.OrderedMap
		want         string // "" = no briefing line
	}{
		{"launched without, config now idle", "YOLO_VERSION=9.9.9-test\n", ioDeclared("idle"), ""},
		{"launched low, key since removed", "YOLO_VERSION=9.9.9-test\n" + ioprio.EnvVar + "=low\n", newConfig(), "`low`"},
		{"launched low, config now idle", "YOLO_VERSION=9.9.9-test\n" + ioprio.EnvVar + "=low\n", ioDeclared("idle"), "`low`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ioAttach(t, "podman", false, tc.cfg, tc.frozen)
			if tc.want == "" && got.briefing != "" {
				t.Errorf("the attach briefed a class no process in the jail holds: %q", got.briefing)
			}
			if tc.want != "" && !strings.Contains(got.briefing, tc.want) {
				t.Errorf("the attach briefed %q, want the launched value %s", got.briefing, tc.want)
			}
		})
	}
}

// TestAnAttachOnAVirtiofsBackendWarnsForTheDeclaration: Apple Container and podman on macOS
// never pass a value, so an attach prints the Warned line for the current declaration
// (IO-D10) and briefs no class, whatever the frozen environment holds.
func TestAnAttachOnAVirtiofsBackendWarnsForTheDeclaration(t *testing.T) {
	for _, tc := range []struct {
		rt, backend string
	}{
		{"container", "Apple Container"},
		{"podman", "podman on macOS"},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			got := ioAttach(t, tc.rt, true, ioDeclared("low"), "YOLO_VERSION=9.9.9-test\n")
			if !strings.Contains(got.stderr, `resources.io.priority "low" is NOT applied on `+tc.backend) {
				t.Errorf("the attach did not print the Warned line for %s:\n%s", tc.backend, got.stderr)
			}
			if got.briefing != "" {
				t.Errorf("the attach briefed a class %s never passes: %q", tc.backend, got.briefing)
			}
			if got := ioAttach(t, tc.rt, true, newConfig(), "YOLO_VERSION=9.9.9-test\n"); strings.Contains(got.stderr, "resources.io.priority") {
				t.Errorf("an undeclared priority printed a line on %s:\n%s", tc.backend, got.stderr)
			}
		})
	}
}
