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

	"github.com/mschulkind-oss/yolo-jail/internal/config"
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

// subsetManifest projects the record back onto the fields the subset reads.
//
// A three-field literal rather than a copy loop, because schema.go makes
// loopholes.HostBindMount and loopholes.HostDaemon ALIASES of the loopholedecl
// types — there is one HostDaemon type, not two that happen to match — so there is
// nothing to convert. The slice and the pointers are SHARED, which is sound only
// because everything downstream of here reads: PackShippedProblems formats messages
// and mutates nothing.
//
// Deliberately partial: a field the subset does not read is not projected, so a
// future subset rule over a field this leaves out fails loudly (an empty answer for
// a declaration that is right there on the record) rather than quietly passing.
func (l *Loophole) subsetManifest() *loopholedecl.Manifest {
	return &loopholedecl.Manifest{
		JailEnv:        l.JailEnv,
		HostBindMounts: l.HostBindMount,
		HostDaemon:     l.HostDaemon,
	}
}

// PlacementProblems applies §4.3a's PLACEMENT rule to this loophole's MANIFEST
// faces: its module dir, its `host_daemon.cmd` and its `doctor_cmd`.
//
// The rule's config faces (an inline entry's `command`/`doctor_cmd`) were already
// wired; the manifest faces were the "still owed" half of landing item 1a, and they
// need this package because two of the three inputs are runtime resolutions. The
// argvs are passed POST-substitution — Path and the Cmd fields on a resolved record
// already have {loophole_dir} expanded — which is the only spelling the check can
// use: an unsubstituted element carries a `{`, and the rule skips those as framework
// placeholders.
//
// workspace is the workspace being mounted, or "" for a caller that has none (the
// doctor path), which narrows the rule to the jail-home tree rather than disabling
// it.
func (l *Loophole) PlacementProblems(workspace string) []string {
	return config.LoopholeManifestPlacementProblems(config.LoopholeManifestPlacement{
		Name:          l.Name,
		ModuleDir:     l.moduleDirForPlacement(),
		HostDaemonCmd: l.hostDaemonCmd(),
		DoctorCmd:     l.DoctorCmd,
	}, workspace)
}

// moduleDirForPlacement is the module dir the placement rule should judge, or ""
// when there is none to judge.
//
// A CONFIG loophole has no module dir at all — Path holds the synthetic
// "<yolo-jail.jsonc:loopholes.name>" marker, which is not a path and must not be
// resolved as one. Its `command` is checked by the config faces, which is where a
// config entry belongs.
func (l *Loophole) moduleDirForPlacement() string {
	if l.FromConfig() {
		return ""
	}
	return l.Path
}

func (l *Loophole) hostDaemonCmd() []string {
	if l.HostDaemon == nil {
		return nil
	}
	return l.HostDaemon.Cmd
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
		Platforms:     m.Platforms,
		PlatformsSet:  m.PlatformsSet,
		Source:        SourceUser,
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

// --- THE INERT REPORT: "this loophole does nothing here, and here is why."
//
// §3.1 and §8 are the same situation on two axes, and the design is explicit that
// they share ONE message and ONE mechanism: platform (darwin vs linux) and backend
// (`container` and `macos-user` skip loopholes entirely) both answer that one
// sentence. Two mechanisms would give two half-messages for one user-visible
// situation, which is the B-0 shape — "a backend that looked provisioned and
// configured nothing" — that the run pipeline was restructured to end.
//
// So the mechanism is a VALUE, not a print: producers hand back notes, one caller
// renders them. This file supplies the PLATFORM producer, because the platform
// declaration is evaluated here; the BACKEND producer belongs with the code that
// knows which backend is running, and plugs in by constructing an InertNote with
// AxisBackend. Neither producer prints, so neither can print twice.

// Inert-report axes. An axis says WHICH fact made the loophole inert, so a reader
// looking at two lines can tell "wrong machine" from "wrong backend" without
// parsing the sentence.
const (
	// AxisPlatform: the manifest's `platforms` declaration excludes this
	// GOOS/GOARCH. Nothing can be installed to fix it.
	AxisPlatform = "platform"
	// AxisBackend: the container backend skips loopholes wholesale (§8 — Apple
	// Container returns before any external service starts; macos-user returns
	// before startLoopholes is reached at all). The loophole is fine; the backend
	// does not carry it.
	AxisBackend = "backend"
)

// InertNote is one loophole that will do nothing on this machine, with the reason
// and the axis it came from.
//
// Name is carried separately from the sentence so a caller can group, sort or
// filter by loophole without string surgery — §3.1 requires the report be BY NAME,
// and a name that only exists inside a rendered sentence is a name no other code
// can use.
type InertNote struct {
	Name string
	Axis string
	// Reason continues "loophole <name> is ": a clause, not a sentence, and never
	// capitalized. It carries its own trailing detail (the platforms the loophole
	// does support, the backend that skipped it).
	Reason string
}

// Line renders the note as the one line a user sees. THE single rendering, so the
// platform axis and the backend axis cannot drift into two shapes.
func (n InertNote) Line() string {
	return "loophole " + n.Name + " is " + n.Reason
}

// PlatformInertNotes evaluates the `platforms` declaration of every loophole
// against THIS machine and returns one note per loophole that is unsupported here.
//
// ONCE PER LOOPHOLE, by name: discovery merges four sources and a launch calls it
// from several places, so the dedup lives here rather than in each caller. A
// duplicated "unsupported on linux/amd64" line reads as two problems.
//
// A loophole DISABLED by the user is skipped: it is inert for a reason the user
// chose, already reported as "disabled", and telling them a switched-off loophole
// also has the wrong platform is noise. `requires` is deliberately NOT consulted —
// the whole point of the axis split is that an unmet requirement and an unsupported
// platform are different answers with different fixes, so a loophole that is both
// gets the categorical one.
func PlatformInertNotes(lps []*Loophole) []InertNote {
	var out []InertNote
	seen := map[string]bool{}
	for _, lp := range lps {
		if lp == nil || !lp.Enabled || seen[lp.Name] {
			continue
		}
		reason, ok := lp.UnsupportedHereReason()
		if !ok {
			continue
		}
		seen[lp.Name] = true
		out = append(out, InertNote{Name: lp.Name, Axis: AxisPlatform, Reason: reason})
	}
	return out
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
