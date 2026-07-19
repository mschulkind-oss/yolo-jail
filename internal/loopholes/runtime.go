package loopholes

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// warnf/infof are the package's log sinks, mirroring log.warning/log.info in
// src/loopholes.py. Default to no-ops: runtime_args_for's parity contract is the
// emitted argv, not log output, but the side effect (one info per in-jail
// device skip) is preserved for callers that install a sink.
var (
	warnf = func(format string, args ...any) {}
	infof = func(format string, args ...any) {}
)

// (podman) path; pass "container" for Apple Container (skips tls-intercept
// loopholes). It is side-effect free and idempotent.
func RuntimeArgsFor(loopholes []*Loophole, runtime string) []string {
	args := []string{}
	trustedCAPaths := []string{}
	jailDaemonsPayload := []any{}

	for _, m := range loopholes {
		if m.FromConfig() {
			continue
		}
		if !m.Active() {
			continue
		}
		if runtime == "container" && m.Transport == "tls-intercept" {
			continue
		}
		containerDir := "/etc/yolo-jail/loopholes/" + m.Name

		for _, intercept := range m.Intercepts {
			args = append(args, "--add-host", intercept.Host+":"+m.BrokerIP)
		}

		stateContainer := "/var/lib/yolo-jail/loopholes/" + m.Name
		stateMounted := false
		dirMounted := false

		if m.JailDaemon != nil {
			args = append(args, "-v", m.Path+":"+containerDir+":ro")
			dirMounted = true
			if isDir(m.StateDir()) {
				args = append(args, "-v", m.StateDir()+":"+stateContainer+":ro")
				stateMounted = true
			}
		}

		if m.HasCA() && m.CACertSet {
			containerCA := ""
			haveCA := false
			if stateMounted {
				if rel, ok := relativeTo(m.CACert, m.StateDir()); ok {
					containerCA = stateContainer + "/" + rel
					haveCA = true
				}
			}
			if !haveCA && dirMounted {
				if rel, ok := relativeTo(m.CACert, m.Path); ok {
					containerCA = containerDir + "/" + rel
					haveCA = true
				}
			}
			if !haveCA {
				containerCA = containerDir + "/ca.crt"
				args = append(args, "-v", m.CACert+":"+containerCA+":ro")
			}
			trustedCAPaths = append(trustedCAPaths, containerCA)
		}

		if m.JailDaemon != nil {
			spec := jsonx.NewOrderedMap()
			spec.Set("name", m.Name)
			spec.Set("cmd", toAnySlice(m.JailDaemon.Cmd))
			spec.Set("restart", m.JailDaemon.Restart)
			jailDaemonsPayload = append(jailDaemonsPayload, spec)
		}

		for _, bm := range m.HostBindMount {
			if !pathExists(bm.Host) {
				warnf("loophole %s: skipping bind mount, host source missing: %s", m.Name, bm.Host)
				continue
			}
			spec := bm.Host + ":" + bm.Container
			if bm.Readonly {
				spec += ":ro"
			}
			args = append(args, "-v", spec)
		}

		if len(m.HostDevices) > 0 && inJail() {
			infof("loophole %s: skipping device passthrough inside a jail — "+
				"devices cannot nest under rootless podman", m.Name)
		} else {
			for _, dev := range m.HostDevices {
				if !pathExists(dev) {
					warnf("loophole %s: skipping device passthrough, host node missing: %s", m.Name, dev)
					continue
				}
				args = append(args, "--device", dev)
			}
		}

		for _, k := range m.JailEnv.Keys() {
			v, _ := m.JailEnv.Get(k)
			args = append(args, "-e", k+"="+v)
		}
	}

	if len(trustedCAPaths) > 0 {
		args = append(args, "-e", "NODE_EXTRA_CA_CERTS="+strings.Join(trustedCAPaths, string(os.PathListSeparator)))
	}
	if len(jailDaemonsPayload) > 0 {
		payload, _ := jsonx.DumpsCompact(jailDaemonsPayload)
		args = append(args, "-e", "YOLO_JAIL_DAEMONS="+payload)
	}
	return args
}

