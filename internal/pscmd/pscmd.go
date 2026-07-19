// Package pscmd implements the `yolo ps` command. It lists running yolo-*
// jails, resolves each to its workspace, prunes stale tracking files, and
// flags problem jails. Every subprocess is behind an injectable seam for
// unit-testability.
package pscmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// Deps are the injectable seams. RunCmd runs argv and returns (stdout, ok):
// ok=false means the probe could NOT enumerate (spawn/exec failure or non-zero
// exit) — the tri-state that must never be collapsed to "no jails" (D11).
// DetectRuntime returns the effective runtime ("podman" / "container"),
// platform-aware for ps. PathIsDir reports whether a workspace path exists.
type Deps struct {
	DetectRuntime func() string
	RunCmd        func(argv []string) (string, bool)
	PathIsDir     func(path string) bool
	Out           io.Writer
}

// Run executes `yolo ps`, writing the table to deps.Out, and returns the exit
// code (always 0 — ps never fails the process). Mirrors ps() exactly:
// list → parse → resolve workspace → prune stale tracking → render → problems.
func Run(deps Deps) int {
	rt := deps.DetectRuntime()

	// The runtime probe is TRI-STATE (audit 2026-07-18 §B / D11): a spawn/exec
	// error means "could not enumerate", which must NEVER be read as "no jails"
	// — pruning the tracking dir on an unconfirmed-empty set deletes the files
	// for LIVE jails (the destructive macOS-AC bug). Only a probe that actually
	// ran (ok=true) authorizes the stale-tracking prune.
	var rows []runtime.PsRow
	var enumerated bool
	if rt == "container" {
		out, ok := deps.RunCmd([]string{"container", "ls"})
		enumerated = ok
		if ok {
			rows = runtime.ParseContainerLsRows(out)
		}
	} else {
		out, ok := deps.RunCmd([]string{rt, "ps", "--filter", "name=^yolo-", "--format", "{{.Names}}\t{{.Status}}\t{{.RunningFor}}"})
		enumerated = ok
		if ok {
			rows = runtime.ParsePodmanPsRows(out)
		}
	}

	if !enumerated {
		// Could not talk to the runtime — decline to prune (fail-safe), and say
		// so rather than the misleading "No running jails."
		fmt.Fprintf(deps.Out, "Could not query the %s runtime for running jails.\n", rt)
		return 0
	}

	if len(rows) == 0 {
		fmt.Fprintln(deps.Out, "No running jails.")
		// Enumeration succeeded and returned nothing → safe to prune all stale
		// tracking files.
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
		out, ok := deps.RunCmd([]string{"container", "inspect", name})
		if ok {
			if ws, ok := runtime.WorkspaceFromContainerInspectJSON(out); ok {
				return ws
			}
		}
		return "unknown"
	}
	out, ok := deps.RunCmd([]string{rt, "inspect", name, "--format", "{{range .Config.Env}}{{println .}}{{end}}"})
	if ok {
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
	out, ok := deps.RunCmd([]string{rt, "top", name, "-eo", "comm"})
	if !ok {
		return ""
	}
	return runtime.StuckReasonFromTop(out)
}

// RealDeps returns Deps backed by real subprocesses / filesystem. runCmd must
// return (stdout, ok) where ok=false signals an enumeration failure (spawn error
// or non-zero exit).
func RealDeps(runCmd func(argv []string) (string, bool), detectRuntime func() string) Deps {
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
