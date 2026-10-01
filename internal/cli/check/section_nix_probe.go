package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/containerbuilder"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// nixVersionTimeout bounds `nix --version`, the same budget the daemon check gives
// `nix store info`: a first exec on a loaded VM (a Podman Machine's jail) can take
// seconds, and a check that times out a working nix reports a false [FAIL].
const nixVersionTimeout = 15 * time.Second

// sectionNix runs the Nix block: nix version, the daemon store connectivity check, then
// (on macOS) the extra-platforms footgun warning and the positive "Linux builder configured"
// line.
//
// THE DAEMON CHECK RUNS WHEREVER nix IS FOUND (docs/design/provisioner-sets.md PS-D5). It was
// macOS-only, on the argument that `check` has no notch-shaped reason to probe a Linux host
// about to launch a container, and a Linux container launch DOES use the host's daemon: it
// runs its image build through the host's nix and bind-mounts the daemon socket into the jail
// when it exists (internal/cli/run/assemble.go), so a hung daemon is a fault `check` can name
// before a launch trips on it. What stays macOS-only diagnoses the macOS Linux-builder
// offload: the trusted-user verdict inside the daemon check, and the extra-platforms and
// builder block below, which would be noise on Linux.
//
// TWO NIX DISTRIBUTIONS, ONE FIX EACH. Determinate Nix and upstream Nix keep user settings in
// different files and run differently-labelled daemons on a Mac, so every hint below that names
// a file or a restart asks which one this is (nixDistribution) and gives that machine's single
// command, never a menu of both (docs/reference/happy-path-principle.md).
func (o *Options) sectionNix(r *reporter) {
	r.sectionHeader("Nix")
	nixPath, hasNix := o.LookPath("nix")
	if hasNix {
		res := o.nixVersionProbe()
		switch {
		case res.Timeout:
			r.fail("nix found but `nix --version` did not answer within "+nixVersionTimeout.String(),
				probeNote("nix", "--version"))
		case !res.Ran:
			r.fail("nix found but could not be run: "+nixPath, o.nixBrokenNote(nixPath))
		case res.RC != 0:
			// nix's own error, then the step a nix that will not start gets: it used to be the
			// error alone, and an empty note when nix printed none.
			note := o.nixBrokenNote(nixPath)
			if stderr := strings.TrimSpace(res.Stderr); stderr != "" {
				note = stderr + "\n" + note
			}
			r.fail(fmt.Sprintf("nix found but `nix --version` exited %d", res.RC), note)
		default:
			r.ok("Nix: " + storage.ParseNixVersion(res.Stdout).Describe())
		}
	} else {
		r.fail("nix not found", o.nixInstallNote())
	}

	if hasNix {
		o.nixDaemonStoreCheck(r)
		if o.IsMacOS {
			o.nixExtraPlatformsAndBuilder(r)
		}
	}
	r.blank()
}

// nixVersionProbe is `nix --version`'s answer, run once per check: the version row reads it,
// and so does every hint that depends on which Nix this is (nixDistribution), the auto-GC
// section's included.
func (o *Options) nixVersionProbe() ExecResult {
	if o.nixVersion == nil {
		res := o.Exec([]string{"nix", "--version"}, "", nil, nixVersionTimeout)
		o.nixVersion = &res
	}
	return *o.nixVersion
}

// nixDistribution is which Nix answered `nix --version`, storage.NixUnknown when it did not
// answer or is neither distribution.
//
// INSIDE A CONTAINER JAIL IT IS THE IMAGE'S OWN nix, whatever the host runs: the jail reaches the
// host's daemon through its socket but runs its own client, so nothing about the host's config or
// daemon may be decided from it there, and sectionAutoGC does not ask inside one. On the macos-user
// backend the sandbox runs the HOST's nix client against the host's /etc/nix, so the distribution
// and /etc/nix there are the host's, and the macOS hints read them as such.
func (o *Options) nixDistribution() storage.NixDistribution {
	res := o.nixVersionProbe()
	if !res.Ran || res.Timeout || res.RC != 0 {
		return storage.NixUnknown
	}
	return storage.ParseNixVersion(res.Stdout).Distribution
}

