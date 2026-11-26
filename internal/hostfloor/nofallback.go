package hostfloor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// nofallback.go is the floor's half of PD-NF1 (docs/design/program-delivery.md#PD-NF1): when the
// floor has found a newer version of a program it manages — a newer npm release, a newer capture,
// a moved declaration, a pre-launch refresh it was about to run — and putting that version in place
// fails, the act STOPS with one refusal block, and the installed version runs only under a hatch the
// user sets (patchrecovery.UseInstalledEnv) or an `agent_updates` hold, which never gets here. Not
// reaching upstream at all is not such a failure: the floor cannot know a newer version exists, so
// it runs what is installed and says it could not check (PD-NF3: where the act itself is the check,
// a capture or a refresh, a bounded probe of the program's upstream tells the two apart).

// ErrCaptureBusy is what Floor.Capture wraps when another `yolo capture` of the program holds its
// lock: not a failed update (PD-NF5), since nothing was checked, so the installed version runs.
var ErrCaptureBusy = errors.New("another capture of it is in progress")

// UpdateRefusal is the error Ensure and the pre-launch refresh return for a failed update the
// hatch does not cover. The refusal block (Render) is already printed on the floor's Out when a
// caller sees it; Error is the one-line form for a caller's list.
type UpdateRefusal struct {
	// Bin and Pack name the program; Installed is the version that would run, Newer the one the
	// floor found ("" when the act that failed was the one that would have said).
	Bin, Pack, Installed, Newer string
	// Act is what failed, in a few words: "the update", "the reinstall the pack now asks for".
	Act string
	// Err is why.
	Err error
	// Repair is the command that retries the act; Command the one the hatch is put in front of.
	Repair, Command string
	// Runs labels the hatch line, "" for "run the installed <Installed>".
	Runs string
}

func (e *UpdateRefusal) Error() string {
	return fmt.Sprintf("%s failed (%v); yolo does not run the installed version on its own — %s=%s runs it",
		e.Act, e.Err, patchrecovery.UseInstalledEnv, e.Bin)
}

func (e *UpdateRefusal) Unwrap() error { return e.Err }

// subject is the refusal's program, as the ERROR line names it.
func (e *UpdateRefusal) subject() string {
	s := e.Bin
	if e.Installed != "" {
		s += " " + e.Installed
	}
	if e.Newer != "" {
		s += " → " + e.Newer
	}
	return s
}

// Render is the refusal block: one ERROR line naming the program and the version found, the facts,
// a blank line and the repair, then the hatch that runs the installed version (patchrecovery.Section),
// the same shape a patch failure's block has (packsrc.PatchFailure.Render).
func (e *UpdateRefusal) Render(color bool) string {
	var b strings.Builder
	head := fmt.Sprintf("ERROR: %s: %s failed", e.subject(), e.Act)
	b.WriteString(richtext.Render("[bold red]"+richtext.Escape(head)+"[/bold red]", color) + "\n")
	for _, l := range strings.Split(strings.TrimRight(fmt.Sprint(e.Err), "\n"), "\n") {
		b.WriteString("  " + l + "\n")
	}
	b.WriteString("\n" + richtext.Render("[bold]Repair:[/bold]", color) + "\n")
	b.WriteString("  fix what the error names, then retry it:\n")
	b.WriteString("    " + e.Repair + "\n")
	label := e.Runs
	switch {
	case label != "":
	case e.Installed != "":
		label = "run the installed " + e.Installed
	default:
		label = "run the installed version"
	}
	b.WriteString(patchrecovery.Section(patchrecovery.StartAnyway, []patchrecovery.Hatch{{
		Env: patchrecovery.UseInstalledEnv, Value: e.Bin, Command: e.Command, Label: label}}, color))
	return b.String()
}

