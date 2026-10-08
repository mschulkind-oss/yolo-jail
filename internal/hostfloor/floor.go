// Package hostfloor is the HOST AGENT FLOOR (docs/design/host-tool-provisioning.md): the
// `program` binaries of the packs the user-scope config selects, installed by yolo into a
// directory it owns — the HOST PREFIX, paths.HostFloorDir — and run from there by absolute
// path, so `yolo host -- <agent>` starts the same agent whichever launcher started yolo (a
// terminal, a Waybar widget, cron).
//
// # The two rules the prefix keeps, and why each is load-bearing
//
//   - ONLY FLOOR NAMES IN bin/. bin/ holds exactly one executable per PROVISIONED entry and
//     nothing else: no node, no npm, no package's secondary binaries. A host launch appends
//     bin/ LAST to the agent's PATH (HE-D1), and this rule is what makes that safe — it adds
//     agent names only, and being last it supplies one only where nothing earlier has it. It
//     can never put a `node` ahead of the project's.
//   - NEVER JAIL-REACHABLE. The host executes these files with the user's full authority, so
//     the directory is created 0700 under the state dir, never under a segment any jail
//     mounts (paths.HostFloorDir; a run-package test pins the mount side).
//
// # What is here and what is not
//
// This package decides and acts on ONE program at a time: its disposition (Status), putting
// it in place (Ensure: first install, the throttled evergreen refresh, a moved declaration),
// taking entries back out (Reconcile, Sweep), and running its declared pre-launch refresh just
// before a launch execs it, whichever copy that is (PrelaunchRefresh). It knows no pack loader
// and no config: the caller hands it the programs (Program) and every policy input as a field of
// Floor, so the launch, `yolo host apply`, `yolo check` and `yolo prune` ask the same questions
// of the same code without this package importing any of their worlds.
//
// Three recipes, per OQ-HP3 and OQ-HP4, and forked-programs-as-packs.md FP-D4:
//
//   - `via: npm`: installed with the floor's OWN Node — the official release tarball, verified
//     against its published sha256 (node.go) — into a prefix-private npm prefix, and started
//     by that Node's absolute path.
//   - `via: installer`: materialized from the machine's `yolo capture` store, the same entry a
//     jail materializes, where this host matches the capture jail (Linux). A Linux host with no
//     container runtime captures it itself, its installer confined by Landlock (HP-D18), and a Mac
//     through the macos-user capture act, Seatbelt and the sandbox account (HP-D2): each fills the
//     same store, and the floor materializes either the same way.
//   - `via: source`, a FORK's program (built.go): the capture store's build of the fork's
//     PINNED commit, relocated into the prefix — on Linux the entry a jail launch materializes, its
//     build jail's platform being the host's, and on a Mac a build of its own, made for darwin by the
//     macos-user fork-build act, Seatbelt and the sandbox account under the seal (FP-D24). A Node
//     script among them is started by the floor's own Node, as an npm program is. A PATCHED fork's
//     (patched.go) is the build of its GOOD BUILD instead, and its install runs the fork's advance
//     first (docs/design/patched-forks.md §9); it is Linux's alone.
//
// # The layout (every name below is this package's)
//
//	<Dir>/                        0700
//	  bin/<bin>                   the launcher yolo host execs; one per provisioned entry
//	  node/v<version>/            an official Node release, extracted and verified
//	  programs/<bin>/<id>/        one install of one program; its completion marker last
//	  records/<bin>.json          which install bin/<bin> runs, and when it was checked
//	  receipts.jsonl              one line per install, update or removal
//	  locks/<name>.lock           one install at a time per program (flock)
//	  refresh/<bin>.stamp         when a launch last ran <bin>'s pre-launch refresh (prelaunch.go)
//	  refresh/<bin>.seen/<key>    one per watched content a refresh of <bin> succeeded with
//	  refresh/<bin>.lock          one pre-launch refresh at a time per program (flock)
//	  cache/npm/                  npm's download cache
//	  downloads/                  Node tarballs in flight
package hostfloor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/updatehint"
)

// Program is one floor candidate: a `program` contribution of a selected pack.
type Program struct {
	// Pack is the pack that declares it — the unit `agent_updates` and `host_floor` key on.
	Pack string
	// Install is the contribution's install projection (packdecl.Install).
	Install packdecl.Install
}

// Bin is the program's binary name, which is also its floor entry's name.
func (p Program) Bin() string { return p.Install.Bin }

// Disposition is a FLOOR ENTRY DISPOSITION (coined in host-tool-provisioning.md's Defined
// terms): what `yolo check` and a launch say about one entry.
type Disposition string

const (
	// Provisioned: in the prefix. It is the copy `yolo host` runs.
	Provisioned Disposition = "provisioned"
	// Missing: the floor can hold it and does not yet. The next `yolo host -- <bin>` or
	// `yolo host apply --assert` installs it (HP-D3), unless a newer yolo wrote its record
	// (Status.Newer), which nothing here installs over.
	Missing Disposition = "missing"
	// NoEntry: the floor cannot hold it on this machine. Status.Reason says why. `yolo host`
	// refuses to launch it (host-notch-readiness.md HNR-D2), unless OutsideTheFloor says the
	// launch's PATH answers for it instead.
	NoEntry Disposition = "no floor entry"
)

