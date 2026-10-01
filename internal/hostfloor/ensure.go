package hostfloor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// ensure.go puts one program in place: the first install, a reinstall when the pack's
// declaration moved, and the throttled evergreen refresh — the three things a jail's generated
// launcher does at the agent's own invocation (host-tool-provisioning.md §3, §4).
//
// # Why this is Go and not the jail's launcher template
//
// The jail's launchers are bash and lean on what its image bakes (jq, timeout, a yolo on PATH to
// materialize a capture with). A host bakes none of that, so the same template run here would
// behave differently per machine — the dependence on the launcher's environment the floor exists
// to remove. What the jail's launcher DECIDES is kept, and nothing else: install on first use, at
// most one poll per UPDATE_INTERVAL when `agent_updates` allows it, a pinned package never
// polled, a failed update keeping the installed version, one writer per install prefix. The
// launcher this package writes into bin/ only starts the installed program; the deciding happens
// in yolo, before the exec, on the launch that asked for it.
//
// # Atomicity (§3, "Install atomicity")
//
// A program installs into a FRESH directory under programs/<bin>/, and bin/<bin> is switched only
// once the install exits 0 and its entry file exists. A failed or killed install leaves the
// previous version serving, or no entry at all — never a half-written one — and the next install
// of that program removes the leftover, which it can do because it holds the program's lock.

// Outcome is what one Ensure did.
type Outcome string

const (
	// Current: nothing to do; the provisioned install runs.
	Current Outcome = "current"
	// Installed: this call installed it (first use, a moved declaration, or files gone).
	Installed Outcome = "installed"
	// Updated: the evergreen refresh moved it to a newer version.
	Updated Outcome = "updated"
	// Kept: a reinstall or refresh failed and the previous install still runs.
	Kept Outcome = "kept"
)

// Ensure makes p runnable from the floor and returns its status afterwards: a Provisioned status
// whose Launcher is what `yolo host` execs. A program the floor cannot hold returns ErrNoEntry
// (wrapped, with the reason); one whose record a newer yolo wrote returns ErrNewerRecord's
// refusal, installing nothing; a first install that fails returns its error.
func (f *Floor) Ensure(ctx context.Context, p Program) (Status, Outcome, error) {
	st := f.Status(p)
	switch {
	case st.Disposition == NoEntry:
		return st, "", fmt.Errorf("%w for %s: %s", ErrNoEntry, p.Bin(), st.Reason)
	case st.Newer:
		// REFUSED, never installed over (HP-D8): replacing a newer yolo's launcher and record
		// with this one's would hand that yolo a floor it did not write, while `yolo check`
		// tells the user to run `yolo update`. Reason is that refusal.
		return st, "", newerRecordError{st.Reason}
	case st.Disposition == Provisioned && st.Pending == "":
		return f.refresh(ctx, p, st)
	}
	if err := f.ensureDir("bin", "programs", "records", "locks"); err != nil {
		return st, "", err
	}
	lk, err := acquire(f.lockPath(p.Bin()), true, func(pid int) {
		f.say("waiting for pid %d, which is installing %s into yolo's floor", pid, p.Bin())
	})
	if err != nil {
		return st, "", err
	}
	defer lk.release()
	// Re-checked UNDER the lock: the holder we waited for may have installed exactly this.
	st = f.Status(p)
	if st.Newer {
		// A newer yolo installed it while this one waited.
		return st, "", newerRecordError{st.Reason}
	}
	if st.Disposition == Provisioned && st.Pending == "" {
		return st, Current, nil
	}
	why := st.Reason
	if st.Pending != "" {
		why = st.Pending
	}
	f.say("installing %s into yolo's floor (%s): %s", p.Bin(), why, f.describeRecipe(p))
	rec, err := f.install(ctx, p)
	if err != nil {
		if st.Disposition == Provisioned && !f.servesANearMiss(p, st.Record) {
			// A reinstall failed, and the previous install is intact: it keeps serving.
			f.say("could not reinstall %s (%v); running the installed %s", p.Bin(), err, st.Record.Version)
			return st, Kept, nil
		}
		if st.Disposition == Provisioned {
			// A FORK'S BUILD IS NEVER KEPT PAST ITS PIN (servesANearMiss): the installed copy is not
			// the build the pack now asks for, which is a near-miss (forked-programs-as-packs.md §9),
			// so it does not serve while that build is missing — the jail's source launcher refuses
			// an older build in its home for the same reason. Not serving means its launcher LEAVES
			// bin/, which ends every host agent's PATH (HE-D1): `yolo host` would not exec it, and an
			// agent's own `<bin>` would still have found it there. The record goes with it, so the
			// next Status says missing, and the install directory stays for the next install to
			// prune: unlinking a launcher stops no agent already running.
			f.say("could not install %s (%v); the installed %s is not the build the pack now asks for, "+
				"so the floor no longer runs it", p.Bin(), err, st.Record.Version)
			_ = os.Remove(f.Launcher(p.Bin()))
			_ = os.Remove(f.recordPath(p.Bin()))
			f.appendReceipt(receipt{Act: "remove", Bin: p.Bin(), Pack: p.Pack, Version: st.Record.Version,
				Dir: st.Record.Dir})
			st.Disposition, st.Record, st.Pending = Missing, nil, ""
			st.Reason = "the install the pack now asks for failed, and the installed copy was not it"
		}
		if why := noEntryReasonOf(err); why != "" {
			// Not a failed install: the install learned the floor cannot hold this program here
			// (a fresh capture that holds no runnable binary). Reported as what it is.
			st.Disposition, st.Reason = NoEntry, why
		}
		return st, "", err
	}
	f.say("installed %s %s → %s", p.Bin(), rec.Version, f.Launcher(p.Bin()))
	return f.Status(p), Installed, nil
}

