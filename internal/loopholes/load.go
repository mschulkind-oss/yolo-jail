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
	"runtime"
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

// LoadPackLoophole loads a loophole a PACK ships: the same tolerant read discovery
// uses, plus loopholedecl's pack-shipped subset (loophole-packaging.md §3.1, §2.1).
//
// TOLERANT and not strict, deliberately, and the two halves answer different
// questions. Version skew is orthogonal to the subset: a manifest key only a newer
// yolo knows must not make a pack's loophole VANISH (that is loadManifest's whole
// argument, and it applies with more force to a pack, which crosses the version
// boundary by construction), while a field a pack MAY NOT SHIP is refused whatever
// build reads it. So an unknown key is skipped and reported, and `jail_env` is
// refused. The strict-plus-subset pairing is the AUTHORING answer and lives in
// loopholedecl.LoadDirPackShipped, where `yolo pack lint` reaches it.
//
// PACK-SHIPPEDNESS IS THE CALLER'S FACT, not the manifest's. It is expressed by
// which loader you call rather than by a field or an option struct: a manifest
// cannot declare that a pack shipped it (it would just lie), and every caller that
// reads one already knows which of the four sources it came from. The returned
// record's Source is whatever resolve() sets; the caller labels it, exactly as
// loadFromDir does today.
func LoadPackLoophole(modulePath string) (*Loophole, error) {
	m, skipped, err := loopholedecl.LoadDirTolerant(modulePath)
	if err != nil {
		return nil, err
	}
	if perr := m.PackShippedError(loopholedecl.ManifestPath(modulePath)); perr != nil {
		return nil, perr
	}
	lp := resolve(m, modulePath)
	lp.SkewNotes = skipped
	return lp, nil
}

// PackShippedProblems reports how THIS resolved loophole would exceed the
// pack-shipped subset, for a caller holding a record rather than a manifest — the
// footprint and the pre-flight, which decode once and then pass records around.
//
// It projects back through loopholedecl rather than reimplementing the rules: two
// checkers over one subset is how a refusal and a report come to disagree about what
// a pack may ship, and here the disagreement would be a field refused at load and
// omitted from the consent string, or the reverse.
//
// One asymmetry to know: the projection carries the RESOLVED bind-mount hosts, so
// `{loophole_dir}` has already become an absolute path by the time it is checked.
// That is why the returned problems are for REPORTING, and LoadPackLoophole is the
// gate — it runs the subset on the raw manifest, where a module-relative host still
// looks module-relative.
func (l *Loophole) PackShippedProblems() []string {
	return l.subsetManifest().PackShippedProblems(loopholedecl.ManifestPath(l.Path))
}

