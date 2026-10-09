package hostfloor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// patched.go is the floor's arm for a PATCHED FORK's program (docs/design/patched-forks.md §9,
// PF-D14): a fork whose `source` names the upstream and whose bytes are that upstream at a commit
// with the fork pack's patch series replayed. Its host copy is the capture store's build of its
// GOOD BUILD — this machine's record of the last build of the fork it admitted — relocated into the
// floor, as a plain fork's is the build at its pin (built.go).
//
// The terms are the design's, defined there: the GOOD BUILD; the ADVANCE, the act that checks the
// upstream (at most hourly), replays the series down the walk's list, builds the newest fit in the
// sealed capture jail and moves the good build once that build is admitted; and HELD, what runs not
// following. This package runs none of it: the caller hands the floor the advance (Floor.Advance)
// and an offline read of the good build (Floor.Patched), as it hands a plain fork's pin and build.
//
// # Where it departs from a plain fork's arm
//
//   - THE GOOD BUILD IS READ WHERE A PLAIN FORK'S PIN IS: the four readers of the manifest's recipe
//     (servesANearMiss, the pending check, the store lookup behind the install, and the install
//     record) read the good build's commit and recipe, and a patched fork has no pin at all
//     (PF-D16).
//   - THE ADVANCE IS THE INSTALL ACT'S FIRST STEP, and its REFRESH: every Ensure of a patched fork
//     that can build runs it, outside the floor's lock and before the floor decides anything, so a
//     launch waits for it as a jail launch does (PF-D25: bounded, and a Ctrl-C ends it on the good
//     build). Only a current entry under `agent_updates` off skips it — the refresh arm's
//     UpdatesAllowed, asked for the fork pack and its base (PF-D19) — and the advance runs no git
//     inside its own hourly throttle (P4).
//   - A NEWER UPSTREAM THAT FAILS KEEPS THE INSTALLED COPY (PF-D8): the advance leaves the good
//     build where it was. A user's own edit to the series, `build` or `produces` is not held
//     (PF-D23): an installed copy of another recipe is a near-miss, and a failed install removes it,
//     as FP-D17 removes a plain fork's build at a moved pin.
//   - A MOVED GOOD BUILD THAT CANNOT LEAVE THE JAIL'S HOME HAS NO FLOOR ENTRY (§9), whatever the
//     floor ran before: its install's no-entry answer removes the installed copy too.
//   - THE COPY IS CHECKED WHOLE AFTER IT ENDS (PF-D51): a move reaps every build no running jail was
//     handed, and the floor's copy is no jail's, so an entry reaped under the copy fails the install
//     rather than becoming a half-copied program.
//   - THE FLOOR'S OWN COPY SERVES THE ADVANCE (PF-D55): the install act hands the advance its installed
//     copy when that is a build of the series as it stands (patchedServing), which keeps running
//     whatever the advance does, so the advance runs as one with a good build serving even after the
//     good build's store entry is gone. An install whose good build the store no longer holds does not
//     start, and its failure names the act that builds it (patchedGoneReason).

// PatchedState is what the floor reads of a patched fork's program, offline: no git, no network.
type PatchedState struct {
	// Recipe is the recipe a build of the series as it stands carries (the series digest in it), ""
	// when the series cannot be read — which Reason then names, with its fix.
	Recipe string
	// Good is the good build when it serves the manifest (its recipe is Recipe), nil when none does.
	Good *PatchedBuild
	// PatchFailure is the current persistent application failure, if any, or the typed failure
	// returned by the immediately preceding Advance when persistence did not retain it.
	PatchFailure *packsrc.PatchFailure
	// PatchFailureSaid records that the Advance which returned PatchFailure already printed its
	// error block on this operation's stream, so the floor does not print the same block again. It
	// is false for a failure read from the record, which nothing in this operation has said yet.
	PatchFailureSaid bool
	// OperationError is an independent check/operation failure returned by Advance. A compatible
	// patch-failure bypass must not turn this into success.
	OperationError string
	// AuthorityError is a failed or unresolved authority read, not a classified patch failure.
	AuthorityError string
	// AdvanceBypassed records that Advance itself took the literal foreground compatibility skip.
	AdvanceBypassed bool
	// Reason is why no good build serves, naming the next step; "" when Good is set.
	Reason string
}