// every active file-backed loophole with a host_daemon, shaped like the
// loopholes: config block. Returned as an insertion-ordered map so it serializes
// deterministically; Python dict equality is order-insensitive anyway.
func ManifestHostDaemonSpecs(loopholes []*Loophole) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, m := range loopholes {
		if m.FromConfig() || m.HostDaemon == nil {
			continue
		}
		if !m.Active() {
			continue
		}
		spec := jsonx.NewOrderedMap()
		spec.Set("command", toAnySlice(m.HostDaemon.Cmd))
		spec.Set("description", m.Description)
		if m.HostDaemon.Env.Len() > 0 {
			env := jsonx.NewOrderedMap()
			for _, k := range m.HostDaemon.Env.Keys() {
				v, _ := m.HostDaemon.Env.Get(k)
				env.Set(k, v)
			}
			spec.Set("env", env)
		}
		out.Set(m.Name, spec)
	}
	return out
}

//	RC is nil when doctor_cmd is
//
// absent or could not run.
type DoctorResult struct {
	Loophole *Loophole
	RC       *int
	Output   string
}

// RunDoctorChecks mirrors run_doctor_checks. timeout defaults to 10s when zero.
func RunDoctorChecks(loopholes []*Loophole, timeout time.Duration) []DoctorResult {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	results := []DoctorResult{}
	for _, m := range loopholes {
		if len(m.DoctorCmd) == 0 {
			results = append(results, DoctorResult{Loophole: m, RC: nil, Output: ""})
			continue
		}
		rc, output := runOne(m.DoctorCmd, timeout)
		results = append(results, DoctorResult{Loophole: m, RC: rc, Output: output})
	}
	return results
}

func runOne(argv []string, timeout time.Duration) (*int, string) {
	cmd := exec.Command(argv[0], argv[1:]...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		// FileNotFoundError / OSError -> returncode None, output = str(e).
		return nil, err.Error()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return nil, "timeout"
	case err := <-done:
		code := cmd.ProcessState.ExitCode()
		out := stdout.String()
		if out == "" {
			out = stderr.String()
		}
		out = strings.TrimSpace(out)
		_ = err
		rc := code
		return &rc, out
	}
}

// through a JSON round-trip. This deliberately DROPS JSONC comments (the parse
// via json5 -> re-serialize as plain JSON degradation documented in the module
// map) — do NOT "fix" this; parity depends on reproducing it.
func SetEnabled(modulePath string, enabled bool) error {
	manifestPath := filepath.Join(modulePath, "manifest.jsonc")
	text, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	decodedAny, err := json5.Decode(text)
	if err != nil {
		return err
	}
	decoded, ok := decodedAny.(*jsonx.OrderedMap)
	if !ok {
		// Python would raise TypeError on data["enabled"]=...; only a
		// non-object manifest reaches here, which never occurs in practice.
		return &LoopholeError{msg: manifestPath + ": manifest must be a JSON object"}
	}
	decoded.Set("enabled", enabled)
	header := "// yolo-jail loophole manifest. See src/loopholes.py for schema.\n" +
		"// 'enabled' toggled via `yolo loopholes {enable,disable}`.\n"
	body, err := jsonx.DumpsIndent(decoded, 2)
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath, []byte(header+body+"\n"), 0o644)
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// Returns (rel, true) when base is a path-component prefix of target, else
// ("", false) — matching the ValueError branch. Both paths are cleaned first.
func relativeTo(target, base string) (string, bool) {
	tc := splitPath(filepath.Clean(target))
	bc := splitPath(filepath.Clean(base))
	if len(bc) > len(tc) {
		return "", false
	}
	for i := range bc {
		if tc[i] != bc[i] {
			return "", false
		}
	}
	rem := tc[len(bc):]
	if len(rem) == 0 {
		return ".", true
	}
	return strings.Join(rem, "/"), true
}

// splitPath breaks an absolute/relative path into components, keeping a leading
// "/" as its own root token so "/a/b" and "a/b" never alias.
func splitPath(p string) []string {
	if p == "/" {
		return []string{"/"}
	}
	var out []string
	if strings.HasPrefix(p, "/") {
		out = append(out, "/")
		p = strings.TrimPrefix(p, "/")
	}
	for _, part := range strings.Split(p, "/") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
