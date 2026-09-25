package packdecl

// envoverride.go is the `overridden_by` declaration on a `kind: "env"` contribution: the
// pack's own statement of what, delivered into the same jail, makes a consumer ignore the
// variables it sets. Core evaluates it (packload.EnvOverrideRefusal) and refuses the
// launch; core names no variable of its own.

import (
	"fmt"
	"path"
	"strings"
)

// EnvOverride is one thing that, delivered beside an env contribution, OVERRIDES it: the
// consumer the contribution is written for reads the other thing first, so the
// contribution is silently unused while every request still succeeds. A launch that
// would deliver both is REFUSED, fatally and with no escape hatch
// (docs/design/sso-backed-bedrock.md, OQ-SSO8).
//
// # Why the pack declares this, and core does not
//
// The facts are about ONE ECOSYSTEM's resolution order, and the maintainer's ruling was
// that such facts stay inside the pack: *"we can't like hard code anything into core to
// look for this stuff."* So `packs/aws-auth` states that a Bedrock bearer, or a static
// key pair, or a grant of the AWS config directory beats its container-credentials
// pointer, and core evaluates the declaration without knowing what any of those names
// mean. A made-up variable in a made-up pack refuses exactly the same way.
//
// # The shape
//
// Exactly one of `vars` or `host_file` per entry, and `because` always:
//
//	{"vars": ["A", "B"], "unless": ["C"], "because": "…"}
//	{"host_file": ".aws", "because": "…"}
//
// The four keys:
//
//   - `vars` — variables that override the contribution when ALL of them are delivered by
//     the launch (a CONJUNCTION: aws-auth's static key pair is one entry, because either
//     half alone answers nothing). An empty value is not a delivery, as everywhere else
//     in the launch's lookup.
//   - `unless` — variables whose delivery makes a `vars` entry STEP ASIDE: if any one of
//     them is delivered too, the entry is not tripped. It exists for the no-false-positive
//     rule below — an AWS JavaScript SDK skips its environment provider when a profile
//     variable is set, so aws-auth's pair plus that variable is not an override there.
//     `vars` only.
//   - `host_file` — a home-relative JAIL path; a `host_files` entry whose destination is
//     this path or anything under it overrides the contribution, when that entry would
//     actually render something (config.RenderedHostFilePaths).
//   - `because` — REQUIRED: the sentence the refusal quotes, saying why the other thing
//     wins. It is the pack's knowledge, so it is the pack's words; core adds only the
//     facts of the launch (what is delivered, from where) and the remedy.
//
// # The rule the shape serves: never a false positive
//
// The ruling accepts false negatives and refuses false positives (OQ-SSO8 condition 3,
// *"we may have false negatives. I really don't want false positives."*). Declare an
// override only where EVERY consumer of the contribution is measured to prefer the other
// thing, and prefer the narrower entry when unsure: a declaration that misses a case lets
// one launch through with a silent wrong answer, and one that over-reaches refuses a
// working jail with no way around it, because there is no hatch.
//
// # What is refused at decode, and why each one
//
// Each is a declaration that would otherwise be accepted and do nothing, or do something
// other than it says, with no message (validateContribution's standing rule):
//
//   - on any kind but `env` — no other kind sets variables for anything to override;
//   - neither or both of `vars` / `host_file` — one entry is one condition;
//   - `unless` without `vars` — nothing for it to step aside from;
//   - a `vars` name this contribution SETS — the contribution would override itself, and
//     every launch delivering it would be refused;
//   - an `unless` name this contribution sets — it is always delivered beside the
//     contribution, so the entry could never fire;
//   - an `unless` name also in `vars` — the entry could never fire either;
//   - an empty or duplicated name, an empty `because`, and a `host_file` that is not a
//     clean home-relative path.
//
// # Skew
//
// The tolerant decoder (DecodeTolerant) reads with json.Unmarshal, so an OLDER build
// reading a manifest that declares `overridden_by` ignores the key — that build simply
// does not refuse, which is a false negative and therefore the safe direction. Whoever adds
// a THIRD condition beside `vars` and `host_file` must extend the tolerance first: this
// build would see an entry with neither and report a problem, which is fatal on the boot
// path (the `tier` incident's shape; validateSkillsTier states the same rule for its enum).
type EnvOverride struct {
	// Vars are delivered together, all of them, to override the contribution.
	Vars []string `json:"vars,omitempty"`
	// Unless are variables any one of which, delivered as well, makes a Vars entry step
	// aside. Vars only.
	Unless []string `json:"unless,omitempty"`
	// HostFile is a home-relative jail path whose `host_files` grant (at it, or under it)
	// overrides the contribution. Clean, relative, no "~/" — HostFileEntry.Path's form.
	HostFile string `json:"host_file,omitempty"`
	// Because is the pack's sentence saying why the other thing wins. Required.
	Because string `json:"because,omitempty"`
}

// EnvOverrideDecl is one env contribution that declares overrides, as the evaluator
// consumes it: what the contribution sets, the gate it is delivered under, and the
// declarations.
type EnvOverrideDecl struct {
	// Sets is the contribution's variable NAMES, sorted. Never its values: the evaluator's
	// output is text a launch prints, and nothing in it needs a value.
	Sets []string
	// Profile is the contribution's `profile` gate, "" when it is unconditional. Whether
	// it is satisfied is the evaluator's question — it depends on the launch's profile
	// table, which the manifest does not see.
	Profile string
	// Overrides are the declarations, in manifest order.
	Overrides []EnvOverride
}