// PatchedBuild is a patched fork's good build as the floor installs it.
type PatchedBuild struct {
	// Commit is the upstream commit it is a build of, and Recipe its recipe hash.
	Commit, Recipe string
	// Label is what a line names it: "v1.0.1 (3f2a9c1e) + 3 patches".
	Label string
	// Entry is its capture store entry, found by the exact lookup; nil when the store holds it no
	// more (a prune, a wiped store), which the next advance builds again.
	Entry *capture.Entry
}

// PatchedPreparation is an operation-local, opaque selection returned by PreparePatched. Its
// unexported fields bind the selected declaration, Floor, recipe, authority and delivery; callers
// can only consume it through EnsurePrepared on the same Floor and Program.
type PatchedPreparation struct {
	floor           *Floor
	program         Program
	goos, goarch    string
	recipe          string
	state           PatchedState
	failure         *packsrc.PatchFailure
	installed       *Record
	selectedGood    *PatchedBuild
	delivery        patchedPreparationDelivery
	bypassRequested bool
}

type patchedPreparationDelivery uint8

const (
	patchedPreparationInstall patchedPreparationDelivery = iota
	patchedPreparationKeepInstalled
	patchedPreparationInstallGood
)

func cloneProgram(p Program) (Program, error) {
	data, err := json.Marshal(p.Install)
	if err != nil {
		return Program{}, fmt.Errorf("copying the host program declaration: %w", err)
	}
	var install packdecl.Install
	if err := json.Unmarshal(data, &install); err != nil {
		return Program{}, fmt.Errorf("copying the host program declaration: %w", err)
	}
	install.ForkRoot = p.Install.ForkRoot
	install.Gate = p.Install.Gate
	install.RefreshTiming = p.Install.RefreshTiming
	return Program{Pack: p.Pack, Install: install}, nil
}

func clonePatchFailure(in *packsrc.PatchFailure) *packsrc.PatchFailure {
	if in == nil {
		return nil
	}
	out := *in
	out.Paths = append([]string(nil), in.Paths...)
	return &out
}

func clonePatchedBuild(in *PatchedBuild) *PatchedBuild {
	if in == nil {
		return nil
	}
	out := *in
	if in.Entry != nil {
		entry := *in.Entry
		out.Entry = &entry
	}
	return &out
}

func clonePatchedState(in PatchedState) PatchedState {
	in.Good = clonePatchedBuild(in.Good)
	in.PatchFailure = clonePatchFailure(in.PatchFailure)
	return in
}

func cloneRecord(in *Record) *Record {
	if in == nil {
		return nil
	}
	out := *in
	out.Exec = append([]string(nil), in.Exec...)
	return &out
}

// patched is Patched's answer for p, or why this floor reads none.
func (f *Floor) patched(p Program) PatchedState {
	if f.Patched == nil {
		return PatchedState{Reason: "this yolo reads no patched fork's good build here"}
	}
	return f.Patched(p)
}

// patchedNoEntryReason is noEntryReason's patched-fork arm, after the platform: a series that cannot
// be read leaves nothing to serve or build (PF-D23), and a floor with no reader holds none.
func (f *Floor) patchedNoEntryReason(p Program) string {
	if f.Patched == nil {
		return "it is a patched fork of fork pack " + p.Install.ForkedBy + ", and this yolo reads no patched " +
			"fork's good build here"
	}
	if ps := f.Patched(p); ps.Recipe == "" {
		return "it is a patched fork of fork pack " + p.Install.ForkedBy + " whose series cannot be read: " + ps.Reason
	}
	return ""
}

// patchedStatus is Status for a patched fork's program: the shared no-entry reasons, then the
// installed record against the good build that serves now. Offline, as every Status is.
func (f *Floor) patchedStatus(p Program) Status {
	st := Status{Program: p, Launcher: f.Launcher(p.Bin())}
	if why := f.noEntryReason(p); why != "" {
		st.Disposition, st.Reason = NoEntry, why
		return st
	}
	rec, err := f.readRecord(p.Bin())
	if errors.Is(err, ErrNewerRecord) {
		st.Disposition, st.Reason, st.Newer = Missing, err.Error(), true
		return st
	}
	ps := f.patched(p)
	if err != nil || rec == nil {
		st.Disposition, st.Reason = Missing, "not installed yet"
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			st.Reason = "its record is unreadable (" + err.Error() + ")"
		}
		return f.patchedProvisionable(st, ps)
	}
	if !usable(rec, st.Launcher) {
		st.Disposition, st.Reason = Missing, "its installed files are gone"
		return f.patchedProvisionable(st, ps)
	}
	st.Disposition, st.Record = Provisioned, rec
	st.Pending = f.patchedPending(p, rec, ps)
	return st
}