// servesANearMiss reports whether rec, provisioned for p, would serve a fork's build the pack does
// not now ask for, were a failed reinstall to keep it (forked-programs-as-packs.md FP-D17, from §9's
// "never serve a near-miss"): for a fork's program, an installed copy that is not its build at the
// pin and the current recipe — another commit, another recipe, or the base's own upstream program
// from before the fork was selected; and for any other program, an installed fork's build, left
// from when a fork delivered it. A fork's build at the pin that is pending only for a raised
// node_floor is the build the lock names, and keeps serving as an npm program's version does.
func (f *Floor) servesANearMiss(p Program, rec *Record) bool {
	if p.Install.Kind != packdecl.InstallKindSource {
		return rec.Via == packdecl.ViaSource
	}
	commit, _ := f.forkPin(p)
	return rec.Via != packdecl.ViaSource || rec.Declared != declared(p.Install) || rec.Revision != commit ||
		rec.Recipe != p.Install.SourceRecipe()
}

// describeRecipe says what an install will run, for the line that starts it.
func (f *Floor) describeRecipe(p Program) string {
	in := p.Install
	switch in.Kind {
	case "npm":
		return "npm package " + declared(in)
	case "native":
		return "the machine's capture of its installer (" + in.InstallerURL + ")"
	case packdecl.InstallKindSource:
		commit, _ := f.forkPin(p)
		return "fork pack " + in.ForkedBy + "'s build of " + in.Source + " at commit " + commit
	}
	return in.Kind
}

