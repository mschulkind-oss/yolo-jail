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

// TestTheFreshLaunchNotesTheIOPriority pins the fresh-launch call site, which a unit fixture
// cannot reach (runContainer runs the whole launch): runContainer must call noteIOPriority,
// after the launch banner, so the line is on screen when the container takes the terminal.
func TestTheFreshLaunchNotesTheIOPriority(t *testing.T) {
	fn := methodDecl(t, "run.go", "runContainer")
	var banner, note int
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			switch sel.Sel.Name {
			case "emitLaunchBanner":
				if banner == 0 {
					banner = int(call.Pos())
				}
			case "noteIOPriority":
				note = int(call.Pos())
			}
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
}

// ioAttach drives the real attachExisting to its exec, as attachExecArgv does, over a
// running jail whose frozen environment is frozenEnv, with this invocation's config cfg and
// the workspace's disk described by kyber. It returns what the attach printed to stderr.
func ioAttach(t *testing.T, cfg *jsonx.OrderedMap, frozenEnv string) string {
	t.Helper()
	home := packHome(t)
	emptyLoopholeDirs(t)
	o := goldenOptions(t.TempDir(), home)
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
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	channel := channelFor(t, o, cfg, nil, nil)
	if rc, _ := o.attachExisting("yolo-attach-ioprio", "podman", "true", cfg, stagedPacks{}, channel, false, nil); rc != 0 {
		t.Fatalf("the attach did not run through to its exec (rc=%d):\n%s", rc, &stderr)
	}
	return stderr.String()
}

// TestAnAttachRepeatsTheIOPriorityLineForTheLaunchedValue: an attach grades the value the
// jail was LAUNCHED with, read from its frozen environment, because that is the value the
// attached shell's entrypoint applies. So a jail launched with "low" repeats the line even
// when the config no longer declares it, and a jail launched without the variable prints
// nothing even when the config now declares it (the edit waits for a fresh launch).
func TestAnAttachRepeatsTheIOPriorityLineForTheLaunchedValue(t *testing.T) {
	const line = `resources.io.priority "low" has no effect on nvme0n1 (scheduler kyber)`
	res := jsonx.NewOrderedMap()
	res.Set("io", "low")
	declared := newConfig("resources", res)

	if got := ioAttach(t, newConfig(), "YOLO_VERSION=9.9.9-test\n"+ioprio.EnvVar+"=low\n"); !strings.Contains(got, line) {
		t.Errorf("an attach to a jail launched with low did not repeat the line:\n%s", got)
	}
	if got := ioAttach(t, declared, "YOLO_VERSION=9.9.9-test\n"); strings.Contains(got, "resources.io.priority") {
		t.Errorf("an attach to a jail launched WITHOUT the variable graded the edited config:\n%s", got)
	}
}
