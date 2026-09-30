package check

import (
	"fmt"
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
func (o *Options) sectionNix(r *reporter) {
	r.sectionHeader("Nix")
	nixPath, hasNix := o.LookPath("nix")
	if hasNix {
		res := o.Exec([]string{"nix", "--version"}, "", nil, nixVersionTimeout)
		switch {
		case res.Timeout:
			r.fail("nix found but `nix --version` did not answer within "+nixVersionTimeout.String(), "")
		case !res.Ran:
			r.fail("nix found but could not be run: "+nixPath, "")
		case res.RC != 0:
			r.fail(fmt.Sprintf("nix found but `nix --version` exited %d", res.RC),
				strings.TrimSpace(res.Stderr))
		default:
			r.ok("nix: " + strings.TrimSpace(res.Stdout))
		}
	} else {
		r.fail("nix not found", "Install Nix: https://nixos.org/download/")
	}

	if hasNix {
		o.nixDaemonStoreCheck(r)
		if o.IsMacOS {
			o.nixExtraPlatformsAndBuilder(r)
		}
	}
	r.blank()
}

// nixDaemonTimeout bounds `nix store info`, the daemon check's budget on every OS.
const nixDaemonTimeout = 15 * time.Second

// nixDaemonStoreCheck runs the `nix store info` daemon-connectivity block: its timeout and its
// failure on every OS, each naming the restart for this OS's service manager
// (nixDaemonRestart), and on macOS alone the trusted-user verdict (PS-D5).
func (o *Options) nixDaemonStoreCheck(r *reporter) {
	res := o.Exec(nixCmdArgv("store", "info"), "", nil, nixDaemonTimeout)
	if res.Timeout {
		if o.IsMacOS {
			r.fail("Nix daemon: store operation timed out (daemon may be hung)",
				"This is a known issue with determinate-nixd. "+
					"Try: "+o.nixDaemonRestart()+" or switch to the vanilla nix-daemon")
			return
		}
		r.fail("Nix daemon: store operation timed out (daemon may be hung)",
			"`nix store info` did not answer within "+nixDaemonTimeout.String()+
				", and a launch builds its image through this daemon. "+o.nixDaemonRestart())
		return
	}
	if !res.Ran {
		r.warn("Could not verify Nix daemon connectivity: exec failed", "")
		return
	}
	output := res.Stdout + res.Stderr
	switch {
	case res.RC == 0 && !o.IsMacOS:
		// No trusted-user verdict here: it diagnoses the macOS Linux-builder offload, whose
		// `--builders` line needs a trusted user, and a Linux build runs locally without one.
		r.ok("Nix daemon: connected")
	case res.RC == 0 && strings.Contains(output, "Trusted: 1"):
		r.ok("Nix daemon: connected, user is trusted")
	case res.RC == 0:
		included, includedKnown := storage.NixCustomConfIncluded()
		label, ok := storage.DetectNixDaemonLabel()
		if !ok {
			label = "<label>"
		}
		restart := "sudo launchctl kickstart -k system/" + label
		var hint string
		if includedKnown && !included {
			hint = "/etc/nix/nix.conf does not include nix.custom.conf. " +
				"Either add it to the trusted-users line directly in " +
				"/etc/nix/nix.conf, or add an include line once: " +
				"echo '!include /etc/nix/nix.custom.conf' | " +
				"sudo tee -a /etc/nix/nix.conf. Then add your user " +
				"(trusted-users = root $(whoami)) and restart the " +
				"daemon: " + restart
		} else {
			hint = "Add your user to trusted-users in " +
				"/etc/nix/nix.custom.conf and restart the Nix daemon: " +
				restart
		}
		r.warn("Nix daemon: connected but user is NOT trusted", hint)
	case o.IsMacOS:
		r.fail("Nix daemon: connection failed", firstLine(strings.TrimSpace(res.Stderr)))
	default:
		// Off macOS the usual cause is a daemon that is not running, so the restart rides
		// along with nix's own first line.
		hint := firstLine(strings.TrimSpace(res.Stderr))
		if hint != "" {
			hint += " — "
		}
		r.fail("Nix daemon: connection failed", hint+o.nixDaemonRestart())
	}
}

// nixDaemonRestart is the restart for this OS's service manager: launchd's kickstart on macOS
// (with the daemon's label when it can be found), systemd's restart where `systemctl` is on
// the PATH, and otherwise a sentence naming the service. A jail takes the last branch, since
// it has no systemctl and the daemon it reaches is the host's.
func (o *Options) nixDaemonRestart() string {
	if o.IsMacOS {
		label, ok := storage.DetectNixDaemonLabel()
		if !ok {
			return "sudo launchctl kickstart -k system/<label>" +
				" — check ls /Library/LaunchDaemons/ for your *nix-daemon.plist"
		}
		return "sudo launchctl kickstart -k system/" + label
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
				r.warn("extra-platforms includes linux — local Linux builds "+
					"will be attempted and fail",
					"Remove '"+containerbuilder.BuilderSystem()+"' from extra-platforms "+
						"in your nix config (~/.config/nix/nix.conf or /etc/nix/nix.conf) "+
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
