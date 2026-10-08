package hostfloor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// built.go is the floor's third recipe: a FORK's program (docs/design/forked-programs-as-packs.md,
// `via: "source"`), whose host copy is the capture store's build of the fork's PINNED commit,
// relocated into the floor (FP-D4: build once per platform, in the capture jail, and relocate at
// materialize).
//
// # What is the installer recipe's, and what is not
//
// The shape is installFromCapture's — the store's entry, a CONFINED relocating materialize into a
// fresh install directory, and the act that fills the store run first when it holds nothing this
// host can use — with four differences, each a rule of the fork route:
//
//   - THE ENTRY IS THE PIN'S, never the newest. ResolveBuild answers only for the commit the fork
//     lock pins and the fork's current recipe, so a build of another commit is a miss and a
//     rebuild, not a substitution (§9: never serve a near-miss).
//   - A BUILD THAT CANNOT MOVE IS NO FLOOR ENTRY, and it is never rebuilt for the floor. A fork's
//     build always records the full reference scan, so a not-relocatable one embeds the jail's
//     home where no rewrite reaches (a compiled binary): the same commit and recipe build the same
//     bytes again, and §9's answer is that this notch does not get the program, and says which
//     home it was built for. An installer capture recorded before the scan is recaptured instead.
//   - NO REFRESH POLL (refresh): a fork moves when its pin does, which `yolo pack update` moves on
//     the host, and a moved pin is Pending.
//   - A NODE SCRIPT RUNS ON THE FLOOR'S NODE, by absolute path, as an npm program does (HP-DIR2
//     item 2): a Node fork built in the jail leaves a script whose `#!` would otherwise pick
//     whatever `node` the caller's PATH holds.

// buildProvisionable is provisionable's source arm: a Missing fork build is NoEntry when the
// store's build at the pin cannot be the floor's copy (buildUnusable), or when the store has none
// and this machine cannot build one. It reads the store offline.
func (f *Floor) buildProvisionable(st Status) Status {
	if f.ResolveBuild == nil {
		return st
	}
	p := st.Program
	commit, _ := f.forkPin(p)
	if commit == "" {
		// AWAITING ITS PIN (noEntryReason lets only that through, FP-D18): the install pins the
		// fork's source first, then builds what it pinned, so the store can hold no build of it yet
		// and the question is only whether this machine can build. A machine that cannot is not
		// pinned either: a pin fetches the fork's source, and with no build to follow it would only
		// fix the commit sooner than the first launch that can build it does.
		if why := f.cannotBuild(p.Bin(), "pins the fork and builds it"); why != "" {
			st.Disposition = NoEntry
			st.Reason = "it is built from source by fork pack " + p.Install.ForkedBy + ", which has no pin yet, and " +
				why
			return st
		}
		st.Reason = "not pinned yet: the install pins fork pack " + p.Install.ForkedBy + "'s source (" +
			p.Install.Source + ") first, then builds it"
		return st
	}
	entry, err := f.ResolveBuild(p, commit)
	if err == nil {
		if why := buildUnusable(p, commit, entry); why != "" {
			st.Disposition, st.Reason = NoEntry, why
		} else if why := f.storeProgramLoaderProblem(entry, p.Install.ProgramPath()); why != "" {
			// Its program's dynamic loader, read from the store before anything is materialized
			// (HP-D15): a build made in the jail asks for the loader the jail has, and a Node script
			// for the one Node's official build asks for.
			st.Disposition = NoEntry
			st.Reason = "fork pack " + p.Install.ForkedBy + "'s build of " + p.Bin() + " at " + buildVersion(commit) +
				" " + why
		}
		return st
	}
	if why := f.cannotBuild(p.Bin(), "builds it"); why != "" {
		st.Disposition, st.Reason = NoEntry, f.noBuildReason(p.Bin(), commit, why)
	}
	return st
}

