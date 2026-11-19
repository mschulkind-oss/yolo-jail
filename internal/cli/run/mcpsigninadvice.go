package run

import (
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// MCPAdviceContext freezes parsed user mappings and effective verified selected
// declarations only. It never resolves or reads a custom source.
func MCPAdviceContext(user *jsonx.OrderedMap, packs []*packload.Pack, userPacks []string) *mcpsignin.AdviceContext {
	context := &mcpsignin.AdviceContext{Version: 1}
	if user == nil {
		return context
	}
	entries, problems := config.ReadOnlyUserPackEntries(user)
	if len(problems) > 0 {
		return context
	}
	context.MappingReadable = true
	rawMappings, _ := user.Get("pack_sources")
	mappings, err := packsrc.ParseSourceDefinitions(rawMappings)
	if err != nil {
		context.MappingReadable = false
		return context
	}
	for name := range mappings {
		context.ShadowNames = append(context.ShadowNames, name)
	}
	slices.Sort(context.ShadowNames)
	for _, entry := range entries {
		if entry.EmbeddedName != "" {
			if slices.Contains(userPacks, entry.Name) {
				context.SelectedEmbedded = append(context.SelectedEmbedded, entry.EmbeddedName)
			}
		} else {
			context.ShadowNames = append(context.ShadowNames, entry.Name)
		}
	}
	for _, pack := range packs {
		if pack == nil || pack.Decl == nil || !slices.Contains(userPacks, pack.Name) {
			continue
		}
		// Implicit shipped dependencies are selected too, but only a verified
		// official declaration can identify a canonical shipped name here.
		if pack.Official && !slices.Contains(context.SelectedEmbedded, pack.Name) {
			context.SelectedEmbedded = append(context.SelectedEmbedded, pack.Name)
		}
		loadable := false
		for _, contribution := range pack.Decl.Contributions() {
			if contribution.Kind == packdecl.KindSkills && contribution.Into != "" && contribution.Agent != "" {
				loadable = true
			}
		}
		if !loadable {
			continue
		}
		for _, format := range pack.Decl.MCPSignInFormats {
			if !slices.Contains(context.Formats, format) {
				context.Formats = append(context.Formats, format)
			}
		}
	}
	if !context.Valid() {
		return &mcpsignin.AdviceContext{Version: 1}
	}
	return context
}