// OutsideTheFloor reports whether the floor holds no entry for p because it is not the floor's to
// hold at all, rather than because this machine cannot: the user-scope `host_floor` leaves its pack
// out, the user's `provisioners` order gives it to another manager, or its vendor publishes no build
// for this platform (host-notch-readiness.md HNR-D4). For such a program `yolo host -- <bin>` runs
// the copy on the launch's PATH, and the launch's readiness act does not install it; for every other
// no-entry program a launch refuses (HNR-D2). The first two are the user's own decisions; the last is
// the jail's own rule, which writes no launcher for an unpublished program.
func (f *Floor) OutsideTheFloor(p Program) bool {
	if f.Include != nil && !f.Include(p.Pack) {
		return true
	}
	if f.Outranked != nil && f.Outranked(p) != "" {
		return true
	}
	return p.Install.UnpublishedReason(f.GOOS, f.GOARCH) != ""
}

// Status is one entry's disposition, with what a report needs to say about it.
type Status struct {
	Program     Program
	Disposition Disposition
	// Reason is why the entry is NoEntry or Missing, as a clause a report can print after the
	// bin name. Empty for a Provisioned entry.
	Reason string
	// Record is the install bin/<bin> runs, for a Provisioned entry; nil otherwise.
	Record *Record
	// Pending is set on a Provisioned entry the next launch reinstalls, saying why (the pack's
	// declaration moved; its interpreter no longer meets the pack's node_floor). The current
	// install serves until the new one succeeds.
	Pending string
	// Newer is set on a Missing entry whose record a newer yolo wrote. Nothing installs it: Ensure
	// refuses to install over that record, and Reason is the refusal (updatehint.NewerSchema),
	// whose next step is `yolo update`.
	Newer bool
	// Launcher is where bin/<bin> is or would be.
	Launcher string
}

// Record is one provisioned install: the file bin/<bin> is generated from, and what `yolo
// check` reports. One per program, rewritten atomically on every install and every check.
type Record struct {
	Schema int    `json:"schema"`
	Bin    string `json:"bin"`
	Pack   string `json:"pack"`
	// Via is the manifest's word for the recipe: "npm", "installer" or "source".
	Via string `json:"via"`
	// Declared is what the pack asked for: the npm install spec, the installer URL, or a fork's
	// source address. A changed declaration reinstalls (a different package, a different URL, a
	// different repository).
	Declared string `json:"declared"`
	// Version is what was installed: npm's resolved package version, for an installer capture
	// the versions-dir entry it left (else "capture <key>"), and for a fork's build
	// "commit <short>" — the revision, the one fact about a source-built program nothing else
	// keeps (forked-programs-as-packs.md OQ-FP6).
	Version string `json:"version"`
	// Node is the Node release an npm program, or a fork's Node script, runs on; "" otherwise.
	Node string `json:"node,omitempty"`
	// Capture is the capture store entry an installer program or a fork's build was
	// materialized from.
	Capture string `json:"capture,omitempty"`
	// Revision and Recipe are a fork's build: the full commit it was built at and its recipe hash
	// (packdecl.ForkRecipe). The fork's pin moving, or its recipe changing, reinstalls; "" for
	// every other recipe.
	Revision string `json:"revision,omitempty"`
	Recipe   string `json:"recipe,omitempty"`
	// Dir is the install directory under programs/<bin>/.
	Dir string `json:"dir"`
	// Entry is the program file: npm's bin link, the capture's ~/.local/bin/<bin>, or the fork
	// build's program path (packdecl.Install.ProgramPath).
	Entry string `json:"entry"`
	// Exec is the argv prefix bin/<bin> starts, by absolute path: [node, entry] for a Node
	// script, [entry] for anything else.
	Exec []string `json:"exec"`
	// Installed is when this install finished; Checked when the evergreen poll last ran.
	Installed time.Time `json:"installed"`
	Checked   time.Time `json:"checked"`
}

const (
	recordSchema = 1
	// completeMarker is the last file an install writes into its directory. A directory
	// without it is an install that did not finish, which the next install of that program
	// removes (it holds the program's lock, so the writer is gone).
	completeMarker = ".yolo-floor-complete"
	// DefaultInstallTimeout bounds a first install (§4: "bounded at 600 s").
	DefaultInstallTimeout = 600 * time.Second
	// DefaultPollTimeout bounds the evergreen poll, the jail launcher's UPDATE_TIMEOUT.
	DefaultPollTimeout = 60 * time.Second
	// DefaultUpdateInterval is the jail launcher's UPDATE_INTERVAL: at most one evergreen
	// poll per program per hour, at the program's own invocation.
	DefaultUpdateInterval = time.Hour
	// DefaultCaptureRefreshAge is how old the machine's newest capture of an installer program
	// may be before the evergreen refresh runs the capture act again to look for a newer release
	// (HP-D16). An npm poll is one registry request; a capture boots a jail and runs the vendor's
	// installer, so it is asked for once a day rather than once an hour.
	DefaultCaptureRefreshAge = 24 * time.Hour
)

