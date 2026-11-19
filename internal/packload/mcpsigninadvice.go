package packload

import (
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

type ProvisioningCandidate struct {
	Pack     string `json:"pack"`
	Origin   string `json:"origin"`
	Revision string `json:"revision"`
}

// ProvisioningCandidates reads retained originals only. Unknown provenance
// fails closed; selected or shadowed packs are excluded before any bundle read.
func ProvisioningCandidates(originals fs.FS, server mcpsignin.Once, context *mcpsignin.AdviceContext) []ProvisioningCandidate {
	out := []ProvisioningCandidate{}
	if originals == nil || context == nil || !context.Valid() || !context.MappingReadable || len(context.Formats) == 0 {
		return out
	}
	names, err := EmbeddedNamesFromFS(originals)
	if err != nil {
		return out
	}
	for _, name := range names {
		if slices.Contains(context.SelectedEmbedded, name) || slices.Contains(context.ShadowNames, name) {
			continue
		}
		pack, problems, err := ReadSelectedPackFS(originals, name, path.Join("/embedded-originals", name), name, nil, nil)
		if err != nil || len(problems) > 0 || pack == nil || pack.Decl == nil {
			continue
		}
		inventories, err := ReadableSignInBundles(pack)
		if err != nil {
			continue
		}
		for _, bundle := range inventories {
			if !slices.Contains(context.Formats, bundle.Declaration.Format) || len(bundle.Inventory.Problems) > 0 {
				continue
			}
			for _, entry := range bundle.Inventory.Servers {
				transport := server.Transport
				if transport == "" {
					transport = "http"
				}
				if entry.Reason == "" && entry.URL == server.URL && entry.Transport == transport && (server.ExpectedClientID == "" || entry.ClientID == server.ExpectedClientID) {
					out = append(out, ProvisioningCandidate{Pack: name, Origin: bundle.Declaration.Origin, Revision: bundle.Declaration.Revision})
					break
				}
			}
			if len(out) > 0 && out[len(out)-1].Pack == name {
				break
			}
		}
	}
	slices.SortFunc(out, func(a, b ProvisioningCandidate) int { return strings.Compare(a.Pack, b.Pack) })
	return out
}

func ProvisioningRecovery(server mcpsignin.Once, candidates []ProvisioningCandidate) string {
	own := "yolo config set mcp_servers." + server.Name + ".oauth.client_id <id> --user"
	if len(candidates) == 0 {
		return mcpsignin.Diagnose(mcpsignin.DiagnosisInput{Server: server.Name, URL: server.URL, Once: true}).NextStep
	}
	commands := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		commands = append(commands, "yolo pack add "+candidate.Pack)
	}
	line := server.Name + ": no agent here can sign in. Run: " + strings.Join(commands, "; ") + ", then relaunch"
	if len(candidates) > 1 {
		line += "; any one serves"
	}
	return line + "; or set your own client: " + own
}