// EnvOverrideContributions returns every env contribution that declares `overridden_by`,
// in declaration order — gated and unconditional alike, each carrying its gate. Empty
// when the pack declares none, which is every shipped pack but aws-auth today.
func (m *Manifest) EnvOverrideContributions() []EnvOverrideDecl {
	var out []EnvOverrideDecl
	for _, c := range m.Contributions() {
		if c.Kind != KindEnv || len(c.OverriddenBy) == 0 {
			continue
		}
		out = append(out, EnvOverrideDecl{
			Sets:      sortedKeys(c.Vars),
			Profile:   c.Profile,
			Overrides: append([]EnvOverride(nil), c.OverriddenBy...),
		})
	}
	return out
}

// Covers reports whether a `host_files` destination falls under this override's
// host_file: the path itself, or anything inside it — never a sibling that merely shares
// a prefix (".awsfoo" is not ".aws"). dest is home-relative with the "~/" stripped, the
// form config.HostFileEntry.Path carries; a stray "~/" or "./" is tolerated.
func (o EnvOverride) Covers(dest string) bool {
	if o.HostFile == "" {
		return false
	}
	d := strings.TrimPrefix(strings.TrimPrefix(dest, "~/"), "./")
	return d == o.HostFile || strings.HasPrefix(d, o.HostFile+"/")
}

// envOverrideProblems validates `overridden_by`. Called for every kind, ahead of the kind
// switch, so a kind added tomorrow inherits the "does not take" refusal.
func envOverrideProblems(label string, c Contribution) []string {
	if len(c.OverriddenBy) == 0 {
		return nil
	}
	if c.Kind != KindEnv {
		return []string{fmt.Sprintf(
			"%s: kind %q does not take \"overridden_by\" — it names what overrides the "+
				"variables an \"env\" contribution sets, so only \"env\" has anything to "+
				"override", label, c.Kind)}
	}
	var problems []string
	for i, o := range c.OverriddenBy {
		problems = append(problems, envOverrideEntryProblems(
			fmt.Sprintf("%s.overridden_by[%d]", label, i), c.Vars, o)...)
	}
	return problems
}

func envOverrideEntryProblems(field string, sets map[string]string, o EnvOverride) []string {
	var problems []string
	if strings.TrimSpace(o.Because) == "" {
		problems = append(problems, field+": needs \"because\" — the sentence the refusal "+
			"quotes, saying why the other thing wins. Core knows nothing about the variables "+
			"involved, so without it the refusal cannot say why it refused")
	}
	switch {
	case len(o.Vars) == 0 && o.HostFile == "":
		problems = append(problems, field+": needs \"vars\" or \"host_file\" — an override "+
			"with no condition could never be tripped")
	case len(o.Vars) > 0 && o.HostFile != "":
		problems = append(problems, field+": names both \"vars\" and \"host_file\" — one "+
			"entry is one condition; declare two entries")
	}
	if len(o.Unless) > 0 && len(o.Vars) == 0 {
		problems = append(problems, field+": \"unless\" needs \"vars\" — it names variables "+
			"that make a vars entry step aside, so without one it steps aside from nothing")
	}
	problems = append(problems, envOverrideNameProblems(field+".vars", o.Vars)...)
	problems = append(problems, envOverrideNameProblems(field+".unless", o.Unless)...)
	for _, v := range o.Vars {
		if _, own := sets[v]; own {
			problems = append(problems, fmt.Sprintf(
				"%s.vars: %q is a variable this contribution sets — it would override itself, "+
					"and every launch delivering it would be refused", field, v))
		}
	}
	inVars := map[string]bool{}
	for _, v := range o.Vars {
		inVars[v] = true
	}
	for _, u := range o.Unless {
		if _, own := sets[u]; own {
			problems = append(problems, fmt.Sprintf(
				"%s.unless: %q is a variable this contribution sets, so it is always "+
					"delivered beside it and this entry could never fire", field, u))
		}
		if inVars[u] {
			problems = append(problems, fmt.Sprintf(
				"%s.unless: %q is also in \"vars\" — the entry needs it delivered and steps "+
					"aside when it is, so it could never fire", field, u))
		}
	}
	if o.HostFile != "" {
		problems = append(problems, hostFileOverrideProblems(field+".host_file", o.HostFile)...)
	}
	return problems
}

// envOverrideNameProblems refuses an empty or repeated variable name in one list.
func envOverrideNameProblems(field string, names []string) []string {
	var problems []string
	seen := map[string]bool{}
	for _, n := range names {
		switch {
		case strings.TrimSpace(n) == "":
			problems = append(problems, field+": empty variable name")
		case seen[n]:
			problems = append(problems, fmt.Sprintf("%s: %q is listed twice", field, n))
		}
		seen[n] = true
	}
	return problems
}

// hostFileOverrideProblems keeps a host_file comparable with a host_files destination:
// relative, inside the home, and CLEAN, because Covers compares strings and an unclean
// spelling ("./.aws", ".aws/") would match nothing and protect nothing, silently.
func hostFileOverrideProblems(field, p string) []string {
	if strings.HasPrefix(p, "~") {
		return []string{fmt.Sprintf("%s: %q — write it home-relative without \"~/\" "+
			"(\".aws\", not \"~/.aws\"), the form a host_files destination is compared in", field, p)}
	}
	before := appendPathProblems(nil, field, p)
	if len(before) > 0 {
		return before
	}
	if clean := path.Clean(p); clean != p || clean == "." {
		return []string{fmt.Sprintf("%s: %q must be a clean home-relative path (%q) — it "+
			"is compared against host_files destinations as a string, so an unclean "+
			"spelling would match nothing", field, p, path.Clean(p))}
	}
	return nil
}
