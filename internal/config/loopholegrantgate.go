package config

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// loopholegrantgate.go is the approval record's LOOPHOLE GRANT PART and its rows at the
// config-change gate (docs/design/loophole-access-approval.md §4.2 item 4, LG-D12; the plan's
// PD-4 and PD-5): for each selected grant-declaring loophole this setup runs, one line per grant
// the gate would hold and one per request the ceiling skips, approved by the same y as the config
// and the repository scope and recorded beside them in `<cname>.loophole-grants.json`.
//
// The standing BEFORE the record (candidate, requestable, not_allowed) is ResolveLoopholeGrants';
// this file reads the record and says granted or pending (PD-10): a candidate the record holds is
// granted, one it does not is pending until a y records it.
//
// AN `always` LINE SHOWS ONCE PER PROJECT (LG-D12): the user's edit alone grants no project
// anything, so a grant the ceiling's `always` adds is a row at each project's next gate, recorded
// by its y, and silent after that until the ceiling changes. A withdrawn grant shows removed once.
//
// AN INERT LOOPHOLE STAYS OUT (LG-D10): its grants serve nothing on this setup, so it has no row,
// changes no record, and its requests are kept apart (ScopeCheck.InertLoopholeGrants) for `yolo
// check` to name it inert.

const loopholeGrantsPartSuffix = ".loophole-grants.json"

// ApprovalLoopholeGrantsPath is the grant part of a workspace's approval record:
// $HOME/.local/share/yolo-jail/approvals/<container-name>.loophole-grants.json, beside
// ApprovalSnapshotPath and keyed the same way.
func ApprovalLoopholeGrantsPath(workspace string) string {
	if workspace == "" {
		workspace = cwd()
	}
	return filepath.Join(paths.ApprovalsDir(), runtime.FromWorkspace(workspace)+loopholeGrantsPartSuffix)
}

// GateLoophole is one selected grant-declaring loophole as a launch resolved it, for the gate.
type GateLoophole struct {
	Pack, Loophole, Access string
	Grants                 []loopholedecl.Grant
	// Inert is the inert report's line (loopholes.InertNote.Line) when this setup runs the
	// loophole not at all, "" when it runs.
	Inert string
}

// LoopholeGrantSource is one grant-declaring loophole in the gate: each kind's standing before
// the record, the requests the ceiling skips, and where each request was written.
type LoopholeGrantSource struct {
	Pack, Loophole, Access string
	Kinds                  []LoopholeKindState
	Skipped                []LoopholeSkippedRequest
	// requestedAt is, per kind, where its request was written: "yolo-jail.toml:14" where the
	// provenance carries a line, else the file, else "".
	requestedAt map[string]string
	// legacyRecorded is the grants a legacy switch file holds as if recorded (the legacy
	// projection, loopholegrantlegacy.go), read beside the record.
	legacyRecorded []string
}

// InertLoopholeGrants is an inert loophole's requests, which the gate does not show.
type InertLoopholeGrants struct {
	Pack, Loophole string
	// Inert is the inert report's line.
	Inert string
	// Requested are the kinds this project requests, in declaration order.
	Requested []string
}

// Loophole grant standings after the record is read (§4.6).
const (
	LoopholeGrantGranted     = "granted"
	LoopholeGrantPending     = "pending"
	LoopholeGrantRequestable = "requestable"
	LoopholeGrantNotAllowed  = "not_allowed"
)

// LoopholeGrantStanding is one kind's standing for a launch, with a valued kind's values.
type LoopholeGrantStanding struct {
	Standing string
	Values   []string
	// Text is the kind's declared line.
	Text string
}

