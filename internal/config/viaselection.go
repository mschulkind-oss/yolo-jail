package config

// viaselection.go is the selection closure's input for a reader with no launch in hand
// (docs/design/wire-bridge-gateway.md WG-I11): config validation's name reservation
// (resolveSelectedPacks), `config promote`'s fold and the lazy loophole resolvers. Each used
// to run the `needs` half of the closure alone, so a pack a `via` profile adds was missing
// from all three; they now call packload.Selection.Close, the resolver the launch calls, and
// this is the one place their shared input is assembled.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// UserScopeSelection returns the closure input these readers share. The selection table is
// the user-scope config's `use_profiles`: a workspace spelling of that key is refused
// (validateProfiles), so the user scope is the whole of what a launch with no `-p` reads,
// and a `-p` is an argument to a launch that has not happened. The declarations are
// LoadProfiles', warnings discarded, the way these readers call LoadPacks: validation
// reports a malformed entry as an error of its own.
func UserScopeSelection(embedded func(name string) (*packload.Pack, bool)) packload.Selection {
	return packload.Selection{
		Embedded: embedded,
		UseProfiles: func([]*packload.Pack) map[string]string {
			v, _ := UserScopeConfigOrEmpty().Get(useProfilesKey)
			m, _ := v.(*jsonx.OrderedMap)
			return packload.ProfileTable(m)
		},
		UserProfiles: func() (map[string]packload.UserProfile, error) {
			return LoadProfiles(nil)
		},
	}
}
