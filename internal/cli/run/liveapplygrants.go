package run

// liveapplygrants.go is `yolo config apply` for grant-declaring loopholes
// (docs/design/loophole-access-approval.md §4.6 "Live apply", LG-D7): what each one's running
// daemon holds against what the gate approves, and the step that rewrites a mediated loophole's
// grant file so its daemon takes the new standing at its next request, as a broker takes a scope
// at its next call. An unmediated loophole's crossing (binds, devices) is container argv decided
// when the jail starts, so its grant is recorded by the gate and its line says it lands at the
// next restart (changetiming.Classify, the `requests` row).
//
// THE FILE IS WRITTEN FROM THE GATE'S RESULT ONLY (WW-P2): the plan compares against what the gate
// is about to approve (config.ScopeCheck.ApprovedLoopholeGrants), and the step writes what it did
// approve (Options.approvedLoopholeGrants), through grantFileKinds, the launch's own shaping.

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/changetiming"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// The grant file discovery and rewrite, seams so a test can drive every outcome without a
// running daemon.
var (
	liveGrantFilesSeam   = loopholegrant.LiveFiles
	replaceGrantFileSeam = loopholegrant.Replace
)

// grantDelta is one selected grant-declaring loophole as the apply sees it: what the gate
// approves (desired, as its grant file would hold it), the live grant files of this jail's
// running daemon holding what they hold now, and the approval record's grants before the y.
type grantDelta struct {
	lp       *loopholes.Loophole
	pack     string
	crossing changetiming.GrantCrossing
	desired  map[string]loopholegrant.Kind
	files    []loopholegrant.LiveFile
	// discoveryErr is why the live files could not be listed; files is then nil.
	discoveryErr string
	// recorded is the record's grants for the pack (`kind`, `kind=value`) before this gate.
	recorded []string
}

// grantDeltas is every grant-declaring loophole the gate's check holds, with its running
// daemon's live grant files when the jail runs. Nothing here writes.
func (o *Options) grantDeltas(ctx context.Context, run *applyRun, scope *config.ScopeCheck) []grantDelta {
	if scope == nil || len(scope.LoopholeGrants) == 0 {
		return nil
	}
	byName := map[string]*loopholes.Loophole{}
	for _, lp := range grantDeclaring(loopholes.NewHostSet(cfgMap(run.cfg, "loopholes"))) {
		byName[lp.Name] = lp
	}
	approved := scope.ApprovedLoopholeGrants()
	recorded := config.RecordedLoopholeGrants(o.Workspace)
	var out []grantDelta
	for _, src := range scope.LoopholeGrants {
		lp := byName[src.Loophole]
		if lp == nil {
			continue
		}
		d := grantDelta{lp: lp, pack: src.Pack, crossing: changetiming.UnmediatedGrant,
			desired: grantFileKinds(lp, approved[lp.Name], true), recorded: recorded[src.Pack]}
		if lp.Mediated() {
			d.crossing = changetiming.MediatedGrant
		}
		if run.mode.running {
			files, err := liveGrantFilesSeam(ctx, loopholes.StateDirFor(lp.Name), run.cname, o.Workspace)
			if err != nil {
				d.discoveryErr = err.Error()
				files = nil
			}
			d.files = files
		}
		out = append(out, d)
	}
	return out
}

// grantStrings is kinds' granted grants as the record spells them (`kind`, or `kind=value` per
// value of a valued kind), in the manifest's declaration order.
func (d grantDelta) grantStrings(kinds map[string]loopholegrant.Kind) []string {
	var out []string
	for _, g := range d.lp.Grants {
		k, ok := kinds[g.Kind]
		if !ok || k.Standing != loopholegrant.Granted {
			continue
		}
		if !g.Valued {
			out = append(out, g.Kind)
			continue
		}
		for _, v := range k.Values {
			out = append(out, g.Kind+"="+v)
		}
	}
	return out
}

