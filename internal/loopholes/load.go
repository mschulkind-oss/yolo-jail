package loopholes

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// LoopholeError is raised when a manifest is malformed. Mirrors the
// LoopholeError(ValueError) type; discovery skips it silently, validate surfaces
// its message.
type LoopholeError struct{ msg string }

func (e *LoopholeError) Error() string { return e.msg }

func loopholeErrorf(format string, args ...any) *LoopholeError {
	return &LoopholeError{msg: fmt.Sprintf(format, args...)}
}

// LoadLoophole loads a single loophole from its directory. Mirrors
// load_loophole.
func LoadLoophole(modulePath string) (*Loophole, error) {
	return loadManifest(modulePath)
}

// loadManifest mirrors _load_manifest.
func loadManifest(modulePath string) (*Loophole, error) {
	manifestPath := filepath.Join(modulePath, "manifest.jsonc")
	if fi, err := stat(manifestPath); err != nil || !fi.Mode().IsRegular() {
		return nil, loopholeErrorf("%s not found", manifestPath)
	}

	raw, err := readFile(manifestPath)
	if err != nil {
		return nil, loopholeErrorf("%s: %s", manifestPath, err)
	}
	decoded, err := json5.Decode(raw)
	if err != nil {
		// pyjson5's exception text is not byte-reproducible; the prefix
		// matches and no parity test compares the JSON-syntax-error body
		// (discovery skips malformed manifests silently). See ledger.
		return nil, loopholeErrorf("%s: %s", manifestPath, err)
	}
	data, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		// A non-object manifest makes Python's data.get(...) raise an
		// uncaught AttributeError (crash); unreachable for authored
		// manifests. We degrade to a skippable LoopholeError. See ledger.
		return nil, loopholeErrorf("%s: manifest must be a JSON object", manifestPath)
	}

	nameV, _ := data.Get("name")
	name, nameIsStr := nameV.(string)
	if !nameIsStr || name == "" {
		return nil, loopholeErrorf("%s: 'name' is required", manifestPath)
	}
	dirName := filepath.Base(modulePath)
	if name != dirName {
		return nil, loopholeErrorf(
			"%s: name='%s' disagrees with directory '%s' — they must match",
			manifestPath, name, dirName)
	}

	description := ""
	if dv, ok := data.Get("description"); ok {
		s, isStr := dv.(string)
		if !isStr {
			return nil, loopholeErrorf("%s: 'description' must be a string", manifestPath)
		}
		description = s
	}

	transport := "tls-intercept"
	if tv, ok := data.Get("transport"); ok {
		transport = pyStr(tv)
	}
	if !inList(transport, validTransports) {
		return nil, loopholeErrorf("%s: transport=%s not in %s",
			manifestPath, pytext.Repr(transport), sortedListRepr(validTransports))
	}

	lifecycle := "external"
	if lv, ok := data.Get("lifecycle"); ok {
		lifecycle = pyStr(lv)
	}
	if !inList(lifecycle, validLifecycles) {
		return nil, loopholeErrorf("%s: lifecycle=%s not in %s",
			manifestPath, pytext.Repr(lifecycle), sortedListRepr(validLifecycles))
	}

	intercepts, err := parseIntercepts(manifestPath, orEmptyList(data, "intercepts"))
	if err != nil {
		return nil, err
	}

	caCert, caCertSet := "", false
	if cv, ok := data.Get("ca_cert"); ok {
		if s, isStr := cv.(string); isStr && s != "" {
			if containsSubstr(s, "{state}") {
				caCert = replaceAll(s, "{state}", StateDirFor(name))
			} else {
				// Python: (module_path / ca_cert_raw).resolve(). pathlib `/`
				// DISCARDS module_path when ca_cert_raw is absolute; Go's
				// filepath.Join would concatenate ("<module>/<abs>"), producing a
				// bogus path that then fails HasCA() and silently drops the CA
				// mount + NODE_EXTRA_CA_CERTS. Guard on IsAbs to match pathlib.
				if filepath.IsAbs(s) {
					caCert = resolvePath(s)
				} else {
					caCert = resolvePath(filepath.Join(modulePath, s))
				}
			}
			caCertSet = true
		}
	}

	jailEnv, err := parseEnvMap(manifestPath, orEmptyMap(data, "jail_env"), "'jail_env' must be a mapping")
	if err != nil {
		return nil, err
	}

	doctorCmd, doctorCmdSet := []string(nil), false
	if dcv, ok := data.Get("doctor_cmd"); ok && dcv != nil {
		list, listOK := dcv.([]any)
		if !listOK || !allStrings(list) {
			return nil, loopholeErrorf("%s: 'doctor_cmd' must be a list of strings", manifestPath)
		}
		doctorCmd = toStringSlice(list)
		doctorCmdSet = true
	}

	hostDaemon, err := parseHostDaemon(manifestPath, getOrNil(data, "host_daemon"))
	if err != nil {
		return nil, err
	}
	jailDaemon, err := parseJailDaemon(manifestPath, getOrNil(data, "jail_daemon"))
	if err != nil {
		return nil, err
	}
	hostBindMounts, err := parseHostBindMounts(manifestPath, getOrNil(data, "host_bind_mounts"))
	if err != nil {
		return nil, err
	}
	hostDevices, err := parseHostDevices(manifestPath, getOrNil(data, "host_devices"))
	if err != nil {
		return nil, err
	}
	requires, err := parseRequires(manifestPath, getOrNil(data, "requires"))
	if err != nil {
		return nil, err
	}

	enabled := true
	if ev, ok := data.Get("enabled"); ok {
		enabled = pyTruthy(ev)
	}

	brokerIP := DefaultBrokerIP
	if bv, ok := data.Get("broker_ip"); ok && pyTruthy(bv) {
		brokerIP = pyStr(bv)
	}

	return &Loophole{
		Name:          name,
		Description:   description,
		Path:          modulePath,
		Enabled:       enabled,
		Transport:     transport,
		Lifecycle:     lifecycle,
		Intercepts:    intercepts,
		BrokerIP:      brokerIP,
		CACert:        caCert,
		CACertSet:     caCertSet,
		JailEnv:       jailEnv,
		DoctorCmd:     doctorCmd,
		DoctorCmdSet:  doctorCmdSet,
		HostDaemon:    hostDaemon,
		JailDaemon:    jailDaemon,
		HostBindMount: hostBindMounts,
		HostDevices:   hostDevices,
		Requires:      requires,
		Source:        SourceUser,
	}, nil
}

