package cli

// configpromote.go is `yolo config promote` — the way OUT of capture
// (docs/design/config-ownership-and-promotion.md §5), and the verb a shipped refusal
// message has advised in English prose since before it existed (§1 P4).
//
// A key typed inside a jail has had exactly one exit until now: `yolo config reset`, which
// DISCARDS it. Promotion is the other one — it takes the captured key and DECLARES it,
// into the conventional local pack (which renders at every notch, so one move reaches every
// jail and the host) or into a pack the user owns.
//
// # This file is the CLASSIFICATION. configpromotewrite.go is the write.
//
// §10 splits the verb in two because the halves have different risk: the analysis writes
// nothing and is useful on its own, and the write is the dangerous part. The split survives
// in the code — everything here answers "what would happen", and nothing here touches a
// file.
//
// # The plan NAMES KEYS AND NEVER PRINTS VALUES
//
// docs/design/report-tiers.md §4.4's first forbidden thing, and here it is load-bearing
// twice over rather than a convention inherited: the values in question are CAPTURED
// CONFIG, this repo has one measured in a live overlay
// (`mcp.tavily.environment.TAVILY_API_KEY`, §5.3), and a plan is the output most likely to
// be pasted into a bug report or handed to another agent. The classification below can
// therefore say a key is sensitive, and name the key path that made it so, without ever
// being the thing that leaks it. `yolo config diff` still prints values — it is the
// command for looking at your own edits — and that difference is deliberate.
//
// # Host-side only, and for a boundary rather than a preference
//
// §5.5: the capture sidecars are in the workspace (a host directory either way), but the
// DESTINATIONS are under the host's `~/.config/yolo-jail/`, which no jail may write — a
// manifest is an input to composition, so an agent that could rewrite one in-jail could
// grant its own pack a host file on the next boot. refuseInJailPromote is the mirror of
// configdiff.go's refuseHostSideWrite, on the same surfacesAreLocal seam and pointing the
// other way.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// The DISPOSITIONS one captured key can carry. They are the JSON wire vocabulary as well
// as the text report's second column, so a consumer branches on a token rather than on
// prose — and the two views cannot disagree about what happened to a key.
//
// Exactly one is promotable; every other value is a key that stays where it is, and each
// says WHY in its own words rather than being folded into one "skipped".
const (
	// promotionPromotable: the key survives every mechanical check and would be written.
	promotionPromotable = "promotable"
	// promotionRedundant: identical to yolo's own last render, so declaring it would
	// declare a value the layers already produce (§5.2 step 2, the free and common case).
	promotionRedundant = "redundant"
	// promotionDead: an owning layer overrides it where it already sits, and the
	// destination is LOWER in the fold, so the move cannot help it.
	promotionDead = "dead"
	// promotionSensitive: the key, or a key nested under it, is named like a credential.
	// Refused, never redacted (§5.3) — see configpromotesensitive.go.
	promotionSensitive = "sensitive"
	// promotionEnvironmentBound: the value is an artifact of the jail it was set in — it
	// names the workspace root or the jail home. Refused rather than rewritten, and reported
	// SEPARATELY from sensitive because a path is not a secret: telling a user their key
	// "looks like a credential" when it holds /workspace is a wrong answer they cannot act on.
	promotionEnvironmentBound = "environment-bound"
	// promotionOutranked: a later-folding pack's config-overlay sets the same key, so the
	// promoted value would lose at the destination (§5.4's "wins after" half).
	promotionOutranked = "outranked"
	// promotionNoHostLayer: `--to host` on a surface that has no host layer, which is
	// [OQ-CO10]'s refusal. Only the two agent-settings surfaces that declare `readsHost`
	// have one, so for every other surface a host promotion is a host-only edit no jail
	// would ever read — §5.1's third fact, made visible per surface.
	promotionNoHostLayer = "no-host-layer"
	// promotionNoPackOwner: the surface has no owner among the loaded packs, so a
	// config-overlay naming it would be INERT — packoverlay's ruling R2 reports such an
	// overlay and ignores it. MEASURED against the live case: mise/config is CORE's own
	// surface, and this repo's own jail carries a captured `tools` key on it, so the first
	// plausible promotion anyone runs here is one that would have declared a contribution
	// nothing folds. Silent non-delivery is the one outcome this verb may not produce.
	promotionNoPackOwner = "no-pack-owner"
)