// refresh is the evergreen half: at most one poll per UpdateInterval, only when `agent_updates`
// allows the pack to move, and a pinned npm package never polled (the declaration IS the answer,
// and a moved declaration is Pending, handled by Ensure). A failed poll or update keeps the
// installed version and waits out the interval — the jail launcher's stamp-on-failure rule.
//
// A FORK'S BUILD IS NEVER POLLED: its pin moves it (`yolo pack update`, on the host), which
// Status reports as Pending, and a poll of anything would be the rebuild on a timer
// forked-programs-as-packs.md §9 forbids. So no lock, no stamp, no store read.
func (f *Floor) refresh(ctx context.Context, p Program, st Status) (Status, Outcome, error) {
	if p.Install.Kind == packdecl.InstallKindSource {
		return st, Current, nil
	}
	rec := st.Record
	if f.UpdatesAllowed != nil && !f.UpdatesAllowed(p.Pack) {
		return st, Current, nil
	}
	if f.now().Sub(rec.Checked) < f.updateInterval() {
		return st, Current, nil
	}
	if p.Install.Kind == "npm" {
		if _, version := packdecl.SplitNpmSpec(p.Install.Package); packdecl.NpmSpecIsPinned(version) {
			return st, Current, nil
		}
	}
	lk, err := acquire(f.lockPath(p.Bin()), false, nil)
	if err != nil {
		if errors.Is(err, errLockHeld) {
			f.say("%s: another update is in progress; running the installed %s", p.Bin(), rec.Version)
		}
		return st, Current, nil
	}
	defer lk.release()
	// Stamped FIRST, whatever the poll says: a registry that is down is asked again in an hour,
	// not on every launch until it answers.
	rec.Checked = f.now().UTC()
	_ = f.writeRecord(rec)

	newer, err := f.newerThan(ctx, p, rec)
	if err != nil {
		f.say("%s: could not check for a newer version (%v); running the installed %s",
			p.Bin(), err, rec.Version)
		return st, Current, nil
	}
	if newer == "" {
		return st, Current, nil
	}
	f.say("updating %s %s → %s in yolo's floor", p.Bin(), rec.Version, newer)
	next, err := f.install(ctx, p)
	if err != nil {
		f.say("could not update %s (%v); running the installed %s", p.Bin(), err, rec.Version)
		return st, Kept, nil
	}
	f.say("updated %s to %s", p.Bin(), next.Version)
	return f.Status(p), Updated, nil
}

// newerThan reports the version an update would install, or "" when the installed one is current.
func (f *Floor) newerThan(ctx context.Context, p Program, rec *Record) (string, error) {
	switch p.Install.Kind {
	case "npm":
		nodeBin := f.nodeBin(rec.Node)
		if !f.NodeReady(rec.Node) {
			return "", fmt.Errorf("its Node v%s is gone from the floor", rec.Node)
		}
		name, _ := packdecl.SplitNpmSpec(p.Install.Package)
		// SAID BEFORE THE POLL, because the poll is the network: a slow or unreachable registry
		// holds the launch for up to the poll timeout, once an interval, before the "starting"
		// line — a wait that is yolo's, not the agent's, and a launch has no quiet mode.
		f.say("checking the npm registry for a newer %s than %s (at most %s)", p.Bin(), rec.Version,
			f.pollTimeout())
		res, err := f.runBounded(ctx, f.pollTimeout(), f.Dir, f.npmEnv(nodeBin, ""),
			[]string{filepath.Join(nodeBin, "npm"), "view", name, "version"}, true)
		if err != nil {
			return "", failure("npm view", err, res)
		}
		latest := strings.TrimSpace(res.stdout.String())
		if latest == "" {
			return "", errors.New("the npm registry gave no answer")
		}
		if latest == rec.Version {
			return "", nil
		}
		return latest, nil
	case "native":
		// An installer program moves when the machine's capture store does: a newer
		// `yolo capture <bin>` is the explicit act that updates it (program-delivery.md §6.3),
		// and the floor never runs the vendor's own updater against the user's real home.
		if f.ResolveCapture == nil {
			return "", nil
		}
		entry, err := f.ResolveCapture(p.Bin())
		if err != nil || entry.Key == rec.Capture {
			return "", nil
		}
		return "capture " + entry.Key, nil
	case packdecl.InstallKindSource:
		// Never newer: a fork moves when its pin does, and refresh returns before asking.
		return "", nil
	}
	return "", nil
}