// subsetManifest projects the record back onto a manifest for the subset to read.
//
// COMPLETE over every field the record carries, and that is a correction rather than
// a widening. This used to be a three-field literal (jail_env, host_bind_mounts,
// host_daemon) under a comment claiming the omissions were deliberate because "a
// future subset rule over a field this leaves out fails loudly (an empty answer for a
// declaration that is right there on the record)". THAT WAS EXACTLY BACKWARDS: an
// unprojected field reads as ABSENT, so the rule over it finds nothing to complain
// about and the report silently says the manifest is clean. It was measured on
// `ca_cert` — the field's own path-scope rule returned no problems for a record whose
// ca_cert was an arbitrary absolute host path, because the projection dropped it.
// A partial projection fails SILENTLY, in the granting direction, which is the one
// direction a subset report may never fail.
//
// So the rule is: project everything, and let a field the subset does not read cost
// nothing (it does — PackShippedProblems only looks at what it looks at).
// TestSubsetManifestProjectsEveryField pins the completeness, so the next field added
// to both types fails a test instead of quietly disappearing from the report.
//
// Copies are structural, not deep: schema.go makes loopholes.HostBindMount,
// HostDaemon, JailDaemon, Requires and EnvMap ALIASES of the loopholedecl types —
// there is one HostDaemon type, not two that happen to match — so slices and pointers
// are SHARED. Sound only because everything downstream reads: PackShippedProblems
// formats messages and mutates nothing.
//
// ONE ASYMMETRY, named because it cannot be fixed here: the record carries RESOLVED
// values, so `{loophole_dir}/ca.crt` and a module-relative bind host have already
// become absolute paths by the time the path-scope rules see them. That makes this
// projection OVER-report for a legal pack manifest, which is why its problems are for
// REPORTING and LoadPackLoophole is the gate — the gate runs the subset on the raw
// manifest, where a module-relative path still looks module-relative.
func (l *Loophole) subsetManifest() *loopholedecl.Manifest {
	return &loopholedecl.Manifest{
		Name:        l.Name,
		Description: l.Description,
		// The record's Enabled has already absorbed any config override, so projecting
		// it back onto DefaultEnabled is lossy in principle. It costs nothing in fact
		// (the subset reads neither), and the honest alternative — carrying the author's
		// declaration separately on the record purely so this projection could be exact —
		// would be a second boolean over one state, which is what OQ-A9 ruled against.
		DefaultEnabled: l.Enabled,
		Transport:      l.Transport,
		Lifecycle:      l.Lifecycle,
		Intercepts:     l.Intercepts,
		BrokerIP:       l.BrokerIP,
		CACert:         l.CACert,
		CACertSet:      l.CACertSet,
		JailEnv:        l.JailEnv,
		DoctorCmd:      l.DoctorCmd,
		DoctorCmdSet:   l.DoctorCmdSet,
		HostDaemon:     l.HostDaemon,
		JailDaemon:     l.JailDaemon,
		HostBindMounts: l.HostBindMount,
		HostDevices:    l.HostDevices,
		StateFiles:     l.StateFiles,
		Requires:       l.Requires,
		Platforms:      l.Platforms,
		PlatformsSet:   l.PlatformsSet,
		Serves:         l.Serves,
	}
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
		case strings.Contains(m.CACert, loopholedecl.TokenState):
			caCert = strings.ReplaceAll(m.CACert, loopholedecl.TokenState, StateDirFor(m.Name))
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

	// EVERY field, listed. This literal is the same shape as subsetManifest's, and
	// the same trap: it builds a NEW struct field by field, so a field left out
	// here does not survive load — it arrives as its ZERO value with nothing
	// anywhere reporting a problem. `Preamble` is where that bites hardest,
	// because its default is TRUE, so the drop is a silent DOWNGRADE to a
	// preamble-free connection rather than an obvious nil. It is the ca_cert drop
	// (see subsetManifest above) one type down.
	// TestManifestPreambleDefaultSurvivesLoad pins the round trip.
	var hostDaemon *HostDaemon
	if m.HostDaemon != nil {
		hostDaemon = &HostDaemon{
			Cmd:        substituteAll(m.HostDaemon.Cmd, loopholedecl.TokenLoopholeDir, hostDir),
			Env:        m.HostDaemon.Env,
			Publishes:  m.HostDaemon.Publishes,
			RequestEnd: m.HostDaemon.RequestEnd,
			Preamble:   m.HostDaemon.Preamble,
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
		Name:        m.Name,
		Description: m.Description,
		Path:        modulePath,
		// THIS LINE IS WHERE THE AUTHOR'S DEFAULT BECOMES THE USER'S SWITCH, and it is
		// the only place the two meet. `Manifest.DefaultEnabled` is what the pack said
		// when nobody had spoken; `Loophole.Enabled` is the resolved state, which
		// discovery then overwrites from the config key `loopholes.<name>.enabled` if
		// the user set it (discover.go's applyWorkspaceOverrides). Seeding the switch
		// with the default is what lets config stay an OVERRIDE rather than a third
		// value to reconcile (docs/design/loophole-activation.md OQ-A9).
		Enabled:       m.DefaultEnabled,
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
		Platforms:     m.Platforms,
		PlatformsSet:  m.PlatformsSet,
		Serves:        m.Serves,
		// SOURCE IS THE CALLER'S FACT and this is only the fail-safe default. A
		// manifest cannot say who shipped it (it would just lie), so every discovery
		// path relabels the record immediately — loadFromDir, loadModuleDirs and
		// ValidateLoopholes each assign the source they walked.
		//
		// It defaults to SourcePack because that is the LEAST-PRIVILEGED of the three
		// labels, on both axes that read it: MayRunHostCode refuses a SourcePack record
		// whose module dir carries no recorded gate decision, and moduleDirForPlacement
		// judges it rather than exempting it the way SourceBundled is exempted. So a
		// record that somehow reached a consumer unlabelled crosses nothing, instead of
		// inheriting the trust of yolo's own bundled content. It used to default to the
		// now-retired `user` label, which was the opposite direction.
		Source: SourcePack,
	}
}

// SupportedHere evaluates the manifest's `platforms` declaration against THIS
// machine (loophole-packaging.md §3.1). A loophole with no declaration is
// supported everywhere, so this is safe to call unconditionally.
//
// It is a separate predicate from RequirementsMet, and that separation is the
// point. `requires` answers "is the thing I need present" — a probe with a fix
// ("install it"). This answers "does this loophole exist for this machine at all",
// which has no fix. Collapsing them is precisely the misattribution the field was
// added to end: a compiled Linux daemon reported on macOS as an unmet prerequisite
// sends the reader after something to install, and there is nothing to install.
//
// EVALUATED AGAINST runtime.GOOS/GOARCH UNCONDITIONALLY, in-jail included, and
// that is deliberate rather than an oversight of the inJail() branch next door.
// The question is "what will the machine that spawns the host daemon be", and
// inside a jail that machine IS the container — a nested launch spawns host
// daemons, binds mounts and publishes endpoints identically (§4.3a: runtime.go's
// device skip is the only jail-aware branch in the runtime), and §4.3a rules the
// nested jail THE development environment for a loophole. Skipping the check there
// would let the development environment spawn exactly the binary the field exists
// to refuse.
//
// The residual, named rather than discovered: `yolo loopholes list` INSIDE a jail
// on a macOS host evaluates a `platforms: ["darwin"]` loophole against the
// container's linux and calls it unsupported, while the host did cross its wiring.
// That answer is wrong for the listing and right for the spawn, one distinction
// short of correct (host-role vs nested-host-role is not a distinction this
// codebase draws anywhere), and it costs a misleading line in a report where the
// alternative costs a daemon that cannot run.
func (l *Loophole) SupportedHere() bool {
	return l.supportsPlatform(runtime.GOOS, runtime.GOARCH)
}

// UnsupportedHereReason is SupportedHere's message half: the by-name report §3.1
// asks for — what this machine is, what the loophole supports, and that nothing is
// missing — or ("", false) when the platform is supported.
func (l *Loophole) UnsupportedHereReason() (string, bool) {
	return l.platformUnsupportedReason(runtime.GOOS, runtime.GOARCH)
}

// supportsPlatform / platformUnsupportedReason take the pair explicitly so every
// OS/arch combination is testable from one process, the same reason
// loopholedecl.Manifest.SupportsPlatform does. They delegate to the schema package
// rather than reimplementing the match: two matchers over one declaration is how a
// report and a gate come to disagree.
func (l *Loophole) supportsPlatform(goos, goarch string) bool {
	return l.platformManifest().SupportsPlatform(goos, goarch)
}

func (l *Loophole) platformUnsupportedReason(goos, goarch string) (string, bool) {
	reason := l.platformManifest().PlatformsUnsupportedReason(goos, goarch)
	return reason, reason != ""
}

// platformManifest is the shim that lets the schema package own the matching. The
// declaration is carried on the record verbatim (Platforms/PlatformsSet), so this
// is a projection, not a re-decode.
func (l *Loophole) platformManifest() *loopholedecl.Manifest {
	return &loopholedecl.Manifest{Platforms: l.Platforms, PlatformsSet: l.PlatformsSet}
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
