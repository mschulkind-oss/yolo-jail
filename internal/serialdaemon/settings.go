package serialdaemon

import (
	"encoding/json"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
	AllowedDevices []string
	DefaultBaud    int
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
		return DefaultSettings(), false
	}
	root, isMap := decoded.(*jsonx.OrderedMap)
	if !isMap {
		return DefaultSettings(), false
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
