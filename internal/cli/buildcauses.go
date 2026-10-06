package cli

// buildcauses.go is ONE CAUSE, ONCE for a jail launch's builds (docs/design/patched-extensions.md
// PPX-D42): the maintainer's first launch with patched extensions ran four build jails sealed to his
// one pack, each refused at its boot by the same file, and each relay said so in full, then the jail
// said it again for each extension. So a build report keeps two things for the rest of its act:
//
//   - THE SEALS WHOSE OWN CONFIG WAS REFUSED (refusedSeal). A build jail sealed to a set of packs
//     that refused at its boot, for a reason that does not depend on what it built
//     (forkBuildNotStarted.configRefused), is refused the same way for every other build sealed to
//     that set, so none of them is started: each says it was skipped and why, and carries the first
//     one's cause, so the launch's refusal (internal/cli/run's missingbuilds.go) says that cause once
//     with every key it left without a build.
//   - THE HELD BUILDS, a newer build that met a cause while a good build serves: said once per cause
//     with every key it holds, when the act ends (flush). A build that leaves nothing serving is
//     said by the launch's refusal or its warning instead, so its cause is not said twice.
//
// A launch's builds run at once (XB-D10), so both are kept under the report's causes lock.

import (
	"io"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// refusedSealEntry is a build jail of this launch whose own config was refused at its boot.
type refusedSealEntry struct {
	// label is the build that met it ("extension matt/pi-subagents").
	label string
	cause *entrypoint.BuildCause
}

// heldGroup is the builds one cause held at their good build.
type heldGroup struct {
	cause *entrypoint.BuildCause
	held  []heldBuild
}

// heldBuild is one build a cause held: its label, what it still runs, what builds it once the cause
// is fixed, and, on Apple Container, `yolo capture`'s argument for it, whose limit (PF-D21) the group
// then names ("" on any other runtime).
type heldBuild struct {
	label, runs, retry, capture string
}

// sorted is g's builds in the order their keys are declared (order, the pool's): a pool's builds
// end in any order, and the group names them as the launch's other lines do.
func (g *heldGroup) sorted(order map[string]int) []heldBuild {
	out := slices.Clone(g.held)
	slices.SortStableFunc(out, func(a, b heldBuild) int { return order[a.label] - order[b.label] })
	return out
}

// sealKey is the identity of f's build jail's config: the packs its seal selects (sealPacks).
func sealKey(f packload.Fork) string { return strings.Join(sealPacks(f), "\x00") }

// refusedSeal is the refusal an earlier build of this act met in a jail sealed as f's would be, nil
// when none did or there is no report.
func (r *buildReport) refusedSeal(f packload.Fork) *refusedSealEntry {
	if r == nil {
		return nil
	}
	r.causes.Lock()
	defer r.causes.Unlock()
	return r.refused[sealKey(f)]
}

// noteRefused records that f's build jail's own config was refused with cause.
func (r *buildReport) noteRefused(f packload.Fork, cause *entrypoint.BuildCause) {
	if r == nil {
		return
	}
	r.causes.Lock()
	defer r.causes.Unlock()
	if r.refused == nil {
		r.refused = map[string]*refusedSealEntry{}
	}
	if _, ok := r.refused[sealKey(f)]; !ok {
		r.refused[sealKey(f)] = &refusedSealEntry{label: f.Label(), cause: cause}
	}
}

// skipLine says f's build was not started, and why, on w, the key's own lines: one line, as a build's
// result would be.
func (r *buildReport) skipLine(f packload.Fork, s *refusedSealEntry, w io.Writer) {
	richtext.Printer{W: w, Color: r.color}.Print("[yellow]" + richtext.Escape("skipped "+f.Label()+": not started — its build jail is sealed to "+
		packsPhrase(sealPacks(f))+", as "+s.label+"'s was, and would refuse to start the same way") + "[/yellow]")
}

// packsPhrase is "pack a" or "packs a and b".
func packsPhrase(packs []string) string {
	if len(packs) == 1 {
		return "pack " + packs[0]
	}
	return "packs " + entrypoint.JoinAnd(packs)
}

// hold records a build held at its good build by cause, which runs names ("still running v1.0.0
// (3f2a9c1e) + 2 patches"), said once per cause when the act ends; retry is what builds it once the
// cause is fixed, and runtime the launch's, for Apple Container's limit.
func (r *buildReport) hold(f packload.Fork, runs, retry, runtime string, cause *entrypoint.BuildCause) {
	if r == nil {
		return
	}
	r.causes.Lock()
	defer r.causes.Unlock()
	var g *heldGroup
	for _, h := range r.held {
		if h.cause.Same(cause) {
			g = h
			break
		}
	}
	if g == nil {
		g = &heldGroup{cause: cause}
		r.held = append(r.held, g)
	}
	b := heldBuild{label: f.Label(), runs: runs, retry: retry}
	if runtime == "container" {
		b.capture = "`yolo capture " + f.CaptureArg() + "`"
	}
	g.held = append(g.held, b)
}

// flush says each held group once: the builds it held, what each still runs, the cause, and who can
// fix it. Called when the act's builds are done.
func (r *buildReport) flush() {
	if r == nil {
		return
	}
	r.causes.Lock()
	defer r.causes.Unlock()
	for _, g := range r.held {
		held := g.sorted(r.order)
		var labels, captures []string
		for _, b := range held {
			labels = append(labels, b.label)
			if b.capture != "" {
				captures = append(captures, b.capture)
			}
		}
		head := "⚠ " + entrypoint.JoinAnd(labels) + ": a newer build's jail refused to start, so "
		if len(held) == 1 {
			head += "it is " + held[0].runs
		} else {
			head += "each is still running its good build"
		}
		r.pr.Print("[yellow]" + richtext.Escape(head) + "[/yellow]")
		for _, l := range g.cause.Lines {
			r.pr.Print("    " + richtext.Escape(l))
		}
		for _, l := range g.cause.WhoFixes(held[0].retry) {
			r.pr.Print("[dim]  " + richtext.Escape(l) + "[/dim]")
		}
		if len(captures) > 0 {
			// PF-D21: on Apple Container a build jail does not start beside a running jail.
			r.pr.Print("[dim]  " + richtext.Escape("On Apple Container a build jail cannot start beside a running jail: "+
				"if that is what stopped it, "+strings.Join(captures, ", ")+" "+plural(len(captures), "builds it",
				"build them")+" once the other jails stop.") + "[/dim]")
		}
	}
	r.held = nil
}
