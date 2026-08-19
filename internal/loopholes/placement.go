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
// A BUNDLED loophole USED TO BE EXEMPT, and the exemption is gone because its subject
// is. `BundledLoopholesDir` preferred the repo checkout when yolo ran from its own
// source tree, so in THIS repo's own jail every bundled loophole resolved to
// /workspace/bundled_loopholes/* — inside the tree the launch mounts :rw — and judging
// them refused the broker, audio and host-processes on every launch of yolo's own
// development jail. A nested-jail smoke caught that and no unit test could: the tests
// build module dirs in t.TempDir(), so the one configuration where the paths coincide is
// the one nobody constructs. The exemption was also right on the merits, because a
// bundled loophole WAS the yolo binary's own content — the same artifact implementing
// this check — so an agent that could rewrite it had already rewritten the checker.
//
// Both halves stopped applying on 2026-08-19, when `bundled_loopholes/` was emptied
// (docs/design/broker-as-a-pack.md OQ-BP4). yolo's own loopholes are OFFICIAL PACKS now,
// and a pack's module dir is its STAGED copy under paths.AgentsDir() — outside every
// workspace by construction — so the self-hosting collision cannot recur through them.
// What replaced the trust half is not an exemption but the origin gate: an official
// pack's content carries yolo's authority through MayRunHostCode, which is a decision
// about the pack rather than about where its files happen to sit.
//
// PACK loopholes are therefore judged, all of them: their module dir being inside the
// mounted workspace is the case the rule was written for, and a locally-developed pack
// is exactly how it happens. Since OQ-LP10 retired the hand-placed user directory, PACK
// is the ONLY source this face applies to — a config entry has no module dir.
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