// promoteFlagForce is the per-key override for a SENSITIVE refusal. It is not
// promoteFlagAccept's synonym and the two stay separate flags (§5.7): --force overrides a
// REFUSAL, --accept-promotion confirms an INTENDED write. Collapsing them is how --force
// becomes the flag people paste without reading, which for this verb means pasting a
// credential into a pack.
const promoteFlagForce = "--force"

// promoteFlagAccept is the consent flag, and it NAMES WHAT IS APPROVED rather than being a
// generic --yes. That is config.AcceptConfigChangesFlag's ruling ([OQ-CO4]) applied to this
// verb: an approval must say what it approves, and must be a flag rather than an
// environment variable a child process inherits.
const promoteFlagAccept = "--accept-promotion"

// promoteOptions is one parsed invocation.
type promoteOptions struct {
	agent   string
	surface string
	// keys selects specific captured keys; empty means every one the classification finds.
	keys []string
	// dest is "local", "host", or "pack:<name>".
	dest string
	// plan forbids the write outright, whatever else is passed.
	plan bool
	// accept is the consent that lets the write happen.
	accept bool
	// forced are the keys whose SENSITIVE refusal the user overrode by name.
	forced map[string]bool
	// format is outfmt.Text or outfmt.JSON — parsed through the shared front end
	// (outputformat.go), so `--json` and `--format json` both work here exactly as they do
	// at every other reporting verb rather than this command inventing a third spelling.
	format string
}

// parsePromoteArgs parses `promote <agent> [flags]`. rc >= 0 means the caller returns it.
func parsePromoteArgs(args []string, out, errw io.Writer) (promoteOptions, int) {
	o := promoteOptions{dest: promoteDestLocal, forced: map[string]bool{}}
	format, ok := parseOutputFormat("config promote", args, errw)
	if !ok {
		return o, 2
	}
	o.format = format
	needsValue := func(i int, flag string) bool {
		if i+1 >= len(args) {
			fmt.Fprintf(errw, "yolo config promote: %s needs a value\n", flag)
			return false
		}
		return true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if n := outputFormatTokens(args, i); n > 0 {
			i += n - 1
			continue
		}
		switch {
		case isHelpToken(a):
			io.WriteString(out, configUsage+"\n")
			return o, 0
		case a == "--surface":
			if !needsValue(i, a) {
				return o, 2
			}
			i++
			o.surface = args[i]
		case strings.HasPrefix(a, "--surface="):
			o.surface = strings.TrimPrefix(a, "--surface=")
		case a == "--keys":
			if !needsValue(i, a) {
				return o, 2
			}
			i++
			o.keys = append(o.keys, splitPromoteList(args[i])...)
		case strings.HasPrefix(a, "--keys="):
			o.keys = append(o.keys, splitPromoteList(strings.TrimPrefix(a, "--keys="))...)
		case a == "--to":
			if !needsValue(i, a) {
				return o, 2
			}
			i++
			o.dest = args[i]
		case strings.HasPrefix(a, "--to="):
			o.dest = strings.TrimPrefix(a, "--to=")
		case a == promoteFlagForce:
			// NAMED KEYS, ALWAYS. A bare --force would be the paste-without-reading flag
			// §5.7 refuses: the user has to type the key whose credential check they are
			// overriding, so the act is specific to that key and to this run.
			if !needsValue(i, a) {
				fmt.Fprintf(errw, "  %s overrides a refusal for the keys it NAMES "+
					"(e.g. `%s mcp`); there is no blanket form.\n", promoteFlagForce, promoteFlagForce)
				return o, 2
			}
			i++
			for _, k := range splitPromoteList(args[i]) {
				o.forced[k] = true
			}
		case strings.HasPrefix(a, promoteFlagForce+"="):
			for _, k := range splitPromoteList(strings.TrimPrefix(a, promoteFlagForce+"=")) {
				o.forced[k] = true
			}
		case a == "--plan":
			o.plan = true
		case a == promoteFlagAccept:
			o.accept = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo config promote: unknown flag %q\n\n%s\n", a, configUsage)
			return o, 2
		default:
			if o.agent != "" {
				fmt.Fprintf(errw, "yolo config promote: unexpected argument %q (agent already %q)\n",
					a, o.agent)
				return o, 2
			}
			o.agent = a
		}
	}
	if o.agent == "" {
		fmt.Fprintf(errw, "yolo config promote: needs an agent (e.g. 'yolo config promote claude')\n\n%s\n",
			configUsage)
		return o, 2
	}
	if o.plan && o.accept {
		// They are opposite instructions about the same run, and guessing which one the
		// user meant is the one thing a verb that writes to a real home must not do.
		fmt.Fprintf(errw, "yolo config promote: --plan and %s contradict — --plan writes "+
			"nothing by definition. Drop one.\n", promoteFlagAccept)
		return o, 2
	}
	if outfmt.IsJSON(o.format) && !o.plan {
		// The document is the PLAN's wire form ([OQ-RO4]'s split by posture, as `yolo host
		// apply` takes it): the acting posture emits prose and an exit code, not a report a
		// consumer might mistake for a dry run.
		fmt.Fprintf(errw, "yolo config promote: --json is the PLAN's output — add --plan "+
			"(a promotion that writes reports what it wrote, not a document).\n")
		return o, 2
	}
	return o, -1
}

