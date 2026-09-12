package cli

// hostapplyjson.go is `yolo host apply --format json`: the dry run's report as data
// (docs/design/report-tiers.md §4.8, §9 step 8).
//
// # Why an acting verb emits one at all
//
// self-documenting-cli.md's requirement 7 — *anything that reports state must emit
// machine-readable output on request* — comes with a non-licence: *an acting verb refuses the
// flag rather than growing a second output mode*. `yolo host apply` is both, and the standard
// did not anticipate that: its DEFAULT POSTURE writes nothing and its whole output is "what
// would change". [OQ-RO4] rules the split by POSTURE rather than by verb — the dry run emits
// the document, `--assert` refuses it (exit 2, stdout empty, the refusal this file's caller
// makes) — and the argument is the standard's own rationale, that agents are the primary
// operators and an in-jail agent cannot read the host by hand. The one report that tells such
// an agent what a host render would do to its own home must not be prose to scrape.
//
// # It is a RECORDING, not a second traversal
//
// The document is the SURVEY (hostapplysurvey.go): the same single observe pass the text
// report and the launch gate read, with its human output discarded through outfmt.Sink. A
// second traversal of the written kinds would be a second model of what an apply does, free
// to drift out of step with the apply it describes — the shape AGENTS.md records this repo as
// having shipped five times. Two consequences worth knowing: every field below is something
// the survey already had to know to print its verdict, and a fact the text report cannot
// state is not available here either.
//
// # What it deliberately does not carry
//
// THE KIND-REFUSAL PROSE. The document names the kinds that do not apply at this notch and
// stops there, because rationale is not data (§4.6): the reasons live in the manual, under a
// drift gate, and no terminal view prints them at any verbosity either.
//
// THE USER'S OWN VALUES. §4.4's first forbidden thing, in every view: a config value can be a
// credential, and a document gets pasted into a bug report exactly as a transcript does. The
// document names the KEYS that would be replaced and the ENTRIES that would be dropped, never
// what is in them.

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// hostApplyDoc is one `yolo host apply --format json` document.
type hostApplyDoc struct {
	Version string `json:"version"`
	// Posture is always "dry-run": it is the only posture that emits a document, and stating
	// it is what keeps a consumer from having to remember that.
	Posture string `json:"posture"`
	Home    string `json:"home"`
	// Outcome is §4.3's verdict as a stable token (hostApplyOutcome); Verdict is the same
	// decision as the sentence the text form prints. Both, because the token is what a
	// consumer branches on and the sentence is what it shows a human when it stops.
	Outcome string `json:"outcome"`
	Verdict string `json:"verdict"`
	// FirstApply is whether yolo has never asserted a surface in this home — the flag that
	// turns every loss below into a confirmation prompt on --assert.
	FirstApply bool `json:"first_apply"`
	// Autonomy is the posture this notch renders ("guarded"), or absent when no pack
	// declares the kind.
	Autonomy string `json:"autonomy,omitempty"`
	// InapplicableKinds names the contribution kinds this notch does nothing with. Names
	// only — see the header.
	InapplicableKinds []string `json:"inapplicable_kinds"`
	// FailedPacks are the packs whose render errored. A blocker that decides the outcome, and
	// the reason every count below may be missing a pack's worth of surfaces.
	FailedPacks []string           `json:"failed_packs"`
	Counts      hostApplyDocCounts `json:"counts"`
	// Destinations are the ones an --assert WOULD CHANGE, with the tier each states. The
	// settled ones are counted rather than listed, for the same reason the text report counts
	// them: the survey holds a tally, not a list (70 of the measured home's 76 "changes" were
	// fourteen skills counted once per agent directory).
	Destinations []hostApplyDocDestination `json:"destinations"`
	// Groups are §4.4's tier-3 groups — every loss and blocker, grouped by remedy key, each
	// with its class and its pasteable fix.
	Groups []hostApplyDocGroup `json:"groups"`
}

// hostApplyDocCounts is §4.3's count block. Every field counts what the reader cares about
// (P6) — files, skills, keys, entries, binaries — and never the destinations a loop visited.
type hostApplyDocCounts struct {
	ConfigFilesChanged int `json:"config_files_changed"`
	SkillsAdopted      int `json:"skills_adopted"`
	SkillsComposed     int `json:"skills_composed"`
	SkillsRetired      int `json:"skills_retired"`
	BriefingsChanged   int `json:"briefing_destinations_changed"`
	FilesChanged       int `json:"delivered_files_changed"`
	WrapperDirsChanged int `json:"wrapper_directories_changed"`
	// InSync is the survey's own count and carries the survey's own meaning: destinations an
	// --assert would leave exactly as they are, INCLUDING ones the render skipped or refused.
	// Named as the survey names it rather than "compared and unchanged", which would be a
	// claim the render did not make (§3.4).
	InSync                     int `json:"destinations_in_sync"`
	ValuesReplaced             int `json:"values_replaced"`
	FilesWithReplacedValues    int `json:"files_with_replaced_values"`
	EntriesDropped             int `json:"entries_dropped"`
	SurfacesWithDroppedEntries int `json:"surfaces_with_dropped_entries"`
	SurfacesLosingComments     int `json:"surfaces_losing_comments"`
	DependenciesPresent        int `json:"dependencies_present"`
	DependenciesMissing        int `json:"dependencies_missing"`
	// DependenciesNotProbed is its own number and never folds into missing: yolo may not call
	// an environment unready on evidence it does not have (§4.9 point 6).
	DependenciesNotProbed int `json:"dependencies_not_probed"`
}

