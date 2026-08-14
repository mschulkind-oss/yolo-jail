package loopholes

// load.go is the RESOLUTION half of loading a loophole: the schema and its static
// validation live in internal/loopholedecl (a leaf, so the pack footprint can read
// a manifest without importing this package — docs/design/loophole-packaging.md
// §3.2), and what remains here is everything that needs to know facts about THIS
// MACHINE.
//
// The split is exactly "does it need the world":
//
//   - loopholedecl decides whether `{jail_loophole_dir}` is legal in
//     `host_daemon.cmd` — a statement about the schema.
//   - this file decides what `{loophole_dir}` RESOLVES TO — the module's real path
//     after symlinks, where yolo keeps per-loophole state, what `$XDG_RUNTIME_DIR`
//     holds right now. None of those are facts about the manifest.

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// LoopholeError is raised when a manifest is malformed.
//
// An alias, not a wrapper: the errors now come from the schema package, and one
// error type means a caller that already knows this name keeps working. Discovery
// warns and continues on it (loadFromDir); ValidateLoopholes surfaces its message.
type LoopholeError = loopholedecl.Error

// LoadLoophole loads a single loophole from its directory.
func LoadLoophole(modulePath string) (*Loophole, error) {
	return loadManifest(modulePath)
}

// loadManifest decodes the manifest and resolves it into a runtime record.
//
// It reads TOLERANTLY (loopholedecl.LoadDirTolerant) because that is discovery's
// contract: a manifest key this build does not know must not make the loophole
// VANISH — loadFromDir's failure mode is "no host daemon, no endpoint, no env var,
// no entry in `yolo loopholes list`, and a downstream error that names something
// else". A key only a newer yolo knows is version skew, not corruption, so it is
// skipped rather than refused. The STRICT decoder (loopholedecl.Decode) is for
// authoring tools, where an author must hear about a typo.
func loadManifest(modulePath string) (*Loophole, error) {
	m, skipped, err := loopholedecl.LoadDirTolerant(modulePath)
	if err != nil {
		return nil, err
	}
	lp := resolve(m, modulePath)
	lp.SkewNotes = skipped
	return lp, nil
}

// resolve turns a decoded manifest into the runtime record, substituting the
// module-dir tokens and resolving the CA path.
//
// The two host-side dirs are deliberately NOT the same string, and that predates
// this split: the command fields substitute {loophole_dir} with the symlink-
// resolved absolute path (resolvePath), while a bind mount's host substitutes the
// lexically-cleaned module path. Preserved verbatim — RuntimeArgsFor's mount
// arguments are byte-compared by tests, and "resolve" is not the change that
// should move them.
func resolve(m *loopholedecl.Manifest, modulePath string) *Loophole {
	hostDir := resolvePath(modulePath)
	mountDir := filepath.Clean(modulePath)

	caCert := ""
	if m.CACertSet {
		switch {
		case strings.Contains(m.CACert, "{state}"):
			caCert = strings.ReplaceAll(m.CACert, "{state}", StateDirFor(m.Name))
		case filepath.IsAbs(m.CACert):
			// An absolute ca_cert must be used as-is, discarding module_path.
			// filepath.Join would instead concatenate ("<module>/<abs>"), producing
			// a bogus path that then fails HasCA() and silently drops the CA mount +
			// NODE_EXTRA_CA_CERTS.
			caCert = resolvePath(m.CACert)
		default:
			caCert = resolvePath(filepath.Join(modulePath, m.CACert))
		}
	}

	var doctorCmd []string
	if m.DoctorCmdSet {
		doctorCmd = substituteAll(m.DoctorCmd, loopholedecl.TokenLoopholeDir, hostDir)
	}

	var hostDaemon *HostDaemon
	if m.HostDaemon != nil {
		hostDaemon = &HostDaemon{
			Cmd:        substituteAll(m.HostDaemon.Cmd, loopholedecl.TokenLoopholeDir, hostDir),
			Env:        m.HostDaemon.Env,
			Publishes:  m.HostDaemon.Publishes,
			RequestEnd: m.HostDaemon.RequestEnd,
		}
	}

	var jailDaemon *JailDaemon
	if m.JailDaemon != nil {
		jailDaemon = &JailDaemon{
			Cmd:     substituteAll(m.JailDaemon.Cmd, loopholedecl.TokenJailLoopholeDir, JailLoopholeDir(m.Name)),
			Restart: m.JailDaemon.Restart,
		}
	}

	var bindMounts []HostBindMount
	if m.HostBindMounts != nil {
		bindMounts = []HostBindMount{}
		for _, bm := range m.HostBindMounts {
			bindMounts = append(bindMounts, HostBindMount{
				Host:      expandEnv(strings.ReplaceAll(bm.Host, loopholedecl.TokenLoopholeDir, mountDir)),
				Container: bm.Container,
				Readonly:  bm.Readonly,
			})
		}
	}

	return &Loophole{
		Name:          m.Name,
		Description:   m.Description,
		Path:          modulePath,
		Enabled:       m.Enabled,
		Transport:     m.Transport,
		Lifecycle:     m.Lifecycle,
		Intercepts:    m.Intercepts,
		BrokerIP:      m.BrokerIP,
		CACert:        caCert,
		CACertSet:     m.CACertSet,
		JailEnv:       m.JailEnv,
		DoctorCmd:     doctorCmd,
		DoctorCmdSet:  m.DoctorCmdSet,
		HostDaemon:    hostDaemon,
		JailDaemon:    jailDaemon,
		HostBindMount: bindMounts,
		HostDevices:   m.HostDevices,
		StateFiles:    m.StateFiles,
		Requires:      m.Requires,
		Source:        SourceUser,
	}
}

// substituteAll replaces token with value in every element of args, returning a
// new slice so the decoded manifest is never mutated.
func substituteAll(args []string, token, value string) []string {
	out := make([]string, len(args))
	for i, s := range args {
		out[i] = strings.ReplaceAll(s, token, value)
	}
	return out
}