// splitPromoteList splits a comma-separated flag value, dropping empties, so `--keys a,b`
// and a repeated `--keys a --keys b` mean the same thing.
func splitPromoteList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// configPromote implements `yolo config promote`.
func configPromote(args []string, out, errw io.Writer, color bool) int {
	o, rc := parsePromoteArgs(args, out, errw)
	if rc >= 0 {
		return rc
	}
	if refuseInJailPromote(errw) {
		return 1
	}
	dest, drc := resolvePromoteDest(o.dest, errw)
	if drc != 0 {
		return drc
	}
	plan, prc := buildPromotePlan(o, dest, errw)
	if prc != 0 {
		return prc
	}
	if outfmt.IsJSON(o.format) {
		return emitPromoteDoc(plan, out, errw)
	}
	pr := richtext.Printer{W: out, Color: color}
	writePromoteReport(pr, plan)
	if o.plan {
		return 0
	}
	return applyPromotion(plan, o, pr, errw)
}

// refuseInJailPromote is §5.5's boundary: promote is a HOST-side verb, and in a jail it
// refuses and prints the host command. The mirror image of refuseHostSideWrite, which
// refuses host-side capture/reset and points into the jail.
//
// TWO CONDITIONS, and the second is not redundant. surfacesAreLocal() answers "is this the
// jail that owns the workspace", which is the mirror the design names — but it is FALSE in
// a nested jail inspecting some other workspace, and that process still has a jail's
// disposable home standing in for the host's config dir. config.InJail() closes that: a
// promotion's destination is only meaningful in the home where the user's real
// `~/.config/yolo-jail` lives, and no jail has one.
//
// There is a jail-side REQUEST channel designed and deliberately unbuilt ([OQ-CO5]): the
// transport is free (the host reads <workspace>/.yolo/ every launch), and what is not free
// is the consent surface for a write an agent asked for and a human approves. Until it
// exists the refusal names the command a human can run.
func refuseInJailPromote(errw io.Writer) bool {
	if !surfacesAreLocal() && !config.InJail() {
		return false
	}
	fmt.Fprintf(errw, "yolo config promote: refusing — promote is a HOST-side verb. Its "+
		"destinations live under the host's ~/.config/yolo-jail/, which no jail may write: a "+
		"pack manifest is an input to composition, so a jail that could edit one could grant "+
		"itself a host file on the next boot.\n"+
		"  Run it on the host, against this workspace:\n"+
		"    yolo config promote <agent> --plan\n"+
		"  `yolo config diff <agent>` in here shows the same captured keys.\n")
	return true
}

// promotePlan is one invocation's whole answer: what would be written, where, and what
// would not be — with a reason per key.
type promotePlan struct {
	Agent string
	Dest  promoteDest
	// Surfaces carries one entry per capture surface INSPECTED, including those whose
	// overlay held nothing: "no captures here" is an answer, and §5.6 requires it be said
	// rather than left as silence.
	Surfaces []promoteSurface
	// Unresolved names configured packs this run could not read (a git pack needing `yolo
	// pack install`). A pack it could not read might be the one whose config-overlay
	// outranks the destination, so a precedence answer computed without it is incomplete
	// and says so.
	Unresolved []string
}

