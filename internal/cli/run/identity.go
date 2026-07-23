package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// loopholeResolver returns the config.LoopholeResolver backing config validation
// (file-backed discovery, include_disabled=True).
func loopholeResolver() config.LoopholeResolver {
	return loopholes.NewResolver()
}
