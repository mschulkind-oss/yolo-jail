package loopholes

// jsonreport.go is `yolo loopholes list --format json` and `… status --format
// json`: the same two reports, as data.
//
// These are the surfaces an in-jail agent most needs and could least reach. The
// jail is credential-isolated, so an agent debugging "why can't I see the host's
// journal" cannot go and look — `loopholes list` is the whole of what it has, and
// until this file existed the answer arrived as a %-36s column followed by
// free-text continuation lines, which is a format that can only be regex'd
// wrongly (docs/reference/self-documenting-cli.md item 7).
//
// ONE STATE COMPUTATION, TWO RENDERINGS. The human label and the JSON `state`
// come from listState below, not from two switches that agree today: a loophole
// reported `active` in one form and `inactive` in the other is worse than either
// form being absent, because it is the kind of disagreement nobody looks for.
// The same holds for status's prefix, which comes from doctorState.
//
// The report is a top-level ARRAY for `list` and an OBJECT for `status`, and the
// asymmetry is deliberate: status has one fact that is not about any single
// loophole — the in-jail short-circuit — and a field is the only honest place to
// put it. `list` has no such fact.

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// ListEntry is one loophole in `yolo loopholes list --format json`.
type ListEntry struct {
	Name string `json:"name"`
	// State is "active", "disabled" or "inactive", and Reason narrows the last
	// one ("superseded", or the unmet requirement InactiveReason names). They
	// are two fields rather than the human form's one "inactive (superseded)"
	// string precisely so a consumer can branch on the state without parsing
	// parentheses.
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// Enabled is the config's own say-so, kept separate from State because they
	// answer different questions: `enabled` is what the user asked for,
	// `state` is what actually happened to it on this machine.
	Enabled bool `json:"enabled"`
	// Source is where the record came from (a pack, a config-inline block),
	// Transport how it reaches the jail, Lifecycle who starts it — the three
	// facts the human form packs into the "(a/b/c)" tag column.
	Source    string `json:"source"`
	Transport string `json:"transport"`
	Lifecycle string `json:"lifecycle"`
	// Intercepts are the hostnames this loophole intercepts, empty for the
	// (majority) non-intercepting kind. Interception is a property of this list
	// and NOT of the transport string — see RuntimeArgsFor — so a consumer must
	// read the list rather than infer it from Transport.
	Intercepts   []string `json:"intercepts,omitempty"`
	Description  string   `json:"description,omitempty"`
	SupersededBy []string `json:"superseded_by,omitempty"`
	// Settings are the config keys this loophole owns under
	// `loopholes.<name>.settings`. They are here for the same reason the human
	// form prints them: they are NOT in core's schema, so `yolo config-ref`
	// cannot document them and this is the only place they are discoverable.
	Settings []ListSetting `json:"settings,omitempty"`
}

// ListSetting is one pack-declared config key: what to write, which file may
// write it, and what happens if you write nothing.
type ListSetting struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Scope       string `json:"scope"`
	Default     string `json:"default"`
	Description string `json:"description,omitempty"`
}

// StatusEntry is one loophole in `yolo loopholes status --format json`.
type StatusEntry struct {
	Name string `json:"name"`
	// State is the human form's bracketed prefix verbatim: ok, fail, no-check,
	// disabled, superseded, unapproved, inactive. `no-check` means the loophole
	// declares no self-check, which is NOT the same as a check that was withheld
	// (`unapproved`) — the distinction Status's comment exists to protect.
	State string `json:"state"`
	// RC is the doctor command's exit code, or null when none ran. Null and 0
	// are different answers.
	RC           *int     `json:"rc"`
	Output       string   `json:"output,omitempty"`
	SupersededBy []string `json:"superseded_by,omitempty"`
}

// listState computes a loophole's reported state and the reason narrowing it.
// It is the ONE place that ordering is decided, and the order is load-bearing:
// a superseded loophole is off because of the user's own pack selection, which
// outranks every machine fact below it, so reporting an unmet requirement
// instead would send the reader after the wrong cause.
func listState(lh *Loophole) (state, reason string) {
	switch {
	case !lh.Enabled:
		return "disabled", ""
	case lh.Superseded():
		return "inactive", "superseded"
	default:
		if r, ok := lh.InactiveReason(); ok {
			return "inactive", r
		}
		return "active", ""
	}
}

