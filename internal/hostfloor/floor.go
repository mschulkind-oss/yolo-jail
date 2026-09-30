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
// and taking entries back out (Reconcile, Sweep). It knows no pack loader and no config: the
// caller hands it the programs (Program) and every policy input as a field of Floor, so the
// launch, `yolo host apply`, `yolo check` and `yolo prune` ask the same questions of the same
// code without this package importing any of their worlds.
//
// Two recipes, per OQ-HP3 and OQ-HP4:
//
//   - `via: npm`: installed with the floor's OWN Node — the official release tarball, verified
//     against its published sha256 (node.go) — into a prefix-private npm prefix, and started
//     by that Node's absolute path.
//   - `via: installer`: materialized from the machine's `yolo capture` store, the same entry a
//     jail materializes, where this host matches the capture jail (Linux). On macOS the entry is
//     NO FLOOR ENTRY until the host capture (HP-D2) is measured on a Mac and ships.
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
//	  cache/npm/                  npm's download cache
//	  downloads/                  Node tarballs in flight
package hostfloor

import (
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
	// `yolo host apply --assert` installs it (HP-D3).
	Missing Disposition = "missing"
	// NoEntry: the floor cannot hold it on this machine. Status.Reason says why; what
	// `yolo host` runs instead is OQ-HE11's question, and today it is the launch's PATH.
	NoEntry Disposition = "no floor entry"
)

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
	// Launcher is where bin/<bin> is or would be.
	Launcher string
}

// Record is one provisioned install: the file bin/<bin> is generated from, and what `yolo
// check` reports. One per program, rewritten atomically on every install and every check.
type Record struct {
	Schema int    `json:"schema"`
	Bin    string `json:"bin"`
	Pack   string `json:"pack"`
	// Via is the manifest's word for the recipe: "npm" or "installer".
	Via string `json:"via"`
	// Declared is what the pack asked for: the npm install spec, or the installer URL. A
	// changed declaration reinstalls (a different package, a different URL).
	Declared string `json:"declared"`
	// Version is what was installed: npm's resolved package version, or for an installer
	// capture the versions-dir entry it left (else "capture <key>").
	Version string `json:"version"`
	// Node is the Node release an npm program runs on, "" otherwise.
	Node string `json:"node,omitempty"`
	// Capture is the capture store entry an installer program was materialized from.
	Capture string `json:"capture,omitempty"`
	// Dir is the install directory under programs/<bin>/.
	Dir string `json:"dir"`
	// Entry is the program file: npm's bin link, or the capture's ~/.local/bin/<bin>.
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
	// UpdatesAllowed is the `agent_updates` policy for a pack. nil => allowed, the ruled default.
	UpdatesAllowed func(pack string) bool
	// ResolveCapture finds the capture store's entry for bin on this host's platform, the same
	// selection a jail's materialize makes. nil => this host has no capture store.
	ResolveCapture func(bin string) (*capture.Entry, error)
	// Capture runs `yolo capture <bin>`, filling the store. nil => this host cannot capture.
	Capture func(bin string) error
	// CaptureUnavailable says why this machine cannot run Capture right now ("" when it can):
	// a capture boots a jail, so a host with no container runtime cannot make one. Asked only
	// for an installer program that is neither provisioned nor in the store, which then has no
	// floor entry HERE rather than an install bound to fail (a selected pack delivers a program
	// the floor "holds, or can provision", host-launch-environment.md §0). nil => it can.
	CaptureUnavailable func() string
	// Environ is the environment the installers are derived from (installerEnv strips the
	// parts that would steer where an install lands). nil => os.Environ().
	Environ []string
	// Home is the HOME an installer runs with. "" => $HOME.
	Home string
	// Out receives the progress lines and the installers' own output. nil => discarded.
	Out io.Writer
	// Prefix starts every line this package prints ("yolo host: ").
	Prefix string
	// InstallTimeout, PollTimeout and UpdateInterval override the defaults above.
	InstallTimeout, PollTimeout, UpdateInterval time.Duration
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

// declared is what a program's record must match to count as current: the npm install spec, or
// the installer URL.
func declared(in packdecl.Install) string {
	switch in.Kind {
	case "npm":
		name, version := packdecl.SplitNpmSpec(in.Package)
		return packdecl.NpmInstallSpec(name, version)
	case "native":
		return in.InstallerURL
	}
	return ""
}

// via renders the install kind in the manifest's word.
func via(in packdecl.Install) string {
	if in.Kind == "native" {
		return "installer"
	}
	return in.Kind
}

// ErrNoEntry wraps the refusal Ensure returns for a program the floor cannot hold.
var ErrNoEntry = errors.New("no floor entry")

// noEntryReason is why the floor cannot hold p on this machine, or "" when it can. It never
// touches the disk: it is a fact about the declaration, the configuration and the platform.
func (f *Floor) noEntryReason(p Program) string {
	in := p.Install
	if f.Include != nil && !f.Include(p.Pack) {
		return "the user config's `host_floor` leaves pack " + p.Pack + " out of the floor"
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
		return ""
	case "native":
		if f.GOOS == "darwin" {
			return "an installer agent on macOS comes from a host capture, which is not built " +
				"yet: it must be measured on a Mac first (host-tool-provisioning.md HP-D2)"
		}
		if f.GOOS != "linux" {
			return "an installer agent comes from a `yolo capture`, which runs in a Linux jail, " +
				"and this machine is " + f.GOOS + "/" + f.GOARCH
		}
		return ""
	case packdecl.InstallKindSource:
		// A FORK (docs/design/forked-programs-as-packs.md): its host copy is the jail build's
		// entry relocated into the floor (FP-D4), and relocating a source build starts with a
		// measurement of what the build embeds (the plan's step 7), which is not made yet.
		return "it is built from source by fork pack " + in.ForkedBy + ", and the floor does " +
			"not hold a source-built program yet (forked-programs-as-packs.md §11 step 3)"
	}
	return fmt.Sprintf("its recipe (via %q) is one this build cannot install", in.Kind)
}

// Status reports p's disposition. It reads the prefix — and, for an installer program the prefix
// does not hold, the capture store and whether a capture could run — and nothing else: no
// network, no install, so `yolo check` and the launch gate can ask it freely.
func (f *Floor) Status(p Program) Status {
	st := Status{Program: p, Launcher: f.Launcher(p.Bin())}
	if why := f.noEntryReason(p); why != "" {
		st.Disposition, st.Reason = NoEntry, why
		return st
	}
	rec, err := f.readRecord(p.Bin())
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
	case p.Install.Kind == "npm" && !packdecl.SatisfiesNodeFloor(rec.Node, p.Install.NodeFloor):
		st.Pending = "it runs on Node " + rec.Node + ", below the pack's node_floor " +
			p.Install.NodeFloor
	}
	return st
}