// promoteSurface is one surface's classified keys.
type promoteSurface struct {
	Surface manifest.Surface
	// OverlayJSON is the capture sidecar's bytes, carried so the writer resets from the
	// exact bytes the classification read (configpromotewrite.go's pre-image contract).
	OverlayJSON []byte
	Keys        []promoteKey
	// Note is a caveat about what this classification could not see, or "".
	Note string
}

// promoteKey is one captured key and what promote decided about it.
type promoteKey struct {
	Key         string
	Disposition string
	// Reason is the sentence the report prints and the document carries. It never contains
	// a captured VALUE (see the file header); a key path is not a value.
	Reason string
	// Forced records that a sensitive key was promoted because --force named it, so the
	// report and the document both say the override happened rather than showing a clean
	// promotion (§5.3).
	Forced bool
	// value is the captured value the writer declares. Lower-case: it is NOT part of any
	// view, and the document type has no field for it.
	value any
}

// promotable reports whether this key would be written.
func (k promoteKey) promotable() bool { return k.Disposition == promotionPromotable }

// buildPromotePlan runs §5.2 steps 1-4: read the overlay, drop redundant and dead keys,
// classify the rest, and check each survivor still wins at the destination. It writes
// nothing. rc != 0 means a MISUSE the caller returns (a named key nothing captured, an
// agent with no capture surfaces).
func buildPromotePlan(o promoteOptions, dest promoteDest, errw io.Writer) (promotePlan, int) {
	plan := promotePlan{Agent: o.agent, Dest: dest}
	if o.agent == "user" {
		// The `user` pseudo-agent's surfaces are host_files slugs, not surface IDENTITIES,
		// and a config-overlay contributes to an "agent/name" a pack owns. There is nothing
		// for a promotion to target, so this refuses instead of inventing an identity.
		fmt.Fprintf(errw, "yolo config promote: `user` surfaces come from the host_files "+
			"config key, not from a pack, so they have no surface identity for a "+
			"config-overlay to name. Move the key in the host file itself, or declare it in "+
			"a pack that owns a surface.\n")
		return plan, 2
	}
	surfaces := capturedSurfaces(o.agent, o.surface)
	if len(surfaces) == 0 {
		fmt.Fprintf(errw, "yolo config promote: no capture surfaces for agent %q%s%s\n",
			o.agent, surfaceSuffix(o.surface), promoteNonCaptureHint(o.agent, o.surface))
		return plan, 2
	}
	fold, unresolved := loadPromoteFold()
	plan.Unresolved = unresolved

	selected := map[string]bool{}
	for _, k := range o.keys {
		selected[k] = true
	}
	seen := map[string]bool{}
	for _, s := range surfaces {
		ps := classifyPromoteSurface(s, o, dest, fold)
		for _, k := range ps.Keys {
			seen[k.Key] = true
		}
		if len(selected) > 0 {
			// A surface where the filter selected nothing is DROPPED rather than reported
			// empty: "no captured keys here" would be a false statement about a surface that
			// has captures the user did not name. A key that matched no surface at all is
			// caught below, so nothing selected goes unreported.
			if ps.Keys = filterPromoteKeys(ps.Keys, selected); len(ps.Keys) == 0 {
				continue
			}
		}
		plan.Surfaces = append(plan.Surfaces, ps)
	}
	// §5.6: `--keys` naming a key nothing captured is an ERROR, not an empty promotion. A
	// user who misspells a key must not be told "nothing to do" — that reads as "already
	// promoted".
	var missing []string
	for _, k := range o.keys {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		fmt.Fprintf(errw, "yolo config promote: --keys names %s that %s not captured for %s%s: %s\n",
			plural(len(missing), "a key", "keys"), plural(len(missing), "is", "are"),
			o.agent, surfaceSuffix(o.surface), strings.Join(missing, ", "))
		return plan, 2
	}
	return plan, 0
}

// filterPromoteKeys keeps only the keys `--keys` named.
func filterPromoteKeys(keys []promoteKey, selected map[string]bool) []promoteKey {
	var out []promoteKey
	for _, k := range keys {
		if selected[k.Key] {
			out = append(out, k)
		}
	}
	return out
}