// listLabel renders listState's pair as the human column: "active", "disabled",
// "inactive (superseded)". Keeping the render here, next to the computation, is
// what makes the two output forms incapable of disagreeing.
func listLabel(state, reason string) string {
	if reason == "" {
		return state
	}
	return state + " (" + reason + ")"
}

// listEntries converts the discovered set into the JSON shape.
func listEntries(all []*Loophole) []ListEntry {
	out := make([]ListEntry, 0, len(all))
	for _, lh := range all {
		state, reason := listState(lh)
		e := ListEntry{
			Name:        lh.Name,
			State:       state,
			Reason:      reason,
			Enabled:     lh.Enabled,
			Source:      lh.Source,
			Transport:   lh.Transport,
			Lifecycle:   lh.Lifecycle,
			Description: lh.Description,
		}
		for _, ic := range lh.Intercepts {
			e.Intercepts = append(e.Intercepts, ic.Host)
		}
		for _, s := range lh.SupersededBy {
			e.SupersededBy = append(e.SupersededBy, s.Line())
		}
		for _, st := range lh.Settings {
			e.Settings = append(e.Settings, ListSetting{
				Key:         st.Key,
				Type:        st.Type,
				Scope:       st.Scope,
				Default:     settingValueRepr(st.Default),
				Description: st.Description,
			})
		}
		out = append(out, e)
	}
	return out
}

// statusEntries converts the doctor results into the JSON shape. It takes the
// Set because the `unapproved` state is the Set's decision (the origin gate),
// not the loophole's.
func statusEntries(set Set, results []DoctorResult) []StatusEntry {
	out := make([]StatusEntry, 0, len(results))
	for _, r := range results {
		e := StatusEntry{
			Name:   r.Loophole.Name,
			State:  doctorState(set, r),
			RC:     r.RC,
			Output: r.Output,
		}
		for _, s := range r.Loophole.SupersededBy {
			e.SupersededBy = append(e.SupersededBy, s.Line())
		}
		out = append(out, e)
	}
	return out
}

// writeJSON encodes v to out. A failure is loud on errw rather than a silent
// empty stdout, which is the one outcome a machine consumer cannot diagnose.
func writeJSON(out, errw io.Writer, what string, v any) int {
	enc, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(errw, "yolo loopholes %s: encoding the report failed: %v\n", what, err)
		return 1
	}
	fmt.Fprintln(out, string(enc))
	return 0
}

// listJSON is `yolo loopholes list --format json`.
//
// An empty set is an empty ARRAY, not the human form's three-line explanation of
// where a loophole could come from. That prose is guidance for a person; a
// consumer asked what is installed and the answer is "nothing".
func listJSON(deps Deps, all []*Loophole) int {
	return writeJSON(deps.Out, deps.Err, "list", listEntries(all))
}

// statusJSON is `yolo loopholes status --format json`.
//
// The in-jail short-circuit keeps its shape here: the checks are host-side, so
// the document says so in a field rather than by returning an empty array that
// reads as "every loophole is fine".
func statusJSON(deps Deps) int {
	type doc struct {
		HostSide bool          `json:"host_side_only"`
		Entries  []StatusEntry `json:"loopholes"`
	}
	if deps.InJail {
		return writeJSON(deps.Out, deps.Err, "status", doc{HostSide: true, Entries: []StatusEntry{}})
	}
	set := loopholesWithConfig(deps, true)
	all := set.All()
	return writeJSON(deps.Out, deps.Err, "status", doc{
		Entries: statusEntries(set, set.RunDoctorChecks(all, doctorCheckTimeout)),
	})
}

// doctorCheckTimeout bounds one loophole's doctor_cmd. Named rather than
// repeated as a literal in both Status and statusJSON: two output forms of one
// report must not be able to run the checks under different deadlines.
const doctorCheckTimeout = 10 * time.Second
