package loopholes

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// LoopholeError is raised when a manifest is malformed.
// LoopholeError(ValueError) type; discovery skips it silently, validate surfaces
// its message.
type LoopholeError struct{ msg string }

func (e *LoopholeError) Error() string { return e.msg }

func loopholeErrorf(format string, args ...any) *LoopholeError {
	return &LoopholeError{msg: fmt.Sprintf(format, args...)}
}

// LoadLoophole loads a single loophole from its directory.
func LoadLoophole(modulePath string) (*Loophole, error) {
	return loadManifest(modulePath)
}

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
		// The decoder's exception text is not stable, but only the prefix
		// matters here: discovery skips malformed manifests silently.
		return nil, loopholeErrorf("%s: %s", manifestPath, err)
	}
	data, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		// A non-object manifest is unreachable for authored manifests.
		// We degrade to a skippable LoopholeError rather than crashing.
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

	// An absent `transport` means loopback-tls: there is one transport, so the
	// default is it. The old default was "tls-intercept", which was never a
	// transport at all — a manifest that said nothing about transports got a value
	// implying it intercepted TLS.
	transport := TransportLoopbackTLS
	if tv, ok := data.Get("transport"); ok {
		transport = pyStr(tv)
	}
	if !inList(transport, validTransports) {
		return nil, loopholeErrorf("%s: transport=%s not in %s%s",
			manifestPath, pytext.Repr(transport), sortedListRepr(validTransports),
			retiredTransportHint(transport))
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
				// An absolute ca_cert must be used as-is, discarding
				// module_path. filepath.Join would instead concatenate
				// ("<module>/<abs>"), producing a bogus path that then fails
				// HasCA() and silently drops the CA mount + NODE_EXTRA_CA_CERTS.
				// Guard on IsAbs to resolve the absolute path directly.
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
		if err := refuseJailTokenInHostField(manifestPath, "'doctor_cmd'", doctorCmd); err != nil {
			return nil, err
		}
		doctorCmd = substituteAll(doctorCmd, tokenLoopholeDir, resolvePath(modulePath))
	}

	hostDaemon, err := parseHostDaemon(manifestPath, modulePath, getOrNil(data, "host_daemon"))
	if err != nil {
		return nil, err
	}
	jailDaemon, err := parseJailDaemon(manifestPath, name, getOrNil(data, "jail_daemon"))
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
	stateFiles, err := parseStateFiles(manifestPath, getOrNil(data, "state_files"))
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
		StateFiles:    stateFiles,
		Requires:      requires,
		Source:        SourceUser,
	}, nil
}

// retiredTransportHint appends the migration instruction to the "not in [...]"
// error when the rejected value is one this repo used to ship.
//
// The bare enum error is technically complete and practically useless here: the
// reader wrote a value the docs told them to write, and the consequence of the
// rejection is that their loophole VANISHES (loadFromDir warns and moves on). The
// hint turns a breaking change into a self-documenting one, which is the price of
// removing a value rather than deprecating it.
func retiredTransportHint(transport string) string {
	switch transport {
	case retiredTransportTLSIntercept:
		return " — 'tls-intercept' was retired: it named the in-jail TLS terminator," +
			" not a transport. Write 'loopback-tls' and keep 'intercepts'/'broker_ip'/" +
			"'ca_cert' exactly as they are; those are what wire the interception."
	case retiredTransportUnixSocket:
		return " — 'unix-socket' was retired (docs/design/loophole-transport.md §7.4):" +
			" it cannot cross virtiofs on macOS + podman. Write 'loopback-tls' and add" +
			" 'publishes': 'socket' to 'host_daemon': the daemon keeps binding its" +
			" AF_UNIX socket at the path yolo substitutes into '{socket}', and yolo" +
			" runs the TLS front over it and publishes the endpoint file for you."
	}
	return ""
}

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
		if err := refuseJailTokenInHostField(manifestPath,
			fmt.Sprintf("host_bind_mounts[%d].host", i), []string{hostRaw}); err != nil {
			return nil, err
		}
		expanded := expandEnv(replaceAll(hostRaw, tokenLoopholeDir, moduleDir))
		out = append(out, HostBindMount{
			Host:      expanded,
			Container: container,
			Readonly:  readonly,
		})
	}
	return out, nil
}

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

// parseStateFiles parses the optional `state_files` list: the subset of the
// per-loophole state dir that is allowed to cross into the jail. ABSENT (or an
// empty list) means the whole state dir is mounted — the historical behavior,
// preserved deliberately so an external manifest without the key keeps its
// meaning.
//
// Entries are paths RELATIVE to the state dir. Absolute paths and any ".."
// escape are rejected here, at load time, so the key can only ever narrow the
// existing mount and never reach outside the directory it is narrowing.
func parseStateFiles(manifestPath string, raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, loopholeErrorf("%s: 'state_files' must be a list", manifestPath)
	}
	out := []string{}
	for i, entry := range list {
		s, isStr := entry.(string)
		if !isStr || s == "" {
			return nil, loopholeErrorf("%s: state_files[%d] must be a non-empty string", manifestPath, i)
		}
		if filepath.IsAbs(s) {
			return nil, loopholeErrorf("%s: state_files[%d]=%s must be relative to the state dir",
				manifestPath, i, pytext.Repr(s))
		}
		clean := filepath.Clean(s)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, loopholeErrorf("%s: state_files[%d]=%s must stay inside the state dir",
				manifestPath, i, pytext.Repr(s))
		}
		out = append(out, clean)
	}
	return out, nil
}

