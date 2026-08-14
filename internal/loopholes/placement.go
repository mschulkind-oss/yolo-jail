package loopholes

// placement.go is §4.3a's PLACEMENT rule applied to a loophole's MANIFEST faces.
//
// The rule itself lives in internal/config (loopholeplacement.go), which owns the
// comparison against the two trees a launch hands an agent. What lives here is the
// half that cannot: two of the three targets a manifest names are RUNTIME
// RESOLUTIONS — the module dir after symlinks, and the argvs after {loophole_dir}
// substitution — and a resolved Loophole is where those exist.
//
// Which is also why the seam is a thin method rather than a second copy of the rule.
// The config faces and the manifest faces have to agree about what "inside the
// workspace" means, and two tree comparisons is how they would come to disagree.

import "github.com/mschulkind-oss/yolo-jail/internal/config"

// PlacementProblems applies §4.3a's PLACEMENT rule to this loophole's MANIFEST
// faces: its module dir, its `host_daemon.cmd` and its `doctor_cmd`.
//
// The rule's config faces (an inline entry's `command`/`doctor_cmd`) were already
// wired; the manifest faces were the "still owed" half of landing item 1a, and they
// need this package because two of the three inputs are runtime resolutions. The
// argvs are passed POST-substitution — Path and the Cmd fields on a resolved record
// already have {loophole_dir} expanded — which is the only spelling the check can
// use: an unsubstituted element carries a `{`, and the rule skips those as framework
// placeholders.
//
// workspace is the workspace being mounted, or "" for a caller that has none (the
// doctor path), which narrows the rule to the jail-home tree rather than disabling
// it.
func (l *Loophole) PlacementProblems(workspace string) []string {
	return config.LoopholeManifestPlacementProblems(config.LoopholeManifestPlacement{
		Name:          l.Name,
		ModuleDir:     l.moduleDirForPlacement(),
		HostDaemonCmd: l.hostDaemonCmd(),
		DoctorCmd:     l.DoctorCmd,
	}, workspace)
}

// moduleDirForPlacement is the module dir the placement rule should judge, or ""
// when there is none to judge.
//
// A CONFIG loophole has no module dir at all — Path holds the synthetic
// "<yolo-jail.jsonc:loopholes.name>" marker, which is not a path and must not be
// resolved as one. Its `command` is checked by the config faces, which is where a
// config entry belongs.
func (l *Loophole) moduleDirForPlacement() string {
	if l.FromConfig() {
		return ""
	}
	return l.Path
}

func (l *Loophole) hostDaemonCmd() []string {
	if l.HostDaemon == nil {
		return nil
	}
	return l.HostDaemon.Cmd
}