// sameKinds compares two grant blocks' kinds, an empty value list equal to none.
func sameKinds(a, b map[string]loopholegrant.Kind) bool {
	norm := func(m map[string]loopholegrant.Kind) map[string]loopholegrant.Kind {
		out := make(map[string]loopholegrant.Kind, len(m))
		for k, v := range m {
			if len(v.Values) == 0 {
				v.Values = nil
			}
			out[k] = v
		}
		return out
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

// staleFiles are the live files whose kinds differ from what the gate approves.
func (d grantDelta) staleFiles() []loopholegrant.LiveFile {
	var out []loopholegrant.LiveFile
	for _, f := range d.files {
		if !sameKinds(f.Block.Kinds, d.desired) {
			out = append(out, f)
		}
	}
	return out
}

// parts names what moves, against held, each grant a "+" or "-", then each other kind whose
// standing a live file holds differently ("read now not_allowed").
func (d grantDelta) parts(held []string, files []loopholegrant.LiveFile) []string {
	want := d.grantStrings(d.desired)
	var out []string
	for _, g := range want {
		if !inStrings(held, g) {
			out = appendOnce(out, "+"+g)
		}
	}
	for _, g := range held {
		if !inStrings(want, g) {
			out = appendOnce(out, "-"+g)
		}
	}
	for _, f := range files {
		for _, g := range d.lp.Grants {
			have, next := f.Block.Kinds[g.Kind], d.desired[g.Kind]
			if have.Standing != next.Standing && have.Standing != loopholegrant.Granted &&
				next.Standing != loopholegrant.Granted {
				out = appendOnce(out, g.Kind+" now "+string(next.Standing))
			}
		}
	}
	return out
}

// holdsAny is whether kinds grant anything, which is what admits a grant-declaring loophole's
// binds and devices into the jail (loopholes.Set.WithGrants).
func holdsAny(kinds map[string]loopholegrant.Kind) bool {
	for _, k := range kinds {
		if k.Standing == loopholegrant.Granted {
			return true
		}
	}
	return false
}

// changes is d's lines of the plan, none when the jail already holds what the gate approves.
//
// A MEDIATED loophole with a running daemon is compared file by file, and a file that differs is
// rewritten live. Any other (unmediated, or no daemon found) is compared with the approval record,
// which is what its jail was started with as far as yolo can say, and waits. Each line is keyed
// `requests`, so the effective-config record folds that key only once every grant took effect.
func (d grantDelta) changes(rt string) []applyChange {
	label := d.lp.Name + " grants"
	if d.crossing == changetiming.MediatedGrant && len(d.files) > 0 {
		stale := d.staleFiles()
		if len(stale) == 0 {
			return nil
		}
		var held []string
		for _, f := range stale {
			for _, g := range d.grantStrings(f.Block.Kinds) {
				held = appendOnce(held, g)
			}
		}
		o, why := grantOutcome(rt, d.crossing, true)
		out := []applyChange{{kind: changeGrants, sign: "~", key: "requests", outcome: o, reason: why,
			label: label + " (" + strings.Join(d.parts(held, stale), ", ") + ")", grant: d, grantFiles: stale}}
		if len(d.lp.HostBindMount) > 0 || len(d.lp.HostDevices) > 0 {
			for _, f := range stale {
				if holdsAny(f.Block.Kinds) != holdsAny(d.desired) {
					o, why := grantOutcome(rt, changetiming.UnmediatedGrant, false)
					out = append(out, applyChange{kind: changeKey, sign: "~", key: "requests",
						label: d.lp.Name + " binds and devices", outcome: o, reason: why})
					break
				}
			}
		}
		return out
	}
	p := d.parts(d.recorded, nil)
	if len(p) == 0 {
		return nil
	}
	o, why := grantOutcome(rt, d.crossing, false)
	if d.crossing == changetiming.MediatedGrant && d.discoveryErr != "" && o != outcomeAppliedNow {
		why = "could not find the running daemon's grant file: " + d.discoveryErr
	}
	return []applyChange{{kind: changeKey, sign: "~", key: "requests", outcome: o, reason: why,
		label: label + " (" + strings.Join(p, ", ") + ")"}}
}

// grantOutcome is the shared timing contract's answer for a grant change.
func grantOutcome(rt string, crossing changetiming.GrantCrossing, live bool) (applyOutcome, string) {
	row, err := changetiming.Classify("requests", changetiming.Context{Backend: timingBackend(rt),
		Grant: crossing, LiveGrants: live})
	if err != nil {
		return outcomeNextRestart, "no change-timing row is available for loophole grants"
	}
	return applyOutcomeForTiming(row.Outcome), row.Reason
}

// replaceGrantFiles is the changeGrants step: each stale live grant file of c's loophole
// rewritten to the kinds the gate approved, keeping the file's identity and settings
// (loopholegrant.Replace refuses a file that names another launch).
func (o *Options) replaceGrantFiles(ctx context.Context, c *applyChange) error {
	standings, ok := o.approvedLoopholeGrants[c.grant.lp.Name]
	if !ok {
		return errors.New("no grants were approved for " + c.grant.lp.Name + " in this apply")
	}
	kinds := grantFileKinds(c.grant.lp, standings, true)
	for _, f := range c.grantFiles {
		next := f.Block
		next.Kinds = kinds
		if err := replaceGrantFileSeam(ctx, f.Path, f.Block, next); err != nil {
			return err
		}
	}
	return nil
}

// requestsLines is the `requests` key's own lines: every changed `requests.<x>` that no grant
// line speaks for. A grant-declaring pack whose request moved but whose grants did not (a request
// the ceiling skips) changes nothing this jail holds, so its line is applied as it stands.
// grantPacks are the grant-declaring packs in play, lined those a grant line already covers.
func requestsLines(in applyPlanInput, grantPacks, lined map[string]bool) []applyChange {
	var out []applyChange
	var rest []string
	for _, p := range requestsPaths(in.boot, in.current) {
		pack, isSub := strings.CutPrefix(p, "requests.")
		if isSub && grantPacks[pack] {
			if !lined[pack] {
				out = append(out, applyChange{kind: changeKey, sign: "~", label: p, key: "requests",
					outcome: outcomeAppliedNow, reason: "it changes no grant this jail holds"})
			}
			continue
		}
		rest = append(rest, p)
	}
	if len(rest) > 0 {
		o, why := keyOutcome(in.rt, "requests")
		out = append(out, applyChange{kind: changeKey, sign: keySign(in.boot, in.current, "requests"),
			label: strings.Join(rest, ", "), key: "requests", outcome: o, reason: why})
	}
	return out
}

// requestsPaths is changedPaths for `requests`, down to each source (`requests.journal`) even
// where one side has no `requests` at all, since each source is its own pack's ask.
func requestsPaths(boot, current *jsonx.OrderedMap) []string {
	side := func(m *jsonx.OrderedMap) *jsonx.OrderedMap {
		v, _ := applyMapGet(m, "requests")
		if t, ok := v.(*jsonx.OrderedMap); ok {
			return t
		}
		if v == nil {
			return jsonx.NewOrderedMap()
		}
		return nil
	}
	b, c := side(boot), side(current)
	if b == nil || c == nil {
		return []string{"requests"}
	}
	sub := changedKeys(b, c)
	out := make([]string, len(sub))
	for i, s := range sub {
		out[i] = "requests." + s
	}
	return out
}

func inStrings(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func appendOnce(l []string, s string) []string {
	if inStrings(l, s) {
		return l
	}
	return append(l, s)
}
