package integration

// attachskew_test.go is the real-runtime half of the attach contract gate
// (internal/cli/run/contracttags.go; docs/design/attach-skew-and-contract-guardrails.md). The
// unit tier drives every disposition against a faked runtime; what only a real podman can say
// is whether the gate reads a real container's frozen environment and its live exec sessions
// the way the fake assumes. attachskewrestart_linux_test.go drives the restart at a real pty.
//
// The "older jail" is a container these tests start themselves, named for the workspace, whose
// frozen environment carries a version and neither the contract tags nor the per-agent marker —
// exactly what an attach can see of a jail a pre-gate yolo launched. Its binaries are never
// reached: the gate acts before any exec.

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// startStandInOlderJail sets up a workspace selecting claude=zai and runs, under its jail's
// name, a container that looks to an attach like a jail a pre-gate yolo launched. It returns
// the workspace and the container's name; the container is removed at cleanup.
func startStandInOlderJail(t *testing.T) (dir, cname string) {
	t.Helper()
	rt := detectRuntime()
	if rt != "podman" {
		t.Skipf("the stand-in older jail is a podman container; runtime is %q", rt)
	}
	image := imageExists(rt)
	if image == "" {
		t.Skip("no jail image loaded to run the stand-in older jail from")
	}
	dir = writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", "zai"]}`)
	t.Setenv("ZAI_API_KEY", "integration-probe-not-a-real-key")
	cname = naming.FromWorkspace(dir)

	ctx, cancel := context.WithTimeout(context.Background(), jailTimeout())
	defer cancel()
	// PID 1 is a shell that EXITS ON SIGTERM, not a bare `sleep`. A process that is PID 1 in its
	// namespace gets no default signal disposition, so a bare sleep ignored every stop's SIGTERM
	// and each one waited out its whole timeout before SIGKILL: the restart's `podman stop -t 5`
	// in TestAttachRestartsAnOlderJailAtATerminal and the cleanup's `rm -f` (10s) here —
	// measured ~15s of the two tests' ~29s. What an attach can see of the container is
	// unchanged: its name, its frozen environment, its exec sessions.
	if out, err := exec.CommandContext(ctx, rt, "run", "-d", "--rm", "--name", cname,
		"--network=none", "-e", "YOLO_VERSION=0.10.0", "--entrypoint", "/bin/sh", image,
		"-c", `trap "exit 0" TERM; sleep 600 & wait`).CombinedOutput(); err != nil {
		t.Fatalf("starting the stand-in older jail: %v\n%s", err, out)
	}
	t.Cleanup(func() { forceRemoveContainer(dir) })
	return dir, cname
}

func TestAttachRefusesAJailThatCannotReceiveTheSelection(t *testing.T) {
	requireJail(t)
	dir, cname := startStandInOlderJail(t)
	rt := detectRuntime()

	// One live exec, the way an attached session is: the refusal must count it.
	exec1 := exec.Command(rt, "exec", cname, "/bin/sleep", "300")
	if err := exec1.Start(); err != nil {
		t.Fatalf("starting an exec session in the stand-in jail: %v", err)
	}
	t.Cleanup(func() { _ = exec1.Process.Kill(); _ = exec1.Wait() })
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, _ := exec.Command(rt, "inspect", "--format", "{{len .ExecIDs}}", cname).Output()
		if strings.TrimSpace(string(out)) == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the exec session never appeared in the stand-in jail's ExecIDs (%q)", out)
		}
		time.Sleep(100 * time.Millisecond)
	}

	r := runCommand(t, dir, append(jailRunArgs(), "-p", "claude=zai", "--", "true"))
	if r.rc != 1 {
		t.Fatalf("an attach whose selection the older jail cannot receive must refuse without a "+
			"terminal: rc %d\n%s", r.rc, r.combined())
	}
	got := r.combined()
	for _, want := range []string{"Refusing to attach", "This jail runs yolo 0.10.0", "agent-env-files",
		"claude (profile zai): ", "ANTHROPIC_BASE_URL", "every session in it (2 running now)",
		"'yolo stop'", "YOLO_ALLOW_ATTACH_SKEW=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must name %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Attaching to existing jail") {
		t.Errorf("a refused attach announced itself as attaching:\n%s", got)
	}
	if n := runningContainers(t, cname); n != 1 {
		t.Errorf("a refusal must leave the running jail alone; %d containers named %s run", n, cname)
	}
}
