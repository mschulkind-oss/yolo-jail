package serialdaemon

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// DeviceEntry represents a discovered host serial port.
type DeviceEntry struct {
	Path       string `json:"path"`
	Accessible bool   `json:"accessible"`
	Error      string `json:"error,omitempty"`
}

// isDeviceAllowed checks whether devicePath matches any pattern in allowed.
func isDeviceAllowed(devicePath string, allowed []string) bool {
	clean := filepath.Clean(devicePath)
	if !filepath.IsAbs(clean) || !strings.HasPrefix(clean, "/dev/") {
		return false
	}

	for _, pat := range allowed {
		if matched, _ := filepath.Match(pat, clean); matched {
			return true
		}
	}

	if target, err := filepath.EvalSymlinks(clean); err == nil {
		targetClean := filepath.Clean(target)
		for _, pat := range allowed {
			if matched, _ := filepath.Match(pat, targetClean); matched {
				return true
			}
		}
	}

	return false
}

// findDevices discovers host serial devices matching allowed patterns.
func findDevices(allowed []string) []DeviceEntry {
	seen := map[string]bool{}
	var result []DeviceEntry

	for _, pat := range allowed {
		matches, err := filepath.Glob(pat)
		if err != nil {
			continue
		}
		for _, m := range matches {
			if seen[m] {
				continue
			}
			seen[m] = true

			entry := DeviceEntry{Path: m}
			f, err := os.OpenFile(m, os.O_RDWR, 0)
			if err == nil {
				entry.Accessible = true
				f.Close()
			} else {
				entry.Accessible = false
				entry.Error = err.Error()
			}
			result = append(result, entry)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})
	return result
}

// BuildHandler constructs the hostservice handler for the serial loophole.
func BuildHandler(cfg Settings) hostservice.Handler {
	return func(s *hostservice.Session) {
		mode := pyStrOr(func() (any, bool) { return s.Get("mode") }, "list")

		switch mode {
		case "list":
			handleList(s, cfg)
		case "read":
			handleRead(s, cfg)
		case "write":
			handleWrite(s, cfg)
		case "monitor", "stream", "bridge", "pty":
			handleMonitor(s, cfg)
		default:
			s.Stderr("unknown mode: " + pytext.Repr(mode) + "\n")
			s.Exit(2)
		}
	}
}

func handleList(s *hostservice.Session, cfg Settings) {
	devices := findDevices(cfg.AllowedDevices)
	if s.Request != nil {
		if raw, ok := s.Get("format"); ok && raw == "json" {
			_ = s.JSON(map[string]any{"devices": devices})
			s.Exit(0)
			return
		}
	}

	if len(devices) == 0 {
		s.Stdout("No serial devices found matching allowlist patterns.\n")
		s.Exit(0)
		return
	}

	s.Stdout(fmt.Sprintf("%-25s %-12s %s\n", "DEVICE", "STATUS", "DETAILS"))
	for _, dev := range devices {
		status := "ok (rw)"
		details := ""
		if !dev.Accessible {
			status = "no access"
			details = dev.Error
		}
		s.Stdout(fmt.Sprintf("%-25s %-12s %s\n", dev.Path, status, details))
	}
	s.Exit(0)
}

