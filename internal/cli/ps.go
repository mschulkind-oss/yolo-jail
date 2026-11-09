// ps.go implements the `yolo ps` command. It lists running yolo-* jails,
// resolves each to its workspace, prunes stale tracking files, and flags
// problem jails. Every subprocess is behind an injectable seam for
// unit-testability.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// psDeps are the injectable seams. RunCmd runs argv and returns (stdout, ok):
// ok=false means the probe could NOT enumerate (spawn/exec failure or non-zero
// exit) — the tri-state that must never be collapsed to "no jails" (D11).
// DetectRuntime returns the effective runtime ("podman" / "container" /
// "macos-user"), platform-aware for ps; macos-user lists its sessions and
// never calls RunCmd (psMacosUser). PathIsDir reports whether a workspace path exists.
// Color enables ANSI on the framing lines (idle notice / problem section /
// doctor tip); the caller resolves it to (requested && os.Stdout is a TTY), so
// a bytes.Buffer or a pipe yields byte-identical plain output.
// Format is "text" (the human table) or "json"; see outputformat.go.
//
// GOOS and HasBinary say which container runtimes this machine has (psContainerAnswers), so the
// tracking-file prune can tell a runtime that was not asked from one that is not installed;
// "" defaults to platformGOOS(), nil to "nothing installed".
type psDeps struct {
	DetectRuntime func() string
	GOOS          string
	HasBinary     func(bin string) bool
	RunCmd        func(argv []string) (string, bool)
	PathIsDir     func(path string) bool
	Out           io.Writer
	Color         bool
	Format        string
}

// psReport is `yolo ps --format json`: the same three facts the table carries
// per jail, plus the two the table can only say in prose.
//
// `enumerated` is in the payload because it is the distinction the whole command
// is built around (D11): "the runtime answered and there are no jails" and "the
// runtime could not be reached" are different states, and the human output says
// so in a red sentence. Collapsing them to an empty list would hand a machine
// consumer exactly the wrong answer — the one that reads a broken probe as an
// idle machine — so a consumer must check this field before believing `jails`.
type psReport struct {
	Runtime    string   `json:"runtime"`
	Enumerated bool     `json:"enumerated"`
	Jails      []psJail `json:"jails"`
}

// psJail is one running jail. Problem is "" for a healthy one and otherwise the
// same reason string the human report prints ("workspace gone", or a stuck-in-
// provisioning reason).
//
// Setup is the setup the jail runs as (psSetupOf), null when it is not known: a jail an older
// yolo started (named-setups.md §12.2).
type psJail struct {
	Name      string  `json:"name"`
	Setup     *string `json:"setup"`
	Status    string  `json:"status"`
	Workspace string  `json:"workspace"`
	Problem   string  `json:"problem,omitempty"`
}

// psSetupOf is the setup the jail named name runs as (named-setups-plan.md slice 6b; the order is
// named-setups.md §12.2's): its launch record's setup; else the setup its name carries, the name
// being runtime.JailName of its workspace and a setup; else "", a jail an older yolo started,
// which recorded none and is keyed by the workspace alone.
func psSetupOf(name, workspace string) string {
	if rec, ok := runtime.ReadLaunchRecord(name); ok && rec.Setup != "" {
		return rec.Setup
	}
	if workspace == "" || workspace == "unknown" {
		return ""
	}
	// A record keeps the resolved path (FromResolved); a path from an inspected environment may
	// not be resolved, so FromWorkspace is asked too.
	for _, key := range []string{runtime.FromResolved(workspace), runtime.FromWorkspace(workspace)} {
		if s, ok := strings.CutPrefix(name, key+"-"); ok && setup.ValidName(s) {
			return s
		}
	}
	return ""
}

