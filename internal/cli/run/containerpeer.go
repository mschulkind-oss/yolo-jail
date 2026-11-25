package run

// THE EXCLUSION RULE (docs/design/unified-workspace-home.md §7.1, unified-workspace-home-plan.md U4):
// at most one CONTAINER setup runs per workspace, a container and a GUEST may run together, a second
// guest is refused, and the same setup is no peer (it attaches as it always did). The class of a
// peer comes from the witness that said it runs (jailStatus), never from a setup definition that
// may since have been removed. A peer that cannot be asked refuses, except where only a runtime
// could not be asked and every keeper lock and session record says not running (OQ-UH3): then one
// line names the setup and the launch goes on.
//
// THE CALL SITE is commitHome's first statement (UHP-D5): after every refusal and before the first
// write, under the project lock selectHome took (UHP-D4). The lock is released only after the
// keeper, which takes the jail's liveness lock before it says ready, answers; a second launch that
// then takes the lock finds the first one's keeper
// (TestTheKeeperIsRegisteredBeforeTheProjectLockIsReleased). There is no lock file in `.yolo` and
// no hatch.

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// peerClass is what kind of jail a witness says runs: a container (a container key or a runtime's
// answer) or a guest (a macos-user or guest key).
type peerClass int

const (
	peerClassContainer peerClass = iota
	peerClassGuest
)

// jailAnswer is jailStatus's answer: jailRuns' two values plus what the exclusion rule needs.
type jailAnswer struct {
	Answer projectJails
	// How names what answered running ("keeper", "session" or a runtime), for a refusal.
	How string
	// Class is the class of the witness that said running.
	Class peerClass
	// KeeperUnknown: a keeper lock could not be read.
	KeeperUnknown bool
	// Unasked are the runtimes that were on PATH and could not be asked, in order.
	Unasked []string
	// RuntimeOnly: the answer is unknown, every keeper lock and session record said not running, and
	// only a runtime could not be asked (OQ-UH3).
	RuntimeOnly bool
}

// peerFact is one other setup of the workspace and what is known of its jail.
type peerFact struct {
	Name   string
	Status jailAnswer
}

// peerVerdict is decidePeers' result: a refusal's text, or notes to print while the launch goes on.
type peerVerdict struct {
	Refusal string
	Notes   []string
}

// selfClass is the class of the launching setup.
func selfClass(s setup.Setup) peerClass {
	switch s.Confinement {
	case setup.ConfinementPodman, setup.ConfinementAppleContainer:
		return peerClassContainer
	}
	return peerClassGuest
}

// runsAsContainer reports whether s is a container setup, the only kind slice 1 checks and composes
// (UHP-D3).
func runsAsContainer(s setup.Setup) bool { return selfClass(s) == peerClassContainer }

// runtimeDisplayName is how a refusal spells a runtime.
func runtimeDisplayName(rt string) string {
	if name, ok := map[string]string{"podman": "Podman", "container": "Apple Container"}[rt]; ok {
		return name
	}
	return rt
}

// decidePeers is the rule of §7.1 over the other setups' facts. Pure: it asks nothing and prints
// nothing. A running peer of the same class refuses first, an unaskable one next; only then do
// notes go on.
func decidePeers(self setup.Setup, facts []peerFact) peerVerdict {
	mine := selfClass(self)
	var unknown *peerFact
	var notes []string
	for i := range facts {
		f := &facts[i]
		switch f.Status.Answer {
		case projectJailsRunning:
			if f.Status.Class == mine {
				return peerVerdict{Refusal: fmt.Sprintf("Refusing to launch: `%s` is running for this workspace and "+
					"shares its home with `%s`. Stop it with `%s`, then launch again.",
					f.Name, self.Name, stopSetupCommand(f.Name))}
			}
		case projectJailsUnknown:
			if f.Status.RuntimeOnly {
				notes = append(notes, fmt.Sprintf("Could not ask %s whether `%s` is running for this workspace; "+
					"its keeper and sessions say it is not, so this launch goes ahead.",
					runtimeDisplayName(f.Status.Unasked[0]), f.Name))
			} else if unknown == nil {
				unknown = f
			}
		}
	}
	if unknown != nil {
		if !unknown.Status.KeeperUnknown && len(unknown.Status.Unasked) > 0 {
			rt := unknown.Status.Unasked[0]
			return peerVerdict{Refusal: fmt.Sprintf("Refusing to launch: could not ask %s whether `%s` is running "+
				"for this workspace. Run `%s -a` to see, then launch again.",
				runtimeDisplayName(rt), unknown.Name, runningListCommand(rt))}
		}
		return peerVerdict{Refusal: fmt.Sprintf("Refusing to launch: could not tell whether `%s` is running for "+
			"this workspace. Run `%s`, then launch again.", unknown.Name, stopSetupCommand(unknown.Name))}
	}
	return peerVerdict{Notes: notes}
}

// stopSetupCommand is the command that stops one setup's jail of this workspace.
func stopSetupCommand(name string) string {
	return shquote.JoinDisplay([]string{"yolo", "stop", "--setup", name})
}

// peerFacts is one fact per setup with state in the workspace other than the launching one. The
// legacy jail name is not asked here: refuseLegacyJail answers for it.
func (o *Options) peerFacts() []peerFact {
	var out []peerFact
	for _, name := range paths.SetupStateNames(o.Workspace) {
		if name == o.Setup.Name {
			continue
		}
		out = append(out, peerFact{Name: name, Status: o.jailStatus(yoloruntime.JailName(o.Workspace, name))})
	}
	return out
}

// refuseContainerPeer is commitHome's first statement: it prints the refusal and returns true when
// another jail of the workspace may not run beside this launch. Skipped for a dry run (it writes
// nothing), for --fresh-home (that home shares nothing) and for a guest launch (slice 2, UHP-D3,
// UHP-D5).
func (o *Options) refuseContainerPeer() bool {
	if o.DryRun || o.FreshHome || !runsAsContainer(o.Setup) {
		return false
	}
	v := decidePeers(o.Setup, o.peerFacts())
	out := o.pr(o.Stderr)
	for _, note := range v.Notes {
		out.print("[dim]" + richtext.Escape(note) + "[/dim]")
	}
	if v.Refusal != "" {
		out.print("[bold red]" + richtext.Escape(v.Refusal) + "[/bold red]")
		return true
	}
	return false
}
