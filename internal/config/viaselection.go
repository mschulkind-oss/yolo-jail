package config

// viaselection.go is the selection closure's input for a reader with no launch in hand
// (docs/design/wire-bridge-gateway.md WG-I11): config validation's name reservation
// (resolveSelectedPacks), `config promote`'s fold, the lazy loophole resolvers and every host
// verb that composes no launch (`yolo host apply`, the footer, the `config` inspection verbs,
// capture, revert and check-deps). Each used to run the `needs` half of the closure alone, or,
// at the host, no closure at all; they now call SelectPacks, the selection the launch calls
// (notch-convergence item 6), and this is the one place their shared input is assembled.

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
//
// Embedded is left unset: SelectPacks fills it from the process's one materialization.
func UserScopeSelection() packload.Selection {
	return packload.Selection{
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
