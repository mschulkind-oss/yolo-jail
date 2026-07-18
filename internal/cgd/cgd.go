// Package cgd is the Go port of the builtin cgroup-delegate daemon
// (src/cli/loopholes_runtime.py: _cgroup_delegate_handler + _cgd_*). It
// performs privileged cgroup v2 operations on the host's cgroup subtree on
// behalf of an in-jail caller, identified by SO_PEERCRED (kernel-attested PID).
//
// Frozen contracts (go-port plan Stage 7): the single-line-JSON request/
// response protocol, the cgroup-name validation regex, the human-readable
// memory parse, the cpu.max/memory.max/pids.max writes and their range checks,
// and the "move caller into the job cgroup by peer PID" semantics.
//
// Source of truth: src/cli/loopholes_runtime.py. The socket name
// (cgroup-delegate.sock) and chmod 0777 live in the daemon wiring, not here.
package cgd

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// cgroupNameRe mirrors _validate_cgroup_name's regex.
var cgroupNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// ValidateCgroupName reports whether name is a safe cgroup name (no traversal).
// Mirrors _validate_cgroup_name.
func ValidateCgroupName(name string) bool {
	return cgroupNameRe.MatchString(name) && !strings.Contains(name, "..")
}

// ParseMemoryValue parses a human-readable memory value (g/m/k suffix, or raw
// bytes) to bytes, returning (0,false) on invalid input. Mirrors
// _parse_memory_value, including the int(float(...)*factor) truncation.
func ParseMemoryValue(val string) (int64, bool) {
	val = strings.ToLower(strings.TrimSpace(val))
	factor := func(suffix string, mult float64) (int64, bool) {
		f, err := strconv.ParseFloat(strings.TrimSuffix(val, suffix), 64)
		if err != nil {
			return 0, false
		}
		return int64(f * mult), true
	}
	switch {
	case strings.HasSuffix(val, "g"):
		return factor("g", 1073741824)
	case strings.HasSuffix(val, "m"):
		return factor("m", 1048576)
	case strings.HasSuffix(val, "k"):
		return factor("k", 1024)
	default:
		// Python int(val): only a bare integer literal (no float, no suffix).
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
}

// cpuCount mirrors os.cpu_count() or 1.
func cpuCount() int {
	n := runtime.NumCPU()
	if n < 1 {
		return 1
	}
	return n
}

// Request is a decoded cgd request. Handle dispatches on Op.
type Request struct {
	raw *jsonx.OrderedMap
}

// RequestOp extracts the "op" field from a raw request line for the audit log,
// or "" if unparseable. Best-effort (logging only — never affects dispatch).
func RequestOp(line []byte) string {
	r, ok := ParseRequest(line)
	if !ok {
		return ""
	}
	if v, ok := r.raw.Get("op"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ParseRequest decodes a single-line JSON request. Mirrors json.loads of the
// first line; returns (nil, false) on empty/invalid.
func ParseRequest(line []byte) (*Request, bool) {
	decoded, err := jsonx.Decode(line)
	if err != nil {
		return nil, false
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return nil, false
	}
	return &Request{raw: m}, true
}

func (r *Request) str(key string) string {
	if v, ok := r.raw.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// intField returns an integer request field and whether it was present as a
// number. Mirrors request.get(key) with an int()-able value.
func (r *Request) intField(key string) (int64, bool) {
	v, ok := r.raw.Get(key)
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		return n, err == nil
	default:
		// jsonx integer literal: re-encode + parse.
		s, err := jsonx.DumpsCompact(v)
		if err != nil {
			return 0, false
		}
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		return n, err == nil
	}
}

// present reports whether key is present and non-null (Python `is not None`).
func (r *Request) present(key string) bool {
	v, ok := r.raw.Get(key)
	return ok && v != nil
}

// rawValue returns the raw string form of a field for error messages.
func (r *Request) rawValue(key string) string {
	v, ok := r.raw.Get(key)
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	s, _ := jsonx.DumpsCompact(v)
	return s
}

// response builders mirror the {"ok": ...} dicts.
func okResp(pairs ...any) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("ok", true)
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func errResp(msg string) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("ok", false)
	m.Set("error", msg)
	return m
}

// Handle dispatches one request against containerCgroup for the caller peerPID,
// returning the response object. Mirrors the op switch in
// _cgroup_delegate_handler (status / create_and_join / destroy / unknown).
func Handle(r *Request, containerCgroup string, peerPID int) *jsonx.OrderedMap {
	op := ""
	if v, ok := r.raw.Get("op"); ok {
		if s, ok := v.(string); ok {
			op = s
		}
	}
	switch op {
	case "status":
		agentCg := filepath.Join(containerCgroup, "agent")
		delegated := dirExists(agentCg)
		controllers := ""
		if delegated {
			if b, err := os.ReadFile(filepath.Join(agentCg, "cgroup.controllers")); err == nil {
				controllers = strings.TrimSpace(string(b))
			}
		}
		return okResp("delegated", delegated, "controllers", controllers, "cgroup", containerCgroup)
	case "create_and_join":
		name := r.str("name")
		if !ValidateCgroupName(name) {
			return errResp("Invalid cgroup name: " + reprName(name))
		}
		if peerPID <= 0 {
			return errResp("Could not determine caller PID")
		}
		return createAndJoin(containerCgroup, name, r, peerPID)
	case "destroy":
		name := r.str("name")
		if !ValidateCgroupName(name) {
			return errResp("Invalid cgroup name: " + reprName(name))
		}
		return destroy(containerCgroup, name)
	default:
		return errResp("Unknown operation: " + reprName(op))
	}
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
