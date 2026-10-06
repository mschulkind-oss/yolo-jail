package packsrc

// seriesprobe.go is the SERIES PROBE (a term coined here): `yolo pack lint --online`'s question
// about one patch series (docs/design/patched-forks.md PF-D64). It is the check a launch runs,
// forced, and then the series replayed at its own base, with nothing picked onto any upstream
// version:
//
//   - the check's own answer (CheckPatched): whether the ref names a branch, a tag or a commit,
//     whether the series' base is a commit of the upstream, the walk's list the follow rule
//     reads, and the problem the check records when there is none to read;
//   - whether a branch followed under a release rule carries any version tag the rule reads at
//     all, asked of the mirror apart from what the check makes of it, so a caller can say "this
//     upstream has no tags" under any rule the check applies to that case;
//   - the series replayed at its base (WalkSeries over an empty list), which a series exported
//     with `--base` passes by construction.
//
// ASKED OF A SCRATCH STORE. The probe forces a check, and a check writes the owner's check record
// and fetches into the store's mirror, so the caller hands it a store rooted in a directory it
// deletes afterwards. Asked of the pack store it would replace a launch's record with a forced
// check's, which no lint may do: lint is read-only toward what a launch decides.

// SeriesProbe is what ProbeSeries found.
type SeriesProbe struct {
	// Found is the forced check's answer. Zero when Err is set.
	Found CheckFound
	// Tagless is true when the ref is a branch, the follow rule is a release rule, and no version
	// tag the rule reads is merged into the branch at all, whatever the series' base.
	Tagless bool
	// Replayed is true when the series was replayed at its base, Walk the replay's result: Walk.Base
	// is a series that does not apply there, Walk.Err an apply error (a git too old among them). No
	// replay runs when the fetch failed or the base is not in the mirror, which Found says.
	Replayed bool
	Walk     WalkResult
	// Err is why no check could run: an address that does not parse, or a record that could not
	// be written.
	Err error
}

// ProbeSeries is the series probe of series, whose check request is w. waiting is told when a
// lock wait begins, as a check's is; nil says nothing.
func (s *Store) ProbeSeries(w PatchedWant, series *Series, waiting func(string)) SeriesProbe {
	var p SeriesProbe
	res := s.CheckPatched(w, CheckOptions{Force: true, Begin: func() (func(string), func()) {
		return waiting, func() {}
	}})
	if res.Err != nil {
		p.Err = res.Err
		return p
	}
	if res.Record == nil || res.Record.Check == nil {
		return p
	}
	p.Found = *res.Record.Check
	_, a, follow, err := w.Inputs()
	if err != nil {
		p.Err = err
		return p
	}
	mirror := s.mirrorPath(a.Repo)
	if p.Found.FetchErr != "" || !mirrorExists(mirror) {
		return p
	}
	unlock, err := s.lockMirror(a.Repo, waiting)
	if err != nil {
		p.Err = err
		return p
	}
	p.Tagless = s.taglessBranch(mirror, a.Ref, follow)
	base, _ := s.revParse(mirror, series.Base)
	unlock()
	if base == "" {
		return p // the check's problem names the base it could not find
	}
	p.Walk = s.WalkSeries(a.Repo, a.Path, series, nil, WalkOptions{Waiting: waiting})
	p.Replayed = true
	return p
}

// taglessBranch reports whether ref is a branch on which a release rule finds no version tag at
// all. The caller holds the mirror's lock. A git that cannot answer reports false: the check's
// own problem is then what the caller says.
func (s *Store) taglessBranch(mirror, ref string, follow FollowRule) bool {
	if follow.Kind != FollowRelease {
		return false
	}
	kind, name, _, err := s.classifyRef(mirror, ref)
	if err != nil || kind != refBranch {
		return false
	}
	merged, err := s.versionsContaining(mirror, name, "", follow)
	return err == nil && len(merged) == 0
}
