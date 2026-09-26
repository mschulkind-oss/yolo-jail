package packload

// viaclosure.go is the SELECTION CLOSURE, whole: the needs closure, then the via closure
// over the needs-closed set, then the needs closure again over what via added
// (docs/reference/wire-bridge.md §3.1, docs/design/wire-bridge-gateway.md OQ-WG7 (c)).
//
// It exists because the two halves used to be called by hand, and only the launch called
// both. `yolo check`, config validation (the name reservation), `config promote`'s fold and
// the lazy loophole resolvers each ran ResolveNeeds alone, so their pack lists omitted a
// pack a via profile adds: a second, narrower answer to "which packs does this launch
// carry", which is AGENTS.md's duplicate-implementation class (WG-I11). One function now
// answers it, and each caller supplies only what differs between them: where the
// selection table comes from and how to read the user's profile declarations.

// Selection is what the closure reads beyond the selected packs.
type Selection struct {
	// Embedded looks a name up in the embedded official set, the only set a need or a
	// via may add from (WB-D9, WG-I7).
	Embedded func(name string) (*Pack, bool)
	// UseProfiles returns the effective CLI-name → profile-name table for the given pack
	// set, which is the needs-closed set when the closure asks. A pack set, because the
	// launch folds a bare `-p <name>` over every bin the set installs; a reader with no
	// launch in hand returns its config's `use_profiles` whatever it is handed. nil means
	// no profile is selected, and the via half is skipped.
	UseProfiles func(set []*Pack) map[string]string
	// UserProfiles reads the user's profile declarations. It is called only when some
	// profile is selected, so a closure with nothing to resolve reads no user file. nil
	// reads as "the user declares none".
	UserProfiles func() (map[string]UserProfile, error)
}

// Close extends selected by the whole selection closure and returns the packs it added
// (never ones already in selected), one cause line per addition in the order they joined,
// and the first refusal. The causes are the lines the launch banner and `yolo check` print
// (WB-D12); printing them is the caller's duty, as it is ResolveNeeds'.
//
// On a refusal it returns no additions at all. Every caller treats a refused closure as
// "the launch will not start", so a half-closed set would describe a launch that cannot
// happen.
func (s Selection) Close(selected []*Pack) (added []*Pack, causes []string, err error) {
	needsAdded, needsCauses, err := ResolveNeeds(selected, s.Embedded)
	if err != nil {
		return nil, nil, err
	}
	added, causes = needsAdded, needsCauses
	if s.UseProfiles == nil {
		return added, causes, nil
	}
	set := append(append([]*Pack{}, selected...), added...)
	active := s.UseProfiles(set)
	if len(active) == 0 {
		return added, causes, nil
	}
	var user map[string]UserProfile
	if s.UserProfiles != nil {
		if user, err = s.UserProfiles(); err != nil {
			return nil, nil, err
		}
	}
	viaAdded, viaCauses, err := ResolveVias(set, active, user, s.Embedded)
	if err != nil {
		return nil, nil, err
	}
	if len(viaAdded) == 0 {
		return added, causes, nil
	}
	added = append(added, viaAdded...)
	causes = append(causes, viaCauses...)
	more, moreCauses, err := ResolveNeeds(append(set, viaAdded...), s.Embedded)
	if err != nil {
		return nil, nil, err
	}
	return append(added, more...), append(causes, moreCauses...), nil
}
