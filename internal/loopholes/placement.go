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
//
// A BUNDLED loophole is exempt, and the reason is the SELF-HOSTING case rather than
// a trust concession. BundledLoopholesDir prefers the repo checkout when yolo runs
// from its own source tree (loopholes.go's reporoot.Resolve branch), so in THIS repo's
// own jail all three bundled loopholes resolve to /workspace/bundled_loopholes/* —
// inside the tree the launch mounts :rw. Judging them refuses the broker, the audio
// pass-through and host-processes on every launch of yolo's own development jail, which
// a nested-jail smoke caught and no unit test could: the tests build module dirs in
// t.TempDir(), so the one configuration where the paths coincide is the one nobody
// constructs.
//
// It is also the right answer on the merits, not just the expedient one. The rule
// exists because installed content in an agent-writable tree can be swapped between
// launches by an actor with none of the authority that installed it
// (docs/design/gate-placement-principle.md's two tests). A bundled loophole IS the
// yolo binary's own content — the same artifact that implements the check — so an
// agent that can rewrite it has already rewritten the checker. The gate protects
// nothing it does not already presuppose, which is Test 1 exactly.
//
// PACK loopholes stay judged: their content is not yolo's, and a pack whose module dir
// sits in the workspace is the case the rule was written for. Since OQ-LP10 retired the
// hand-placed user directory, PACK is the ONLY source this face applies to — bundled is
// exempt above and a config entry has no module dir. That is not a narrowing of the rule:
// the retired directory's manifests are not read at all any more, which is strictly
// stronger than judging where they sat.
func (l *Loophole) moduleDirForPlacement() string {
	if l.FromConfig() || l.Source == SourceBundled {
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