// psSetupJSON is a row's setup as JSON: null when it is not known.
func psSetupJSON(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Run executes `yolo ps`, writing the table to deps.Out, and returns the exit
// code (always 0 — ps never fails the process).
// list → parse → resolve workspace → prune stale tracking → render → problems.
func psRun(deps psDeps) int {
	// The human report is discarded, not reshaped, while JSON is produced — see
	// outfmt.Sink. Color goes with it: there is no terminal to decorate. Named `w`
	// rather than `out` because the runtime probes below bind their stdout to
	// `out`, and one of the two shadowing the other is how the table body ends up
	// on stdout in the middle of a JSON document.
	w := outfmt.Sink(deps.Out, deps.Format)
	pr := richtext.Printer{W: w, Color: deps.Color && !outfmt.IsJSON(deps.Format)}
	rt := deps.DetectRuntime()
	report := psReport{Runtime: rt, Jails: []psJail{}}
	if rt == "macos-user" { // parity: HonoredBy — macos-user runs no container, so its jails are its live sessions
		return psMacosUser(deps, pr, report)
	}

	// The runtime probe is TRI-STATE (audit 2026-07-18 §B / D11): a spawn/exec
	// error means "could not enumerate", which must NEVER be read as "no jails"
	// — pruning the tracking dir on an unconfirmed-empty set deletes the files
	// for LIVE jails (the destructive macOS-AC bug). Only a probe that actually
	// ran (ok=true) authorizes the stale-tracking prune.
	var rows []runtime.PsRow
	var enumerated bool
	if rt == "container" {
		// The JSON listing, not the table: the table parser put the whole row in STATUS (lab
		// report 2026-11-04 §7). Output that does not parse is "could not enumerate" (D11).
		if out, ok := deps.RunCmd(runtime.ContainerLsJSONArgv); ok {
			rows, enumerated = runtime.ParseContainerLsRows(out)
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
		pr.Printf("[red]Could not query the %s runtime for running jails.[/red]", rt)
		return psFinish(deps, report)
	}
	report.Enumerated = true

	if len(rows) == 0 {
		pr.Print("[dim]No running jails.[/dim]")
		// Enumeration succeeded and returned nothing → the prune may drop every
		// tracking file, but only if every other container runtime here is absent.
		runtime.PruneStaleTrackingFiles(psContainerAnswers(deps, rt, map[string]struct{}{}))
		return psFinish(deps, report)
	}

	// Resolve each row's workspace (tracking file first, then inspect env).
	type resolved struct {
		name, setup, status, workspace string
	}
	var containers []resolved
	running := map[string]struct{}{}
	for _, r := range rows {
		ws := getContainerWorkspace(deps, r.Name, rt)
		containers = append(containers, resolved{r.Name, psSetupOf(r.Name, ws), r.Status, ws})
		running[r.Name] = struct{}{}
	}

	// Prune stale tracking files: keep the running names, and keep everything when
	// another installed container runtime was not asked (psContainerAnswers).
	runtime.PruneStaleTrackingFiles(psContainerAnswers(deps, rt, running))

	// Render the table via the ported renderer.
	pcs := make([]runtime.PsContainer, len(containers))
	for i, c := range containers {
		pcs[i] = runtime.PsContainer{Name: c.name, Setup: c.setup, Status: c.status, Workspace: c.workspace}
	}
	if table := runtime.RenderPsTable(pcs); table != "" {
		// Bold only the header row (the first line); the aligned body cells are
		// left verbatim so color never fights column alignment. Splitting on the
		// first "\n" is safe — RenderPsTable always emits a header, and body rows
		// (if any) follow after newlines.
		header, body, hasBody := strings.Cut(table, "\n")
		pr.Print("[bold]" + header + "[/bold]")
		if hasBody {
			fmt.Fprintln(w, body)
		}
	}

	// Problem jails: workspace-gone or stuck-in-provisioning. The same pass fills
	// the JSON rows, so a problem can never appear in one form and not the other.
	var problems [][2]string
	for _, c := range containers {
		jail := psJail{Name: c.name, Setup: psSetupJSON(c.setup), Status: c.status, Workspace: c.workspace}
		switch {
		case c.workspace != "unknown" && !deps.PathIsDir(c.workspace):
			jail.Problem = "workspace gone"
		default:
			jail.Problem = stuckReason(deps, c.name, rt)
		}
		if jail.Problem != "" {
			problems = append(problems, [2]string{c.name, jail.Problem})
		}
		report.Jails = append(report.Jails, jail)
	}
	if len(problems) > 0 {
		pr.Printf("\n[yellow]⚠  %d problem jail(s):[/yellow]", len(problems))
		for _, p := range problems {
			pr.Printf("  [red]%s  (%s)[/red]", p[0], p[1])
		}
		pr.Print("\n  [dim]Run 'yolo doctor' to clean up[/dim]")
	}
	return psFinish(deps, report)
}

// psMacosUser is `yolo ps` on macos-user, which has no container runtime to ask: the backend
// runs `macos-user` as a runtime name, not a binary, and the exec the container branch makes
// printed a red "Could not query" on every Mac that used it. Its jails are its SESSIONS
// (runtime.ListSessions): one per macos-user invocation, each holding a lock on its own
// host-services dir for exactly as long as its sandbox runs.
//
// IT EXECS NOTHING AND PRUNES NO TRACKING FILE. The tracking files under CONTAINER_DIR are
// container jails' (an Apple Container or podman jail beside this backend on one Mac), and an
// empty session list says nothing about them: pruning on it would delete live containers'
// files, which is the D11 defect psRun's container branch exists to avoid.
//
// A session whose lock this listing cannot read (no lock file yet: one starting up) is listed as
// "starting or unknown" rather than dropped; one whose lock is free has ended and is not a jail.
// A `yolo host` launch's session is not a jail either (runtime.MacosUserSessions leaves it out).
func psMacosUser(deps psDeps, pr richtext.Printer, report psReport) int {
	base := paths.HostServicesBase(paths.IsMacOS)
	sessions, ok := runtime.MacosUserSessions(base)
	if !ok {
		pr.Printf("[red]Could not list the macos-user sessions under %s.[/red]", base)
		return psFinish(deps, report)
	}
	report.Enumerated = true
	var rows []runtime.PsContainer
	for _, s := range sessions {
		status := "running (macos-user session)"
		switch s.Liveness {
		case runtime.SessionGone:
			continue
		case runtime.SessionUnknown:
			status = "starting or unknown"
		}
		name, ws := s.Name, s.Workspace
		if name == "" {
			// A dir an older yolo made carries no record: its own name is the one thing that
			// identifies it, and the workspace it serves is not knowable.
			name = filepath.Base(s.Dir)
		}
		if ws == "" {
			ws = "unknown"
		}
		setupName := psSetupOf(name, ws)
		rows = append(rows, runtime.PsContainer{Name: name, Setup: setupName, Status: status, Workspace: ws})
		report.Jails = append(report.Jails, psJail{Name: name, Setup: psSetupJSON(setupName), Status: status, Workspace: ws})
	}
	if len(rows) == 0 {
		pr.Print("[dim]No running macos-user sessions.[/dim]")
		pr.Print("[dim]Container jails are listed under their own runtime: " +
			"YOLO_SETUP=apple-container-self yolo ps (or YOLO_SETUP=podman-self yolo ps).[/dim]")
		return psFinish(deps, report)
	}
	// RenderPsTable's header names a CONTAINER column; a macos-user row is a session. Same width,
	// so the columns stay aligned.
	header, body, hasBody := strings.Cut(runtime.RenderPsTable(rows), "\n")
	header = strings.Replace(header, "CONTAINER", "SESSION  ", 1)
	pr.Print("[bold]" + header + "[/bold]")
	if hasBody {
		fmt.Fprintln(pr.W, body)
	}
	return psFinish(deps, report)
}

// psFinish emits the JSON document when one was asked for and returns ps's exit
// code — always 0, as it always was: `ps` reports, it does not judge.
//
// Every one of psRun's exits goes through here, including the two early ones, so
// `--format json` can never answer with an empty stream. An unreachable runtime
// still produces a document; it is the one with `"enumerated": false`.
func psFinish(deps psDeps, report psReport) int {
	if !outfmt.IsJSON(deps.Format) {
		return 0
	}
	enc, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		// Unreachable for this struct (three strings, a bool and a slice of the
		// same), but a silent empty stdout is the one outcome a machine consumer
		// cannot diagnose, so say it on stderr and fail.
		fmt.Fprintf(os.Stderr, "yolo ps: encoding the report failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(deps.Out, string(enc))
	return 0
}

// psContainerAnswers is the prune's view of every container runtime this OS has
// (named-setups.md SU-D10, named-setups-plan.md SUP-D22/SUP-D26): rt, the one asked, listed
// running; every other one installed here was not asked, so its jails are unknown
// (unreachable); one not installed runs nothing. A technology this OS does not have (Apple
// Container on Linux) is no source at all. Whether a runtime is here is setup.Buildable of
// its built-in, the launch's own answer.
func psContainerAnswers(deps psDeps, rt string, running map[string]struct{}) []runtime.SourceAnswer {
	goos := deps.GOOS
	if goos == "" {
		goos = platformGOOS()
	}
	hasBinary := deps.HasBinary
	if hasBinary == nil {
		hasBinary = func(string) bool { return false }
	}
	var answers []runtime.SourceAnswer
	asked := false
	for _, name := range []string{"podman-self", "apple-container-self"} {
		s := setup.MustBuiltIn(name)
		a := runtime.SourceAnswer{Confinement: string(s.Confinement)}
		err := setup.Buildable(s, goos, hasBinary)
		var missing *setup.MissingTechnologyError
		switch {
		case s.Backend() == rt:
			asked = true
			a.State, a.Names = runtime.SourceListed, running
		case err == nil:
			a.State = runtime.SourceUnreachable
		case errors.As(err, &missing):
			a.State = runtime.SourceNotInstalled
		default:
			continue
		}
		answers = append(answers, a)
	}
	if !asked {
		// A runtime no built-in runs on: its jails' records cannot be told from the others',
		// so its answer keeps them all rather than being dropped.
		answers = append(answers, runtime.SourceAnswer{Confinement: rt, State: runtime.SourceUnreachable})
	}
	return answers
}

// getContainerWorkspace resolves a container's workspace: the tracking file
// first (fast), then the runtime inspect's YOLO_HOST_DIR env; "unknown" when
// neither yields a value.
func getContainerWorkspace(deps psDeps, name, rt string) string {
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
// _check_container_stuck's early return).
// _check_container_stuck around the ported StuckReasonFromTop analyzer.
func stuckReason(deps psDeps, name, rt string) string {
	if rt == "container" {
		return ""
	}
	out, ok := deps.RunCmd([]string{rt, "top", name, "-eo", "comm"})
	if !ok {
		return ""
	}
	return runtime.StuckReasonFromTop(out)
}

// psRealDeps returns psDeps backed by real subprocesses / filesystem. runCmd
// must return (stdout, ok) where ok=false signals an enumeration failure (spawn
// error or non-zero exit).
func psRealDeps(runCmd func(argv []string) (string, bool), detectRuntime func() string, format string) psDeps {
	return psDeps{
		DetectRuntime: detectRuntime,
		GOOS:          platformGOOS(),
		HasBinary: func(bin string) bool {
			_, err := exec.LookPath(bin)
			return err == nil
		},
		RunCmd: runCmd,
		PathIsDir: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && info.IsDir()
		},
		Out:    os.Stdout,
		Color:  colorForWriter(os.Stdout),
		Format: format,
	}
}
