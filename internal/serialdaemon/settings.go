package serialdaemon

import (
	"bytes"
	"encoding/json"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

var DefaultAllowedDevices = []string{
	"/dev/ttyUSB*",
	"/dev/ttyACM*",
	"/dev/cu.usbserial*",
	"/dev/cu.usbmodem*",
	"/dev/serial/by-id/*",
}

const (
	DefaultBaudRate  = 115200
	DefaultTimeoutMs = 2000
	DefaultMaxBytes  = 65536
)

// Settings represents the resolved settings file written by yolo.
type Settings struct {
	// AllowedDevices is the device globs the daemon lists and opens. Under a grant block it is
	// the granted `devices` values and nothing else: no defaults, no `allowed_devices`.
	AllowedDevices []string
	DefaultBaud    int
	// Grants is the grant file's block (docs/design/loophole-access-approval.md §4.6), nil when
	// the settings file has no `grants` key; then the daemon behaves as before grants (plan
	// PD-12) until serial's flip deletes that path.
	Grants *loopholegrant.Block
}

// DefaultSettings returns the default configuration.
func DefaultSettings() Settings {
	return Settings{
		AllowedDevices: append([]string(nil), DefaultAllowedDevices...),
		DefaultBaud:    DefaultBaudRate,
	}
}

// LoadSettings reads the settings JSON file written by yolo at launch.
func LoadSettings(settingsPath string) Settings {
	cfg, _ := loadSettings(settingsPath)
	return cfg
}

func loadSettings(settingsPath string) (cfg Settings, ok bool) {
	if settingsPath == "" {
		return DefaultSettings(), true
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return DefaultSettings(), true
	}
	decoded, err := json5.Decode(data)
	if err != nil {
		if bytes.Contains(data, []byte(`"`+loopholegrant.ReservedKey+`"`)) {
			return loadGranted(settingsPath) // a grant file it cannot read fails closed
		}
		return DefaultSettings(), false
	}
	root, isMap := decoded.(*jsonx.OrderedMap)
	if !isMap {
		return DefaultSettings(), false
	}
	if _, granted := root.Get(loopholegrant.ReservedKey); granted {
		return loadGranted(settingsPath)
	}

	res := DefaultSettings()

	if v, exists := root.Get("allowed_devices"); exists && v != nil {
		if arr, ok := v.([]any); ok {
			var list []string
			for _, elem := range arr {
				if s, ok := elem.(string); ok && s != "" {
					list = append(list, s)
				}
			}
			if len(list) > 0 {
				res.AllowedDevices = list
			}
		}
	}

	if v, exists := root.Get("default_baud"); exists && v != nil {
		if n, ok := jsonx.AsInt(v); ok && n > 0 {
			res.DefaultBaud = int(n)
		} else {
			switch n := v.(type) {
			case int:
				if n > 0 {
					res.DefaultBaud = n
				}
			case int64:
				if n > 0 {
					res.DefaultBaud = int(n)
				}
			case float64:
				if n > 0 {
					res.DefaultBaud = int(n)
				}
			case json.Number:
				if i, err := n.Int64(); err == nil && i > 0 {
					res.DefaultBaud = int(i)
				}
			}
		}
	}

	return res, true
}

// loadGranted reads a grant file through loopholegrant, the one reader of the `grants` key. The
// granted `devices` values are the whole allowlist. A grant file it cannot read fails closed:
// a block with no kinds, which every request refuses with the not_allowed text.
func loadGranted(settingsPath string) (Settings, bool) {
	res := Settings{DefaultBaud: DefaultBaudRate}
	settings, block, err := loopholegrant.Read(settingsPath)
	if err != nil || block == nil {
		res.Grants = &loopholegrant.Block{Pack: grantPack}
		return res, false
	}
	res.Grants = block
	if k, ok := block.Kinds[devicesKind]; ok && k.Standing == loopholegrant.Granted {
		res.AllowedDevices = append([]string(nil), k.Values...)
	}
	if n, ok := positiveInt(settings["default_baud"]); ok {
		res.DefaultBaud = n
	}
	return res, true
}

// positiveInt is a JSON number that is a positive integer.
func positiveInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f <= 0 || f != float64(int(f)) {
		return 0, false
	}
	return int(f), true
}