// patchedPending is why a provisioned patched fork's install is not the good build that serves now,
// "" when it is: the good build moved (an advance here or a jail launch's), none serves (the user's
// edit, or a good build no record or store names), or a Node script below the node_floor. Each
// reinstalls on the next Ensure, which runs the advance first.
func (f *Floor) patchedPending(p Program, rec *Record, ps PatchedState) string {
	in := p.Install
	switch {
	case rec.Via != via(in):
		return "the pack now installs it via " + via(in) + ", not " + rec.Via
	case rec.Declared != declared(in):
		return "the pack now declares " + declared(in) + " (installed: " + rec.Declared + ")"
	case ps.Good == nil:
		return ps.Reason + " (installed: " + rec.Version + ")"
	case rec.Revision != ps.Good.Commit || rec.Recipe != ps.Good.Recipe:
		return "fork pack " + in.ForkedBy + "'s good build is now " + ps.Good.Label + " (installed: " + rec.Version + ")"
	case rec.Node != "" && !packdecl.SatisfiesNodeFloor(rec.Node, in.NodeFloor):
		return "it runs on Node " + rec.Node + ", below the pack's node_floor " + in.NodeFloor
	}
	return ""
}

// patchedProvisionable is provisionable's patched arm, for a Missing entry: NoEntry when the good
// build's entry cannot be the floor's copy — its manifest's program, or that program's dynamic
// loader (HP-D15) — or when nothing serves and this machine cannot run the advance's build. It
// reads the store offline.
func (f *Floor) patchedProvisionable(st Status, ps PatchedState) Status {
	p := st.Program
	in := p.Install
	if ps.Good != nil && ps.Good.Entry != nil {
		if why := buildUnusableAs(p, f.patchedWhat(p, ps.Good), ps.Good.Entry); why != "" {
			st.Disposition, st.Reason = NoEntry, why
		} else if why := f.storeProgramLoaderProblem(ps.Good.Entry, in.ProgramPath()); why != "" {
			// Its program's dynamic loader, read from the store before anything is materialized
			// (HP-D15), as a plain fork's build is (buildProvisionable).
			st.Disposition, st.Reason = NoEntry, f.patchedWhat(p, ps.Good)+" "+why
		}
		return st
	}
	if why := f.cannotAdvance(); why != "" {
		st.Disposition = NoEntry
		st.Reason = "there is no build of " + p.Bin() + " from fork pack " + in.ForkedBy + "'s patch series on this " +
			"machine, and " + why + runtimeStep(f.canAdvance(), "builds it")
		return st
	}
	switch {
	case ps.Good != nil:
		st.Reason = "not installed yet: its good build " + ps.Good.Label + " is gone from the capture store, and the " +
			"install builds it again"
	default:
		st.Reason = "not installed yet: the install checks fork pack " + in.ForkedBy + "'s upstream, replays its " +
			"patch series and builds it"
	}
	return st
}

// patchedWhat names a patched fork's build as a line says it.
func (f *Floor) patchedWhat(p Program, g *PatchedBuild) string {
	return "fork pack " + p.Install.ForkedBy + "'s patched build of " + p.Bin() + " at " + g.Label
}

// cannotAdvance says why this machine cannot run a patched fork's advance now, "" when it can: the
// advance builds in a jail, as a plain fork's build act does (cannotBuild).
func (f *Floor) cannotAdvance() string {
	switch {
	case f.NoAdvance != "":
		return f.NoAdvance
	case !f.canAdvance():
		return "this yolo runs no patched fork's advance here"
	case f.CaptureUnavailable != nil:
		return f.CaptureUnavailable()
	}
	return ""
}

func (f *Floor) canAdvance() bool {
	return f.NoAdvance == "" && (f.Advance != nil || f.ResolvePatched != nil)
}