// promoteNonCaptureHint names the §5.6 reason when the surface the user asked for EXISTS
// but keeps no overlay — `rmw`, `computed` and `unrendered` surfaces write no sidecar, so
// there is nothing to promote and "no capture surfaces" alone would read as a typo.
func promoteNonCaptureHint(agent, surface string) string {
	if surface == "" {
		return ""
	}
	s, ok := surfaceManifest().Lookup(agent, surface)
	if !ok {
		return ""
	}
	return fmt.Sprintf(" — %s/%s is a `%s` surface, which records no captured edits (only a "+
		"whole-file `capture` surface has an overlay to promote out of)", agent, surface, surfaceMode(s))
}

// classifyPromoteSurface is §5.2 steps 1-4 for ONE surface.
func classifyPromoteSurface(s manifest.Surface, o promoteOptions, dest promoteDest, fold promoteFold) promoteSurface {
	ps := promoteSurface{Surface: s}
	ps.OverlayJSON, _ = os.ReadFile(prismOverlayPath(s.Agent, s.Name))

	// The DISPLAY reader (jsonx, key order and integer literals preserved) is the one the
	// captured values are taken from, because those values are written back into a
	// hand-readable pack.json — encoding/json would turn a port number into a float.
	// agentcfg's own reader is used where the ENGINE's answer is wanted (dead keys).
	overlay := readOverlayValue(s.Agent, s.Name)
	states, isObject := overlayKeyStates(overlay, readLastRenderKeys(s))
	if !isObject {
		if !overlayIsEmpty(overlay) {
			ps.Note = "keyless surface (" + s.Codec + "): the whole file is one captured " +
				"value, so there is no key to declare — `yolo config reset` is the only exit"
		}
		return ps
	}
	m, _ := overlay.(*jsonx.OrderedMap)

	// THE DESTINATION CHECKS ARE PER SURFACE, so they are asked once and answered on every
	// key rather than being rediscovered per key. Both are facts about whether this surface
	// can receive this KIND of destination at all, not about any particular key: a surface
	// no loaded pack declares cannot receive a config-overlay (ruling R2), and a surface
	// with no host layer cannot receive a `--to host` promotion ([OQ-CO10]).
	//
	// HasHostLayer is the surface's own `readsHost` declaration
	// (manifest.Surface.ReadsHost). It was `HostSource != ""` — a /ctx path populated from a
	// `reads-host` contribution through a basename match — when this check was written, and
	// [OQ-CO10]'s restructure landed on 2026-09-12; promote needed no change, which is the
	// evidence that binding to the predicate rather than to its implementation was right.
	unowned := !dest.host && !fold.ownedByPack(s)
	noHostLayer := dest.host && !s.HasHostLayer()

	// The ENGINE's own narrowing, against today's declarations rather than the last boot's
	// (agentcfg.DeadOverlayKeys). The computed layer is NOT available host-side — it is
	// built from jail-absolute paths, which is why `config render` omits it too — so what
	// is asked here is the managed half; the last boot's own narrowing covered computed for
	// every key that predates it.
	dead := map[string]bool{}
	for _, k := range agentcfg.DeadOverlayKeys(s.Kind(), ps.OverlayJSON, nil, s.Managed) {
		dead[k] = true
	}
	if surfaceHasComputedLayer(s) {
		ps.Note = "this surface also has a `computed` layer, which is built from jail paths " +
			"and cannot be read host-side — a key a NEW derive started computing since the " +
			"last boot would not be reported dead here"
	}

	for _, st := range states {
		v, _ := m.Get(st.Key)
		if noHostLayer {
			ps.Keys = append(ps.Keys, promoteKey{
				Key: st.Key, Disposition: promotionNoHostLayer, value: v,
				Reason: fmt.Sprintf("%s/%s has no host layer — its surface does not declare "+
					"`readsHost`, so a key written into its real-home file would be read by no "+
					"jail. `--to local` reaches every jail and the host", s.Agent, s.Name),
			})
			continue
		}
		if unowned {
			ps.Keys = append(ps.Keys, promoteKey{
				Key: st.Key, Disposition: promotionNoPackOwner, value: v,
				Reason: fmt.Sprintf("no selected pack owns %s/%s, so a config-overlay naming "+
					"it would be inert — it is %s", s.Agent, s.Name, unownedSurfaceReason(s)),
			})
			continue
		}
		ps.Keys = append(ps.Keys, classifyPromoteKey(s, st, v, dead[st.Key], o, dest, fold))
	}
	return ps
}