// Module-dir tokens. {loophole_dir} resolves to the HOST-side absolute module
// dir and is legal in host_daemon.cmd, doctor_cmd and host_bind_mounts[].host;
// {jail_loophole_dir} resolves to the module dir's CONTAINER mount point
// (JailLoopholeDir) and is legal in jail_daemon.cmd. Two tokens on purpose —
// one token with two resolutions is the asymmetry an author discovers by
// debugging — and each is refused in the other half, at load, naming the fix.
const (
	tokenLoopholeDir     = "{loophole_dir}"
	tokenJailLoopholeDir = "{jail_loophole_dir}"
)

// refuseJailTokenInHostField rejects {jail_loophole_dir} in a field that runs
// (or resolves) on the HOST.
func refuseJailTokenInHostField(manifestPath, field string, args []string) error {
	for _, s := range args {
		if containsSubstr(s, tokenJailLoopholeDir) {
			return loopholeErrorf(
				"%s: %s names '%s', the module dir's CONTAINER mount point — this field"+
					" resolves on the HOST; write '%s'",
				manifestPath, field, tokenJailLoopholeDir, tokenLoopholeDir)
		}
	}
	return nil
}

// refuseHostTokenInJailField rejects {loophole_dir} in a field that runs inside
// the container.
func refuseHostTokenInJailField(manifestPath, field string, args []string) error {
	for _, s := range args {
		if containsSubstr(s, tokenLoopholeDir) {
			return loopholeErrorf(
				"%s: %s names '%s', the module dir's HOST path — this command runs inside"+
					" the container, where the dir is mounted at %s; write '%s'",
				manifestPath, field, tokenLoopholeDir, JailLoopholeDir("<name>"), tokenJailLoopholeDir)
		}
	}
	return nil
}

// substituteAll replaces token with value in every element of args.
func substituteAll(args []string, token, value string) []string {
	out := make([]string, len(args))
	for i, s := range args {
		out[i] = replaceAll(s, token, value)
	}
	return out
}

func parseHostDaemon(manifestPath, modulePath string, raw any) (*HostDaemon, error) {
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
	publishes := PublishesEndpoint
	if pv, ok := m.Get("publishes"); ok {
		publishes = pyStr(pv)
	}
	if !inList(publishes, validPublishes) {
		return nil, loopholeErrorf("%s: 'host_daemon.publishes' = %s not in %s",
			manifestPath, pytext.Repr(publishes), sortedListRepr(validPublishes))
	}
	requestEnd := RequestEndFramed
	if rv, ok := m.Get("request_end"); ok {
		requestEnd = pyStr(rv)
	}
	if !inList(requestEnd, validRequestEnds) {
		return nil, loopholeErrorf("%s: 'host_daemon.request_end' = %s not in %s",
			manifestPath, pytext.Repr(requestEnd), sortedListRepr(validRequestEnds))
	}
	cmd := toStringSlice(cmdList)
	if err := refuseJailTokenInHostField(manifestPath, "'host_daemon.cmd'", cmd); err != nil {
		return nil, err
	}
	cmd = substituteAll(cmd, tokenLoopholeDir, resolvePath(modulePath))
	// Under publishes:"socket" the two tokens DIVERGE: {socket} is the upstream
	// AF_UNIX path the daemon binds, {endpoint} is the file yolo's front
	// publishes. An argv naming {endpoint} there would silently publish nothing
	// while yolo publishes over it, so it is an author error refused with the fix.
	if publishes == PublishesSocket {
		for _, s := range cmd {
			if containsSubstr(s, "{endpoint}") {
				return nil, loopholeErrorf(
					"%s: 'host_daemon.cmd' names '{endpoint}' but publishes='socket' —"+
						" under that mode the daemon binds an AF_UNIX socket at the path"+
						" yolo substitutes into '{socket}', and yolo publishes the endpoint"+
						" file in front of it; write '{socket}'", manifestPath)
			}
		}
	}
	return &HostDaemon{Cmd: cmd, Env: env, Publishes: publishes, RequestEnd: requestEnd}, nil
}

func parseJailDaemon(manifestPath, name string, raw any) (*JailDaemon, error) {
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
	cmd := toStringSlice(cmdList)
	if err := refuseHostTokenInJailField(manifestPath, "'jail_daemon.cmd'", cmd); err != nil {
		return nil, err
	}
	cmd = substituteAll(cmd, tokenJailLoopholeDir, JailLoopholeDir(name))
	return &JailDaemon{Cmd: cmd, Restart: restart}, nil
}

// parseEnvMap builds an insertion-ordered EnvMap from a JSON object, coercing
// each key and value to a string. raw must already be resolved to a value that
// is either an *jsonx.OrderedMap or an empty-map sentinel.
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

// --- small decode helpers for `get(key) or default` idioms ---
// orEmptyList implements `get(key) or []`: a falsy value yields an empty list
// (which passes the list type check); a truthy non-list stays as-is (so the
// caller's type check fires the error).
func orEmptyList(m *jsonx.OrderedMap, key string) any {
	v, ok := m.Get(key)
	if !ok || !pyTruthy(v) {
		return []any{}
	}
	return v
}

// orEmptyMap implements `get(key) or {}` for the jail_env path.
func orEmptyMap(m *jsonx.OrderedMap, key string) any {
	v, ok := m.Get(key)
	if !ok || !pyTruthy(v) {
		return jsonx.NewOrderedMap()
	}
	return v
}

func orEmptyMapValue(v any) any {
	if !pyTruthy(v) {
		return jsonx.NewOrderedMap()
	}
	return v
}

// getOrNil returns m[key] or nil when absent.
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

// sortedListRepr renders a list literal of the sorted values.
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