// patchedUpdatesAllowed is the refresh arm's `agent_updates` question for a patched fork, asked for
// the fork pack and for its base: a user who holds either holds the program it runs (PF-D19).
func (f *Floor) patchedUpdatesAllowed(p Program) bool {
	if f.UpdatesAllowed == nil {
		return true
	}
	return f.UpdatesAllowed(p.Pack) && (p.Install.ForkedBy == "" || f.UpdatesAllowed(p.Install.ForkedBy))
}

// advances reports whether an Ensure of p runs the advance before it decides: whenever this machine
// can build, except for a current entry `agent_updates` holds — the refresh arm, under UpdatesAllowed.
// A Missing or pending entry runs it even under a hold, which builds an edit at the good build's
// commit and gives a fork with no good build its first advance (PF-D19), and checks nothing else.
func (f *Floor) advances(p Program, st Status) bool {
	if f.cannotAdvance() != "" {
		return false
	}
	return !(st.Disposition == Provisioned && st.Pending == "" && !f.patchedUpdatesAllowed(p))
}

// writePatchFailureOnce prints state's patch failure block unless the advance that found it already
// did: PF-D81's error is said once per operation, so a reader never wonders whether two failures
// happened.
func (f *Floor) writePatchFailureOnce(p Program, state PatchedState) {
	if state.PatchFailureSaid {
		return
	}
	f.writePatchFailure(p, state.PatchFailure)
}

func (f *Floor) writePatchFailure(p Program, pf *packsrc.PatchFailure) {
	target := pf.Target.Tag
	if target == "" {
		target = pf.Target.Commit
	}
	fmt.Fprintf(f.out(), "ERROR: fork %s: patch application failed at upstream %s (%s)\n", p.Install.ForkedBy+"/"+p.Bin(), target, pf.Target.Commit)
	if pf.Member != "" {
		fmt.Fprintf(f.out(), "  Patch: %s\n", printableFloorDetail(pf.Member))
	}
	if pf.Kind == "conflict" {
		fmt.Fprintf(f.out(), "  Conflict: %s\n", printableFloorDetail(strings.Join(pf.Paths, ", ")))
	} else {
		fmt.Fprintf(f.out(), "  Application command: %s\n", printableFloorDetail(pf.Detail))
	}
	fmt.Fprintln(f.out(), "  Operation stopped; no older fit or base will be built.")
	fmt.Fprintf(f.out(), "  Repair: yolo pack rebase %s/%s --onto %s\n", p.Install.ForkedBy, p.Bin(), pf.Target.Commit)
	fmt.Fprintln(f.out(), "  Bypass: YOLO_ALLOW_PATCH_FAILURES=1 yolo host -- "+p.Bin())
}

func printableFloorDetail(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "\\\\x%02x", r)
		}
	}
	return b.String()
}

// ensurePatched is Ensure for a patched fork's program: the advance first, outside the floor's lock
// — it has its own, and a launch waits for it interruptibly (PF-D25), which a wait on the floor's
// unbounded lock would not be — and then the install of the good build it leaves, under the lock.
func (f *Floor) ensurePatched(ctx context.Context, p Program) (Status, Outcome, error) {
	prepared, err := f.PreparePatched(ctx, p, true)
	if err != nil {
		return f.Status(p), "", err
	}
	return f.EnsurePrepared(ctx, p, prepared)
}

// patchedGoneReason is why the floor cannot install good build g, whose store entry is gone, with the
// act that builds it again: `yolo capture <bin>` on a machine that runs the advance — which builds it
// whatever a back-off or a hold says, where the next launch's advance may not — and, on one that
// cannot, the runtime it needs.
func (f *Floor) patchedGoneReason(p Program, g *PatchedBuild) string {
	gone := "fork pack " + p.Install.ForkedBy + "'s good build " + g.Label + " is gone from the capture store"
	if why := f.cannotAdvance(); why != "" {
		return gone + ", and " + why + runtimeStep(f.canAdvance(), "builds it")
	}
	return gone + ", and nothing built it again — `yolo capture " + p.Bin() + "` builds it, and the next `yolo host -- " +
		p.Bin() + "` installs it"
}

