package run

// liveapplyplan.go is the pure half of `yolo config apply` (docs/design/live-config-apply.md
// §4): which changes a running jail can take, per key and per backend, and the plan, which is
// one change per difference between what the jail has and what the config now asks for. It
// runs nothing, and reads nothing but what the mountRefusal it is handed stats;
// liveapply.go gathers its inputs and carries it out.
//
// Terms (the design's, §1): the EFFECTIVE CONFIG is the workspace config a running jail has,
// recorded in .yolo/config-boot.json; a PENDING CHANGE is approved but not in it; a LIVE-APPLY
// STEP moves one change from pending to effective.

import (
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/changetiming"
	"github.com/mschulkind-oss/yolo-jail/internal/ctxinject"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

// applyOutcome is the one outcome every change gets (LA-D4, LA-P3).
type applyOutcome int

const (
	// outcomeAppliedNow: effective in every session, running processes included.
	outcomeAppliedNow applyOutcome = iota + 1
	// outcomeNextSession: effective for every later `yolo` entry; running processes keep
	// what they have.
	outcomeNextSession
	// outcomeNextRestart: approved and recorded, not applied; `yolo stop`, then `yolo`.
	outcomeNextRestart
	// outcomeNextLaunch: no jail is running, so the next `yolo` launches with it.
	outcomeNextLaunch
)

// String is the outcome as its line says it. Every line that waits names the next step
// (docs/reference/happy-path-principle.md); the command never offers to restart (OQ-LA5).
func (a applyOutcome) String() string {
	switch a {
	case outcomeAppliedNow:
		return "applied now"
	case outcomeNextSession:
		return "from your next yolo session"
	case outcomeNextRestart:
		return "at next restart (yolo stop, then yolo)"
	case outcomeNextLaunch:
		return "at your next yolo launch"
	}
	return "unknown"
}

// pending reports whether the outcome leaves the change owed to a restart or a launch: the
// command's exit 3, as `yolo config drift`'s 3.
func (a applyOutcome) pending() bool { return a == outcomeNextRestart || a == outcomeNextLaunch }

// keyOutcome delegates the stable backend/key matrix to the shared mechanics contract.
// Runtime observations remain inputs to Classify and can narrow its expectation.
func keyOutcome(rt, key string) (applyOutcome, string) {
	row, err := changetiming.Classify(key, changetiming.Context{Backend: timingBackend(rt),
		PackageDelivery: changetiming.BakedImage})
	if err != nil {
		return outcomeNextRestart, "no change-timing row is available for this key"
	}
	return applyOutcomeForTiming(row.Outcome), row.Reason
}

func timingBackend(rt string) changetiming.Backend {
	switch rt {
	case "podman":
		return changetiming.Podman
	case "container":
		return changetiming.AppleContainer
	case "macos-user":
		return changetiming.MacOSUser
	default:
		return changetiming.Backend(rt)
	}
}

func applyOutcomeForTiming(outcome changetiming.Outcome) applyOutcome {
	switch outcome {
	case changetiming.AppliedNow:
		return outcomeAppliedNow
	case changetiming.NextSession:
		return outcomeNextSession
	case changetiming.AtNextLaunch:
		return outcomeNextLaunch
	default:
		return outcomeNextRestart
	}
}

// mountsOutcome is the `mounts` row. The shared contract owns backend expectations; live is
// the runtime adapter's proven capability and reason preserves its probe-specific downgrade.
func mountsOutcome(rt string, live bool, reason string) (applyOutcome, string) {
	row, err := changetiming.Classify("mounts", changetiming.Context{
		Backend: timingBackend(rt), LiveMounts: live,
	})
	if err != nil {
		return outcomeNextRestart, "no change-timing row is available for mounts"
	}
	if reason != "" && row.Outcome == changetiming.AtNextRestart {
		row.Reason = reason
	}
	return applyOutcomeForTiming(row.Outcome), row.Reason
}

// scopeChangeOutcome is the `brokered` and repository-scope row, over the live broker files of
// this jail (the B lane's LiveFiles): applied now only when every one of them takes live scope
// (LA-D11). macos-user's sessions each run their own gate and broker, so with no live session
// the change reaches the next one.
func scopeChangeOutcome(rt string, files []liveScopeFile) (applyOutcome, string) {
	live := len(files) > 0
	for _, file := range files {
		live = live && file.LiveScope
	}
	row, err := changetiming.Classify("brokered", changetiming.Context{Backend: timingBackend(rt), LiveScope: live})
	if err != nil {
		return outcomeNextRestart, "no change-timing row is available for brokered scope"
	}
	if rt == "podman" && len(files) == 0 { // parity: HonoredBy — the Podman live-broker file scan found no broker for this jail
		row.Reason = "no running broker of this jail was found"
	} else if rt == "podman" && !live { // parity: HonoredBy — the Podman live-broker file scan found a broker without live scope
		row.Reason = "this jail's broker started before it could take a scope change live"
	} else if rt == "macos-user" && len(files) > 0 && !live { // parity: HonoredBy — native session broker files show a running broker without live scope
		row.Reason = "a running session's broker started before live scope existed"
	}
	return applyOutcomeForTiming(row.Outcome), row.Reason
}

func verifiedScopeOutcome(rt string, scopes []scopeDelta) (applyOutcome, string) {
	if len(scopes) == 0 {
		return scopeChangeOutcome(rt, nil)
	}
	for _, d := range scopes {
		if d.discoveryErr != "" {
			return outcomeNextRestart, "could not verify the running broker's scope: " + d.discoveryErr
		}
		outcome, reason := scopeChangeOutcome(rt, d.files)
		if outcome != outcomeAppliedNow {
			return outcome, reason
		}
		added, removed := d.addedRemoved()
		gAdded, gRemoved := d.grantsAddedRemoved()
		if len(added) != 0 || len(removed) != 0 || len(gAdded) != 0 || len(gRemoved) != 0 {
			return outcomeNextRestart, "the running broker's scope differs from the approved scope"
		}
	}
	return outcomeAppliedNow, "the running broker's scope already matches"
}

// changeKind is what a change's live-apply step does.
type changeKind int

const (
	changeKey         changeKind = iota // no step of its own: the outcome is the table's
	changeMountAdd                      // inject one context mount
	changeMountRemove                   // detach one context mount
	changeScope                         // replace one loophole's live scope files' repositories
	changeGrants                        // rewrite one loophole's live grant files (liveapplygrants.go)
)

// applyChange is one entry of the plan, and later of the result.
type applyChange struct {
	kind  changeKind
	sign  string // "+" added, "-" removed, "~" changed
	label string
	// key is the top-level workspace key folded into the effective-config record once every
	// change carrying it applied (LA-D8); "" folds nothing.
	key     string
	outcome applyOutcome
	reason  string
	// note is a fact the line always carries, such as a removal's open-file caveat.
	note string
	// disclosure is a launch disclosure the change carries (a read-write mount's line).
	disclosure  bool
	mountAdd    configCtxMount
	mountRemove liveCtxMount
	// scope is the loophole whose files a changeScope step rewrites, and to what.
	scope scopeDelta
	// grant is the loophole whose grant files a changeGrants step rewrites, grantFiles those files.
	grant      grantDelta
	grantFiles []loopholegrant.LiveFile
}

// line is the change's outcome line, in plain text: `+ /ctx/datasets (read-only): applied now`.
func (c applyChange) line() string {
	s := c.sign + " " + c.label + ": " + c.outcome.String()
	if c.reason != "" {
		s += " — " + c.reason
	}
	if c.note != "" {
		s += " — " + c.note
	}
	return s
}

// renderApplyChanges is each change's line, in plan order.
func renderApplyChanges(plan []applyChange) []string {
	out := make([]string, len(plan))
	for i, c := range plan {
		out[i] = c.line()
	}
	return out
}

// scopeDelta is one brokered loophole's scope as the apply sees it: what the gate approves
// (desired), and the live broker files of this jail holding what they hold now.
type scopeDelta struct {
	label, source string
	desired       []string
	// desiredGrants are the user grants the gate approves (docs/design/git-broker-plan.md ID-10),
	// compared with each live file's own.
	desiredGrants []brokerscope.Grant
	files         []liveScopeFile
	discoveryErr  string
	// recordChanged is the gate's own answer when no live file was found: the scope differs
	// from the approval record. With no broker to compare against, that is the change.
	recordChanged bool
}

// addedRemoved is, over every live file, what the gate approves that a file lacks (added) and
// what a file holds that the gate does not approve (removed), each repository once, compared as
// the file's form compares its entries (brokerscope.EntryKey): without case for OWNER/REPO, and
// exactly for remote URLs. Empty both ways means every file already holds the approval.
func (d scopeDelta) addedRemoved() (added, removed []string) {
	keyed := func(form string, l []string) map[string]bool {
		m := map[string]bool{}
		for _, r := range l {
			m[brokerscope.EntryKey(form, r)] = true
		}
		return m
	}
	seenAdd, seenRm := map[string]bool{}, map[string]bool{}
	for _, f := range d.files {
		form := f.File.Form
		want, have := keyed(form, d.desired), keyed(form, f.File.Repos)
		for _, r := range d.desired {
			if k := brokerscope.EntryKey(form, r); !have[k] && !seenAdd[k] {
				seenAdd[k] = true
				added = append(added, r)
			}
		}
		for _, r := range f.File.Repos {
			if k := brokerscope.EntryKey(form, r); !want[k] && !seenRm[k] {
				seenRm[k] = true
				removed = append(removed, r)
			}
		}
	}
	return added, removed
}

// grantsAddedRemoved is addedRemoved for the grants: over every live file, each approved grant a
// file lacks and each grant a file holds that the gate does not approve, compared exactly (a
// grant's kind and repository identity are already canonical), each once.
func (d scopeDelta) grantsAddedRemoved() (added, removed []brokerscope.Grant) {
	has := func(l []brokerscope.Grant, g brokerscope.Grant) bool {
		for _, x := range l {
			if x == g {
				return true
			}
		}
		return false
	}
	for _, f := range d.files {
		for _, g := range d.desiredGrants {
			if !has(f.File.Grants, g) && !has(added, g) {
				added = append(added, g)
			}
		}
		for _, g := range f.File.Grants {
			if !has(d.desiredGrants, g) && !has(removed, g) {
				removed = append(removed, g)
			}
		}
	}
	return added, removed
}

// applyPlanInput is everything the plan is computed from, gathered by liveapply.go.
type applyPlanInput struct {
	rt      string
	running bool
	// boot is the effective-config record, bootOK false when there is none; current is the
	// workspace config on disk.
	boot    *jsonx.OrderedMap
	bootOK  bool
	current *jsonx.OrderedMap
	// mountsLive and mountsReason are the jail's own answer (the C lane's ctxMountsLive);
	// mountRecordOK says the host's record of its mounts was readable, and the adds and
	// removes are that record against what the launch's own reading would bind now.
	mountsLive             bool
	mountsReason           string
	mountRecordOK          bool
	mountRecordInvalidated bool
	mountAdds              []configCtxMount
	mountRemoves           []liveCtxMount
	// mountRefusal, when set, is why one element cannot be injected or removed live (dest and
	// host source), "" when it can: liveMountRefusal.
	mountRefusal func(dest, source string) string
	// userMountsUnknown: the record is unreadable and the user scope has read-write elements,
	// which the workspace config never carries, so only a line of their own can say they wait.
	userMountsUnknown bool
	scopes            []scopeDelta
	// grants are the selected grant-declaring loopholes, each with its running daemon's live
	// grant files (liveapplygrants.go).
	grants []grantDelta
}

// liveMountRefusal is why the injector cannot reach the mount at dest from source, "" when it
// can: it targets only a directory it makes below /ctx/ (ctxinject.RelDest), so a destination
// elsewhere or a source that is a file waits for the launch that binds it.
func liveMountRefusal(dest, source string) string {
	if _, err := ctxinject.RelDest(dest); err != nil {
		return "a mount outside " + ctxinject.Root + "/ is bound only when the jail starts"
	}
	if st, err := os.Stat(source); err == nil && !st.IsDir() {
		return "a file is bound only when the jail starts"
	}
	return ""
}

// elementOutcome is one mount element's outcome: the key's, unless the element is one the
// injector cannot make.
func (in applyPlanInput) elementOutcome(mo applyOutcome, mwhy, dest, source string) (applyOutcome, string) {
	if mo == outcomeAppliedNow && in.mountRefusal != nil {
		if why := in.mountRefusal(dest, source); why != "" {
			return outcomeNextRestart, why
		}
	}
	return mo, mwhy
}

// openFileNote is a removal's caveat (design §4, M5): a lazy detach leaves open files readable.
const openFileNote = "a file an agent already holds open stays readable until it is closed"

// buildApplyPlan is the plan: mount elements first, then each loophole's scope, then every
// other changed workspace key in name order. That is also the order the steps run in.
func buildApplyPlan(in applyPlanInput) []applyChange {
	var plan []applyChange
	keys := changedKeys(in.boot, in.current)
	if !in.bootOK {
		// No record of what the jail has: nothing can be compared or folded, so the whole
		// workspace config waits for the launch that writes one.
		keys = nil
		if in.current != nil && in.current.Len() > 0 {
			plan = append(plan, applyChange{kind: changeKey, sign: "~", label: "workspace config",
				outcome: outcomeNextRestart,
				reason:  "this jail has no record of the config it has (.yolo/config-boot.json)"})
		}
	}
	changed := map[string]bool{}
	for _, k := range keys {
		changed[k] = true
	}

	// MOUNTS: per element where the host's record says what the jail has, else the key.
	mo, mwhy := mountsOutcome(in.rt, in.mountsLive, in.mountsReason)
	elements := 0
	if in.mountRecordOK && in.bootOK {
		// REMOVALS FIRST. A destination is a mount's identity in the jail, so a source or mode
		// changed at one is a removal and an addition of the same destination: the addition run
		// first finds it taken, and the mount is lost.
		for _, m := range in.mountRemoves {
			// A removal names only its destination: the detach never reads the source, which
			// may since have become a file or gone.
			o, why := in.elementOutcome(mo, mwhy, m.Dest, "")
			plan = append(plan, applyChange{kind: changeMountRemove, sign: "-", label: mountLabel(m.Dest, m.RW),
				key: "mounts", outcome: o, reason: why, note: openFileNote, mountRemove: m})
			elements++
		}
		for _, m := range in.mountAdds {
			o, why := in.elementOutcome(mo, mwhy, m.dest, m.source)
			plan = append(plan, applyChange{kind: changeMountAdd, sign: "+", label: mountLabel(m.dest, m.rw),
				key: "mounts", outcome: o, reason: why, disclosure: m.rw, mountAdd: m})
			elements++
		}
	}
	if in.mountRecordInvalidated && !changed["mounts"] {
		plan = append(plan, applyChange{kind: changeKey, sign: "~", label: "mounts (live state)", key: "mounts",
			outcome: outcomeNextRestart,
			reason:  "the host invalidated this jail's mount record after a failed update; the jail must restart to restore known state"})
	}
	if in.userMountsUnknown {
		// Folds nothing: the user scope is no workspace key.
		why := mwhy
		if why == "" {
			why = "yolo has no record of the mounts this jail has"
		}
		o := mo
		if o == outcomeAppliedNow {
			o = outcomeNextRestart
		}
		plan = append(plan, applyChange{kind: changeKey, sign: "~", label: "mounts (user config)",
			outcome: o, reason: why})
	}
	if changed["mounts"] && elements == 0 {
		// The key moved but no element did (a source that does not exist, which a launch skips
		// too), or the record cannot say: one line for the key.
		o, why := mo, mwhy
		if in.mountRecordOK && mo == outcomeAppliedNow {
			why = "nothing for this jail to bind"
		}
		plan = append(plan, applyChange{kind: changeKey, sign: "~", label: "mounts", key: "mounts",
			outcome: o, reason: why})
	}

	// SCOPE: one change per loophole whose live files differ from what the gate approves.
	scopeLines := 0
	for _, d := range in.scopes {
		o, why := scopeChangeOutcome(in.rt, d.files)
		if len(d.files) == 0 {
			if !d.recordChanged {
				continue
			}
			if d.discoveryErr != "" {
				why = "could not verify the running broker's scope: " + d.discoveryErr
			}
			plan = append(plan, applyChange{kind: changeKey, sign: "~", label: d.label + " repository scope",
				key: "brokered", outcome: o, reason: why})
			scopeLines++
			continue
		}
		added, removed := d.addedRemoved()
		gAdded, gRemoved := d.grantsAddedRemoved()
		if len(added) == 0 && len(removed) == 0 && len(gAdded) == 0 && len(gRemoved) == 0 {
			continue
		}
		var parts []string
		for _, r := range added {
			parts = append(parts, "+"+r)
		}
		for _, r := range removed {
			parts = append(parts, "-"+r)
		}
		for _, g := range gAdded {
			parts = append(parts, "+grant "+g.String())
		}
		for _, g := range gRemoved {
			parts = append(parts, "-grant "+g.String())
		}
		c := applyChange{kind: changeScope, sign: "~",
			label: d.label + " repository scope (" + strings.Join(parts, ", ") + ")",
			key:   "brokered", outcome: o, reason: why, scope: d}
		if len(removed) > 0 {
			c.note = "a removed repository is refused at its next request"
		}
		plan = append(plan, c)
		scopeLines++
	}
	if changed["brokered"] && scopeLines == 0 {
		o, why := verifiedScopeOutcome(in.rt, in.scopes)
		plan = append(plan, applyChange{kind: changeKey, sign: "~", label: "brokered", key: "brokered",
			outcome: o, reason: why})
	}

	// GRANTS: one change per grant-declaring loophole whose running daemon, or record, holds other
	// than the gate approves; the `requests` key is then what no grant line speaks for.
	grantPacks, lined := map[string]bool{}, map[string]bool{}
	for _, d := range in.grants {
		grantPacks[d.pack] = true
		if cs := d.changes(in.rt); len(cs) > 0 {
			plan = append(plan, cs...)
			lined[d.pack] = true
		}
	}
	if changed["requests"] {
		plan = append(plan, requestsLines(in, grantPacks, lined)...)
	}

	for _, k := range keys {
		if k == "mounts" || k == "brokered" || k == "requests" {
			continue
		}
		o, why := keyOutcome(in.rt, k)
		plan = append(plan, applyChange{kind: changeKey, sign: keySign(in.boot, in.current, k),
			label: strings.Join(changedPaths(in.boot, in.current, k), ", "), key: k, outcome: o, reason: why})
	}

	if !in.running {
		for i := range plan {
			plan[i].outcome, plan[i].reason = outcomeNextLaunch, ""
		}
	}
	return plan
}

// mountLabel is a mount element as its line names it: `/ctx/datasets (read-only)`.
func mountLabel(dest string, rw bool) string {
	if rw {
		return dest + " (read-write)"
	}
	return dest + " (read-only)"
}

// changedKeys are the top-level keys whose canonical values differ between boot and current,
// sorted.
func changedKeys(boot, current *jsonx.OrderedMap) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range []*jsonx.OrderedMap{boot, current} {
		if m == nil {
			continue
		}
		for _, k := range m.Keys() {
			if !seen[k] {
				seen[k] = true
				if !sameValue(boot, current, k) {
					keys = append(keys, k)
				}
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// sameValue compares one key of two maps on the canonical snapshot form, so cosmetic order
// is not a change (as drift compares).
func sameValue(a, b *jsonx.OrderedMap, k string) bool {
	av, aok := applyMapGet(a, k)
	bv, bok := applyMapGet(b, k)
	if aok != bok {
		return false
	}
	as, err1 := jsonx.DumpsSnapshot(av)
	bs, err2 := jsonx.DumpsSnapshot(bv)
	return err1 == nil && err2 == nil && as == bs
}

func applyMapGet(m *jsonx.OrderedMap, k string) (any, bool) {
	if m == nil {
		return nil, false
	}
	return m.Get(k)
}

// keySign is "+" for a key the jail does not have, "-" for one the config dropped, "~" else.
func keySign(boot, current *jsonx.OrderedMap, k string) string {
	_, inBoot := applyMapGet(boot, k)
	_, inCur := applyMapGet(current, k)
	switch {
	case !inBoot:
		return "+"
	case !inCur:
		return "-"
	}
	return "~"
}

// changedPaths names a changed key down to its changed sub-keys when both sides are objects
// (`network.ports`), so a line says which setting moved; the key alone otherwise.
func changedPaths(boot, current *jsonx.OrderedMap, k string) []string {
	bv, _ := applyMapGet(boot, k)
	cv, _ := applyMapGet(current, k)
	bm, bok := bv.(*jsonx.OrderedMap)
	cm, cok := cv.(*jsonx.OrderedMap)
	if !bok || !cok {
		return []string{k}
	}
	sub := changedKeys(bm, cm)
	if len(sub) == 0 {
		return []string{k}
	}
	out := make([]string, len(sub))
	for i, s := range sub {
		out[i] = k + "." + s
	}
	return out
}