// parseIntercepts mirrors the intercepts loop of _load_manifest.
func parseIntercepts(manifestPath string, raw any) ([]Intercept, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, loopholeErrorf("%s: 'intercepts' must be a list", manifestPath)
	}
	out := []Intercept{}
	for _, entry := range list {
		m, isMap := entry.(*jsonx.OrderedMap)
		if !isMap {
			return nil, loopholeErrorf("%s: each intercept needs a string 'host'", manifestPath)
		}
		hv, _ := m.Get("host")
		host, isStr := hv.(string)
		if !isStr {
			return nil, loopholeErrorf("%s: each intercept needs a string 'host'", manifestPath)
		}
		out = append(out, Intercept{Host: host})
	}
	return out, nil
}

// parseRequires mirrors _parse_requires.
func parseRequires(manifestPath string, raw any) (Requires, error) {
	if raw == nil {
		return Requires{}, nil
	}
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return Requires{}, loopholeErrorf("%s: 'requires' must be a mapping", manifestPath)
	}
	var req Requires
	if cv, ok := m.Get("command_on_path"); ok && cv != nil {
		s, isStr := cv.(string)
		if !isStr {
			return Requires{}, loopholeErrorf("%s: 'requires.command_on_path' must be a string", manifestPath)
		}
		req.CommandOnPath = s
		req.CommandOnPathSet = true
	}
	if fv, ok := m.Get("file_exists"); ok && fv != nil {
		s, isStr := fv.(string)
		if !isStr {
			return Requires{}, loopholeErrorf("%s: 'requires.file_exists' must be a string", manifestPath)
		}
		req.FileExists = s
		req.FileExistsSet = true
	}
	return req, nil
}

// parseHostBindMounts mirrors _parse_host_bind_mounts.
func parseHostBindMounts(manifestPath string, raw any) ([]HostBindMount, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, loopholeErrorf("%s: 'host_bind_mounts' must be a list", manifestPath)
	}
	moduleDir := filepath.Dir(manifestPath)
	out := []HostBindMount{}
	for i, entry := range list {
		m, isMap := entry.(*jsonx.OrderedMap)
		if !isMap {
			return nil, loopholeErrorf("%s: host_bind_mounts[%d] must be a mapping", manifestPath, i)
		}
		hostV, _ := m.Get("host")
		hostRaw, hostIsStr := hostV.(string)
		if !hostIsStr || hostRaw == "" {
			return nil, loopholeErrorf("%s: host_bind_mounts[%d].host must be a non-empty string", manifestPath, i)
		}
		containerV, _ := m.Get("container")
		container, contIsStr := containerV.(string)
		if !contIsStr || container == "" {
			return nil, loopholeErrorf("%s: host_bind_mounts[%d].container must be a non-empty string", manifestPath, i)
		}
		readonly := true
		if rv, ok := m.Get("readonly"); ok {
			b, isBool := rv.(bool)
			if !isBool {
				return nil, loopholeErrorf("%s: host_bind_mounts[%d].readonly must be a boolean", manifestPath, i)
			}
			readonly = b
		}
		expanded := expandEnv(replaceAll(hostRaw, "{loophole_dir}", moduleDir))
		out = append(out, HostBindMount{
			Host:      expanded,
			Container: container,
			Readonly:  readonly,
		})
	}
	return out, nil
}