// Floor is one host prefix and every input its decisions read. The zero values of the func
// fields mean the conservative answer each documents; the caller in internal/cli fills them.
type Floor struct {
	// Dir is the prefix (paths.HostFloorDir).
	Dir string
	// GOOS and GOARCH are this host's platform: the one the floor installs for.
	GOOS, GOARCH string
	// Now is the clock. nil => time.Now.
	Now func() time.Time
	// Node says where the floor's Node comes from (node.go).
	Node NodeDist
	// NodeFloor is the highest `node_floor` any selected npm program declares; the floor's Node
	// is Node.Shipped raised to it (OQ-HP4).
	NodeFloor string
	// Include reports whether a pack's programs are in the floor (`host_floor`). nil => every
	// selected pack's are, which is the default by OQ-HP1.
	Include func(pack string) bool
	// Outranked is why the user's provisioner order gives a program to a provisioner other than
	// the pack's own recipe, the one the floor installs (docs/design/provisioner-sets.md PS-D12),
	// "" when it does not; such a program has no floor entry. nil => no order, so never
	// (Outranker builds one).
	Outranked func(p Program) string
	// UpdatesAllowed is the `agent_updates` policy for a pack. nil => allowed, the ruled default.
	UpdatesAllowed func(pack string) bool
	// ResolveCapture finds the capture store's entry for bin on this host's platform, the same
	// selection a jail's materialize makes. nil => this host has no capture store.
	ResolveCapture func(bin string) (*capture.Entry, error)
	// Capture runs `yolo capture <bin>`, filling the store. nil => this host cannot capture.
	Capture func(bin string) error
	// CaptureUnavailable says why this machine cannot boot a capture or build JAIL right now ("" when
	// it can): a Linux fork's build and a patched fork's advance each boot one, so a host with no
	// container runtime cannot make one (a Mac's fork build boots none: BuildActUnavailable). Asked only for a program that is neither provisioned nor in the store,
	// which then has no floor entry HERE rather than an install bound to fail (a selected pack
	// delivers a program the floor "holds, or can provision", host-agent-environment.md's launch PATH
	// terms), its reason ending with runtimeStep. It answers for an installer's capture too, unless
	// CaptureActUnavailable does. nil => it can.
	CaptureUnavailable func() string
	// CaptureActUnavailable says why Capture cannot capture bin on this machine right now, as a whole
	// clause that ends with the step that ends it — does is what the next launch then does ("captures
	// it") — or "" when it can. It exists because an installer's capture need not boot a container: a
	// Mac's is the macos-user capture act (HP-D2), and a Linux host with no runtime captures under
	// Landlock (HP-D18), so its reasons and their steps are not a runtime's. nil => CaptureUnavailable
	// answers, with runtimeStep.
	CaptureActUnavailable func(bin, does string) string
	// CaptureHow is how Capture runs its installer, for the line that starts one: a parenthetical,
	// without its parentheses. nil, or "", is a capture jail's.
	CaptureHow func() string
	// ForkPin is the fork lock's pin of a source-built program (forked-programs-as-packs.md
	// FP-D7): the full commit its fork's source is pinned to, or "" and why there is none, naming
	// what pins it. nil => no pin can be read, so no source-built program has a floor
	// entry: the floor never builds or serves a fork at a revision the lock does not name.
	ForkPin func(p Program) (commit, reason string)
	// ForkPinnable reports whether ForkPin's missing pin of p is one an install can make: the fork
	// lock was read and holds no pin for the fork's declared source (forked-programs-as-packs.md
	// FP-D18). nil => none is, and a fork with no pin has no floor entry.
	ForkPinnable func(p Program) bool
	// PinFork makes that pin, as a launch does: the fork's ref resolved once and the commit recorded
	// in the fork lock. It returns the commit, or "" and why there is still no pin, naming the next
	// step, and says what it did through say. Only Ensure asks it, for a program ForkPinnable says
	// it can pin, never Status, since a pin can fetch. nil => none is made.
	PinFork func(p Program, say func(line string)) (commit, reason string)
	// madePins and failedPins are this Floor's own PinFork answers, by bin: the commit a pin made,
	// or why it could not be made. forkPin reads them ahead of ForkPin, whose answer was read before
	// the pin, so every status after an install agrees with what it installed.
	madePins, failedPins map[string]string
	// ResolveBuild finds the capture store's build of a source-built program at commit, for this
	// host's platform: the same hit a jail launch asks for (the newest build of the fork's source,
	// and only when it is of that commit and the fork's current recipe — never a near-miss).
	// nil => this host has no capture store.
	ResolveBuild func(p Program, commit string) (*capture.Entry, error)
	// Patched reads a PATCHED fork's program (docs/design/patched-forks.md §9, patched.go): the
	// recipe its series asks for now and the good build that serves it, from this machine's check
	// record and capture store, offline — file reads, never git or the network, so Status may ask
	// it. nil => this floor holds no patched fork.
	Patched func(p Program) PatchedState
	// Advance runs a patched fork's ADVANCE for p — the check (throttled hourly), the replay of the
	// series and the build of the newest fit in a sealed capture jail, and the move of the good build
	// once that build is admitted — waiting for it as a launch does (PF-D25: bounded, and a Ctrl-C
	// ends it on the good build), and returns the state after it. Ensure asks it before it decides,
	// outside the floor's lock; never Status. installed is the floor's own copy of p when it is a
	// build of the series as it stands — the copy a failed install keeps (PF-D8) — and nil otherwise:
	// it serves whatever the advance does, so the advance runs as one with a good build serving, and
	// builds no good build that copy already is (PF-D55). nil => no patched fork is advanced here, and
	// none with no good build in the store has a floor entry.
	Advance func(ctx context.Context, p Program, installed *Record) PatchedState
	// NoAdvance is why Advance is nil for this act when the act, not the machine, builds no patched
	// fork, naming the act that does: the host apply `yolo pack update` runs (PF-D12, PF-D56). ""
	// keeps the machine's reason.
	NoAdvance string
	// Build runs the fork's build act for p at commit (a sealed capture jail, or on a Mac the sealed
	// macos-user fork-build act; never unconfined on the host), waiting, bounded, for a build of the
	// same key another process is running, as a jail launch does (FP-D1), and returns the entry it
	// admitted or the one that process did. The entry is taken from the act rather than looked up
	// again: selection is newest-wins on a one-second receipt stamp, so a lookup straight after two
	// builds in one second could answer with the other. nil => this host cannot build.
	Build func(p Program, commit string) (*capture.Entry, error)
	// BuildActUnavailable says why Build cannot build bin on this machine right now, as a whole clause
	// that ends with the step that ends it — does is what the next launch then does ("builds it") — or
	// "" when it can. It exists because a fork's build need not boot a container: a Mac's is the
	// macos-user fork-build act (FP-D24), whose reasons and steps are the sandbox account's, not a
	// runtime's. On a Mac it is also what admits a plain fork's program at all: a floor given none has
	// no darwin build to run. nil => CaptureUnavailable answers, with runtimeStep.
	BuildActUnavailable func(bin, does string) string
	// Environ is the environment the installers are derived from (installerEnv strips the
	// parts that would steer where an install lands). nil => os.Environ().
	Environ []string
	// Home is the HOME an installer runs with. "" => $HOME.
	Home string
	// Hints is the hint locations OtherCopies looks at for a home, besides the PATH it is handed.
	// nil => HintLocations. A test hands in folders under its own root, so what it reports never
	// depends on what this machine keeps in /opt/homebrew/bin.
	Hints func(home string) []string
	// Out receives the progress lines and the installers' own output. nil => discarded.
	Out io.Writer
	// Prefix starts every line this package prints ("yolo host: ").
	Prefix string
	// Root is the filesystem root a program's dynamic loader is looked up under (elfinterp.go,
	// HP-D15). "" => "/". A test hands in a directory of its own, which also makes the check run
	// where it otherwise would not: a Linux floor tested on a Mac.
	Root string
	// InstallTimeout, PollTimeout and UpdateInterval override the defaults above, and
	// CaptureRefreshAge DefaultCaptureRefreshAge.
	InstallTimeout, PollTimeout, UpdateInterval, CaptureRefreshAge time.Duration
}