// storeProgramLoaderProblem says why the program a fork's store entry holds at the home-relative
// rel cannot start on this machine, its links resolved through the entry's manifest
// (programInManifest), as a clause that follows the build's name — "" when it can, and when the
// manifest holds no program there, which the caller's own check reports. A program asks for its own
// loader (programLoaderProblem), and A NODE SCRIPT for Node's official build's, the build execRecord
// runs it on: a script names no loader itself, so without this a machine lacking Node's would
// materialize it and fetch Node on every launch, only to refuse it once the tarball was in.
func (f *Floor) storeProgramLoaderProblem(entry *capture.Entry, rel string) string {
	if !f.probesLoaders() {
		return ""
	}
	m, err := capture.ReadManifest(entry.Root)
	if err != nil {
		return ""
	}
	final, why := programInManifest(m, rel)
	if why != "" {
		return ""
	}
	program := filepath.Join(entry.Tree, filepath.FromSlash(final))
	if isNodeScript(program) {
		if why := f.nodeLoaderProblem(); why != "" {
			return "is a Node script, and " + why
		}
		return ""
	}
	return f.programLoaderProblem(program)
}

// noBuildReason is the no-floor-entry reason for a fork the store has no build of at commit, on a
// machine that cannot run the build act (why, cannotBuild's, which ends with its step): the one
// spelling Status and an install share.
func (f *Floor) noBuildReason(bin, commit, why string) string {
	return "there is no build of " + bin + " at " + buildVersion(commit) + " on this machine, and " + why
}

// buildUnusable says why the store's build entry of p at commit cannot be the floor's copy, "" when
// it can: its manifest records no runnable program at the fork's program path, or it was built for
// the jail's home and cannot move out of it. Read from the manifest alone, before a materialize.
func buildUnusable(p Program, commit string, entry *capture.Entry) string {
	return buildUnusableAs(p, "fork pack "+p.Install.ForkedBy+"'s build of "+p.Bin()+" at "+buildVersion(commit), entry)
}

// buildUnusableAs is buildUnusable with the build named as what: a plain fork's by its pin, a
// patched fork's by its good build (patched.go).
func buildUnusableAs(p Program, what string, entry *capture.Entry) string {
	in := p.Install
	m, err := capture.ReadManifest(entry.Root)
	if err != nil {
		return what + " has an unreadable manifest (" + err.Error() + ")"
	}
	if _, why := programInManifest(m, in.ProgramPath()); why != "" {
		return what + " cannot run outside a jail: " + why
	}
	if !m.Relocatable {
		return notRelocatableReason(what, m.Home, m.NotRelocatable)
	}
	return ""
}

// notRelocatableReason is the no-floor-entry reason for a build that cannot move out of the jail's
// home: which home it was built for, and the manifest's own reasons — §9's "that notch does not get
// the program, and says which notch it was built for".
func notRelocatableReason(what, home string, reasons []string) string {
	why := "its build recorded no reason"
	if len(reasons) > 0 {
		why = strings.Join(reasons, "; ")
	}
	return what + " was built for the jail's home, " + home + ", and cannot be moved out of it, so it " +
		"runs in a jail only (" + why + ")"
}