// provisionable turns a Missing installer program into NoEntry when this machine can neither
// materialize it (the store has no entry for it) nor capture one (no container runtime): the
// floor cannot provision it HERE, so a launch looks for it on PATH (OQ-HE11) instead of failing an
// install. It reads the store offline, never the network. A provisioned entry never comes through
// here: the floor already holds it, whatever the store says now.
func (f *Floor) provisionable(st Status) Status {
	if st.Program.Install.Kind != "native" || f.ResolveCapture == nil {
		return st
	}
	entry, err := f.ResolveCapture(st.Program.Bin())
	if err == nil {
		if why := capturedProgram(entry, st.Program.Bin()); why != "" {
			st.Disposition = NoEntry
			st.Reason = "the capture of " + st.Program.Bin() + " on this machine cannot run outside a jail: " + why
			return st
		}
		// An entry recorded before captures scanned their contents moves out of /home/agent only
		// by being captured again (HP-D7), which needs what any capture needs.
		if !captureRelocatable(entry) {
			if why := f.cannotCapture(); why != "" {
				st.Disposition = NoEntry
				st.Reason = "the capture of " + st.Program.Bin() + " on this machine was recorded for a " +
					"jail's home only, and " + why
			}
		}
		return st
	}
	if why := f.cannotCapture(); why != "" {
		st.Disposition = NoEntry
		st.Reason = "there is no capture of " + st.Program.Bin() + " on this machine, and " + why
	}
	return st
}

// cannotCapture says why this machine cannot run the capture act now, "" when it can.
func (f *Floor) cannotCapture() string {
	switch {
	case f.Capture == nil:
		return "this machine cannot run `yolo capture`"
	case f.CaptureUnavailable != nil:
		return f.CaptureUnavailable()
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
		return nil, fmt.Errorf("record schema %d is newer than this yolo's %d", rec.Schema, recordSchema)
	}
	return &rec, nil
}

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

// HighestNodeFloor is the highest `node_floor` among the npm programs the floor holds, "" when
// none declares one. It is Floor.NodeFloor's input.
func HighestNodeFloor(progs []Program) string {
	highest := ""
	for _, p := range progs {
		if p.Install.Kind != "npm" || p.Install.NodeFloor == "" {
			continue
		}
		if highest == "" || packdecl.CompareVersions(p.Install.NodeFloor, highest) > 0 {
			highest = p.Install.NodeFloor
		}
	}
	return highest
}
