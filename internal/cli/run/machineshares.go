package run

import (
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// machineshares.go is the macOS Podman pre-flight on BIND SOURCES: before `podman run`,
// every host path the launch is about to bind is checked against the folders the active
// Podman Machine actually shares with its VM (runtime.ReadMachineShares, which says where
// that list comes from and why it is not `podman machine inspect`'s output).
//
// A source the VM cannot see fails inside the VM as `statfs …: no such file or directory`
// at rc 125, with nothing from yolo beforehand. The measured case is a Homebrew install:
// jailPrefixSource resolves the prefix's symlinks, so both prefix sources are under
// $(brew --prefix)/Cellar, which a default machine does not share. Any other source —
// a workspace on /Volumes, a `mounts` entry, a `host_files` folder — fails the same way.
//
// It runs TWICE per launch, on one memoized probe: on the two jail-prefix sources in
// resolveJailPrefix, so the Homebrew case refuses before a multi-gigabyte image load
// rather than after it, and on the whole assembled argv in Run, which is the only place
// every source is known.
//
// TRI-STATE: an unreadable, ambiguous or unrecognized share list refuses nothing. Podman
// on Linux has no machine, and Apple Container binds arbitrary host paths into a VM of its
// own per container, so both are out of scope entirely.

// machineSharesProbe memoizes podmanMachineShares' answer for this launch, including an
// unknown one, so the two call sites ask podman once.
type machineSharesProbe struct {
	shares runtime.MachineShares
	ok     bool
}

// podmanMachineShares reads the active Podman Machine's share list once per launch.
func (o *Options) podmanMachineShares() (runtime.MachineShares, bool) {
	if o.machineShares != nil {
		return o.machineShares.shares, o.machineShares.ok
	}
	run := func(argv []string) (string, bool) {
		res := o.Exec(argv, "", nil, runtime.MachineShareProbeTimeout)
		return res.Stdout, res.Ran && !res.Timeout && res.RC == 0
	}
	s, ok := runtime.ReadMachineShares(run, o.Getenv)
	o.machineShares = &machineSharesProbe{shares: s, ok: ok}
	return s, ok
}

// unsharedBindSources returns the refusal text when a macOS Podman launch would bind a
// source its machine does not share, and "" otherwise — including whenever the share list
// cannot be read.
func (o *Options) unsharedBindSources(rt string, sources []string) string {
	if !o.IsMacOS || rt != "podman" { // parity: NotApplicable — Linux podman has no VM; Apple Container binds arbitrary host paths per container (see prefixUnreachableFromVM)
		return ""
	}
	shares, ok := o.podmanMachineShares()
	if !ok {
		return ""
	}
	unreachable := shares.Unreachable(sources, runtime.ResolveThroughExisting)
	if len(unreachable) == 0 {
		return ""
	}
	return "[bold red]Cannot start jail: the Podman Machine does not share a folder this " +
		"launch binds.[/bold red]\n" + shares.UnsharedRefusal(unreachable)
}

// bindSources returns the host source of every bind the argv makes before the image ref
// (after it is the jailed command, whose arguments are not podman's): `-v`/`--volume`
// specs and `--mount` fields, as argvMounts reads them — the one reader of podman's mount
// syntax, which the host path map reads too (translatedroots.go). Named volumes come back
// too; the check skips any source that is not an absolute path.
func bindSources(argv []string, imageRef string) []string {
	if i := slices.Index(argv, imageRef); i >= 0 {
		argv = argv[:i]
	}
	var out []string
	for _, m := range argvMounts(argv) {
		if m.Source != "" {
			out = append(out, m.Source)
		}
	}
	return out
}
