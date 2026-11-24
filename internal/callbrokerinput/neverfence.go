package callbrokerinput

import (
	"encoding/json"
	"os"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// BrokersIgnoringNever names the launch of each live broker among files that was started by a yolo
// predating `never`: its private input lacks the NeverAware marker, or cannot be read (an unknown
// broker is not assumed to honor a never). Such a broker reads only always and on_request, so a
// source `never` the user adds is invisible to it until the jail is stopped and launched again.
func BrokersIgnoringNever(files []brokerscope.LiveFile) []string {
	var out []string
	for _, f := range files {
		if !neverAware(f.File.CallInput) {
			out = append(out, f.File.LaunchID)
		}
	}
	return out
}

func neverAware(path string) bool {
	if path == "" {
		return false
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	defer file.Close()
	var in struct {
		NeverAware bool `json:"never_aware"`
	}
	return json.NewDecoder(file).Decode(&in) == nil && in.NeverAware
}