// WithLoopholeGrants adds the selected grant-declaring loopholes to check, the scope a gate is
// about to ask about (nil when no broker starts): each one's ceiling, from the user config, and
// request, from read, the gate's own read of the workspace config. It returns check, or a new one
// when check was nil and a grant-declaring loophole is selected; nil when neither. A ceiling or a
// request the resolver refuses is the check's Fatal, which stops the gate naming it.
func WithLoopholeGrants(check *ScopeCheck, workspace string, read *WorkspaceRead, lps []GateLoophole) *ScopeCheck {
	if len(lps) == 0 {
		return check
	}
	out := check
	if out == nil {
		out = &ScopeCheck{}
	}
	var ws *jsonx.OrderedMap
	var wsSrc *Sources
	if read != nil {
		ws, wsSrc = read.Config, read.sources
	}
	user, userSrc, err := LoadReadableUserScopeConfigWithSources(true, func(string) {})
	if err != nil {
		if out.Fatal == nil {
			out.Fatal = err
		}
		return out
	}
	decls := make([]LoopholeGrantDeclaration, 0, len(lps))
	inert := map[string]string{}
	for _, lp := range lps {
		decls = append(decls, LoopholeGrantDeclaration{Pack: lp.Pack, Loophole: lp.Loophole, Access: lp.Access,
			Grants: append([]loopholedecl.Grant(nil), lp.Grants...)})
		if lp.Inert != "" {
			inert[lp.Pack] = lp.Inert
		}
	}
	// The legacy projection (loopholegrantlegacy.go): a JSONC file's loophole switches read as grants.
	input, legacyRecorded := withLegacyLoopholeGrants(LoopholeGrantInput{User: user, Workspace: ws, Declarations: decls,
		UserOrigins: readableUserOrigins(user, userSrc), WorkspaceOrigins: readableWorkspaceOrigins(ws, wsSrc)}, userSrc, wsSrc, workspace)
	plan, err := ResolveLoopholeGrants(input)
	if err != nil {
		if out.Fatal == nil {
			out.Fatal = err
		}
		return out
	}
	for _, p := range plan.Packs {
		if why, isInert := inert[p.Pack]; isInert {
			ig := InertLoopholeGrants{Pack: p.Pack, Loophole: p.Loophole, Inert: why}
			for _, k := range p.Kinds {
				if k.Requested {
					ig.Requested = append(ig.Requested, k.Grant.Kind)
				}
			}
			out.InertLoopholeGrants = append(out.InertLoopholeGrants, ig)
			continue
		}
		src := LoopholeGrantSource{Pack: p.Pack, Loophole: p.Loophole, Access: p.Access, Kinds: p.Kinds,
			Skipped: p.Skipped, requestedAt: map[string]string{}, legacyRecorded: legacyRecorded[p.Pack]}
		for _, k := range p.Kinds {
			if !k.Requested {
				continue
			}
			at := strings.TrimSuffix(lineLocated(wsSrc.node(), "", "requests", p.Pack, k.Grant.Kind), ": ")
			if at == "" && len(k.RequestFiles) > 0 {
				at = termsafe.Visible(k.RequestFiles[0])
			}
			src.requestedAt[k.Grant.Kind] = at
		}
		out.LoopholeGrants = append(out.LoopholeGrants, src)
	}
	if check == nil && len(out.LoopholeGrants) == 0 && len(out.InertLoopholeGrants) == 0 {
		return nil
	}
	return out
}

func (s *ScopeCheck) grantsInPlay() bool { return s != nil && len(s.LoopholeGrants) > 0 }

// loopholeGrantRow is one grant a gate would hold: its record spelling and its line.
type loopholeGrantRow struct {
	grant, label, from string
}

// rows are the grants the gate would hold for s, in declaration order: a candidate kind, one row
// per value for a valued one (§4.3: approved one line per value).
func (s LoopholeGrantSource) rows() []loopholeGrantRow {
	var out []loopholeGrantRow
	for _, k := range s.Kinds {
		if k.Standing != LoopholeKindCandidate {
			continue
		}
		from := "from your user config, for every project"
		if k.Ceiling != LoopholeCeilingAlways {
			from = "requested by this project"
			if at := s.requestedAt[k.Grant.Kind]; at != "" {
				from = "requested by " + at
			}
		}
		if !k.Grant.Valued {
			out = append(out, loopholeGrantRow{grant: k.Grant.Kind, label: s.Pack + ": " + grantText(k.Grant), from: from})
			continue
		}
		for _, v := range k.Values {
			out = append(out, loopholeGrantRow{grant: k.Grant.Kind + "=" + v,
				label: s.Pack + ": " + grantText(k.Grant) + " " + termsafe.Visible(v), from: from})
		}
	}
	return out
}

func grantText(g loopholedecl.Grant) string {
	if g.Text != "" {
		return g.Text
	}
	return g.Kind
}

// labelOfRecorded is a recorded grant's line, from the declaration where it still declares the
// kind, else the record's own spelling.
func (s LoopholeGrantSource) labelOfRecorded(grant string) string {
	kind, value, valued := strings.Cut(grant, "=")
	for _, k := range s.Kinds {
		if k.Grant.Kind != kind {
			continue
		}
		if valued {
			return s.Pack + ": " + grantText(k.Grant) + " " + termsafe.Visible(value)
		}
		return s.Pack + ": " + grantText(k.Grant)
	}
	return s.Pack + ": " + termsafe.Visible(grant)
}

// skippedLines are §4.2's lines for the requests the ceiling skips.
func (s LoopholeGrantSource) skippedLines() []string {
	out := make([]string, 0, len(s.Skipped))
	for _, sk := range s.Skipped {
		what := sk.Kind
		if len(sk.Values) > 0 {
			shown := make([]string, len(sk.Values))
			for i, v := range sk.Values {
				shown[i] = termsafe.Visible(v)
			}
			what += " " + strings.Join(shown, ", ")
		}
		out = append(out, fmt.Sprintf("  – %s: %s (not allowed on request by your config: yolo allow %s on_request %s)",
			s.Pack, what, s.Pack, sk.Kind))
	}
	return out
}