func (f *Floor) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *Floor) out() io.Writer {
	if f.Out == nil {
		return io.Discard
	}
	return f.Out
}

// say prints one progress line. A launch has no quiet mode (OQ-RO3), so nothing here is gated.
func (f *Floor) say(format string, args ...any) {
	fmt.Fprintf(f.out(), f.Prefix+format+"\n", args...)
}

func (f *Floor) installTimeout() time.Duration {
	if f.InstallTimeout > 0 {
		return f.InstallTimeout
	}
	return DefaultInstallTimeout
}

func (f *Floor) pollTimeout() time.Duration {
	if f.PollTimeout > 0 {
		return f.PollTimeout
	}
	return DefaultPollTimeout
}

func (f *Floor) updateInterval() time.Duration {
	if f.UpdateInterval > 0 {
		return f.UpdateInterval
	}
	return DefaultUpdateInterval
}

func (f *Floor) captureRefreshAge() time.Duration {
	if f.CaptureRefreshAge > 0 {
		return f.CaptureRefreshAge
	}
	return DefaultCaptureRefreshAge
}

// BinDir is the prefix's bin/: the one directory of it a host launch puts on the agent's PATH,
// last (HE-D1).
func (f *Floor) BinDir() string { return filepath.Join(f.Dir, "bin") }

// Launcher is bin/<bin>.
func (f *Floor) Launcher(bin string) string { return filepath.Join(f.BinDir(), bin) }

func (f *Floor) programsDir(bin string) string { return filepath.Join(f.Dir, "programs", bin) }
func (f *Floor) recordPath(bin string) string {
	return filepath.Join(f.Dir, "records", bin+".json")
}
func (f *Floor) receiptsPath() string     { return filepath.Join(f.Dir, "receipts.jsonl") }
func (f *Floor) lockPath(n string) string { return filepath.Join(f.Dir, "locks", n+".lock") }
func (f *Floor) nodeRoot() string         { return filepath.Join(f.Dir, "node") }
func (f *Floor) npmCache() string         { return filepath.Join(f.Dir, "cache", "npm") }
func (f *Floor) downloads() string        { return filepath.Join(f.Dir, "downloads") }