// nixHostPath is a host path (/etc/nix/nix.conf, /Library/LaunchDaemons) as this check reads
// it: under nixHostRoot when a test set one. A hint prints the real path.
func (o *Options) nixHostPath(p string) string {
	if o.nixHostRoot == "" {
		return p
	}
	return filepath.Join(o.nixHostRoot, p)
}

// nixSettingFile is the one file a `key = …` line appended to takes effect in on this host, so a
// hint can give the fix as a command. generated is true when that file is a nix.conf this host
// generates from its nix-darwin or NixOS configuration (nixConfGenerated), where no appended line
// lasts and the hint names the configuration instead.
//
// Determinate Nix reads user settings from nix.custom.conf alone: determinate-nixd owns its
// nix.conf and replaces it. Upstream Nix, and a Nix this check does not recognize, take the line
// in nix.custom.conf only where nix.conf includes that file and does not set the key again below
// the include (storage.NixCustomConfTakesEffect): that is the Determinate installer's layout.
// Anywhere else the line goes in nix.conf itself, where an appended line is the last assignment
// and so wins; the official nixos.org installer writes no include, so its users get nix.conf.
// Nothing asks for an include line to be added first: on upstream Nix, appending to nix.conf works
// by itself.
func (o *Options) nixSettingFile(key string) (file string, generated bool) {
	custom := filepath.Join(storage.NixConfDir, "nix.custom.conf")
	if o.nixDistribution() == storage.NixDeterminate {
		return custom, false
	}
	conf := filepath.Join(storage.NixConfDir, "nix.conf")
	if effective, _ := storage.NixCustomConfTakesEffect(o.nixHostPath(conf), key); effective {
		return custom, false
	}
	return conf, o.nixConfGenerated()
}

// nixConfGenerated reports whether this host's /etc/nix/nix.conf is generated from a nix-darwin or
// NixOS configuration: a symlink resolving into the Nix store (/etc/nix/nix.conf ->
// /etc/static/nix/nix.conf -> /nix/store/…-nix.conf). A line appended there fails on the read-only
// store or is discarded by the next darwin-rebuild or nixos-rebuild, so the fix is the
// configuration's nix.settings. A nix.conf linked anywhere else (a dotfiles checkout) is not.
func (o *Options) nixConfGenerated() bool {
	conf := o.nixHostPath(filepath.Join(storage.NixConfDir, "nix.conf"))
	if fi, err := os.Lstat(conf); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	target, err := filepath.EvalSymlinks(conf)
	if err != nil {
		return false
	}
	store := o.nixHostPath("/nix/store")
	if resolved, err := filepath.EvalSymlinks(store); err == nil {
		store = resolved
	}
	return strings.HasPrefix(target, store+string(filepath.Separator))
}

// nixConfGeneratedNote says why a hint on a nixConfGenerated host names the configuration.
const nixConfGeneratedNote = "/etc/nix/nix.conf is generated by your nix-darwin or NixOS configuration, " +
	"so a line appended to it does not last"

// nixDaemonLabel is the launchd label of this Mac's nix daemon: the plist of the daemon this
// distribution runs when it is there (a switch between distributions can leave both daemons'
// plists behind, and sorted order alone picked the upstream one), else whichever nix-daemon
// plist is, else the distribution's known label. ok is false only for a Nix this check does not
// recognize with no plist to read.
func (o *Options) nixDaemonLabel() (string, bool) {
	known := o.nixDistribution().DaemonLabel()
	if label, ok := storage.NixDaemonLabelIn(o.nixHostPath(storage.LaunchDaemonsDir), known); ok {
		return label, true
	}
	return known, known != ""
}

