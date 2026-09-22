package check

// protocols.go predicts the launch's PROTOCOL-PAIRING gate.
//
// The gate is `packload.AgentEnv`'s: a profiled agent pointed at a provider whose
// endpoints it cannot speak, with no selected pack declaring an adapter between them,
// refuses the launch (docs/reference/protocol-resolution.md). It shipped there and only
// there, so a config `yolo check` called clean was still refused at launch — the one thing
// `check` exists to prevent, and the same defect capabilities.go was written to fix for the
// capability gate, one feature later.
//
// ⚠ THIS IS NOT A SECOND COPY, AND THE DIFFERENCE FROM capabilities.go IS THE POINT.
// That file restates the launch's capability census because reaching the real one would
// mean exporting a method on `run.Options` and dragging its printer in; its ⚠ records what
// the copy costs. Nothing of the kind applies here — the gate is a free function over
// inputs a caller can assemble — so this file calls `packload.PairingRefusals`, which is
// the gate AgentEnv applies, through the same owner lookup and the same resolver. Two
// copies of a pairing rule could disagree; there is only one.
//
// WHAT IS DUPLICATED IS THE INPUT ASSEMBLY, and that is the honest residue: composing the
// providers and resolving the profiles the way the launch does. Get one of those wrong and
// the prediction is wrong in a way no shared function can catch.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// protocolPairingGap reports what the Packs block should say about the pairing gate, as
// (errors, warnings) in this section's own vocabulary.
//
// Three outcomes, mirroring capabilityGap's shape because the two predict gates of the same
// severity and a reader should not have to learn two report vocabularies:
//
//   - Every pairing resolves → NOTHING, not a PASS line. It matches the launch, which never
//     announces a gate it did not trip, and it keeps the golden that pins section ordering
//     and the pass/warn/fail counts from needing a bump for a line saying nothing happened.
//   - A pairing is refused → a FAIL carrying the launch's own refusal text verbatim, so the
//     prediction and the thing predicted cannot word the same problem differently. The
//     launch refusal is fatal, and a prediction of it that exited 0 would be the defect this
//     file exists to close.
//   - The inputs would not assemble → a WARN naming why, and NEVER silence. "I could not
//     look" reported as a pass is the defect OQ-3 ruled against in
//     docs/design/broker-ca-and-nested-hosts.md; the same rule binds here.
//
// ⚠ IT CANNOT SEE `-p`. `check` reads configuration; a `-p <name>` is an argument to a
// launch that has not happened, and `effectiveUseProfiles` folds it in ABOVE this. So a
// clean prediction means "the `use_profiles` selection pairs", never "any launch from this
// config pairs". Narrowing that needs the flag, not a wider census — and widening the census
// here to guess at flags would make the prediction wrong in places the launch is not, which
// is capabilities.go's ⚠ with the sign flipped.
//
// configWarn receives the two config loaders' own findings. They are GRADED and COUNTED
// rather than printed beside the verdict, because a finding nothing counts is the
// two-channel defect docs/design/reference-mismatch-diagnostics.md §3 names — and
// TestEveryConfigWarnSinkIsGraded is what noticed the first draft of this file discarding
// both.
func protocolPairingGap(packs []*packload.Pack, merged *jsonx.OrderedMap,
	configWarn func(string)) (errs []string, warns []string) {
	profiles := packload.ProfileTable(subMap(merged, "use_profiles"))
	if len(profiles) == 0 || len(packs) == 0 {
		return nil, nil
	}

	// The adapter overrides come from the USER FILE DIRECTLY, which is where the launch
	// reads them (config/adapters.go: workspace scope is inexpressible for a key that
	// decides where inference goes). A read problem degrades to the packs' declared
	// addresses there, so it degrades to them here too rather than changing the verdict.
	addresses, _ := config.LoadAdapterAddresses(configWarn)
	providers, err := packload.ComposeProviders(subMap(merged, "providers"), packs,
		packload.WithAdapterAddresses(addresses))
	if err != nil {
		return nil, []string{"Could not predict the protocol-pairing gate: the provider " +
			"table did not compose (" + err.Error() + "). The launch will report this " +
			"problem first; the pairing is unchecked until it is fixed"}
	}

	userProfiles, err := config.LoadProfiles(configWarn)
	if err != nil {
		return nil, []string{"Could not predict the protocol-pairing gate: the user " +
			"profile declarations did not load (" + err.Error() + "). The pairing is " +
			"unchecked"}
	}
	resolved, err := packload.ResolveProfiles(packs, userProfiles, providers)
	if err != nil {
		return nil, []string{"Could not predict the protocol-pairing gate: the profiles " +
			"did not resolve (" + err.Error() + "). The launch will report this problem " +
			"first; the pairing is unchecked until it is fixed"}
	}

	for _, refusal := range packload.PairingRefusals(packs, providers, resolved, profiles) {
		errs = append(errs, "This launch will be REFUSED: "+
			strings.TrimSuffix(refusal.Error(), "\n"))
	}
	return errs, nil
}
