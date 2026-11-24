package callbroker

import "github.com/mschulkind-oss/yolo-jail/internal/config"

// readNeverNamed reports whether the host user config refuses a read of this cell by a
// never, at the source or on the cell. An unreadable config refuses.
func readNeverNamed(source, cell string) bool {
	user, err := config.UserScopeConfig(true, func(string) {})
	if err != nil || user == nil {
		return true
	}
	return config.CallBrokerReadNeverRefuses(user, source, "") || cell != "" && config.CallBrokerReadNeverRefuses(user, source, cell)
}