// nixMacRestartCmd is the command restarting this Mac's nix daemon, and where to find its label
// when it cannot be named ("" when it can).
func (o *Options) nixMacRestartCmd() (cmd, labelPointer string) {
	label, ok := o.nixDaemonLabel()
	if !ok {
		return "sudo launchctl kickstart -k system/<label>",
			"<label> is your *nix-daemon.plist's name without .plist: ls " + storage.LaunchDaemonsDir + "/"
	}
	return "sudo launchctl kickstart -k system/" + label, ""
}

// nixTrustHint is the untrusted-user fix for this Mac as the commands to paste: append an
// extra-trusted-users line naming this account to the file it takes effect in, and restart this
// Nix's daemon.
//
// EXTRA-, because a `trusted-users = …` line is an assignment: appended after an existing list it
// replaces it (measured, nix 2.34.8: `trusted-users = root alice` then `trusted-users = root bob`
// trusts `root bob`), untrusting whoever was on it, while `extra-trusted-users = bob` appends
// (`root alice bob`).
//
// THE ACCOUNT IS NAMED, never `$(whoami)`, which expands in whatever shell the fix is pasted into.
// On the macos-user backend check runs inside the sandbox as its own account, which has no sudo,
// so the human pastes the fix in their own terminal, and `$(whoami)` named their own user.
func (o *Options) nixTrustHint() string {
	cmd, labelPointer := o.nixMacRestartCmd()
	name := o.currentUser()
	if name == "" {
		name = "$(whoami)" // nothing names this account; the paster's shell is the last resort
	}
	file, generated := o.nixSettingFile("trusted-users")
	var hint string
	if generated {
		hint = "Add " + name + " to trusted-users and restart the Nix daemon. " + nixConfGeneratedNote +
			": add \"" + name + "\" to nix.settings.trusted-users there and rebuild, then:\n" +
			"    " + cmd
	} else {
		hint = "Add " + name + " to trusted-users and restart the Nix daemon:\n" +
			`    echo "extra-trusted-users = ` + name + `" | sudo tee -a ` + file + "\n" +
			"    " + cmd
	}
	if labelPointer != "" {
		hint += "\n" + labelPointer
	}
	return hint
}

// nixDaemonTimeout bounds `nix store info`, the daemon check's budget on every OS.
const nixDaemonTimeout = 15 * time.Second

// nixDaemonStoreCheck runs the `nix store info` daemon-connectivity block: its timeout and its
// failure on every OS, each naming the restart for this OS's service manager and, on a Mac, for
// this Nix's daemon (nixDaemonRestart), and on macOS alone the trusted-user verdict (PS-D5).
func (o *Options) nixDaemonStoreCheck(r *reporter) {
	res := o.Exec(nixCmdArgv("store", "info"), "", nil, nixDaemonTimeout)
	if res.Timeout {
		// nixDaemonRestart names this Nix's own daemon, Determinate's included.
		note := "`nix store info` did not answer within " + nixDaemonTimeout.String() +
			", and a launch builds its image through this daemon.\n" + o.nixDaemonRestart()
		r.fail("Nix daemon: store operation timed out (daemon may be hung)", note)
		return
	}
	if !res.Ran {
		r.warn("Could not verify Nix daemon connectivity: exec failed", probeNote(nixCmdArgv("store", "info")...))
		return
	}
	output := res.Stdout + res.Stderr
	switch {
	case res.RC == 0 && !o.IsMacOS:
		// No trusted-user verdict here: it diagnoses the macOS Linux-builder offload, whose
		// `--builders` line needs a trusted user, and a Linux build runs locally without one.
		//
		// A STORE nix OPENED ITSELF IS NOT A DAEMON THAT ANSWERED: a single-user install, or
		// root on any install, reports "Store URL: local", and no daemon was asked at all.
		if url := nixStoreURL(output); url != "" && !nixDaemonStoreURL(url) {
			r.ok("Nix store: " + url + ", opened directly (no daemon was asked)")
			return
		}
		r.ok("Nix daemon: connected")
	case res.RC == 0 && strings.Contains(output, "Trusted: 1"):
		r.ok("Nix daemon: connected, user is trusted")
	case res.RC == 0:
		r.warn("Nix daemon: connected but user is NOT trusted", o.nixTrustHint())
	default:
		// The usual cause is a daemon that is not running, so the restart rides along with
		// nix's own first line. On a Mac that line used to be the whole note: a dead end.
		hint := firstLine(strings.TrimSpace(res.Stderr))
		if hint != "" {
			hint += " — "
		}
		r.fail("Nix daemon: connection failed", hint+o.nixDaemonRestart())
	}
}

