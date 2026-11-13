// ps.go implements `yolo ps`: one list of every running jail on this machine, whatever its
// setup (docs/design/named-setups.md §12, ruled as OQ-SU6; plan slices 11d and 11e). Every
// source of this OS is asked at once, each ends in one state (SU-D9), each row is filled from its
// launch record and then the runtime (§12.2), and the rows are printed as one table or one JSON
// document (SU-D13). Every subprocess and clock is behind an injectable seam.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// The budgets of named-setups.md §12.1: 10 s for a source's listing, 5 s for each per-row call.
const (
	psSourceBudget = 10 * time.Second
	psRowBudget    = 5 * time.Second
)

// psDeps are the injectable seams.
//
// RunCmd runs argv under ctx and returns its stdout; an error means the command could not answer
// (spawn failure, non-zero exit), and its text is the detail a source line shows. A source whose
// ctx expired is timed-out whatever RunCmd returned (SUP-D24).
//
// GOOS and HasBinary say which technologies this machine has (setup.Buildable, SUP-D22); "" is
// platformGOOS(), nil "nothing installed". SessionBase is where the session dirs live ("" is
// paths.HostServicesBase for GOOS). Home is the path a workspace under it prints as `~`. Now is
// the clock an age is read against. SourceBudget and RowBudget default to psSourceBudget and
// psRowBudget. InJail heads the list "Jails inside this jail:" and makes the JSON frame "jail".
// UserSetups are the user config's setups, read for a row's run_as when its record names none.
//
// Only, when set, is --setup's setup (slice 11e): only its technology is asked, only its rows
// are listed, and no launch record is pruned.
type psDeps struct {
	GOOS         string
	HasBinary    func(bin string) bool
	RunCmd       func(ctx context.Context, argv []string) (string, error)
	PathIsDir    func(path string) bool
	SessionBase  string
	Home         string
	Now          func() time.Time
	SourceBudget time.Duration
	RowBudget    time.Duration
	InJail       bool
	UserSetups   map[string]setup.Setup
	Only         *setup.Setup
	Out          io.Writer
	Color        bool
	Format       string
}

// psSourceSpec is one source of this OS (named-setups.md §12.1): a technology's way of answering
// "what is running".
type psSourceSpec struct {
	confinement setup.Confinement
	// display names the technology in prose ("Apple Container", "yolo host").
	display string
	// builtIn is the built-in whose Buildable says whether the technology is here; "" for the
	// session sources, which are directories and always asked on their OS.
	builtIn string
}

// psSourceSpecs is every source yolo knows, in the fixed order of §12.4.
var psSourceSpecs = []psSourceSpec{
	{setup.ConfinementPodman, "Podman", "podman-self"},
	{setup.ConfinementAppleContainer, "Apple Container", "apple-container-self"},
	{setup.ConfinementSeatbelt, "Seatbelt", ""},
	{setup.ConfinementHost, "yolo host", ""},
}

// psSource is one source's answer, and the rows it gave.
type psSource struct {
	spec     psSourceSpec
	state    runtime.SourceState
	detail   string
	nextStep string
	// nextLabel introduces nextStep in the table's source line ("Start it:", "See why:").
	nextLabel string
	rows      []*psRow
}

// psRow is one running jail, as §12.2 fills it. An unknown string is "", an unknown time zero,
// and unknown packs nil.
type psRow struct {
	name        string
	setup       string
	confinement setup.Confinement
	runAs       string
	workspace   string
	status      string
	started     time.Time
	packs       []string
	yoloVersion string
	problems    []psProblem
	// session is a Seatbelt or host session's row, which has no runtime to stop it.
	session bool
}

// psProblem is a row that needs a person (§12.2), with its one next step.
type psProblem struct {
	Kind     string `json:"kind"`
	Detail   string `json:"detail"`
	NextStep string `json:"next_step"`
}

// psDoc is `yolo ps --format json`, the document of named-setups.md §12.4.
type psDoc struct {
	Schema   string         `json:"schema"`
	Frame    string         `json:"frame"`
	Complete bool           `json:"complete"`
	Sources  []psSourceJSON `json:"sources"`
	Jails    []psJailJSON   `json:"jails"`
}