// install puts a fresh copy of p in a new directory, and switches bin/<bin> to it on success.
// The caller holds p's lock.
func (f *Floor) install(ctx context.Context, p Program) (*Record, error) {
	bin := p.Bin()
	prev, err := f.readRecord(bin)
	if errors.Is(err, ErrNewerRecord) {
		// Never written over, whoever calls (Ensure refuses before it gets here).
		return nil, err
	}
	f.removeIncomplete(bin, prev)
	dir := filepath.Join(f.programsDir(bin), installID(f))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var rec *Record
	switch p.Install.Kind {
	case "npm":
		rec, err = f.installNpm(ctx, p, dir)
	case "native":
		rec, err = f.installFromCapture(p, dir)
	case packdecl.InstallKindSource:
		rec, err = f.installFromBuild(ctx, p, dir)
	default:
		err = fmt.Errorf("no recipe for via %q", p.Install.Kind)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	rec.Bin, rec.Pack, rec.Via, rec.Declared, rec.Dir = bin, p.Pack, via(p.Install), declared(p.Install), dir
	rec.Installed = f.now().UTC()
	rec.Checked = rec.Installed
	if err := os.WriteFile(filepath.Join(dir, completeMarker), nil, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	// THE SWITCH: the launcher, then the record that describes it. A reader between the two
	// renames finds the new launcher and the old record, and the old record's files are still
	// there (the previous version is kept, below), so it is never shown a broken entry.
	if err := writeAtomic(f.Launcher(bin), []byte(launcherScript(rec)), 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := f.writeRecord(rec); err != nil {
		return nil, err
	}
	act := "install"
	if prev != nil {
		act = "update"
	}
	f.appendReceipt(receipt{Act: act, Bin: bin, Pack: p.Pack, Via: rec.Via, Declared: rec.Declared,
		Version: rec.Version, Dir: dir})
	f.pruneVersions(bin, rec, prev)
	return rec, nil
}

// installID names one install directory: time-ordered, unique per process.
func installID(f *Floor) string {
	return f.now().UTC().Format("20060102T150405.000000000Z") + "-" + strconv.Itoa(os.Getpid())
}

// removeIncomplete removes every install directory of bin without a completion marker — the
// leftover of an install that was killed. The caller holds bin's lock, so no writer is alive.
func (f *Floor) removeIncomplete(bin string, keep *Record) {
	entries, err := os.ReadDir(f.programsDir(bin))
	if err != nil {
		return
	}
	for _, e := range entries {
		dir := filepath.Join(f.programsDir(bin), e.Name())
		if keep != nil && dir == keep.Dir {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, completeMarker)); err != nil {
			_ = os.RemoveAll(dir)
		}
	}
}

// pruneVersions keeps the install just made and the one it replaced — an agent started a moment
// ago may still be loading files from that one — and removes every older one.
func (f *Floor) pruneVersions(bin string, cur, prev *Record) {
	entries, err := os.ReadDir(f.programsDir(bin))
	if err != nil {
		return
	}
	for _, e := range entries {
		dir := filepath.Join(f.programsDir(bin), e.Name())
		if dir == cur.Dir || (prev != nil && dir == prev.Dir) {
			continue
		}
		_ = os.RemoveAll(dir)
	}
}

// npmEnv is the environment npm runs with in the floor: installerEnv plus npm's own settings,
// the prefix among them when there is one.
func (f *Floor) npmEnv(nodeBin, prefix string) []string {
	set := []string{
		"NPM_CONFIG_CACHE=" + f.npmCache(),
		"NPM_CONFIG_UPDATE_NOTIFIER=false",
		"NPM_CONFIG_FUND=false",
		"NPM_CONFIG_AUDIT=false",
		// The environment's own shims must not intercept npm's children (the jail launcher's
		// YOLO_BYPASS_SHIMS=1, for the same reason).
		"YOLO_BYPASS_SHIMS=1",
	}
	if prefix != "" {
		set = append(set, "NPM_CONFIG_PREFIX="+prefix)
	}
	return f.installerEnv(nodeBin, set...)
}

// installNpm installs an npm program into dir/npm with the floor's Node.
func (f *Floor) installNpm(ctx context.Context, p Program, dir string) (*Record, error) {
	v := f.NodeVersion()
	if !packdecl.SatisfiesNodeFloor(v, p.Install.NodeFloor) {
		return nil, fmt.Errorf("the floor's Node v%s is below the pack's node_floor %s", v, p.Install.NodeFloor)
	}
	nodeBin, err := f.ensureNode(ctx, v)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(f.npmCache(), 0o700); err != nil {
		return nil, err
	}
	prefix := npmPrefix(dir)
	name, version := packdecl.SplitNpmSpec(p.Install.Package)
	argv := []string{filepath.Join(nodeBin, "npm"), "install", "-g", "--prefer-online"}
	argv = append(argv, p.Install.Flags...)
	argv = append(argv, packdecl.NpmInstallSpec(name, version))
	res, err := f.runBounded(ctx, f.installTimeout(), dir, f.npmEnv(nodeBin, prefix), argv, false)
	if err != nil {
		return nil, failure("npm install "+packdecl.NpmInstallSpec(name, version), err, res)
	}
	entry := filepath.Join(prefix, "bin", p.Bin())
	if _, err := os.Stat(entry); err != nil {
		return nil, fmt.Errorf("npm installed %s but it provides no %s (%s is absent)", name, p.Bin(), entry)
	}
	rec := &Record{Node: v, Entry: entry, Version: npmPackageVersion(prefix, name)}
	if isNodeScript(entry) {
		rec.Exec = []string{filepath.Join(nodeBin, "node"), entry}
	} else {
		// A native build shipped through npm (opencode-ai's is an ELF): started directly, never
		// wrapped in an interpreter.
		rec.Exec = []string{entry}
	}
	return rec, nil
}

// npmPrefix is the npm prefix an npm program's install directory holds: `npm install -g` runs
// with it (installNpm), and Record.NpmPackageDir reads the installed package under it.
func npmPrefix(dir string) string { return filepath.Join(dir, "npm") }

// NpmPackageDir is where the npm package pkg (a spec, its version selector ignored) sits inside
// this record's install, "" for a record npm did not install. `yolo check` reads a program's
// declared model catalog there (packdecl.Contribution.ModelCatalog).
func (r *Record) NpmPackageDir(pkg string) string {
	if r == nil || r.Via != "npm" || r.Dir == "" {
		return ""
	}
	name, _ := packdecl.SplitNpmSpec(pkg)
	if name == "" {
		return ""
	}
	return filepath.Join(npmPrefix(r.Dir), "lib", "node_modules", filepath.FromSlash(name))
}

// npmPackageVersion reads what npm put on disk, "" when it cannot tell — never a sentinel a
// report could mistake for a version.
func npmPackageVersion(prefix, name string) string {
	b, err := os.ReadFile(filepath.Join(prefix, "lib", "node_modules", filepath.FromSlash(name), "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return ""
	}
	return pkg.Version
}

// isNodeScript reports whether an npm bin is a script Node runs: a `#!` line naming node, or a
// .js/.cjs/.mjs file with none. Anything else — an ELF, a Mach-O, a shell script — is started as
// itself.
func isNodeScript(path string) bool {
	fh, err := os.Open(path)
	if err != nil {
		return false
	}
	defer fh.Close()
	head := make([]byte, 256)
	n, _ := fh.Read(head)
	head = head[:n]
	if len(head) >= 2 && head[0] == '#' && head[1] == '!' {
		line := string(head)
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		return strings.Contains(line, "node")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	switch filepath.Ext(real) {
	case ".js", ".cjs", ".mjs":
		return true
	}
	return false
}

// installFromCapture materializes the machine's capture of p's installer into dir/home — the
// same store entry a jail materializes, "one package inside and outside" (OQ-HP3) — capturing it
// first when the store has none this host can use.
func (f *Floor) installFromCapture(p Program, dir string) (*Record, error) {
	if f.ResolveCapture == nil {
		return nil, errors.New("this machine has no capture store")
	}
	home := filepath.Join(dir, "home")
	entry, err := f.ResolveCapture(p.Bin())
	recaptured := false
	if err != nil {
		if f.Capture == nil {
			return nil, fmt.Errorf("no capture of %s on this machine (%v), and this machine cannot run "+
				"`yolo capture`", p.Bin(), err)
		}
		f.say("no capture of %s on this machine yet; running `yolo capture %s` (a throwaway jail runs "+
			"its installer once, and every jail on this machine reuses the result)", p.Bin(), p.Bin())
		if entry, err = f.recapture(p.Bin()); err != nil {
			return nil, err
		}
		recaptured = true
	}
	if why := capturedProgram(entry, p.Bin()); why != "" {
		return nil, &noEntryError{reason: "the capture of " + p.Bin() + " on this machine cannot run " +
			"outside a jail: " + why}
	}
	// CONFINED: the manifest is the capture jail's account of itself, and this materialize runs on
	// the host, so nothing it names may land outside home or be written through a link beneath it.
	materialize := func(e *capture.Entry) (*capture.MaterializeResult, error) {
		return capture.Materialize(capture.MaterializeOptions{Entry: e, Home: home, Stderr: f.out(),
			Confined: true})
	}
	res, err := materialize(entry)
	if errors.Is(err, capture.ErrNotRelocatable) && !recaptured && f.Capture != nil {
		// A capture recorded before captures scanned their contents may only be materialized
		// into the jail home it was made in. A new capture records the full scan, and newest
		// wins for every jail too, so this is the machine's one recapture of it.
		f.say("the capture of %s on this machine was recorded for a jail's home only; recapturing it "+
			"so the floor can use it", p.Bin())
		_ = os.RemoveAll(home)
		if entry, err = f.recapture(p.Bin()); err != nil {
			return nil, err
		}
		res, err = materialize(entry)
	}
	if err != nil {
		return nil, fmt.Errorf("materializing the capture of %s: %w", p.Bin(), err)
	}
	entryPath := filepath.Join(home, ".local", "bin", p.Bin())
	st, err := os.Stat(entryPath)
	if err != nil || st.IsDir() || st.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("the capture of %s (%s) holds no runnable ~/.local/bin/%s", p.Bin(), entry.Key, p.Bin())
	}
	f.say("materialized %s from capture %s by %s (%d files)", p.Bin(), entry.Key, res.Mechanism(), res.Files)
	return &Record{Entry: entryPath, Exec: []string{entryPath}, Capture: entry.Key,
		Version: capturedVersion(home, p.Install, entry.Key)}, nil
}

// recapture runs the capture act and resolves what it stored.
func (f *Floor) recapture(bin string) (*capture.Entry, error) {
	if err := f.Capture(bin); err != nil {
		return nil, fmt.Errorf("capturing %s: %w", bin, err)
	}
	entry, err := f.ResolveCapture(bin)
	if err != nil {
		return nil, fmt.Errorf("the capture of %s ran but the store has no usable entry: %w", bin, err)
	}
	return entry, nil
}

// capturedVersion is the human version an installer capture carries: the newest entry of the
// program's versions directory (claude's `.local/share/claude/versions/2.1.267`), else the
// capture key.
func capturedVersion(home string, in packdecl.Install, key string) string {
	entries, err := os.ReadDir(filepath.Join(home, filepath.FromSlash(in.VersionsDirOrDefault())))
	if err == nil && len(entries) > 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Slice(names, func(i, j int) bool { return packdecl.CompareVersions(names[i], names[j]) < 0 })
		return names[len(names)-1]
	}
	return "capture " + key
}

// launcherScript is bin/<bin>: it starts the recorded argv by absolute path and nothing else.
//
// This is the SET ENVIRONMENT of HP-DIR2 item 2, as far as it reaches: the program is started by
// its absolute path, and an npm program by the floor's own Node's absolute path, so nothing on
// PATH — a mise shim, an activated install dir — chooses what starts or what interpreter it runs
// on. Nothing else is changed: the commands the program runs see the environment it was handed,
// the user's PATH first (OQ-HP7).
//
// The comment line names the record's bin, version and pack, and each goes through commentField:
// the version is what the package's own package.json says, or the name of a directory the
// capture's installer made, so it is text a vendor chose, and a newline in it would end the
// comment and make the rest a line of this file — which the host runs with the user's full
// authority on every `yolo host -- <bin>`. The exec line is the only line that runs.
func launcherScript(rec *Record) string {
	return "#!/bin/sh\n" +
		"# yolo's host agent floor: " + commentField(rec.Bin) + " " + commentField(rec.Version) +
		", from pack " + commentField(rec.Pack) + ".\n" +
		"# Generated by yolo (docs/design/host-tool-provisioning.md); `yolo host` and\n" +
		"# `yolo host apply` rewrite or remove it. It starts the program by absolute path.\n" +
		"exec " + shquote.Join(rec.Exec) + " \"$@\"\n"
}

// commentField is s for a shell comment: every character outside a conservative set — letters,
// digits and ._+-@/:~ — becomes '?', so no value can carry a line break, a control character or
// anything a reader of the file could mistake for more than a label.
func commentField(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			strings.ContainsRune("._+-@/:~", r):
			return r
		}
		return '?'
	}, s)
}