// nixStoreURL is the "Store URL:" line's value in `nix store info` output, "" when there is none.
func nixStoreURL(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Store URL:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// nixDaemonStoreURL reports whether a store URL names the daemon: `daemon`, or the daemon socket
// spelled as a `unix://` URL.
func nixDaemonStoreURL(url string) bool {
	return url == "daemon" || strings.HasPrefix(url, "unix://")
}

// nixDaemonRestart is the restart for this OS's service manager: launchd's kickstart on macOS,
// for the daemon this Nix runs (nixMacRestartCmd), systemd's restart where `systemctl` is on the
// PATH, and otherwise a sentence naming the service. Both distributions install the systemd unit
// as nix-daemon, so the Linux command is the same for either. A jail takes the last branch,
// since it has no systemctl and the daemon it reaches is the host's.
func (o *Options) nixDaemonRestart() string {
	if o.IsMacOS {
		cmd, labelPointer := o.nixMacRestartCmd()
		if labelPointer != "" {
			return "Restart the Nix daemon: " + cmd + " (" + labelPointer + ")"
		}
		return "Restart the Nix daemon: " + cmd
	}
	if _, ok := o.LookPath("systemctl"); ok {
		return "Restart the Nix daemon: sudo systemctl restart nix-daemon"
	}
	return "Restart the nix-daemon service with the init system that runs it (the host's, " +
		"when this is a jail); there is no systemctl here"
}

// nixExtraPlatformsAndBuilder runs the `nix config show` extra-platforms
// warning + positive builder-present line.
func (o *Options) nixExtraPlatformsAndBuilder(r *reporter) {
	res := o.Exec(nixCmdArgv("config", "show"), "", nil, 10*time.Second)
	if res.Ran && !res.Timeout && res.RC == 0 {
		for _, line := range strings.Split(res.Stdout, "\n") {
			if strings.HasPrefix(line, "extra-platforms =") && strings.Contains(line, "linux") {
				// The remedy names THIS host's linux double, not a hardcoded
				// `aarch64-linux`: the detector above matches any `<arch>-linux`, so on
				// an Intel Mac it fires for `x86_64-linux` and then told the user to
				// remove a line that is not in their nix.conf — an unfollowable remedy
				// for a real problem. Same source the builder probe uses
				// (containerbuilder.BuilderSystem), so the two cannot disagree about
				// which arch this host wants. BACKLOG E8's bug class.
				//
				// The system half names this Nix's file: Determinate Nix's nix.custom.conf, never
				// the nix.conf determinate-nixd replaces, and the configuration's
				// nix.settings where nix-darwin generates nix.conf (nixConfGenerated).
				sys := "/etc/nix/nix.conf"
				switch {
				case o.nixDistribution() == storage.NixDeterminate:
					sys = "/etc/nix/nix.custom.conf"
				case o.nixConfGenerated():
					sys = "nix.settings.extra-platforms in the nix-darwin configuration that " +
						"generates /etc/nix/nix.conf, then rebuild"
				}
				r.warn("extra-platforms includes linux — local Linux builds "+
					"will be attempted and fail",
					"Remove '"+containerbuilder.BuilderSystem()+"' from extra-platforms "+
						"in your nix config (~/.config/nix/nix.conf or "+sys+") "+
						"— it makes nix try to run Linux binaries locally, which "+
						"fails on macOS.  A normal `yolo` run offloads any "+
						"from-source Linux build to an on-demand container builder "+
						"on the active runtime, so no local Linux build is needed.")
			}
		}
	}
	if o.hasLinuxBuilder() {
		r.ok("Linux builder configured")
	}
}
