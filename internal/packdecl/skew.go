package packdecl

// skew.go is the USE READ of a manifest on the host (DecodeForUse) and the per-field half of the
// version-skew skip both use reads share with the jail's tolerant read (DecodeTolerant).
//
// A USE READ (a term coined here) is a read whose result a launch or a host verb acts on, as
// against an AUTHORING read (`yolo pack lint`, `yolo pack footprint`, the packs yolo ships), whose
// job is to refuse. Both are strict about everything this build knows. They differ only about
// what it does not know: an authoring read refuses a field, a kind or a `via` this build has never
// heard of, and a use read skips the contribution that holds it and says so in one line
// (docs/design/patched-forks.md PF-D68). Every host read used to be an authoring read, so a pack
// written for a newer yolo, such as a patched fork's `patches` read by a yolo older than the mode,
// failed every launch on that host, not only the contribution's.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DecodeForUse parses a manifest for a USE READ (see the file doc): Decode's checks over every
// contribution this build can read, and a contribution it cannot read skipped and reported in
// `skipped`, one note per skip, never as a problem.
//
// "Cannot read" is the version-skew classes DecodeTolerant already skips in a jail, plus a field
// this build does not know:
//
//   - a contribution of an unknown KIND, or a `program` naming an unknown `via`, is skipped;
//   - a contribution holding a FIELD this build does not know is skipped, unless its kind only
//     restricts the agent (restrictingKind), in which case it is kept without the field: a
//     restriction is never dropped for a field this build cannot read (PF-D69);
//   - an endpoint's unknown `wire_api` is dropped from the endpoint, as in a jail;
//   - a pack-wide field this build does not know is ignored, and named.
//
// What a newer build may never have read stays a problem, as on the authoring read: a RETIRED
// kind, hook or field names an edit to the pack that no newer yolo makes unnecessary, and so do a
// malformed value, a missing required field and a second `autonomy`.
//
// Problems name a contribution by its index in pack.json, whatever was skipped before it.
func DecodeForUse(data []byte) (m *Manifest, problems, skipped []string) {
	clean, err := cleanJSON5(data)
	if err != nil {
		return nil, []string{ManifestName + ": " + err.Error()}, nil
	}
	var man Manifest
	if err := json.Unmarshal(clean, &man); err != nil {
		return nil, []string{ManifestName + ": " + err.Error()}, nil
	}
	fields, skipped := unknownFields(clean)
	kept := make([]Contribution, 0, len(man.Contributes))
	index := make([]int, 0, len(man.Contributes))
	for i, c := range man.Contributes {
		if c.Kind != "" && !KnownKind(c.Kind) && RetiredKind(c.Kind) == "" {
			skipped = append(skipped, unknownKindNote(i, c.Kind))
			continue
		}
		if note := unknownViaSkip(i, c); note != "" {
			skipped = append(skipped, note)
			continue
		}
		if note, keep := unknownFieldSkip(i, c, fieldAt(fields, i)); note != "" {
			skipped = append(skipped, note)
			if !keep {
				continue
			}
		}
		if trimmed, notes := unknownWireAPISkip(i, c); len(notes) > 0 {
			skipped = append(skipped, notes...)
			c = trimmed
		}
		kept = append(kept, c)
		index = append(index, i)
	}
	if len(kept) != len(man.Contributes) {
		man.Contributes = kept
	}
	problems = append(man.retiredFieldProblems(), man.installHintProblems()...)
	problems = append(problems, man.Validate()...)
	return &man, relabelContributions(problems, index), skipped
}

// restrictingKind reports whether a contribution of kind k only RESTRICTS the agent, so that
// skipping it would loosen confinement: a blocked tool would run, a forwarded CLI would run
// unmediated, and the guarded posture's prompts and lists would not hold at the host.
//
// Such a contribution holding a field this build does not know is KEPT without the field instead
// of skipped (PF-D69): what it still says is a restriction, so the rest holds and only what the
// field adds is lost, and if what is left no longer validates the pack is refused, which fails
// closed. A kind belongs here when every effect it has withholds something from the agent at some
// notch; a config kind's effect can go either way, so it is skipped like any other, and its line
// says the contribution is not used. A kind added to the closed set is weighed here in the same
// commit.
func restrictingKind(k Kind) bool {
	switch k {
	case KindBlockedTool, KindIntercept, KindAutonomy:
		return true
	}
	return false
}

// unknownFieldSkip is the note for contribution i holding a field this build does not know, ""
// when field is "", and whether the contribution is kept (restrictingKind) rather than skipped.
//
// The line names the contribution's kind, the field, and the two next steps: a newer yolo reads
// the field, or it is a typo that `yolo pack lint` names. Both, because this build cannot tell
// them apart, and the happy-path principle wants the step for each.
func unknownFieldSkip(i int, c Contribution, field string) (note string, keep bool) {
	if field == "" {
		return "", false
	}
	what := describeContribution(c)
	if restrictingKind(c.Kind) {
		return fmt.Sprintf("contributes[%d]: keeping %s without its unknown field %q, which this "+
			"yolo does not know — %s contribution restricts the agent, and a restriction is never "+
			"dropped for a field this yolo cannot read, so the rest of it holds and only what the "+
			"field adds is lost; %s", i, what, field, article(c.Kind), skewNextStep), true
	}
	return fmt.Sprintf("contributes[%d]: skipping %s — unknown field %q, which this yolo does not "+
		"know, so the contribution is not used; %s", i, what, field, skewNextStep), false
}