type psSourceJSON struct {
	Confinement string `json:"confinement"`
	State       string `json:"state"`
	Jails       *int   `json:"jails,omitempty"`
	Detail      string `json:"detail,omitempty"`
	NextStep    string `json:"next_step,omitempty"`
}

type psJailJSON struct {
	Name        string      `json:"name"`
	Setup       *string     `json:"setup"`
	Confinement string      `json:"confinement"`
	RunAs       *string     `json:"run_as"`
	Workspace   *string     `json:"workspace"`
	Status      string      `json:"status"`
	Started     *string     `json:"started"`
	Packs       []string    `json:"packs"`
	YoloVersion *string     `json:"yolo_version"`
	Problems    []psProblem `json:"problems"`
}

// psSchema names the JSON document and its version (§12.4).
const psSchema = "yolo.ps/1"

// psUnknownSetupFootnote is the line under a table holding a row whose setup is not known.
const psUnknownSetupFootnote = "? — launched by an older yolo, which did not record its setup. It ends when its agent exits;\n" +
	"    a new launch records it."

// psRun executes `yolo ps` and returns its exit code: always 0, since ps reports and does not
// judge (§12.4); `complete` carries what an exit code would.
func psRun(deps psDeps) int {
	deps = psDefaults(deps)
	// The human report is discarded, not reshaped, while JSON is produced (outfmt.Sink).
	w := outfmt.Sink(deps.Out, deps.Format)
	pr := richtext.Printer{W: w, Color: deps.Color && !outfmt.IsJSON(deps.Format)}

	sources := psDiscover(deps)
	var rows []*psRow
	for _, s := range sources {
		for _, r := range s.rows {
			if deps.Only == nil || r.setup == deps.Only.Name {
				rows = append(rows, r)
			}
		}
	}
	unmatched := 0
	if deps.Only != nil {
		for _, s := range sources {
			for _, r := range s.rows {
				if r.setup == "" {
					unmatched++
				}
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.workspace != b.workspace {
			return a.workspace < b.workspace
		}
		if a.setup != b.setup {
			return a.setup < b.setup
		}
		return a.name < b.name
	})

	// A launch record is pruned only on its own technology's answer (SU-D10), and never by a
	// narrowed listing, which is no answer about the other setups (§12.6).
	if deps.Only == nil {
		runtime.PruneStaleTrackingFiles(psContainerAnswers(sources))
	}

	psPrintText(deps, pr, sources, rows, unmatched)
	return psFinish(deps, sources, rows)
}

// psDefaults fills every unset seam with its real or default value.
func psDefaults(deps psDeps) psDeps {
	if deps.GOOS == "" {
		deps.GOOS = platformGOOS()
	}
	if deps.HasBinary == nil {
		deps.HasBinary = func(string) bool { return false }
	}
	if deps.RunCmd == nil {
		deps.RunCmd = func(context.Context, []string) (string, error) { return "", errors.New("no runner") }
	}
	if deps.PathIsDir == nil {
		deps.PathIsDir = func(string) bool { return true }
	}
	if deps.SessionBase == "" {
		deps.SessionBase = paths.HostServicesBase(deps.GOOS == "darwin")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.SourceBudget <= 0 {
		deps.SourceBudget = psSourceBudget
	}
	if deps.RowBudget <= 0 {
		deps.RowBudget = psRowBudget
	}
	if deps.Out == nil {
		deps.Out = io.Discard
	}
	return deps
}

// psSourcesOf is every source of goos, in §12.4's order, with the state of the ones that are not
// asked already decided: a container technology this OS has but this machine lacks is
// not-installed; one of another OS is no source at all. Seatbelt is a Mac's; host is everyone's.
// only narrows them to one setup's technology (§12.6).
func psSourcesOf(deps psDeps) []*psSource {
	var out []*psSource
	for _, spec := range psSourceSpecs {
		if deps.Only != nil && spec.confinement != deps.Only.Confinement {
			continue
		}
		s := &psSource{spec: spec}
		switch {
		case spec.builtIn != "":
			err := setup.Buildable(setup.MustBuiltIn(spec.builtIn), deps.GOOS, deps.HasBinary)
			var missing *setup.MissingTechnologyError
			switch {
			case err == nil:
			case errors.As(err, &missing):
				s.state = runtime.SourceNotInstalled
			default:
				continue // a technology of the other OS
			}
		case spec.confinement == setup.ConfinementSeatbelt && deps.GOOS != "darwin":
			continue
		}
		out = append(out, s)
	}
	return out
}

// psDiscover asks every source concurrently, each under its own budget, and returns them once all
// have answered or run out of time, so the order never depends on which answered first (§12.1).
// The session sources share one directory listing (SUP-D28).
func psDiscover(deps psDeps) []*psSource {
	sources := psSourcesOf(deps)
	var sessions []runtime.Session
	sessionsOK := false
	var sessionsOnce sync.Once
	listSessions := func() ([]runtime.Session, bool) {
		sessionsOnce.Do(func() { sessions, sessionsOK = runtime.ListSessions(deps.SessionBase) })
		return sessions, sessionsOK
	}
	var wg sync.WaitGroup
	for _, s := range sources {
		if s.state != "" {
			continue // not installed: not asked
		}
		wg.Add(1)
		go func(s *psSource) {
			defer wg.Done()
			switch s.spec.confinement {
			case setup.ConfinementPodman, setup.ConfinementAppleContainer:
				psAskContainer(deps, s)
			default:
				psAskSessions(deps, s, listSessions)
			}
		}(s)
	}
	wg.Wait()
	for _, s := range sources {
		if s.state != runtime.SourceListed {
			s.nextLabel, s.nextStep = psSourceNextStep(deps, s)
		}
	}
	return sources
}

// psAskContainer lists one container runtime under its budget, then fills each row (§12.2).
func psAskContainer(deps psDeps, s *psSource) {
	ctx, cancel := context.WithTimeout(context.Background(), deps.SourceBudget)
	defer cancel()
	var argv []string
	if s.spec.confinement == setup.ConfinementAppleContainer {
		argv = runtime.ContainerLsJSONArgv
	} else {
		argv = runtime.PodmanPsArgv
	}
	out, err := deps.RunCmd(ctx, argv)
	if ctx.Err() != nil {
		s.state, s.detail = runtime.SourceTimedOut, fmt.Sprintf("no answer within %s", deps.SourceBudget)
		return
	}
	if err != nil {
		s.state, s.detail = runtime.SourceUnreachable, psErrDetail(err)
		return
	}
	var listed []runtime.PsRow
	if s.spec.confinement == setup.ConfinementAppleContainer {
		var ok bool
		// Output that does not parse is "could not enumerate", never "no jails" (D11).
		if listed, ok = runtime.ParseContainerLsRows(out); !ok {
			s.state, s.detail = runtime.SourceUnreachable, "its listing did not parse as JSON"
			return
		}
	} else {
		listed = runtime.ParsePodmanPsRows(out)
	}
	s.state = runtime.SourceListed
	s.rows = make([]*psRow, len(listed))
	var wg sync.WaitGroup
	for i, l := range listed {
		wg.Add(1)
		go func(i int, l runtime.PsRow) {
			defer wg.Done()
			s.rows[i] = psFillContainerRow(deps, s.spec.confinement, l)
		}(i, l)
	}
	wg.Wait()
}

// psFillContainerRow fills a container's row in §12.2's order: the launch record, then the
// runtime, each per-row call bounded by RowBudget. A call that fails leaves its fields unknown; it
// never drops the row or changes its source's state.
func psFillContainerRow(deps psDeps, conf setup.Confinement, l runtime.PsRow) *psRow {
	r := &psRow{name: l.Name, confinement: conf, status: "running", started: l.Started}
	if rec, ok := runtime.ReadLaunchRecord(l.Name); ok {
		r.setup, r.runAs, r.workspace = rec.Setup, string(rec.RunAs), rec.Workspace
		r.packs, r.yoloVersion = rec.Packs, rec.YoloVersion
		if !rec.Started.IsZero() {
			r.started = rec.Started
		}
		if rec.Confinement != "" && rec.Confinement != conf {
			// The source that listed it is authoritative when the two disagree (§12.2).
			r.runAs = ""
		}
	}
	if r.setup == "" || r.workspace == "" {
		if env, ok := psInspectEnv(deps, conf, l.Name); ok {
			if r.workspace == "" {
				r.workspace, _ = runtime.WorkspaceFromInspectEnv(env)
			}
			if r.setup == "" {
				r.setup = psEnvValue(env, setup.EnvVar)
			}
		}
	}
	if r.setup == "" {
		r.setup = psSetupFromName(l.Name, r.workspace)
	}
	if r.runAs == "" {
		r.runAs = psRunAsOf(deps, r.setup)
	}
	if r.runAs == "" {
		r.runAs = string(setup.RunAsSelf) // no container runs as the yolo account (OQ-MT7)
	}
	stop := "podman stop " + l.Name
	if conf == setup.ConfinementAppleContainer {
		stop = "container stop " + l.Name
	}
	switch {
	case r.workspace != "" && !deps.PathIsDir(r.workspace):
		r.problems = append(r.problems, psProblem{Kind: "workspace-gone",
			Detail: r.workspace + " does not exist", NextStep: stop})
	case conf == setup.ConfinementPodman:
		// Apple Container has no `top`, so only Podman is checked for a stuck provisioning.
		ctx, cancel := context.WithTimeout(context.Background(), deps.RowBudget)
		out, err := deps.RunCmd(ctx, []string{"podman", "top", l.Name, "-eo", "comm"})
		cancel()
		if err == nil {
			if why := runtime.StuckReasonFromTop(out); why != "" {
				r.problems = append(r.problems, psProblem{Kind: "stuck-provisioning", Detail: why, NextStep: "yolo doctor"})
			}
		}
	}
	return r
}

// psInspectEnv is a container's environment, from the runtime's inspect under RowBudget.
func psInspectEnv(deps psDeps, conf setup.Confinement, name string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), deps.RowBudget)
	defer cancel()
	if conf == setup.ConfinementAppleContainer {
		out, err := deps.RunCmd(ctx, []string{"container", "inspect", name})
		if err != nil {
			return nil, false
		}
		return runtime.EnvFromContainerInspectJSON(out)
	}
	out, err := deps.RunCmd(ctx, []string{"podman", "inspect", name, "--format", "{{range .Config.Env}}{{println .}}{{end}}"})
	if err != nil {
		return nil, false
	}
	return strings.Split(out, "\n"), true
}

