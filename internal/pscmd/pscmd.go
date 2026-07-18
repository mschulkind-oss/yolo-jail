// Package pscmd is the Go port of the `yolo ps` command body (src/cli/run_cmd.py
// ps()). It lists running yolo-* jails, resolves each to its workspace, prunes
// stale tracking files, and flags problem jails. The parsing + rendering are the
// already-ported internal/runtime engines; this package is the thin
// orchestration with every subprocess behind an injectable seam so the whole
// command is unit-testable and golden-able (the check/run precedent).
//
// Output is plain typer.echo (not rich), so it is byte-parity with Python.
//
// Source of truth: src/cli/run_cmd.py:ps().
package pscmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// Deps are the injectable seams. RunCmd runs argv and returns stdout (stderr
// discarded, matching capture_output + text); a spawn error yields ("", err) and
// the caller degrades. DetectRuntime returns the effective runtime ("podman" /
// "container"). PathIsDir reports whether a workspace path is an existing dir.
type Deps struct {
	DetectRuntime func() string
	RunCmd        func(argv []string) (string, error)
	PathIsDir     func(path string) bool
	Out           io.Writer
}

// Run executes `yolo ps`, writing the table to deps.Out, and returns the exit
// code (always 0 — ps never fails the process). Mirrors ps() exactly:
// list → parse → resolve workspace → prune stale tracking → render → problems.
func Run(deps Deps) int {
	rt := deps.DetectRuntime()

	var rows []runtime.PsRow
	if rt == "container" {
		out, _ := deps.RunCmd([]string{"container", "ls"})
		rows = runtime.ParseContainerLsRows(out)
	} else {
		out, _ := deps.RunCmd([]string{rt, "ps", "--filter", "name=^yolo-", "--format", "{{.Names}}\t{{.Status}}\t{{.RunningFor}}"})
		rows = runtime.ParsePodmanPsRows(out)
	}

	if len(rows) == 0 {
		fmt.Fprintln(deps.Out, "No running jails.")
		// Clean up ALL stale tracking files (nothing is running).
		runtime.PruneStaleTrackingFiles(map[string]struct{}{})
		return 0
	}

	// Resolve each row's workspace (tracking file first, then inspect env).
	type resolved struct {
		name, status, workspace string
	}
	var containers []resolved
	running := map[string]struct{}{}
	for _, r := range rows {
		ws := getContainerWorkspace(deps, r.Name, rt)
		containers = append(containers, resolved{r.Name, r.Status, ws})
		running[r.Name] = struct{}{}
	}

	// Prune stale tracking files (keep only running names).
	runtime.PruneStaleTrackingFiles(running)

	// Render the table via the ported renderer.
	pcs := make([]runtime.PsContainer, len(containers))
	for i, c := range containers {
		pcs[i] = runtime.PsContainer{Name: c.name, Status: c.status, Workspace: c.workspace}
	}
	if table := runtime.RenderPsTable(pcs); table != "" {
		fmt.Fprintln(deps.Out, table)
	}

	// Problem jails: workspace-gone or stuck-in-provisioning.
	var problems [][2]string
	for _, c := range containers {
		if c.workspace != "unknown" && !deps.PathIsDir(c.workspace) {
			problems = append(problems, [2]string{c.name, "workspace gone"})
			continue
		}
		if reason := stuckReason(deps, c.name, rt); reason != "" {
			problems = append(problems, [2]string{c.name, reason})
		}
	}
	if len(problems) > 0 {
		fmt.Fprintf(deps.Out, "\n⚠  %d problem jail(s):\n", len(problems))
		for _, p := range problems {
			fmt.Fprintf(deps.Out, "  %s  (%s)\n", p[0], p[1])
		}
		fmt.Fprintln(deps.Out, "\n  Run 'yolo doctor' to clean up")
	}
	return 0
}

// getContainerWorkspace resolves a container's workspace: the tracking file
// first (fast), then the runtime inspect's YOLO_HOST_DIR env; "unknown" when
// neither yields a value. Mirrors _get_container_workspace.
func getContainerWorkspace(deps Deps, name, rt string) string {
	if ws, ok := runtime.ReadContainerWorkspace(name); ok {
		return ws
	}
	if rt == "container" {
		// Apple Container inspect emits JSON (no --format). The env lives under
		// config.env; ReadContainerWorkspace already covered the tracking file,
		// so parse the inspect JSON for YOLO_HOST_DIR here.
		out, err := deps.RunCmd([]string{"container", "inspect", name})
		if err == nil {
			if ws, ok := runtime.WorkspaceFromContainerInspectJSON(out); ok {
				return ws
			}
		}
		return "unknown"
	}
	out, err := deps.RunCmd([]string{rt, "inspect", name, "--format", "{{range .Config.Env}}{{println .}}{{end}}"})
	if err == nil {
		if ws, ok := runtime.WorkspaceFromInspectEnv(strings.Split(out, "\n")); ok {
			return ws
		}
	}
	return "unknown"
}

// stuckReason returns the stuck-in-provisioning reason for a container, or "".
// Apple Container has no `top`, so it's never checked there (matches
// _check_container_stuck's early return). Mirrors the exec side of
// _check_container_stuck around the ported StuckReasonFromTop analyzer.
func stuckReason(deps Deps, name, rt string) string {
	if rt == "container" {
		return ""
	}
	out, err := deps.RunCmd([]string{rt, "top", name, "-eo", "comm"})
	if err != nil {
		return ""
	}
	return runtime.StuckReasonFromTop(out)
}

// RealDeps returns Deps backed by real subprocesses / filesystem.
func RealDeps(runCmd func(argv []string) (string, error), detectRuntime func() string) Deps {
	return Deps{
		DetectRuntime: detectRuntime,
		RunCmd:        runCmd,
		PathIsDir: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && info.IsDir()
		},
		Out: os.Stdout,
	}
}