// unownedSurfaceReason distinguishes the two ways a surface ends up with no pack owner,
// because the remedies are opposite: a CORE surface can never have one (nothing to select),
// while a pack surface just needs its pack in `packs`.
func unownedSurfaceReason(s manifest.Surface) string {
	if _, core := agentcfg.BuiltinManifest().Lookup(s.Agent, s.Name); core {
		return "one of yolo's OWN surfaces rather than a pack's, and config-overlay " +
			"contributes to a surface a pack owns"
	}
	return "declared by a pack that is not in `packs` — select that pack and re-run"
}

// classifyPromoteKey decides ONE key. The order is §5.2's and it is load-bearing: the free
// mechanical drops come first, so a redundant key is never reported as a credential and a
// dead key is never reported as losing precedence — each key gets the reason that actually
// stopped it.
func classifyPromoteKey(s manifest.Surface, st overlayKeyState, value any, dead bool,
	o promoteOptions, dest promoteDest, fold promoteFold,
) promoteKey {
	k := promoteKey{Key: st.Key, value: value}
	switch {
	case st.Redundant:
		k.Disposition = promotionRedundant
		k.Reason = "identical to yolo's own last render — the layers already produce it"
		return k
	case dead:
		k.Disposition = promotionDead
		k.Reason = "the surface's `managed` layer overrides it where it already sits, and a " +
			"promotion moves it lower in the fold"
		return k
	}
	if path, marker := environmentBoundPath(st.Key, value); path != "" {
		k.Disposition = promotionEnvironmentBound
		k.Reason = fmt.Sprintf("%s holds %s — a value that is an artifact of the jail it was "+
			"set in, not a preference that means the same thing elsewhere", path, marker)
		return k
	}
	if path := sensitiveKeyPath(st.Key, value); path != "" {
		if !o.forced[st.Key] {
			k.Disposition = promotionSensitive
			k.Reason = fmt.Sprintf("%s is named like a credential; promote refuses rather than "+
				"redacting. If it is not one, re-run with `%s %s`", path, promoteFlagForce, st.Key)
			return k
		}
		k.Forced = true
	}
	if winner := fold.outrankedBy(s, st.Key, dest); winner != "" {
		k.Disposition = promotionOutranked
		k.Reason = fmt.Sprintf("the `%s` pack's config-overlay sets this key and folds AFTER "+
			"%s, so the promoted value would lose", winner, dest.label())
		return k
	}
	k.Disposition = promotionPromotable
	k.Reason = promotableReason(st, dest)
	return k
}

// promotableReason is what a promotable key's line says. It carries the JUDGEMENT §5.3
// leaves to a human — mechanically this key is fine, and whether it BELONGS in a pack
// shared with a team is not a question any heuristic should pretend to answer.
func promotableReason(st overlayKeyState, dest promoteDest) string {
	if dest.host {
		// The host destination writes the KEYS THEMSELVES into the surface's own real-home
		// file — there is no pack and no declaration in the loop (§5.1 fact 1), so saying
		// "declared" would describe the wrong mechanism.
		return "would be written into the surface's own file in your real home"
	}
	if st.Deleted {
		return "a captured DELETION — declaring it in " + dest.label() + " deletes the key " +
			"wherever that pack renders"
	}
	return "would be declared in " + dest.label()
}

// promoteFold is the pack fold order this invocation reasons about, plus the config-overlay
// contributions resolved onto each surface.
//
// ONE NOTCH, and it is the JAIL's: the captures being promoted are a jail's, the sidecar
// was narrowed by a jail's boot, and the destination that matters (`local`) renders at every
// notch anyway. Describing the fold at the host notch instead would answer a question about
// a different posture than the one that produced the captures.
type promoteFold struct {
	// order maps a pack name to its position in the fold; later wins.
	order map[string]int
	// afterConfigured is the position a destination pack that is not currently in the fold
	// would take — the conventional local pack's slot, which is after every configured entry
	// and before anything `needs` appended.
	afterConfigured int
	// packs is the fold in order, kept so the owner question is asked of the same set the
	// overlays were collected over.
	packs []*packload.Pack
	set   *packoverlay.OverlaySet
}