// hostApplyDocDestination is one destination an --assert would alter.
type hostApplyDocDestination struct {
	Kind    string `json:"kind"`
	Surface string `json:"surface"`
	Path    string `json:"path"`
	// Tier is §4.1's report tier as a number: 2 for a run fact, 3 for a loss.
	Tier int `json:"tier"`
	// Action is §4.6's vocabulary word. Always "would change" here — the document is the dry
	// run's, and these are the destinations that would.
	Action string `json:"action"`
}

// hostApplyDocGroup is one tier-3 group: what is lost or blocked, whose it is, and the fix.
type hostApplyDocGroup struct {
	// Class is which of §4.4's classes this is (remedyClass*).
	Class string `json:"class"`
	// Key is the remedy key the group is grouped ON — the config key, the local-pack path,
	// the missing binary — or absent for a group whose fix does not exist.
	Key      string   `json:"key,omitempty"`
	Headline string   `json:"headline"`
	Items    []string `json:"items"`
	// Remedy is the pasteable fix, once for the whole group; Alt a second route to it.
	Remedy string `json:"remedy,omitempty"`
	Alt    string `json:"alt,omitempty"`
	// NoRemedy is why there is none, for the classes that have none. Mutually exclusive with
	// Remedy — a group states one or the other, never both and never neither.
	NoRemedy string `json:"no_remedy,omitempty"`
}

// buildHostApplyDoc renders a finished observe pass's survey as the document.
//
// write is not a parameter: only the dry run emits one ([OQ-RO4]), so the posture is a
// constant here and hostApplyOutcome/hostApplyVerdict are asked the dry run's question.
func buildHostApplyDoc(s *hostApplySurvey) hostApplyDoc {
	home := s.Home()
	replacedKeys, replacedFiles := s.ReplacedValues()
	droppedEntries, droppedFrom := s.DroppedEntries()
	present, missing, notProbed := s.Deps()

	doc := hostApplyDoc{
		Version:    version.Get(""),
		Posture:    "dry-run",
		Home:       home,
		Outcome:    hostApplyOutcome(s, false),
		Verdict:    hostApplyVerdict(s, false),
		FirstApply: s.FirstApply(),
		Autonomy:   s.AutonomyPosture(),
		Counts: hostApplyDocCounts{
			ConfigFilesChanged:         s.ChangedOfKind("config"),
			SkillsAdopted:              len(s.SkillNames(skillAdopted)),
			SkillsComposed:             len(s.SkillNames(skillComposed)),
			SkillsRetired:              len(s.SkillNames(skillRetired)),
			BriefingsChanged:           s.ChangedOfKind("briefing"),
			FilesChanged:               s.ChangedOfKind("files"),
			WrapperDirsChanged:         s.ChangedOfKind("host_wrappers"),
			InSync:                     s.InSync,
			ValuesReplaced:             replacedKeys,
			FilesWithReplacedValues:    replacedFiles,
			EntriesDropped:             droppedEntries,
			SurfacesWithDroppedEntries: droppedFrom,
			SurfacesLosingComments:     s.DroppedComments(),
			DependenciesPresent:        present,
			DependenciesMissing:        missing,
			DependenciesNotProbed:      notProbed,
		},
		// `[]`, never `null`, for every list: a consumer looping over one should not have to
		// special-case the run that found nothing — which is the same rule that makes a
		// zero-packs run emit a document at all rather than nothing (§4.8).
		InapplicableKinds: emptyIfNil(s.InapplicableKinds()),
		FailedPacks:       emptyIfNil(s.FailedPacks()),
		Destinations:      []hostApplyDocDestination{},
		Groups:            []hostApplyDocGroup{},
	}
	for _, c := range s.Changed {
		doc.Destinations = append(doc.Destinations, hostApplyDocDestination{
			Kind: c.Kind, Surface: c.Surface, Path: c.Path,
			Tier: int(c.Tier), Action: "would change",
		})
	}
	// THE SAME GROUPS THE TEXT REPORT PRINTS, from the same builder: §4.4's contract is that
	// no default view omits a loss or a blocker, and a document assembling its own groups
	// would be a second place for that contract to hold or fail.
	for _, g := range hostApplyRemedyGroups(s, home, false) {
		doc.Groups = append(doc.Groups, hostApplyDocGroup{
			Class: g.Class, Key: g.Key, Headline: g.Headline,
			Items: emptyIfNil(g.Items), Remedy: g.Remedy, Alt: g.Alt, NoRemedy: g.NoRemedy,
		})
	}
	return doc
}

// emptyIfNil is the `[]`-not-`null` rule, in one place.
func emptyIfNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// emitHostApplyDoc writes the document to out and returns rc unchanged.
//
// It takes and returns the exit code for the reason `yolo check`'s finish does: a caller that
// remembers to emit on the happy path and forgets on an early return produces exactly the
// silence a machine consumer cannot diagnose. The dry run exits 0 whatever it finds
// ([OQ-RO5]) — its output IS the finding — so a non-zero here is the apply reporting a
// problem of its OWN, and the document still goes out beside it.
func emitHostApplyDoc(out, errw io.Writer, format string, s *hostApplySurvey, rc int) int {
	if !outfmt.IsJSON(format) {
		return rc
	}
	enc, err := json.MarshalIndent(buildHostApplyDoc(s), "", "  ")
	if err != nil {
		fmt.Fprintf(errw, "yolo host apply: encoding the report failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, string(enc))
	return rc
}
