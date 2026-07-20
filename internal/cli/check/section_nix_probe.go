package check

import (
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// sectionNix runs the Nix block: nix version, then (on macOS) the daemon store
// connectivity check, the extra-platforms footgun warning, and the positive
// "Linux builder configured" line.
func (o *Options) sectionNix(r *reporter) {
	r.section("Nix")
	nixPath, hasNix := o.LookPath("nix")
	if hasNix {
		res := o.Exec([]string{"nix", "--version"}, "", nil, 5*time.Second)
		if !res.Ran || res.Timeout {
			r.fail("nix found but not working: probe failed", "")
		} else {
			r.ok("nix: " + strings.TrimSpace(res.Stdout))
		}
	} else {
		r.fail("nix not found", "Install Nix: https://nixos.org/download/")
	}

	if o.IsMacOS && hasNix {
		o.nixDaemonStoreCheck(r)
		o.nixExtraPlatformsAndBuilder(r)
	}
	_ = nixPath
	r.blank()
}

// nixDaemonStoreCheck runs the `nix store info` daemon-connectivity block.
func (o *Options) nixDaemonStoreCheck(r *reporter) {
	res := o.Exec([]string{"nix", "store", "info"}, "", nil, 15*time.Second)
	if res.Timeout {
		label, ok := storage.DetectNixDaemonLabel()
		kickstart := "sudo launchctl kickstart -k system/<label>" +
			" — check ls /Library/LaunchDaemons/ for your *nix-daemon.plist"
		if ok {
			kickstart = "sudo launchctl kickstart -k system/" + label
		}
		r.fail("Nix daemon: store operation timed out (daemon may be hung)",
			"This is a known issue with determinate-nixd. "+
				"Try: "+kickstart+" or switch to the vanilla nix-daemon")
		return
	}
	if !res.Ran {
		r.warn("Could not verify Nix daemon connectivity: exec failed", "")
		return
	}
	output := res.Stdout + res.Stderr
	switch {
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
	default:
		r.fail("Nix daemon: connection failed", firstLine(strings.TrimSpace(res.Stderr)))
	}
}

// nixExtraPlatformsAndBuilder runs the `nix config show` extra-platforms
// warning + positive builder-present line.
func (o *Options) nixExtraPlatformsAndBuilder(r *reporter) {
	res := o.Exec([]string{"nix", "config", "show"}, "", nil, 10*time.Second)
	if res.Ran && !res.Timeout && res.RC == 0 {
		for _, line := range strings.Split(res.Stdout, "\n") {
			if strings.HasPrefix(line, "extra-platforms =") && strings.Contains(line, "linux") {
				r.warn("extra-platforms includes linux — local Linux builds "+
					"will be attempted and fail",
					"Remove 'aarch64-linux' from extra-platforms in your "+
						"nix config (~/.config/nix/nix.conf or /etc/nix/nix.conf) "+
						"— it makes nix try to run Linux binaries locally, which "+
						"fails on macOS.  Use a Linux builder instead: "+
						"`nix run nixpkgs#darwin.linux-builder`.")
			}
		}
	}
	if o.hasLinuxBuilder() {
		r.ok("Linux builder configured")
	}
}