// loopholeGrantRecord is the grant part's content: per pack, its recorded grants, each `kind` or
// `kind=value`.
type loopholeGrantRecord map[string][]string

// loopholeGrantOutcome is what comparing the in-play grant sources against the record found.
type loopholeGrantOutcome struct {
	changed bool
	next    loopholeGrantRecord
	// block is the changed sources' lines, under one heading; otherSkipped the skipped lines of
	// sources that did not change, shown only when the gate asks anyway (PD-5).
	block, otherSkipped []string
	counts              []string
	// requestEdits are the request keys whose rows changed, for a decline's next step.
	requestEdits []EntryEdit
}

// loopholeGrantsHeading is the grant block's first line.
const loopholeGrantsHeading = "Loophole grants for this project:"

func compareLoopholeGrants(check *ScopeCheck, old loopholeGrantRecord) loopholeGrantOutcome {
	out := loopholeGrantOutcome{next: loopholeGrantRecord{}}
	for k, v := range old {
		out.next[k] = append([]string(nil), v...)
	}
	if !check.grantsInPlay() {
		return out
	}
	for _, s := range check.LoopholeGrants {
		rows := s.rows()
		prev := append([]string(nil), old[s.Pack]...)
		for _, r := range rows {
			// A legacy switch file's grant reads as recorded only while the gate would hold it;
			// one the ceiling now leaves out is no grant, never a removal shown at every launch.
			if legacyRecordHolds(s.legacyRecorded, r.grant) {
				prev = appendUnique(prev, r.grant)
			}
		}
		inPrev := map[string]bool{}
		for _, g := range prev {
			inPrev[g] = true
		}
		var lines []string
		added, removed := 0, 0
		grants := make([]string, 0, len(rows))
		for _, r := range rows {
			if inList(grants, r.grant) {
				continue
			}
			grants = append(grants, r.grant)
			if inPrev[r.grant] {
				delete(inPrev, r.grant)
				lines = append(lines, "    "+r.label+" (unchanged)")
				continue
			}
			lines = append(lines, "  + "+r.label+" ("+r.from+")")
			added++
		}
		for _, g := range prev {
			if inPrev[g] {
				lines = append(lines, "  - "+s.labelOfRecorded(g)+" (removed)")
				removed++
			}
		}
		if len(grants) > 0 {
			out.next[s.Pack] = grants
		} else {
			delete(out.next, s.Pack)
		}
		skipped := s.skippedLines()
		if added+removed == 0 {
			out.otherSkipped = append(out.otherSkipped, skipped...)
			continue
		}
		out.changed = true
		out.block = append(out.block, lines...)
		out.block = append(out.block, skipped...)
		out.counts = append(out.counts, fmt.Sprintf("%s grants: %d added, %d removed", s.Pack, added, removed))
		for _, k := range s.Kinds {
			if k.Requested && len(k.RequestFiles) > 0 {
				out.requestEdits = append(out.requestEdits, EntryEdit{Key: k.RequestPath,
					Files: append([]string(nil), k.RequestFiles...)})
			}
		}
	}
	if len(out.block) > 0 {
		out.block = append([]string{loopholeGrantsHeading}, out.block...)
	}
	return out
}

// shownBlock is the grant block a gate that asks shows: the changed sources, and the skipped
// requests of the others, since the gate asks anyway.
func (g loopholeGrantOutcome) shownBlock() []string {
	if len(g.otherSkipped) == 0 {
		return g.block
	}
	out := append([]string(nil), g.block...)
	if len(out) == 0 {
		out = append(out, loopholeGrantsHeading)
	}
	return append(out, g.otherSkipped...)
}

// readLoopholeGrantRecord reads the grant part; exists=false when there is none. One that does
// not decode is a record of nothing, which fails safe: every grant reads as added and asks.
func readLoopholeGrantRecord(workspace string) (loopholeGrantRecord, bool, error) {
	rec, exists, err := readScopeRecord(ApprovalLoopholeGrantsPath(workspace))
	return loopholeGrantRecord(rec), exists, err
}

func loopholeGrantRecordJSON(rec loopholeGrantRecord) (string, error) {
	return scopeRecordJSON(scopeRecord(rec))
}

func writeLoopholeGrantRecord(workspace string, rec loopholeGrantRecord) error {
	js, err := loopholeGrantRecordJSON(rec)
	if err != nil {
		return err
	}
	return writeSnapshot(ApprovalLoopholeGrantsPath(workspace), js)
}

// loopholeGrantsGate is the grant half of one gate: the record as it was and the comparison.
type loopholeGrantsGate struct {
	oldJSON string
	exists  bool
	out     loopholeGrantOutcome
}