// ensureDir creates the prefix 0700 — the guest account of macos-user is another uid, and must
// be able neither to read nor to replace anything the host runs from here — and a child of it.
func (f *Floor) ensureDir(children ...string) error {
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return err
	}
	// MkdirAll leaves an EXISTING directory's mode alone, and a prefix a user or an older
	// build created looser must not stay looser.
	if err := os.Chmod(f.Dir, 0o700); err != nil {
		return err
	}
	for _, c := range children {
		if err := os.MkdirAll(filepath.Join(f.Dir, c), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// declared is what a program's record must match to count as current: the npm install spec, the
// installer URL, or a fork's source address. A fork's pin and recipe are matched beside it
// (Record.Revision, Record.Recipe), because the address alone names a repository, not a build.
func declared(in packdecl.Install) string {
	switch in.Kind {
	case "npm":
		name, version := packdecl.SplitNpmSpec(in.Package)
		return packdecl.NpmInstallSpec(name, version)
	case "native":
		return in.InstallerURL
	case packdecl.InstallKindSource:
		if in.IsPatchedFork() {
			// A PATCHED fork's build is identified by its repository and subdirectory, never its ref
			// (docs/design/patched-forks.md §6.3), so moving a hold from `?ref=main` to `?ref=v1.0.1`
			// is not a new declaration: the good build it names is the record's to compare.
			return packsrc.BuildSource(in.Source)
		}
		return in.Source
	}
	return ""
}

// via renders the install kind in the manifest's word.
func via(in packdecl.Install) string {
	switch in.Kind {
	case "native":
		return "installer"
	case packdecl.InstallKindSource:
		return packdecl.ViaSource
	}
	return in.Kind
}

// forkPin is ForkPin's answer for p — after this Floor's own pin of it, when it made or tried one —
// or why there is none when nothing can read a pin.
func (f *Floor) forkPin(p Program) (commit, reason string) {
	if c, ok := f.madePins[p.Bin()]; ok {
		return c, ""
	}
	if why, ok := f.failedPins[p.Bin()]; ok {
		return "", why
	}
	if f.ForkPin == nil {
		return "", "this yolo reads no fork pin here"
	}
	commit, reason = f.ForkPin(p)
	if commit == "" && reason == "" {
		reason = "the fork lock names no commit for it"
	}
	return commit, reason
}

// awaitsPin reports whether p is a fork's program whose pin is missing and can be made by an install
// (FP-D18): ForkPin names no commit, ForkPinnable says the lock can take one, PinFork exists, and
// this Floor has not tried already. Such a program is not "no floor entry" — the install pins it
// first — so Status reports it as an install would find it.
func (f *Floor) awaitsPin(p Program) bool {
	if p.Install.Kind != packdecl.InstallKindSource || f.PinFork == nil || f.ForkPinnable == nil {
		return false
	}
	if _, tried := f.failedPins[p.Bin()]; tried {
		return false
	}
	commit, _ := f.forkPin(p)
	return commit == "" && f.ForkPinnable(p)
}

// pinFork is Ensure's pin of a program awaitsPin names: PinFork's answer, recorded on this Floor so
// every later status reads it (forkPin).
func (f *Floor) pinFork(p Program) (commit, reason string) {
	commit, reason = f.PinFork(p, func(line string) { f.say("%s", line) })
	if commit == "" {
		if reason == "" {
			reason = "the fork's pin could not be made"
		}
		if f.failedPins == nil {
			f.failedPins = map[string]string{}
		}
		f.failedPins[p.Bin()] = reason
		return "", reason
	}
	if f.madePins == nil {
		f.madePins = map[string]string{}
	}
	f.madePins[p.Bin()] = commit
	return commit, ""
}

// shortCommit is a commit as a line names it: its first 12 hex digits.
func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// buildVersion is a fork build's Record.Version: the revision it was built at.
func buildVersion(commit string) string { return "commit " + shortCommit(commit) }

// ErrNoEntry wraps the refusal Ensure returns for a program the floor cannot hold.
var ErrNoEntry = errors.New("no floor entry")

// noEntryReason is why the floor cannot hold p on this machine, or "" when it can. It writes
// nothing and never reads the capture store: it is a fact about the declaration, the configuration
// (a fork's pin, which ForkPin answers, among it) and the platform, the dynamic loader the floor's
// copy asks for included (HP-D15) — Node's official build's for an npm program, and for an
// installed copy of any other the one its own file names, which is the one read of the prefix here.
// So a copy whose loader went away has no floor entry, and `yolo host apply --assert` removes it
// (Reconcile) as it does any other the floor can no longer hold.
func (f *Floor) noEntryReason(p Program) string {
	if why := f.recipeNoEntryReason(p); why != "" {
		return why
	}
	return f.installedLoaderReason(p.Bin())
}

// installedLoaderReason is why the floor's installed copy of bin cannot start on this machine — the
// dynamic loader the file its record starts asks for is missing, or is NixOS's stub — or "" when it
// can, or when nothing is installed.
func (f *Floor) installedLoaderReason(bin string) string {
	if !f.probesLoaders() {
		return ""
	}
	rec, err := f.readRecord(bin)
	if err != nil || rec == nil || len(rec.Exec) == 0 {
		return ""
	}
	if why := f.programLoaderProblem(rec.Exec[0]); why != "" {
		return "its copy in yolo's floor (" + rec.Version + ") " + why
	}
	return ""
}

// recipeNoEntryReason is noEntryReason's half that reads nothing but the declaration, the
// configuration and the platform — the provisioner order's half (Outranked) also asking the launch
// PATH which managers it holds, since an order skips one this machine lacks.
func (f *Floor) recipeNoEntryReason(p Program) string {
	in := p.Install
	if f.Include != nil && !f.Include(p.Pack) {
		return "the user config's `host_floor` leaves pack " + p.Pack + " out of the floor"
	}
	if f.Outranked != nil {
		if why := f.Outranked(p); why != "" {
			return why
		}
	}
	if !packdecl.ValidBinName(in.Bin) {
		return fmt.Sprintf("%q is not a program name the floor can file", in.Bin)
	}
	if why := in.UnpublishedReason(f.GOOS, f.GOARCH); why != "" {
		return "its vendor publishes no build here: " + why
	}
	switch in.Kind {
	case "npm":
		if _, ok := nodePlatform(f.GOOS, f.GOARCH); !ok {
			return "Node publishes no official build for " + f.GOOS + "/" + f.GOARCH +
				", so the floor has no interpreter to run it on"
		}
		// THE INTERPRETER'S LOADER, before any download (HP-D15): Node's official Linux build
		// cannot start without it, so a machine that lacks it gets the copy on PATH, not a fetched
		// tarball that exits 127.
		return f.nodeLoaderProblem()
	case "native":
		switch f.GOOS {
		case "linux":
			return ""
		case "darwin":
			// A MAC CAPTURES ONLY THROUGH THE MACOS-USER ACT (HP-D2), so its entry is decided as Linux's
			// is — the store's capture, the act, or why neither can be had here (provisionable) — on a
			// floor that was given that act's predicate. One given none has no capture to run: a capture
			// jail's runtime stands in for nothing here, its entry being a Linux one no Mac runs.
			if f.CaptureActUnavailable == nil {
				return "an installer agent on a Mac comes from the macos-user capture act, which this floor " +
					"runs none of"
			}
			return ""
		}
		return "an installer agent comes from a `yolo capture`, which runs in a Linux jail or, on a Mac, " +
			"as the macos-user sandbox account, and this machine is " + f.GOOS + "/" + f.GOARCH
	case packdecl.InstallKindSource:
		// A FORK (docs/design/forked-programs-as-packs.md): its host copy is the capture store's
		// build of the fork's PINNED commit, relocated into the floor (FP-D4). A notch gets a build
		// made for its own platform or none (§1: no cross-compilation): on Linux the capture jail's,
		// and on a Mac a darwin build of the macos-user fork-build act (FP-D24), which a floor given
		// that act's predicate runs. With no pin there is no build to ask for, and an older build the
		// floor still holds is a near-miss it never serves (§9), so that is no entry too — unless
		// the install can make the pin (awaitsPin, FP-D18), which it does before it builds.
		switch {
		case f.GOOS == "linux":
		case f.GOOS == "darwin" && !in.IsPatchedFork():
			// A MAC BUILDS A PLAIN FORK THROUGH THE MACOS-USER ACT ALONE: a container build jail's
			// entry is a Linux one no Mac runs. Whether that act can run HERE is the build's question
			// (cannotBuild, asked where the store holds no build), so only a floor given none is
			// refused now.
			if f.BuildActUnavailable == nil {
				return "it is built from source by fork pack " + in.ForkedBy + ", which on a Mac the floor " +
					"builds as the macos-user sandbox account, and this floor runs no such build"
			}
		default:
			// The next step is a jail's: a patched fork's advance builds for a container's platform
			// alone, and a jail launch on a container backend runs it.
			return "it is built from source by fork pack " + in.ForkedBy + " in a Linux capture " +
				"jail, and this machine is " + f.GOOS + "/" + f.GOARCH + ": the floor holds a build " +
				"made for its own platform only — run it in a jail instead (`yolo -- " + in.Bin + "`, on " +
				"a container backend: Apple Container or podman), whose fresh launch builds it"
		}
		if in.IsPatchedFork() {
			// A PATCHED fork has no pin (PF-D16): its good build answers instead (patched.go).
			return f.patchedNoEntryReason(p)
		}
		if _, why := f.forkPin(p); why != "" && !f.awaitsPin(p) {
			return "it is built from source by fork pack " + in.ForkedBy + ", and " + why
		}
		return ""
	}
	return fmt.Sprintf("its recipe (via %q) is one this build cannot install", in.Kind)
}

// Status reports p's disposition. It reads the prefix — and, for an installer program the prefix
// does not hold, the capture store and whether a capture could run — and nothing else: no
// network, no install, so `yolo check` and the launch gate can ask it freely.
func (f *Floor) Status(p Program) Status {
	if p.Install.IsPatchedFork() {
		return f.patchedStatus(p)
	}
	st := Status{Program: p, Launcher: f.Launcher(p.Bin())}
	if why := f.noEntryReason(p); why != "" {
		st.Disposition, st.Reason = NoEntry, why
		return st
	}
	rec, err := f.readRecord(p.Bin())
	if errors.Is(err, ErrNewerRecord) {
		// Not this yolo's to install: Ensure refuses to install over the record (HP-D8), so no
		// line may say a launch or an --assert installs it. Not offered to provisionable either,
		// whose answer is about what this machine could install.
		st.Disposition, st.Reason, st.Newer = Missing, err.Error(), true
		return st
	}
	if err != nil || rec == nil {
		st.Disposition, st.Reason = Missing, "not installed yet"
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			st.Reason = "its record is unreadable (" + err.Error() + ")"
		}
		return f.provisionable(st)
	}
	if !usable(rec, st.Launcher) {
		st.Disposition, st.Reason = Missing, "its installed files are gone"
		return f.provisionable(st)
	}
	st.Disposition, st.Record = Provisioned, rec
	switch {
	case rec.Via != via(p.Install):
		st.Pending = "the pack now installs it via " + via(p.Install) + ", not " + rec.Via
	case rec.Declared != declared(p.Install):
		st.Pending = "the pack now declares " + declared(p.Install) + " (installed: " +
			rec.Declared + ")"
	case p.Install.Kind == packdecl.InstallKindSource:
		st.Pending = f.buildPending(p, rec)
	case p.Install.Kind == "npm" && !packdecl.SatisfiesNodeFloor(rec.Node, p.Install.NodeFloor):
		st.Pending = "it runs on Node " + rec.Node + ", below the pack's node_floor " +
			p.Install.NodeFloor
	}
	return st
}

// buildPending is why a provisioned fork build is not the one its fork asks for now, "" when it
// is: A MOVED PIN, a changed recipe, or a Node script below the node_floor. Each reinstalls on the
// next Ensure — and, unlike any other recipe's, a failed reinstall at a moved pin or recipe does
// not keep the installed build serving (Ensure, servesANearMiss), since a build of another commit
// is a near-miss (§9). One pending only for the node_floor is the pin's build, and is kept.
func (f *Floor) buildPending(p Program, rec *Record) string {
	in := p.Install
	commit, _ := f.forkPin(p)
	switch {
	case commit == "":
		// Awaiting its pin (noEntryReason lets only that through): its source was edited since the
		// installed build's pin, and the install pins what it names now.
		return "fork pack " + in.ForkedBy + " has no pin for " + in.Source + " yet: the install pins it, " +
			"then builds that commit (installed: " + rec.Version + ")"
	case rec.Revision != commit:
		return "fork pack " + in.ForkedBy + " now pins it at " + buildVersion(commit) +
			" (installed: " + rec.Version + ")"
	case rec.Recipe != in.SourceRecipe():
		return "fork pack " + in.ForkedBy + "'s build recipe changed since " + rec.Version +
			" was installed"
	case rec.Node != "" && !packdecl.SatisfiesNodeFloor(rec.Node, in.NodeFloor):
		return "it runs on Node " + rec.Node + ", below the pack's node_floor " + in.NodeFloor
	}
	return ""
}

// provisionable turns a Missing installer or source-built program into NoEntry when this machine
// can neither materialize it (the store has no entry for it) nor capture or build one (cannotCapture,
// cannotBuild): the floor cannot provision it HERE, so a launch looks for it on PATH
// (OQ-HE11) instead of failing an install. So is one whose store entry holds no program that runs
// here: none outside a jail, or one asking for a dynamic loader this machine lacks (HP-D15). It
// reads the store offline, never the network. A provisioned entry never comes through here: the
// floor already holds it, whatever the store says now.
func (f *Floor) provisionable(st Status) Status {
	if st.Program.Install.Kind == packdecl.InstallKindSource {
		return f.buildProvisionable(st)
	}
	if st.Program.Install.Kind != "native" || f.ResolveCapture == nil {
		return st
	}
	bin := st.Program.Bin()
	entry, err := f.ResolveCapture(bin)
	if err == nil {
		// A CAPTURE THE INSTALL RECAPTURES is judged as that, BEFORE its program (HP-D17): an entry
		// recorded before captures scanned their contents moves out of /home/agent only by being
		// captured again (HP-D7), and one recorded before a capture surface existed may hold no
		// program the recapture would not record — codex's, whose ~/.local/bin/codex links into
		// ~/.codex/packages/standalone. The recapture needs what any capture needs.
		if stale := recaptureReason(entry, bin); stale != "" {
			if why := f.cannotCapture(bin, "recaptures it"); why != "" {
				st.Disposition = NoEntry
				st.Reason = "the capture of " + bin + " on this machine " + stale + ", and " + why
				return st
			}
			st.Reason += "; the capture of " + bin + " on this machine " + stale + ", and the install " +
				"recaptures it"
			return st
		}
		final, why := capturedProgram(entry, bin)
		if why != "" {
			st.Disposition = NoEntry
			st.Reason = "the capture of " + bin + " on this machine cannot run outside a jail: " + why
			return st
		}
		// Its program's dynamic loader, read from the store before anything is materialized.
		if why := f.programLoaderProblem(filepath.Join(entry.Tree, filepath.FromSlash(final))); why != "" {
			st.Disposition = NoEntry
			st.Reason = "the capture of " + bin + " on this machine " + why
		}
		return st
	}
	if why := f.cannotCapture(bin, "captures it"); why != "" {
		st.Disposition = NoEntry
		st.Reason = "there is no capture of " + bin + " on this machine, and " + why
	}
	return st
}

// runtimeStep is the next step a no-floor-entry reason ends with when the floor stopped before an
// act that boots a jail — the capture act, or a fork's build — for want of a container runtime
// (CaptureUnavailable). Nothing is left to run by hand once one is installed: the next launch's
// install runs the act itself (installFromCapture captures, or recaptures, an installer program the
// store holds no usable capture of; FP-D18: a launch pins a fork, and the floor builds the pin), and
// does names what that launch does. So the step is the runtime, whose install line for this
// machine `yolo check` prints. "" when act is false — this yolo has no such act at all, which
// nothing the user installs moves.
func runtimeStep(act bool, does string) string {
	if !act {
		return ""
	}
	return " — install one (`yolo check` names how on this machine) and the next `yolo host` launch " + does
}

// RuntimeStep is runtimeStep for an act this yolo has: the clause a reason about a missing container
// runtime ends with, for a caller of CaptureActUnavailable that answers one.
func RuntimeStep(does string) string { return runtimeStep(true, does) }

// cannotCapture says why this machine cannot run the capture act for bin now, with the step that ends
// it, "" when it can: CaptureActUnavailable's answer, or a capture jail's (CaptureUnavailable). It is
// the capture's own question, never the build's (cannotBuild): a capture can run where no jail can
// boot (HP-D2, HP-D18), and a fork's build cannot.
func (f *Floor) cannotCapture(bin, does string) string {
	switch {
	case f.Capture == nil:
		return "this machine cannot run `yolo capture`"
	case f.CaptureActUnavailable != nil:
		return f.CaptureActUnavailable(bin, does)
	case f.CaptureUnavailable != nil:
		if why := f.CaptureUnavailable(); why != "" {
			return why + runtimeStep(true, does)
		}
	}
	return ""
}

// captureHow is the parenthetical the line that starts a capture says how it runs.
func (f *Floor) captureHow() string {
	if f.CaptureHow != nil {
		if how := f.CaptureHow(); how != "" {
			return how
		}
	}
	return "a throwaway jail runs its installer once, and every jail on this machine reuses the result"
}

// cannotBuild says why this machine cannot run a fork's build act for bin now, with the step that
// ends it, "" when it can: BuildActUnavailable's answer — a Mac's macos-user act (FP-D24) — or the
// build jail's runtime (CaptureUnavailable). It is never the capture's question (cannotCapture): a
// Linux host captures under Landlock where no jail can boot, and a fork's build never runs that way.
// does is what the next launch does once the step is taken.
func (f *Floor) cannotBuild(bin, does string) string {
	switch {
	case f.Build == nil:
		return "this machine cannot run a fork's build"
	case f.BuildActUnavailable != nil:
		return f.BuildActUnavailable(bin, does)
	case f.CaptureUnavailable != nil:
		if why := f.CaptureUnavailable(); why != "" {
			return why + runtimeStep(true, does)
		}
	}
	return ""
}

// usable reports whether a record's files are all still there: the launcher and every
// absolute path it starts.
func usable(rec *Record, launcher string) bool {
	if len(rec.Exec) == 0 {
		return false
	}
	if _, err := os.Stat(launcher); err != nil {
		return false
	}
	for _, p := range rec.Exec {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

func (f *Floor) readRecord(bin string) (*Record, error) {
	b, err := os.ReadFile(f.recordPath(bin))
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	if rec.Schema > recordSchema {
		// A newer yolo wrote it. The refusal names the way to that yolo, in the sentence every
		// reader of a newer yolo's file prints (updatehint); it used to end at the two numbers.
		//
		// AND A SECOND STEP, because the first reaches a newer yolo only through THIS install's
		// channel, while this prefix is one every yolo on the machine shares: a record a
		// from-source build wrote beside a Homebrew yolo, or one left by a yolo the user went
		// back from on purpose, is one `yolo update` finds nothing newer than (or undoes the
		// choice), and every `yolo host -- <bin>` would refuse with no way on. Removing the
		// record is the user's own act, so the floor still never writes over it.
		return nil, newerRecordError{updatehint.NewerSchema("host floor record "+f.recordPath(bin),
			rec.Schema, recordSchema).Error() + ". To run this yolo's own copy instead, remove " +
			"that record: the next `yolo host -- " + bin + "` installs one"}
	}
	return &rec, nil
}

// ErrNewerRecord matches (errors.Is) the error for a record a newer yolo wrote, which Ensure
// returns rather than install over it: a newer schema is refused, never rewritten, as a pack
// lockfile's is (docs/design/host-tool-provisioning.md, HP-D8). The error's text is
// updatehint.NewerSchema's, naming `yolo update`, then the removal that installs this yolo's own
// copy, for the machine whose update finds nothing newer.
var ErrNewerRecord = errors.New("host floor record written by a newer yolo")

// newerRecordError is ErrNewerRecord with the refusal's own words.
type newerRecordError struct{ msg string }

func (e newerRecordError) Error() string        { return e.msg }
func (e newerRecordError) Is(target error) bool { return target == ErrNewerRecord }

// writeRecord replaces bin's record atomically, so a reader never sees half of one.
func (f *Floor) writeRecord(rec *Record) error {
	rec.Schema = recordSchema
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(f.recordPath(rec.Bin), append(b, '\n'), 0o600)
}

// Records lists every record in the prefix, by bin — the provisioned set as the prefix itself
// says it, whatever the current selection is. Reconcile compares the two.
func (f *Floor) Records() map[string]*Record {
	out := map[string]*Record{}
	entries, err := os.ReadDir(filepath.Join(f.Dir, "records"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		bin, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		if rec, err := f.readRecord(bin); err == nil {
			out[bin] = rec
		}
	}
	return out
}

// receipt is one line of receipts.jsonl.
type receipt struct {
	Time     time.Time `json:"time"`
	Act      string    `json:"act"`
	Bin      string    `json:"bin"`
	Pack     string    `json:"pack,omitempty"`
	Via      string    `json:"via,omitempty"`
	Declared string    `json:"declared,omitempty"`
	Version  string    `json:"version,omitempty"`
	Dir      string    `json:"dir,omitempty"`
}

// appendReceipt records one act. A receipt RECORDS and never gates: an install that happened is
// not undone because its log line could not be written.
func (f *Floor) appendReceipt(r receipt) {
	r.Time = f.now().UTC()
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	fh, err := os.OpenFile(f.receiptsPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer fh.Close()
	_, _ = fh.Write(append(b, '\n'))
}

// writeAtomic writes path through a sibling temp file and a rename.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// Programs returns one Program per bin across packs, in pack order and then declaration order,
// the FIRST declaration of a bin winning — the jail's launcher generator's rule, so the two
// notches agree about which pack's recipe a name gets (the footprint check refuses two packs
// claiming one bin before either reads this).
func Programs(packs []PackPrograms) []Program {
	seen := map[string]bool{}
	var out []Program
	for _, p := range packs {
		for _, in := range p.Installs {
			if in.Bin == "" || seen[in.Bin] {
				continue
			}
			seen[in.Bin] = true
			out = append(out, Program{Pack: p.Pack, Install: in})
		}
	}
	return out
}

// PackPrograms is one selected pack's name and its install contributions, the input Programs
// flattens; the caller builds it from its own pack loader.
type PackPrograms struct {
	Pack     string
	Installs []packdecl.Install
}

// HighestNodeFloor is the highest `node_floor` among the npm and source-built programs the floor
// holds — the two whose entrypoint the floor's own Node may start — "" when none declares one. It
// is Floor.NodeFloor's input.
func HighestNodeFloor(progs []Program) string {
	highest := ""
	for _, p := range progs {
		switch p.Install.Kind {
		case "npm", packdecl.InstallKindSource:
		default:
			continue
		}
		if p.Install.NodeFloor == "" {
			continue
		}
		if highest == "" || packdecl.CompareVersions(p.Install.NodeFloor, highest) > 0 {
			highest = p.Install.NodeFloor
		}
	}
	return highest
}