func handleRead(s *hostservice.Session, cfg Settings) {
	devicePath, ok := pyString(s, "device")
	if !ok || devicePath == "" {
		s.Stderr("serial: missing required field \"device\"\n")
		s.Exit(2)
		return
	}

	if !isDeviceAllowed(devicePath, cfg.AllowedDevices) {
		s.Stderr(fmt.Sprintf("serial: device %q is not in the allowed_devices list\n", devicePath))
		s.Exit(2)
		return
	}

	baud := pyIntOr(s, "baud", cfg.DefaultBaud)
	timeoutMs := pyIntOr(s, "timeout_ms", DefaultTimeoutMs)
	maxBytes := pyIntOr(s, "max_bytes", DefaultMaxBytes)

	f, err := openAndConfigureSerial(devicePath, baud)
	if err != nil {
		s.Stderr(fmt.Sprintf("serial: cannot open %s: %v\n", devicePath, err))
		s.Exit(1)
		return
	}
	defer f.Close()

	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	buf := make([]byte, 1024)
	totalRead := 0

	for time.Now().Before(deadline) && totalRead < maxBytes {
		toRead := len(buf)
		if maxBytes-totalRead < toRead {
			toRead = maxBytes - totalRead
		}
		n, err := f.Read(buf[:toRead])
		if n > 0 {
			s.StdoutBytes(buf[:n])
			totalRead += n
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	s.Exit(0)
}

func handleWrite(s *hostservice.Session, cfg Settings) {
	devicePath, ok := pyString(s, "device")
	if !ok || devicePath == "" {
		s.Stderr("serial: missing required field \"device\"\n")
		s.Exit(2)
		return
	}

	if !isDeviceAllowed(devicePath, cfg.AllowedDevices) {
		s.Stderr(fmt.Sprintf("serial: device %q is not in the allowed_devices list\n", devicePath))
		s.Exit(2)
		return
	}

	data, ok := pyString(s, "data")
	if !ok {
		s.Stderr("serial: missing required field \"data\"\n")
		s.Exit(2)
		return
	}

	baud := pyIntOr(s, "baud", cfg.DefaultBaud)
	appendNewline := pyBoolOr(s, "append_newline", true)

	f, err := openAndConfigureSerial(devicePath, baud)
	if err != nil {
		s.Stderr(fmt.Sprintf("serial: cannot open %s: %v\n", devicePath, err))
		s.Exit(1)
		return
	}
	defer f.Close()

	toWrite := []byte(data)
	if appendNewline && !strings.HasSuffix(data, "\n") {
		toWrite = append(toWrite, '\n')
	}

	if _, err := f.Write(toWrite); err != nil {
		s.Stderr(fmt.Sprintf("serial: write error on %s: %v\n", devicePath, err))
		s.Exit(1)
		return
	}

	s.Stdout(fmt.Sprintf("Wrote %d bytes to %s\n", len(toWrite), devicePath))
	s.Exit(0)
}

func handleMonitor(s *hostservice.Session, cfg Settings) {
	devicePath, ok := pyString(s, "device")
	if !ok || devicePath == "" {
		s.Stderr("serial: missing required field \"device\"\n")
		s.Exit(2)
		return
	}

	if !isDeviceAllowed(devicePath, cfg.AllowedDevices) {
		s.Stderr(fmt.Sprintf("serial: device %q is not in the allowed_devices list\n", devicePath))
		s.Exit(2)
		return
	}

	baud := pyIntOr(s, "baud", cfg.DefaultBaud)
	s.Stdout(fmt.Sprintf("--- Connected to serial bridge: %s (%d baud) ---\n", devicePath, baud))

	stopCh := make(chan struct{})
	var currentFileMu sync.Mutex
	var currentFile *os.File

	// Background inbound pump (client writes -> host serial port)
	go func() {
		for {
			select {
			case <-stopCh:
				return
			default:
			}
			frame, err := frameproto.ReadFrame(s.Conn())
			if err != nil {
				return
			}
			if len(frame.Payload) > 0 {
				currentFileMu.Lock()
				f := currentFile
				currentFileMu.Unlock()
				if f != nil {
					_, _ = f.Write(frame.Payload)
				}
			}
		}
	}()

	buf := make([]byte, 1024)
	consecutiveErrors := 0

	for {
		f, err := openAndConfigureSerial(devicePath, baud)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors > 20 {
				close(stopCh)
				s.Stderr(fmt.Sprintf("serial monitor: device %s disconnected and did not recover\n", devicePath))
				s.Exit(1)
				return
			}
			time.Sleep(250 * time.Millisecond)
			continue
		}

		currentFileMu.Lock()
		currentFile = f
		currentFileMu.Unlock()

		consecutiveErrors = 0
		for {
			n, err := f.Read(buf)
			if n > 0 {
				s.StdoutBytes(buf[:n])
			}
			if err != nil {
				currentFileMu.Lock()
				currentFile = nil
				currentFileMu.Unlock()
				f.Close()
				s.Stdout("\n[Device disconnected/rebooting... waiting to reconnect]\n")
				time.Sleep(300 * time.Millisecond)
				break
			}
		}
	}
}

func pyString(s *hostservice.Session, key string) (string, bool) {
	if v, ok := s.Get(key); ok && v != nil {
		if str, ok := v.(string); ok {
			return str, true
		}
	}
	return "", false
}

func pyStrOr(get func() (any, bool), def string) string {
	v, ok := get()
	if !ok || v == nil || v == "" {
		return def
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func pyIntOr(s *hostservice.Session, key string, def int) int {
	if v, ok := s.Get(key); ok && v != nil {
		if n, ok := jsonx.AsInt(v); ok && n > 0 {
			return int(n)
		}
		switch n := v.(type) {
		case int:
			return n
		case float64:
			return int(n)
		case json.Number:
			if i, err := n.Int64(); err == nil {
				return int(i)
			}
		}
	}
	return def
}

func pyBoolOr(s *hostservice.Session, key string, def bool) bool {
	if v, ok := s.Get(key); ok && v != nil {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}
