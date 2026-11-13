package packload

// unselectedclaims.go reads which names the providers of the packs yolo SHIPS, and this launch did
// not select, claim — to WORD a line, never to decide a reach (docs/reference/providers.md#the-two-env-sets-public-and-private
// SE-D8). A public name such a provider claims gets one line naming `yolo config secret`; a private
// name nothing selected claims gets `yolo pack add <pack>` as its remedy. A release that ships a new
// pack can therefore add a line to a working launch and never changes who receives a value.
//
// It reads the embedded set for the reason UnselectedAdaptations does (protocolresolution.go): the
// question is what is true of everything yolo ships, and the remedy names the pack to add. Only a
// shipped pack can be named; a third-party pack the user has not selected is invisible here, which
// bounds the wording and never the rule.

import "sort"

// UnselectedProviderClaims maps each name a provider of an unselected shipped pack claims to those
// providers and their packs, sorted by provider then pack. One ComposeProvidersAt per pack, so each
// claim is the pack's own (a same-platform sibling's inherited names included, as the gate reads
// them). A pack whose providers cannot compose alone words nothing. Nil when no unselected shipped
// pack claims a name.
func UnselectedProviderClaims(selected []*Pack) map[string][]ShippedClaim {
	var out map[string][]ShippedClaim
	for _, p := range unselectedEmbedded(selected) {
		if p == nil || p.Decl == nil {
			continue
		}
		table, _, err := ComposeProvidersAt(nil, []*Pack{p}, nil, ServedDaemons{})
		if err != nil || table == nil || table.Len() == 0 {
			continue
		}
		for name, providers := range credentialClaims(table) {
			for _, provider := range providers {
				if out == nil {
					out = map[string][]ShippedClaim{}
				}
				out[name] = append(out[name], ShippedClaim{Provider: provider, Pack: p.Name})
			}
		}
	}
	for name := range out {
		claims := out[name]
		sort.Slice(claims, func(i, j int) bool {
			if claims[i].Provider != claims[j].Provider {
				return claims[i].Provider < claims[j].Provider
			}
			return claims[i].Pack < claims[j].Pack
		})
	}
	return out
}