// psEnvValue is the value of key in env's K=V lines, "" when absent.
func psEnvValue(env []string, key string) string {
	for _, line := range env {
		if v, ok := strings.CutPrefix(line, key+"="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// psSetupFromName is the setup a jail's name carries (runtime.JailName of its workspace and a
// setup, named-setups-plan.md SUP-D48), "" when it carries none: a jail an older yolo started.
func psSetupFromName(name, workspace string) string {
	if workspace == "" {
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

// psRunAsOf is the run_as of the setup named name when it is a built-in or one of the user's, ""
// otherwise (§12.2's second answer).
func psRunAsOf(deps psDeps, name string) string {
	if name == "" {
		return ""
	}
	if s, err := setup.Lookup(name, deps.UserSetups); err == nil {
		return string(s.RunAs)
	}
	return ""
}

// psAskSessions lists the Seatbelt or the host source from the session dirs (SUP-D28): a session
// whose record names the host notch is the host source's; every other one is Seatbelt's on a Mac,
// and the host source's on Linux, where it is the only session source.
func psAskSessions(deps psDeps, s *psSource, list func() ([]runtime.Session, bool)) {
	all, ok := list()
	if !ok {
		s.state, s.detail = runtime.SourceUnreachable, "the session directory "+deps.SessionBase+" could not be listed"
		return
	}
	s.state = runtime.SourceListed
	for _, ss := range all {
		host := ss.Notch == runtime.NotchHost || deps.GOOS != "darwin"
		if host != (s.spec.confinement == setup.ConfinementHost) || ss.Liveness == runtime.SessionGone {
			continue
		}
		r := &psRow{name: ss.Name, confinement: s.spec.confinement, status: "running",
			workspace: ss.Workspace, started: ss.Started, setup: ss.Setup, yoloVersion: ss.YoloVersion, session: true}
		if ss.Liveness == runtime.SessionUnknown {
			r.status = "starting" // no lock yet: a session starting up
		}
		if r.name == "" {
			// A dir an older yolo made carries no record: its own name is what identifies it.
			r.name = filepath.Base(ss.Dir)
		}
		if r.setup == "" {
			r.setup = psSetupFromName(ss.Name, ss.Workspace)
		}
		r.runAs = psRunAsOf(deps, r.setup)
		if r.workspace != "" && !deps.PathIsDir(r.workspace) {
			r.problems = append(r.problems, psProblem{Kind: "workspace-gone", Detail: r.workspace + " does not exist",
				NextStep: "it ends when its agent exits; close the terminal running it"})
		}
		s.rows = append(s.rows, r)
	}
}

// psErrDetail is the one line a failed command's error says: its stderr's first line when it
// exited non-zero, else the error itself.
func psErrDetail(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if line, _, _ := strings.Cut(strings.TrimSpace(string(ee.Stderr)), "\n"); line != "" {
			return line
		}
	}
	return err.Error()
}

// psSourceNextStep is the step that makes a source answer, and the words that introduce it.
func psSourceNextStep(deps psDeps, s *psSource) (label, step string) {
	switch s.spec.confinement {
	case setup.ConfinementAppleContainer:
		return "Start it:", "container system start"
	case setup.ConfinementPodman:
		if deps.GOOS == "darwin" {
			return "Start it:", "podman machine start"
		}
		return "See why:", "podman info"
	default:
		return "See why:", "ls -ld " + deps.SessionBase
	}
}

// psContainerAnswers is every container source's answer, for the launch-record prune (SU-D10).
func psContainerAnswers(sources []*psSource) []runtime.SourceAnswer {
	var answers []runtime.SourceAnswer
	for _, s := range sources {
		if s.spec.builtIn == "" {
			continue
		}
		a := runtime.SourceAnswer{Confinement: string(s.spec.confinement), State: s.state}
		if s.state == runtime.SourceListed {
			a.Names = map[string]struct{}{}
			for _, r := range s.rows {
				a.Names[r.name] = struct{}{}
			}
		}
		answers = append(answers, a)
	}
	return answers
}

// psComplete is true exactly when every source is listed or not installed (§12.4).
func psComplete(sources []*psSource) bool {
	for _, s := range sources {
		if s.state != runtime.SourceListed && s.state != runtime.SourceNotInstalled {
			return false
		}
	}
	return true
}

// psRecordedOn counts the launch records naming conf, the jails a source that did not answer
// leaves unconfirmed.
func psRecordedOn(conf setup.Confinement) int {
	entries, err := os.ReadDir(paths.ContainerDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if rec, ok := runtime.ReadLaunchRecord(e.Name()); ok && rec.Confinement == conf {
			n++
		}
	}
	return n
}

// psPrintText prints the table of §12.3 with its footnote, source lines, problems and empty
// states.
func psPrintText(deps psDeps, pr richtext.Printer, sources []*psSource, rows []*psRow, unmatched int) {
	if deps.InJail {
		pr.Print("Jails inside this jail:")
	}
	unknownSetup := false
	if len(rows) > 0 {
		table := make([]runtime.PsTableRow, len(rows))
		for i, r := range rows {
			table[i] = runtime.PsTableRow{
				Workspace: psTildeOr(deps.Home, r.workspace), Setup: psCell(r.setup), Status: r.status,
				Age: psAge(deps.Now(), r.started), Packs: psPacksCell(r.packs),
			}
			unknownSetup = unknownSetup || r.setup == ""
		}
		header, body, hasBody := strings.Cut(runtime.RenderPsTable(table), "\n")
		pr.Print("[bold]" + richtext.Escape(header) + "[/bold]")
		if hasBody {
			fmt.Fprintln(pr.W, body)
		}
		if unknownSetup {
			pr.Print("")
			pr.Print("[dim]" + richtext.Escape(psUnknownSetupFootnote) + "[/dim]")
		}
	}
	if unmatched > 0 {
		pr.Print("")
		pr.Printf("[dim]%d %s whose setup is not recorded %s not matched; `yolo ps` lists them.[/dim]",
			unmatched, plural(unmatched, "jail", "jails"), plural(unmatched, "is", "are"))
	}

	allAnswered := true
	for _, s := range sources {
		if s.state == runtime.SourceListed || s.state == runtime.SourceNotInstalled {
			continue
		}
		allAnswered = false
		pr.Print("")
		psPrintSourceLine(pr, s)
	}

	if len(rows) == 0 && allAnswered {
		switch {
		case deps.Only != nil:
			why := ""
			if err := setup.Buildable(*deps.Only, deps.GOOS, deps.HasBinary); err != nil {
				why = " " + strings.TrimSpace(err.Error())
			}
			pr.Printf("[dim]No jails running under setup `%s`.%s Every setup's: `yolo ps`.[/dim]",
				richtext.Escape(deps.Only.Name), richtext.Escape(why))
		default:
			var asked []string
			for _, s := range sources {
				if s.state == runtime.SourceListed {
					asked = append(asked, s.spec.display)
				}
			}
			pr.Printf("[dim]No jails running (asked %s). Start one in a project: `yolo -- <agent>`.[/dim]",
				richtext.Escape(joinAnd(asked)))
		}
	}

	var problems []*psRow
	for _, r := range rows {
		if len(r.problems) > 0 {
			problems = append(problems, r)
		}
	}
	if len(problems) == 0 {
		return
	}
	n := 0
	for _, r := range problems {
		n += len(r.problems)
	}
	pr.Print("")
	pr.Printf("[yellow]%d %s:[/yellow]", n, plural(n, "problem", "problems"))
	for _, r := range problems {
		where := psTildeOr(deps.Home, r.workspace)
		for _, p := range r.problems {
			switch p.Kind {
			case "workspace-gone":
				if r.session {
					pr.Printf("  [red]%s  workspace gone.[/red] %s.", richtext.Escape(where), richtext.Escape(capitalize(p.NextStep)))
					continue
				}
				pr.Printf("  [red]%s  workspace gone.[/red] Stop it:", richtext.Escape(where))
			default:
				pr.Printf("  [red]%s  %s (%s).[/red] Diagnose it:", richtext.Escape(where),
					richtext.Escape(strings.ReplaceAll(p.Kind, "-", " ")), richtext.Escape(p.Detail))
			}
			fmt.Fprintf(pr.W, "    %s\n", p.NextStep)
		}
	}
}

// psPrintSourceLine is the line under the table for a source that did not answer (§12.3): dim when
// no launch record names a jail on it, red and counted when some do.
func psPrintSourceLine(pr richtext.Printer, s *psSource) {
	recorded := 0
	if s.spec.builtIn != "" {
		recorded = psRecordedOn(s.spec.confinement)
	}
	what := s.spec.display + " could not be asked (" + s.detail + ")"
	if s.state == runtime.SourceTimedOut {
		what = s.spec.display + " did not answer (" + s.detail + ")"
	}
	switch {
	case recorded > 0:
		pr.Printf("[red]%s; %d %s recorded on %s could not be confirmed.[/red] %s",
			richtext.Escape(what), recorded, plural(recorded, "jail", "jails"), richtext.Escape(s.spec.display),
			richtext.Escape(s.nextLabel))
	case s.spec.builtIn != "":
		pr.Printf("[dim]%s is installed but not answering (%s); no yolo jail is recorded on it. %s[/dim]",
			richtext.Escape(s.spec.display), richtext.Escape(s.detail), richtext.Escape(s.nextLabel))
	default:
		pr.Printf("[red]%s; its jails are not listed.[/red] %s", richtext.Escape(what), richtext.Escape(s.nextLabel))
	}
	fmt.Fprintf(pr.W, "    %s\n", s.nextStep)
}

// psFinish emits the JSON document when one was asked for, and returns ps's exit code: 0.
func psFinish(deps psDeps, sources []*psSource, rows []*psRow) int {
	if !outfmt.IsJSON(deps.Format) {
		return 0
	}
	doc := psDoc{Schema: psSchema, Frame: "host", Complete: psComplete(sources),
		Sources: []psSourceJSON{}, Jails: []psJailJSON{}}
	if deps.InJail {
		doc.Frame = "jail"
	}
	for _, s := range sources {
		j := psSourceJSON{Confinement: string(s.spec.confinement), State: string(s.state)}
		switch s.state {
		case runtime.SourceListed:
			n := len(s.rows)
			if deps.Only != nil {
				n = 0
				for _, r := range s.rows {
					if r.setup == deps.Only.Name {
						n++
					}
				}
			}
			j.Jails = &n
		case runtime.SourceNotInstalled:
		default:
			j.Detail, j.NextStep = s.detail, s.nextStep
		}
		doc.Sources = append(doc.Sources, j)
	}
	for _, r := range rows {
		j := psJailJSON{Name: r.name, Setup: psNull(r.setup), Confinement: string(r.confinement),
			RunAs: psNull(r.runAs), Workspace: psNull(r.workspace), Status: r.status, Packs: r.packs,
			YoloVersion: psNull(r.yoloVersion), Problems: r.problems}
		if j.Problems == nil {
			j.Problems = []psProblem{}
		}
		if !r.started.IsZero() {
			j.Started = psNull(r.started.UTC().Format(time.RFC3339))
		}
		doc.Jails = append(doc.Jails, j)
	}
	enc, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo ps: encoding the report failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(deps.Out, string(enc))
	return 0
}

// psNull is s as JSON: null when unknown ("").
func psNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// psCell is a table cell: s, or runtime.PsUnknown.
func psCell(s string) string {
	if s == "" {
		return runtime.PsUnknown
	}
	return s
}

// psPacksCell is the PACKS cell: the packs comma-separated, "?" when unknown, "" for none.
func psPacksCell(packs []string) string {
	if packs == nil {
		return runtime.PsUnknown
	}
	return strings.Join(packs, ", ")
}

// psTildeOr prints a workspace under home as `~`, and an unknown one as "?".
func psTildeOr(home, ws string) string {
	if ws == "" {
		return runtime.PsUnknown
	}
	if home != "" && home != "/" {
		home = strings.TrimRight(home, "/")
		if ws == home {
			return "~"
		}
		if rest, ok := strings.CutPrefix(ws, home+"/"); ok {
			return "~/" + rest
		}
	}
	return ws
}

// psAge is the largest whole unit since started (`4s`, `12m`, `3h`, `2d`), "?" when unknown.
func psAge(now, started time.Time) string {
	if started.IsZero() {
		return runtime.PsUnknown
	}
	d := now.Sub(started)
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

// joinAnd joins words as prose: "a", "a and b", "a, b and c".
func joinAnd(words []string) string {
	switch len(words) {
	case 0:
		return "nothing"
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// capitalize upper-cases s's first byte.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// psRunCmdCtx runs argv under ctx, killing it when ctx expires; the error carries its stderr.
func psRunCmdCtx(ctx context.Context, argv []string) (string, error) {
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	return string(out), err
}

// psRealDeps returns psDeps backed by real subprocesses, the filesystem and the user config.
func psRealDeps(format string, only *setup.Setup) psDeps {
	_, user, _ := config.SelectedSetupFromConfig()
	home, _ := os.UserHomeDir()
	return psDeps{
		GOOS: platformGOOS(),
		HasBinary: func(bin string) bool {
			_, err := exec.LookPath(bin)
			return err == nil
		},
		RunCmd: psRunCmdCtx,
		PathIsDir: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && info.IsDir()
		},
		Home:       home,
		InJail:     config.InJail(),
		UserSetups: user,
		Only:       only,
		Out:        os.Stdout,
		Color:      colorForWriter(os.Stdout),
		Format:     format,
	}
}
