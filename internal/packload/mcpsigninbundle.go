package packload

import (
	"fmt"
	"path"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// SignInBundleInventory pairs selected declaration provenance with original
// content. Only the later delivered-tree reader can establish a native point.
type SignInBundleInventory struct {
	Declaration packdecl.MCPSignInBundle
	Inventory   mcpsignin.Inventory
}

func validateSignInBundles(name string, decl *packdecl.Manifest, reader mcpsignin.FileReader) []string {
	var problems []string
	for _, b := range decl.SignInBundles() {
		label := fmt.Sprintf("pack %s mcp_sign_in_bundle %s", name, b.Bundle)
		if _, err := reader.ReadFile(b.License); err != nil {
			problems = append(problems, label+": retained license unavailable: "+err.Error()+"; restore the license or remove the declaration")
		}
		// Reading the manifest also checks every component of the bundle path.
		if _, err := reader.ReadFile(path.Join(b.Bundle, ".claude-plugin/plugin.json")); err != nil {
			problems = append(problems, label+": bundle unavailable: "+err.Error()+"; restore the bundle or remove the declaration")
		}
	}
	return problems
}

// ReadableSignInBundles uses the same selected view as declaration validation.
// It neither resolves an unselected source nor materializes an embedded tree.
func ReadableSignInBundles(p *Pack) ([]SignInBundleInventory, error) {
	if p == nil || p.Decl == nil {
		return nil, fmt.Errorf("sign-in inventory requires a selected declaration")
	}
	var reader mcpsignin.FileReader
	if binding := p.embeddedMetadata; binding != nil {
		identity, err := declarationIdentity(p.Decl)
		if err != nil || binding.selectedFS == nil || binding.loadedDecl != p.Decl || binding.selectedName != p.Name || binding.selectedRoot != p.Root || identity != binding.loadedIdentity {
			return nil, fmt.Errorf("pack %s selected sign-in metadata no longer matches its declaration", p.Name)
		}
		reader = mcpsignin.FSReader{FS: binding.selectedFS}
	} else {
		disk, err := mcpsignin.OpenDisk(p.Root)
		if err != nil {
			return nil, err
		}
		defer disk.Close()
		reader = disk
	}
	var out []SignInBundleInventory
	for _, b := range p.Decl.SignInBundles() {
		out = append(out, SignInBundleInventory{Declaration: b, Inventory: mcpsignin.ReadInventory(reader, b.Bundle, b.Format)})
	}
	return out, nil
}