// useInstalled reports whether the user's hatch covers p: Floor.UseInstalled, else
// patchrecovery.UseInstalledEnv in this process's environment, "1" or a name equal to p's pack,
// its bin or its npm package.
func (f *Floor) useInstalled(p Program) bool {
	if f.UseInstalled != nil {
		return f.UseInstalled(p)
	}
	scope := patchrecovery.Read(os.Getenv, patchrecovery.UseInstalledEnv)
	if scope.Covers(patchrecovery.Subject{Key: p.Pack + "/" + p.Bin(), Pack: p.Pack, Name: p.Bin()}) {
		return true
	}
	if p.Install.Kind == "npm" {
		if name, _ := packdecl.SplitNpmSpec(p.Install.Package); name != "" {
			return scope.Covers(patchrecovery.Subject{Name: name})
		}
	}
	return false
}

// hatchCommand is the command the hatch is put in front of: the operation this floor serves, else
// `yolo host -- <bin>`.
func (f *Floor) hatchCommand(p Program) string {
	if f.PatchBypassCommand != "" {
		return f.PatchBypassCommand
	}
	return "yolo host -- " + shquote.Quote(p.Bin())
}

// refuseUpdate prints the refusal block for p on Out and returns it as the error.
func (f *Floor) refuseUpdate(p Program, installed, newer, act string, err error) error {
	r := &UpdateRefusal{Bin: p.Bin(), Pack: p.Pack, Installed: installed, Newer: newer, Act: act, Err: err,
		Repair: "yolo host --update -- " + shquote.Quote(p.Bin()), Command: f.hatchCommand(p)}
	out := f.out()
	_, _ = io.WriteString(out, r.Render(colorFor(out)))
	return r
}

// sayUsingInstalled is the one line a run says when the hatch runs the installed version past a
// failed update.
func (f *Floor) sayUsingInstalled(p Program, installed, act string, err error) {
	f.say("%s: %s failed (%v); %s is set, so yolo runs the installed %s", p.Bin(), act, err,
		patchrecovery.UseInstalledEnv, installed)
}

// UpstreamProbeTimeout bounds one probe of a program's upstream.
const UpstreamProbeTimeout = 8 * time.Second

// ProbeUpstreamDefault is the probe a Floor with no ProbeUpstream uses, probeUpstreamHEAD. A
// variable so a test can keep the network out of every floor it builds.
var ProbeUpstreamDefault = probeUpstreamHEAD

// probeUpstreamHEAD is one HEAD request to url, bounded by UpstreamProbeTimeout. Its classification
// is the jail launchers' too (_yolo_url_reachable): no answer (a connection error, a timeout) or a
// server error (5xx) is an unreachable upstream, the could-not-check case; any other HTTP status, a
// 404 or a 405 for HEAD included, is an upstream that answered.
func probeUpstreamHEAD(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, UpstreamProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return nil
}

// upstreamUnreachable probes url and returns why it cannot be reached, nil when it can — or when
// there is no url to probe, since then the floor cannot show the failure was the network's.
func (f *Floor) upstreamUnreachable(ctx context.Context, url string) error {
	if url == "" {
		return nil
	}
	probe := f.ProbeUpstream
	if probe == nil {
		probe = ProbeUpstreamDefault
	}
	if err := probe(ctx, url); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}

// firstUnreachable probes each non-empty url in order and returns the first that cannot be reached
// and why, or "" and nil when every one answers.
func (f *Floor) firstUnreachable(ctx context.Context, urls ...string) (string, error) {
	for _, u := range urls {
		if why := f.upstreamUnreachable(ctx, u); why != nil {
			return u, why
		}
	}
	return "", nil
}

// defaultNpmRegistry is the registry an npm-backed refresh reaches when its environment names none.
const defaultNpmRegistry = "https://registry.npmjs.org/"

// refreshUpstream is the URL a failed pre-launch refresh is judged by (PD-NF3), read from the
// refresh's own environment: the npm registry, for every program that declares a refresh, whatever
// its install kind, as the jail's launchers probe it (_yolo_registry_reachable). A fork (kind
// "source") keeps its base's refresh (packdecl/fork.go), so a patched pi still runs `pi update
// --extensions`, which reaches the registry the same way.
func refreshUpstream(env []string) string {
	for _, key := range []string{"npm_config_registry", "NPM_CONFIG_REGISTRY"} {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, key+"="); ok && v != "" {
				return v
			}
		}
	}
	return defaultNpmRegistry
}