// removeInstalled takes p's launcher and record out of the floor, as Ensure's failed install of a
// near-miss does: its launcher leaves bin/, which ends every host agent's PATH (HE-D1), and the
// install directory stays for the next install to prune (unlinking stops no agent already running).
func (f *Floor) removeInstalled(p Program) {
	rec, err := f.readRecord(p.Bin())
	if err != nil || rec == nil {
		return
	}
	_ = os.Remove(f.Launcher(p.Bin()))
	_ = os.Remove(f.recordPath(p.Bin()))
	f.appendReceipt(receipt{Act: "remove", Bin: p.Bin(), Pack: p.Pack, Version: rec.Version, Dir: rec.Dir})
}

// patchedServesANearMiss is servesANearMiss's patched arm: an installed copy serves a near-miss when
// it is not a build of this fork's series and recipe as they stand — another repository, a plain
// fork's build or the base's upstream program, or the user's edit (PF-D23). Its upstream commit may
// lag the good build's, which a newer upstream that fails leaves it at (PF-D8).
func (f *Floor) patchedServesANearMiss(p Program, rec *Record) bool {
	ps := f.patched(p)
	return rec.Via != packdecl.ViaSource || rec.Declared != declared(p.Install) || ps.Recipe == "" ||
		rec.Recipe != ps.Recipe || rec.Node != "" && !packdecl.SatisfiesNodeFloor(rec.Node, p.Install.NodeFloor)
}

// installFromPatchedBuild materializes the good build of p into dir/home — the entry a jail launch
// is handed, relocated — the advance having run first (ensurePatched). It builds nothing itself.
func (f *Floor) installFromPatchedBuild(ctx context.Context, p Program, dir string) (*Record, error) {
	return f.installFromPatchedState(ctx, p, dir, f.patched(p))
}

func (f *Floor) installFromPatchedState(ctx context.Context, p Program, dir string, ps PatchedState) (*Record, error) {
	in := p.Install
	switch {
	case ps.Good == nil:
		// The advance said why above, with its next step; this is the floor's half of it.
		return nil, fmt.Errorf("%s — the next `yolo host -- %s` tries again", ps.Reason, p.Bin())
	case ps.Good.Entry == nil:
		return nil, errors.New(f.patchedGoneReason(p, ps.Good))
	}
	g := ps.Good
	what := f.patchedWhat(p, g)
	if why := buildUnusableAs(p, what, g.Entry); why != "" {
		return nil, &noEntryError{reason: why}
	}
	home := filepath.Join(dir, "home")
	res, err := capture.Materialize(capture.MaterializeOptions{Entry: g.Entry, Home: home, Stderr: f.out(),
		Confined: true})
	if errors.Is(err, capture.ErrNotRelocatable) {
		buildHome := "/home/agent"
		if m, merr := capture.ReadManifest(g.Entry.Root); merr == nil && m.Home != "" {
			buildHome = m.Home
		}
		return nil, errors.Join(&noEntryError{reason: notRelocatableReason(what, buildHome, []string{err.Error()})}, err)
	}
	if err != nil {
		return nil, fmt.Errorf("materializing the build of %s: %w", p.Bin(), err)
	}
	// THE ENTRY IS STILL WHOLE (PF-D51): another advance's move reaps, marker first, every build no
	// running jail was handed, and this copy is no jail's.
	if !g.Entry.Complete() {
		return nil, fmt.Errorf("the build %s left the capture store while it was copied (another launch moved "+
			"fork pack %s's good build); the next `yolo host -- %s` installs the good build it moved to",
			g.Entry.Key, in.ForkedBy, p.Bin())
	}
	entryPath := filepath.Join(home, filepath.FromSlash(in.ProgramPath()))
	st, err := os.Stat(entryPath)
	if err != nil || st.IsDir() || st.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("the build of %s (%s) holds no runnable ~/%s", p.Bin(), g.Entry.Key, in.ProgramPath())
	}
	f.say("materialized %s from fork build %s (%s) by %s (%d files)", p.Bin(), g.Entry.Key, g.Label,
		res.Mechanism(), res.Files)
	rec := &Record{Entry: entryPath, Capture: g.Entry.Key, Revision: g.Commit, Recipe: g.Recipe, Version: g.Label}
	return f.execRecord(ctx, in, rec)
}

// describePatched is describeRecipe's patched arm.
func (f *Floor) describePatched(p Program) string {
	ps := f.patched(p)
	if ps.Good != nil {
		return f.patchedWhat(p, ps.Good)
	}
	return "fork pack " + p.Install.ForkedBy + "'s patch series on " + p.Install.Source
}