// installFromBuild materializes the store's build of p at its pin into dir/home — the entry a jail
// launch materializes, relocated — building it first, in a sealed capture jail (Build), when the
// store has none.
func (f *Floor) installFromBuild(ctx context.Context, p Program, dir string) (*Record, error) {
	in := p.Install
	bin := p.Bin()
	commit, why := f.forkPin(p)
	if commit == "" {
		return nil, &noEntryError{reason: "it is built from source by fork pack " + in.ForkedBy + ", and " + why}
	}
	if f.ResolveBuild == nil {
		return nil, errors.New("this machine has no capture store")
	}
	entry, err := f.ResolveBuild(p, commit)
	if err != nil {
		// NO BUILD AND NOTHING TO BUILD ONE WITH is no floor entry here, as provisionable answers
		// for a fork the floor does not hold yet. It is asked again because a reinstall at a moved
		// pin never comes through provisionable, and a build act started with no runtime would
		// only fail, turning a no-floor-entry refusal that names its reason (host-notch-readiness.md
		// HNR-D2) into a failed build.
		if why := f.cannotBuild(bin, "builds it"); why != "" {
			return nil, &noEntryError{reason: f.noBuildReason(bin, commit, why)}
		}
		f.say("no build of %s at %s on this machine yet; building it from fork pack %s's source %s",
			bin, buildVersion(commit), in.ForkedBy, f.buildHow())
		if entry, err = f.Build(p, commit); err != nil {
			return nil, fmt.Errorf("building %s at %s: %w", bin, buildVersion(commit), err)
		}
		if entry == nil {
			return nil, fmt.Errorf("the build of %s at %s ran and stored nothing", bin, buildVersion(commit))
		}
	}
	if why := buildUnusable(p, commit, entry); why != "" {
		return nil, &noEntryError{reason: why}
	}
	home := filepath.Join(dir, "home")
	// CONFINED, as an installer's capture is: the manifest is the build jail's account of itself,
	// and this materialize runs on the host.
	res, err := capture.Materialize(capture.MaterializeOptions{Entry: entry, Home: home, Stderr: f.out(),
		Confined: true})
	if errors.Is(err, capture.ErrNotRelocatable) {
		// The manifest said it may move, and a reference it lists cannot be honored: the same
		// answer, from the rewrite rather than the record.
		buildHome := "/home/agent"
		if m, merr := capture.ReadManifest(entry.Root); merr == nil && m.Home != "" {
			buildHome = m.Home
		}
		return nil, &noEntryError{reason: notRelocatableReason("fork pack "+in.ForkedBy+"'s build of "+bin+
			" at "+buildVersion(commit), buildHome, []string{err.Error()})}
	}
	if err != nil {
		return nil, fmt.Errorf("materializing the build of %s: %w", bin, err)
	}
	entryPath := filepath.Join(home, filepath.FromSlash(in.ProgramPath()))
	st, err := os.Stat(entryPath)
	if err != nil || st.IsDir() || st.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("the build of %s (%s) holds no runnable ~/%s", bin, entry.Key, in.ProgramPath())
	}
	f.say("materialized %s from fork build %s (%s) by %s (%d files)", bin, entry.Key, buildVersion(commit),
		res.Mechanism(), res.Files)
	rec := &Record{Entry: entryPath, Capture: entry.Key, Revision: commit, Recipe: in.SourceRecipe(),
		Version: buildVersion(commit)}
	return f.execRecord(ctx, in, rec)
}

// buildHow is the clause the line that starts a fork's build says how it runs: the macos-user
// sandbox account on a Mac (FP-D24), whose build serves this floor alone, and on Linux the sealed jail
// whose build every jail on the machine reuses.
func (f *Floor) buildHow() string {
	if f.GOOS == "darwin" {
		return "as the macos-user sandbox account, sealed under Seatbelt in a throwaway home (once per " +
			"commit per Mac; sudo may ask for your password)"
	}
	return "in a sealed jail (once per commit per machine, and every jail on this machine reuses it)"
}

// execRecord fills rec's Exec for a fork's build materialized at rec.Entry: the program itself, or
// A NODE SCRIPT started by the floor's own Node, never by the `node` its `#!` would find.
func (f *Floor) execRecord(ctx context.Context, in packdecl.Install, rec *Record) (*Record, error) {
	if !isNodeScript(rec.Entry) {
		rec.Exec = []string{rec.Entry}
		return rec, nil
	}
	// NODE'S LOADER, BEFORE NODE IS FETCHED (HP-D15): the floor's status checks the store's build
	// first where it can (buildProvisionable, patchedProvisionable), and this is the check every
	// fork's install reaches, a patched fork's and a build the install itself just made included,
	// so no launch downloads a Node this machine cannot start.
	if why := f.nodeLoaderProblem(); why != "" {
		return nil, &noEntryError{reason: "the build of " + in.Bin + " (" + rec.Version + ") is a Node script, and " + why}
	}
	v := f.NodeVersion()
	if !packdecl.SatisfiesNodeFloor(v, in.NodeFloor) {
		return nil, fmt.Errorf("the floor's Node v%s is below the pack's node_floor %s", v, in.NodeFloor)
	}
	nodeBin, err := f.ensureNode(ctx, v)
	if err != nil {
		return nil, err
	}
	rec.Node = v
	rec.Exec = []string{filepath.Join(nodeBin, "node"), rec.Entry}
	return rec, nil
}