// skewNextStep is the next step every unknown-field line names.
const skewNextStep = "a newer yolo may read the field (update yolo), or it is misspelled " +
	"(`yolo pack lint <pack dir>` names it)"

// article is "a <kind>" or "an <kind>", for a sentence that names a kind as a noun.
func article(k Kind) string {
	if strings.ContainsRune("aeiou", rune(string(k + " ")[0])) {
		return "an " + string(k)
	}
	return "a " + string(k)
}

// describeContribution names a contribution in a skip line: its kind and the first identity
// field it carries (the bin, the name, where it lands, the surface, the hook, the source), so a
// pack with three `files` contributions says which one.
func describeContribution(c Contribution) string {
	for _, id := range []string{c.Bin, c.Name, c.Into, c.Surface, c.Hook, c.From} {
		if id != "" {
			return fmt.Sprintf("the %s contribution %q", c.Kind, id)
		}
	}
	if c.Kind == "" {
		return "a contribution with no kind"
	}
	return fmt.Sprintf("the %s contribution", c.Kind)
}

// unknownKindNote is the skip note for contribution i of a kind this build does not know, shared
// by the jail's tolerant read and the host's use read so the two say one thing.
func unknownKindNote(i int, kind Kind) string {
	return fmt.Sprintf(
		"contributes[%d]: skipping unknown kind %q — this build does not know it, "+
			"so the contribution is not rendered (version skew; a build that "+
			"knows the kind will render it)", i, kind)
}

// fieldAt is fields[i], "" past its end (a manifest whose contributes did not decode as a list).
func fieldAt(fields []string, i int) string {
	if i < len(fields) {
		return fields[i]
	}
	return ""
}

// unknownFields reads, from a cleaned manifest, the fields this build does not know: for each
// contribution, by its index in pack.json, the first such field it holds at any depth ("" for
// none), and one note per pack-wide field, which every read ignores.
//
// The first, not every: encoding/json's strict decoder stops at one, and the line's job is to
// say why the contribution is not used, which one field does. Anything that does not decode at
// all is left to the caller's lenient decode, which reports it as the problem it is.
func unknownFields(clean []byte) (perContribution, packWide []string) {
	var top map[string]json.RawMessage
	if json.Unmarshal(clean, &top) != nil {
		return nil, nil
	}
	for _, key := range sortedKeys(top) {
		raw := top[key]
		if strings.EqualFold(key, "contributes") {
			var list []json.RawMessage
			if json.Unmarshal(raw, &list) != nil {
				continue
			}
			perContribution = make([]string, len(list))
			for i, rc := range list {
				var c Contribution
				perContribution[i] = unknownField(rc, &c)
			}
			continue
		}
		alone, err := json.Marshal(map[string]json.RawMessage{key: json.RawMessage("null")})
		if err != nil {
			continue
		}
		if unknownField(alone, &Manifest{}) != "" {
			packWide = append(packWide, fmt.Sprintf("unknown field %q is ignored — this yolo "+
				"does not know it; %s", key, skewNextStep))
			continue
		}
		whole, err := json.Marshal(map[string]json.RawMessage{key: raw})
		if err != nil {
			continue
		}
		if field := unknownField(whole, &Manifest{}); field != "" {
			packWide = append(packWide, fmt.Sprintf("%q is read without its unknown field %q, "+
				"which this yolo does not know; %s", key, field, skewNextStep))
		}
	}
	return perContribution, packWide
}

// unknownField decodes raw into into strictly and returns the field encoding/json reports it
// does not know, "" when it reports none (or fails for another reason, which the caller's own
// decode reports).
func unknownField(raw []byte, into any) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(into)
	if err == nil {
		return ""
	}
	const prefix = "json: unknown field "
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return ""
	}
	if name, uerr := strconv.Unquote(msg[len(prefix):]); uerr == nil {
		return name
	}
	return msg[len(prefix):]
}

// contributionLabel matches the label every per-contribution problem carries.
var contributionLabel = regexp.MustCompile(`contributes\[(\d+)\]`)

// relabelContributions rewrites each `contributes[n]` in problems, n an index into the kept
// contributions, to the index index[n] that contribution has in pack.json, so a skip never moves
// the label on a sibling's problem. Identity when nothing was skipped.
func relabelContributions(problems []string, index []int) []string {
	identity := true
	for n, i := range index {
		if n != i {
			identity = false
			break
		}
	}
	if identity {
		return problems
	}
	for p, prob := range problems {
		problems[p] = contributionLabel.ReplaceAllStringFunc(prob, func(m string) string {
			n, err := strconv.Atoi(m[len("contributes[") : len(m)-1])
			if err != nil || n >= len(index) {
				return m
			}
			return fmt.Sprintf("contributes[%d]", index[n])
		})
	}
	return problems
}