// parseHostDevices mirrors _parse_host_devices.
func parseHostDevices(manifestPath string, raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, loopholeErrorf("%s: 'host_devices' must be a list", manifestPath)
	}
	out := []string{}
	for i, entry := range list {
		s, isStr := entry.(string)
		if !isStr || s == "" {
			return nil, loopholeErrorf("%s: host_devices[%d] must be a non-empty string", manifestPath, i)
		}
		out = append(out, s)
	}
	return out, nil
}

// parseHostDaemon mirrors _parse_host_daemon.
func parseHostDaemon(manifestPath string, raw any) (*HostDaemon, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil, loopholeErrorf("%s: 'host_daemon' must be a mapping", manifestPath)
	}
	cmdV, _ := m.Get("cmd")
	cmdList, isList := cmdV.([]any)
	if !isList || len(cmdList) == 0 || !allStrings(cmdList) {
		return nil, loopholeErrorf("%s: 'host_daemon.cmd' must be a non-empty list of strings", manifestPath)
	}
	env, err := parseEnvMap(manifestPath, orEmptyMapValue(getOrNil(m, "env")), "'host_daemon.env' must be a mapping")
	if err != nil {
		return nil, err
	}
	return &HostDaemon{Cmd: toStringSlice(cmdList), Env: env}, nil
}

// parseJailDaemon mirrors _parse_jail_daemon.
func parseJailDaemon(manifestPath string, raw any) (*JailDaemon, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil, loopholeErrorf("%s: 'jail_daemon' must be a mapping", manifestPath)
	}
	cmdV, _ := m.Get("cmd")
	cmdList, isList := cmdV.([]any)
	if !isList || len(cmdList) == 0 || !allStrings(cmdList) {
		return nil, loopholeErrorf("%s: 'jail_daemon.cmd' must be a non-empty list of strings", manifestPath)
	}
	restart := "on-failure"
	if rv, ok := m.Get("restart"); ok {
		restart = pyStr(rv)
	}
	if !inList(restart, validRestarts) {
		return nil, loopholeErrorf("%s: 'jail_daemon.restart' not in %s", manifestPath, sortedListRepr(validRestarts))
	}
	return &JailDaemon{Cmd: toStringSlice(cmdList), Restart: restart}, nil
}

// parseEnvMap builds an insertion-ordered EnvMap from a JSON object, mirroring
// {str(k): str(v) for k, v in raw.items()}. raw must already be resolved to a
// value that is either an *jsonx.OrderedMap or an empty-map sentinel.
func parseEnvMap(manifestPath string, raw any, mappingErr string) (*EnvMap, error) {
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil, loopholeErrorf("%s: %s", manifestPath, mappingErr)
	}
	out := NewEnvMap()
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out.Set(k, pyStr(v))
	}
	return out, nil
}

// --- small decode helpers mirroring Python's `.get(...) or default` idioms ---

// orEmptyList mirrors `data.get(key) or []`: a falsy value yields an empty list
// (which passes the isinstance-list check); a truthy non-list stays as-is (so
// the caller's isinstance check fires the error).
func orEmptyList(m *jsonx.OrderedMap, key string) any {
	v, ok := m.Get(key)
	if !ok || !pyTruthy(v) {
		return []any{}
	}
	return v
}

// orEmptyMap mirrors `data.get(key) or {}` for the jail_env path.
func orEmptyMap(m *jsonx.OrderedMap, key string) any {
	v, ok := m.Get(key)
	if !ok || !pyTruthy(v) {
		return jsonx.NewOrderedMap()
	}
	return v
}

// orEmptyMapValue mirrors `X or {}` for an already-fetched value.
func orEmptyMapValue(v any) any {
	if !pyTruthy(v) {
		return jsonx.NewOrderedMap()
	}
	return v
}

// getOrNil returns m[key] or nil (Python dict.get default None).
func getOrNil(m *jsonx.OrderedMap, key string) any {
	v, ok := m.Get(key)
	if !ok {
		return nil
	}
	return v
}

func allStrings(list []any) bool {
	for _, x := range list {
		if _, ok := x.(string); !ok {
			return false
		}
	}
	return true
}

func toStringSlice(list []any) []string {
	out := make([]string, len(list))
	for i, x := range list {
		out[i], _ = x.(string)
	}
	return out
}

func inList(s string, list []string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// sortedListRepr renders repr(sorted(set)) — a Python list literal of the
// sorted values.
func sortedListRepr(values []string) string {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	return pyListRepr(sorted)
}

func containsSubstr(s, sub string) bool {
	return indexOf(s, sub) >= 0
}

func replaceAll(s, old, new string) string {
	if old == "" {
		return s
	}
	var b []byte
	for {
		i := indexOf(s, old)
		if i < 0 {
			b = append(b, s...)
			break
		}
		b = append(b, s[:i]...)
		b = append(b, new...)
		s = s[i+len(old):]
	}
	return string(b)
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
