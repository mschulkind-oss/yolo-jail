package packload

import "github.com/mschulkind-oss/yolo-jail/internal/packdecl"

// intercepts.go collects the `intercept` contributions of the selected packs
// (packdecl.KindIntercept): a command name the jail's block dir routes to a pack-declared
// forwarder. Core renders the name and the argv and knows neither.

// Intercept is one pack-declared intercept, in the shape the entrypoint's shim writer
// consumes.
type Intercept struct {
	// Name is the command intercepted — the FILE written into the block dir.
	Name string
	// Forward is the argv the shim execs, with the caller's arguments appended.
	Forward []string
	// Pack is the declaring pack, for the shim's comment and the boot's warnings.
	Pack string
}

// Intercepts returns every `intercept` contribution across packs, in pack order. Two
// packs intercepting one name is a collision the footprint reports (the kind is
// CombineExclusive); the writer keeps the first and warns.
func Intercepts(packs []*Pack) []Intercept {
	var out []Intercept
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindIntercept || c.Bin == "" || len(c.Forward) == 0 {
				continue
			}
			out = append(out, Intercept{Name: c.Bin, Forward: append([]string(nil), c.Forward...), Pack: p.Name})
		}
	}
	return out
}