// loadPromoteFold resolves the pack fold order the way a launch does, plus the names of any
// configured pack it could not read.
//
// The NEEDS CLOSURE is included, and it is the whole reason this is not just
// configuredPacksForInspection: `config/packs.go` appends the conventional local pack LAST,
// which is what makes `--to local` outrank every other pack's overlay — with one exception,
// a pack pulled in through `needs`, which run/packs.go appends AFTER the closure runs. No
// shipped pack in that position declares a config-overlay today, so this changes no answer
// yet; leaving it out would make the check silently wrong on the day one does.
func loadPromoteFold() (promoteFold, []string) {
	packs, unresolved := configuredPacksForInspection()
	fold := promoteFold{order: map[string]int{}, afterConfigured: len(packs)}
	byName := map[string]*packload.Pack{}
	for _, p := range packload.Embedded() {
		byName[p.Name] = p
	}
	added, _, err := packload.ResolveNeeds(packs, func(name string) (*packload.Pack, bool) {
		p, ok := byName[name]
		return p, ok
	})
	if err == nil {
		packs = append(packs, added...)
	}
	for i, p := range packs {
		fold.order[p.Name] = i
	}
	fold.packs = packs
	notch := render.KindJail
	fold.set = packoverlay.Collect(packs, render.ProfileFor(notch).AgentAutonomy,
		overlayGateProfiles(notch))
	return fold, unresolved
}

// ownedByPack reports whether some loaded pack declares this surface. A config-overlay can
// only contribute to a surface a pack OWNS (packoverlay.Collect's owner pass), so this is
// the precondition on the destination side of every promotion.
//
// It reads the LOADED packs through the same helper `config diff` uses, not the embedded
// manifest: a third-party pack is a legitimate owner, and the embedded set would report a
// user's own pack's surface as ownerless.
func (f promoteFold) ownedByPack(s manifest.Surface) bool {
	return len(packSurfacesForAgent(f.packs, s.Agent, s.Name)) > 0
}

// outrankedBy names the pack whose config-overlay would beat this key once it is declared
// at dest, or "" when the promotion wins.
//
// This is §5.4's "wins AFTER" half; the "wins now" half is the dead check above, which is
// the same question asked of the layers that outrank the capture overlay itself. Both are
// pure functions of the layer set, and a key that fails either is refused BY NAME with the
// winning layer rather than promoted with a warning (§5.4).
func (f promoteFold) outrankedBy(s manifest.Surface, key string, dest promoteDest) string {
	destIdx, known := f.order[dest.pack]
	switch {
	case dest.host:
		// The host layer is SECOND from the bottom of the stack (§2.1) — under workspace,
		// every config-overlay, capture, computed and managed — so EVERY pack's overlay on
		// the key outranks a key written into the real-home file. Spelled as a position
		// before the fold rather than as a special case in the loop, so the one comparison
		// below answers for all three destinations.
		destIdx = -1
	case !known:
		// A destination pack that is not in the fold yet: the conventional local pack,
		// created by this very promotion. It lands where LoadPacks appends it.
		destIdx = f.afterConfigured
	}
	for _, ov := range f.set.For(s.Agent, s.Name) {
		m, isObject := ov.Data.(map[string]any)
		if !isObject {
			continue
		}
		if _, sets := m[key]; !sets {
			continue
		}
		if idx, ok := f.order[ov.Pack]; ok && idx > destIdx {
			return ov.Pack
		}
	}
	return ""
}

// writePromoteReport prints the human view: one block per surface, one line per key.
func writePromoteReport(pr richtext.Printer, plan promotePlan) {
	if len(plan.Unresolved) > 0 {
		pr.Printf("[yellow]⚠ not inspected (fetched packs need `yolo pack install`): %s — a "+
			"config-overlay they declare could outrank this promotion.[/yellow]",
			strings.Join(plan.Unresolved, ", "))
	}
	for _, ps := range plan.Surfaces {
		pr.Printf("[bold]# %s/%s → %s[/bold]", ps.Surface.Agent, ps.Surface.Name,
			surfacePathOrSidecar(ps.Surface))
		if len(ps.Keys) == 0 {
			pr.Printf("  [dim]no captured keys here[/dim]")
		}
		width := 0
		for _, k := range ps.Keys {
			if len(k.Key) > width {
				width = len(k.Key)
			}
		}
		for _, k := range ps.Keys {
			pad := strings.Repeat(" ", width-len(k.Key))
			forced := ""
			if k.Forced {
				forced = " [yellow](sensitive, overridden by " + promoteFlagForce + ")[/yellow]"
			}
			pr.Printf("  [magenta]%s[/magenta]%s  %s  [dim]%s[/dim]%s",
				k.Key, pad, promoteDispositionTag(k.Disposition), k.Reason, forced)
		}
		if ps.Note != "" {
			pr.Printf("  [dim]note: %s[/dim]", ps.Note)
		}
		pr.Printf("")
	}
}