func readLoopholeGrantsGate(workspace string, check *ScopeCheck) (loopholeGrantsGate, error) {
	var g loopholeGrantsGate
	if !check.grantsInPlay() {
		return g, nil
	}
	old, exists, err := readLoopholeGrantRecord(workspace)
	if err != nil {
		return g, err
	}
	g.exists = exists
	if exists {
		g.oldJSON, _ = loopholeGrantRecordJSON(old)
	}
	g.out = compareLoopholeGrants(check, old)
	return g, nil
}

// record writes the grant part when grants are in play.
func (g loopholeGrantsGate) record(workspace string, check *ScopeCheck) error {
	if !check.grantsInPlay() {
		return nil
	}
	return writeLoopholeGrantRecord(workspace, g.out.next)
}

// recordSilently writes the grant part on a gate that asked nothing, only when its bytes would
// change and there is something to hold: a workspace whose launches never hold a grant never
// gains the part.
func (g loopholeGrantsGate) recordSilently(workspace string, check *ScopeCheck) error {
	if !check.grantsInPlay() {
		return nil
	}
	next, err := loopholeGrantRecordJSON(g.out.next)
	if err != nil || next == g.oldJSON || (!g.exists && len(g.out.next) == 0) {
		return err
	}
	return writeLoopholeGrantRecord(workspace, g.out.next)
}

// LoopholeGrantStandings is each in-play grant-declaring loophole's standing per kind, by pack,
// against the approval record as it is now: a candidate the record holds is granted, one it does
// not is pending (PD-10). nil when no grant-declaring loophole is in play.
func (s *ScopeCheck) LoopholeGrantStandings(workspace string) map[string]map[string]LoopholeGrantStanding {
	if !s.grantsInPlay() {
		return nil
	}
	rec, _, _ := readLoopholeGrantRecord(workspace)
	legacy := map[string][]string{}
	for _, src := range s.LoopholeGrants {
		legacy[src.Pack] = src.legacyRecorded
	}
	return s.loopholeGrantStandings(func(pack, grant string) bool {
		return inList(rec[pack], grant) || legacyRecordHolds(legacy[pack], grant)
	})
}

// ApprovedLoopholeGrants is the standing a passing gate over this check approved, by loophole
// name: every candidate granted, as the y just recorded it (WW-P2: the gate's in-memory result,
// never a re-read of the record). Meaningful only once the gate passed; nil when no
// grant-declaring loophole is in play.
func (s *ScopeCheck) ApprovedLoopholeGrants() map[string]map[string]LoopholeGrantStanding {
	if !s.grantsInPlay() {
		return nil
	}
	byPack := s.loopholeGrantStandings(func(string, string) bool { return true })
	out := map[string]map[string]LoopholeGrantStanding{}
	for _, src := range s.LoopholeGrants {
		out[src.Loophole] = byPack[src.Pack]
	}
	return out
}

func (s *ScopeCheck) loopholeGrantStandings(recorded func(pack, grant string) bool) map[string]map[string]LoopholeGrantStanding {
	out := map[string]map[string]LoopholeGrantStanding{}
	for _, src := range s.LoopholeGrants {
		kinds := map[string]LoopholeGrantStanding{}
		for _, k := range src.Kinds {
			st := LoopholeGrantStanding{Text: k.Grant.Text}
			switch k.Standing {
			case LoopholeKindCandidate:
				st.Standing = LoopholeGrantGranted
				st.Values = append([]string(nil), k.Values...)
				grants := []string{k.Grant.Kind}
				if k.Grant.Valued {
					grants = grants[:0]
					for _, v := range k.Values {
						grants = append(grants, k.Grant.Kind+"="+v)
					}
				}
				for _, g := range grants {
					if !recorded(src.Pack, g) {
						st.Standing = LoopholeGrantPending
					}
				}
			case LoopholeKindRequestable:
				st.Standing = LoopholeGrantRequestable
			default:
				st.Standing = LoopholeGrantNotAllowed
				st.Values = append([]string(nil), k.Values...)
			}
			kinds[k.Grant.Kind] = st
		}
		out[src.Pack] = kinds
	}
	return out
}

// sortedGrantPacks is rec's packs, sorted.
func sortedGrantPacks(rec loopholeGrantRecord) []string {
	out := make([]string, 0, len(rec))
	for k := range rec {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RecordedLoopholeGrants is the grants the approval record holds per pack, for a report that is
// not a launch (`yolo check`). A launch hands its loophole the gate's result instead.
func RecordedLoopholeGrants(workspace string) map[string][]string {
	rec, _, err := readLoopholeGrantRecord(workspace)
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, p := range sortedGrantPacks(rec) {
		out[p] = append([]string(nil), rec[p]...)
	}
	return out
}
