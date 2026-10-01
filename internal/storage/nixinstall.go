package storage

// nixinstall.go is the install of Nix that userguide/getting-started.md recommends ("Step 1:
// Install Nix"), as the commands a message prints when a machine has no Nix, or one that will not
// run. The messages used to point at https://nixos.org/download/, a page that offers several
// installers and leaves the choice to the reader: a task, not a next step
// (docs/reference/happy-path-principle.md coins "next step"). Each command here is a line of that
// guide, and TestNixInstallHintsMatchTheGuide (internal/cli/check) reads the guide to check it.

// NixInstallerCommand is the NixOS Nix installer, the guide's install for an Apple silicon Mac and
// for Linux. Its --extra-conf makes the Nix daemon trust the user who runs it, which a Mac
// requires and Linux takes as an option; `$(whoami)` names the user who pastes the line, the
// account a launch builds as.
const NixInstallerCommand = `curl -sSfL https://artifacts.nixos.org/nix-installer | sh -s -- install --extra-conf "extra-trusted-users = $(whoami)"`

// NixIntelMacCommands are the guide's install for an Intel Mac, which the NixOS Nix installer has
// no build for: the install script on nixos.org, then the trust line and the restart of the daemon
// that script sets up ("Other ways to get Nix").
var NixIntelMacCommands = []string{
	`curl --proto '=https' --tlsv1.2 -L https://nixos.org/nix/install | sh`,
	`echo "extra-trusted-users = $(whoami)" | sudo tee -a /etc/nix/nix.conf`,
	`sudo launchctl kickstart -k system/org.nixos.nix-daemon`,
}

// NixInstallerReceipt is where the NixOS Nix installer, and the Determinate installer it grew
// from, leave the program that removes the Nix they installed; NixInstallerUninstall runs it (the
// guide's "Uninstall").
const (
	NixInstallerReceipt   = "/nix/nix-installer"
	NixInstallerUninstall = NixInstallerReceipt + " uninstall"
)

// NixUninstallManual is where the steps for removing a Nix the nixos.org script installed are,
// since that script has no uninstaller (the guide's "Uninstall" names the same page).
const NixUninstallManual = "https://nix.dev/manual/nix/latest/installation/uninstall"

// NixInstall is the guide's install for this machine: the sentence that introduces it, and its
// commands in order. intelMac selects the nixos.org script; every other host takes the NixOS Nix
// installer. The installer puts nix on the PATH of new shells only, so a caller's re-check belongs
// in a new terminal.
func NixInstall(intelMac bool) (intro string, commands []string) {
	if intelMac {
		return "On an Intel Mac, install Nix with the nixos.org script, then trust yourself and restart " +
			"its daemon", append([]string(nil), NixIntelMacCommands...)
	}
	return "Install Nix with the NixOS Nix installer (its --extra-conf makes the Nix daemon trust you)",
		[]string{NixInstallerCommand}
}