// promoteDispositionTag colours one disposition token — promotable green, the refusals
// yellow, the free drops dim — and pads it so the reasons line up in a column.
//
// The TOKEN is printed, not a prettified phrase, so the text report and the JSON document
// name each outcome the same way and a reader who has seen one view can search the other.
func promoteDispositionTag(d string) string {
	if pad := len(promotionEnvironmentBound) - len(d); pad > 0 {
		d += strings.Repeat(" ", pad)
	}
	switch strings.TrimSpace(d) {
	case promotionPromotable:
		return "[green]" + d + "[/green]"
	case promotionRedundant, promotionDead:
		return "[dim]" + d + "[/dim]"
	default:
		return "[yellow]" + d + "[/yellow]"
	}
}

// promotePlanDoc is the `--plan --json` document: the classified key list, as data.
//
// NO VALUES, in any field — see the file header. The document carries key NAMES, the
// disposition token, and the reason sentence (which may name a nested key PATH, since that
// is what makes a sensitive refusal actionable).
type promotePlanDoc struct {
	Version string `json:"version"`
	// Posture is always "plan": the acting form emits prose, so a consumer that has a
	// document is holding a dry run and this says so without it having to remember.
	Posture     string `json:"posture"`
	Agent       string `json:"agent"`
	Destination string `json:"destination"`
	// DestinationPath is the file a promotion would write.
	DestinationPath string `json:"destination_path"`
	// Unresolved names configured packs that could not be read, so a consumer knows the
	// precedence answer below was computed over an incomplete fold.
	Unresolved []string            `json:"unresolved_packs,omitempty"`
	Counts     promotePlanCounts   `json:"counts"`
	Surfaces   []promotePlanDocSfc `json:"surfaces"`
}

// promotePlanCounts is the one-glance tally: how many keys would move, and how many are
// held back.
type promotePlanCounts struct {
	Promotable int `json:"promotable"`
	Held       int `json:"held"`
}

// promotePlanDocSfc is one surface in the document.
type promotePlanDocSfc struct {
	Surface string              `json:"surface"`
	Path    string              `json:"path"`
	Note    string              `json:"note,omitempty"`
	Keys    []promotePlanDocKey `json:"keys"`
}

// promotePlanDocKey is one classified key.
type promotePlanDocKey struct {
	Key         string `json:"key"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
	Forced      bool   `json:"forced,omitempty"`
}

// buildPromotePlanDoc projects the plan into its wire form. A projection of the SAME
// classification the text report prints, never a second pass — the two views differ in
// shape and can never differ in verdict.
func buildPromotePlanDoc(plan promotePlan) promotePlanDoc {
	doc := promotePlanDoc{
		Version:         version.Get(""),
		Posture:         "plan",
		Agent:           plan.Agent,
		Destination:     plan.Dest.label(),
		DestinationPath: plan.Dest.path,
		Unresolved:      plan.Unresolved,
		Surfaces:        []promotePlanDocSfc{},
	}
	for _, ps := range plan.Surfaces {
		sfc := promotePlanDocSfc{
			Surface: ps.Surface.Agent + "/" + ps.Surface.Name,
			Path:    surfacePathOrSidecar(ps.Surface),
			Note:    ps.Note,
			Keys:    []promotePlanDocKey{},
		}
		for _, k := range ps.Keys {
			if k.promotable() {
				doc.Counts.Promotable++
			} else {
				doc.Counts.Held++
			}
			sfc.Keys = append(sfc.Keys, promotePlanDocKey{
				Key: k.Key, Disposition: k.Disposition, Reason: k.Reason, Forced: k.Forced,
			})
		}
		doc.Surfaces = append(doc.Surfaces, sfc)
	}
	return doc
}

// emitPromoteDoc writes the plan document to out.
func emitPromoteDoc(plan promotePlan, out, errw io.Writer) int {
	enc, err := json.MarshalIndent(buildPromotePlanDoc(plan), "", "  ")
	if err != nil {
		fmt.Fprintf(errw, "yolo config promote: encoding the plan failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, string(enc))
	return 0
}
