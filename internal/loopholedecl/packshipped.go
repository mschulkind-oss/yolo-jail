package loopholedecl

// packshipped.go is the PACK-SHIPPED SUBSET of the manifest
// (docs/design/loophole-packaging.md §3.1, "The pack-shipped subset of the
// manifest, corrected", plus §2.1's review ruling).
//
// Four declarations a bundled loophole may make are refused when a PACK ships the
// loophole. The asymmetry is the point and it is not squeamishness: a bundled
// manifest is yolo's own code in yolo's own repository, reviewed by whoever
// reviews yolo, while a pack-shipped manifest is a distributed artifact that
// lands on a stranger's machine. So the subset is not "the safe fields" — it is
// "the fields whose enforcement does not depend on reading somebody else's
// source".
//
// # Why the subset lives HERE and not in the pack loader
//
// Every one of these refusals is a statement about the SCHEMA — a key, a value, a
// path shape — decidable from the manifest bytes and a single boolean the manifest
// cannot carry ("did a pack ship this?"). That boolean is the caller's, which is
// why this is a method taking no world and no options struct: the pack loader, the
// footprint, `pack lint` and discovery each know the answer and none of them can
// import the runtime (see the package doc's cycle measurement).
//
// # What is NOT here
//
// The RESERVED-NAME refusal (a pack shipping `journal`, `cgroup-delegate` or one of
// the three bundled names) is deliberately absent. §3.1 requires the reserved set be
// defined ONCE — `paths.Builtin*LoopholeName` plus the bundled names — and this
// package may not import `internal/paths`. It belongs in the pack pre-flight, which
// already has the set in hand.

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// PackShippedProblems reports every way this manifest exceeds what a PACK may
// ship: one problem per violating declaration, each naming what to write instead.
// An empty result means the manifest is inside the subset.
//
// manifestPath supplies the message prefix (the same `<dir>/manifest.jsonc` every
// other refusal in this package uses). PURE — it reads the decoded manifest and
// nothing else, so a caller may apply it to a manifest it decoded strictly (the
// authoring path) or tolerantly (discovery) without the two answers diverging.
//
// Every problem is reported, not just the first: an author fixing four things
// should not need four edit-check cycles. That is packdecl.Decode's contract
// rather than the structural walk's first-problem one, and it is the right one
// here because these are independent declarations, not a parse.
func (m *Manifest) PackShippedProblems(manifestPath string) []string {
	var out []string
	out = append(out, m.packJailEnvProblems(manifestPath)...)
	out = append(out, m.packBindMountProblems(manifestPath)...)
	out = append(out, m.packCACertProblems(manifestPath)...)
	out = append(out, m.packPublishesProblems(manifestPath)...)
	return out
}

// PackShippedError renders PackShippedProblems as one *Error, or nil when the
// manifest is inside the subset.
//
// Typed *Error rather than error, so a caller assigning it into an error variable
// has to do so deliberately: `var err error = m.PackShippedError(p)` on a clean
// manifest would otherwise be a non-nil interface holding a nil pointer, which is
// the classic way a refusal-checking loader comes to refuse everything.
func (m *Manifest) PackShippedError(manifestPath string) *Error {
	problems := m.PackShippedProblems(manifestPath)
	if len(problems) == 0 {
		return nil
	}
	return &Error{problems: problems}
}

// LoadDirPackShipped reads <dir>/manifest.jsonc, decodes it STRICTLY, and applies
// the pack-shipped subset — the AUTHORING seam (`yolo pack lint`, `pack init`, the
// footprint's own decode), where an author must hear about both a typo and a field
// their pack may not ship.
//
// Discovery wants the TOLERANT decode with the same subset applied, and does not
// get a loader here: the tolerant read has to be paired with token resolution
// against a real module path, so the pairing lives in `internal/loopholes`
// (LoadPackLoophole) where those facts are known. Two loaders in two packages
// rather than four in one, and neither can silently skip the subset.
func LoadDirPackShipped(dir string) (*Manifest, error) {
	m, err := LoadDir(dir)
	if err != nil {
		return nil, err
	}
	if perr := m.PackShippedError(ManifestPath(dir)); perr != nil {
		return nil, perr
	}
	return m, nil
}

// packJailEnvProblems refuses `jail_env` (§3.1, first table row).
//
// THE REASON IS THE COST OF A CROSS-KIND COLLISION PASS, not an invariant about
// disjoint namespaces. `jail_env` emits `-e K=V` (internal/loopholes/runtime.go),
// which is the `env` contribution kind's target namespace — and packload.Collisions
// keys on {kind, target}, so two DIFFERENT kinds claiming one target can never
// collide there. Draft 1 justified this with "today every kind's namespace is
// disjoint by luck"; that is FALSE and worth stating so nobody restores it:
// `program` and `launch` already share the bin-name namespace BY DESIGN, and the
// census pack declares both on `censusbin`. Nothing is being preserved. What is
// being avoided is a fourth bespoke collision pass beside the three already there.
//
// AND THE COST IS REAL, so it is stated rather than hidden. A loophole's `jail_env`
// is CONDITIONAL on the loophole being ACTIVE; the `env` kind is UNCONDITIONAL.
// `audio` relies on exactly that conditionality — PULSE_SERVER only makes sense
// once the sockets crossed — so a pack-shipped audio-shaped loophole routed through
// `env` would set the variable even when the loophole is inactive, pointing a
// PulseAudio client at a socket that is not there. That is OQ-LP5, and it is
// resolved by the first real pack that wants conditional env: the fix is the
// cross-kind pass, which is purely additive.
func (m *Manifest) packJailEnvProblems(manifestPath string) []string {
	if m.JailEnv == nil || m.JailEnv.Len() == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"%s: 'jail_env' is not available to a pack-shipped loophole — declare the"+
			" variables with the pack's `env` contribution kind instead:"+
			" {\"kind\": \"env\", \"vars\": {%s}}, which the pack footprint already"+
			" reports and collides on. Note the difference you are accepting: `env` is"+
			" UNCONDITIONAL, while `jail_env` only applied when the loophole was active"+
			" (loophole-packaging.md §3.1, OQ-LP5)",
		manifestPath, envVarsHint(m.JailEnv))}
}

// envVarsHint renders the declared jail_env as the `env` kind's `vars` object, so
// the refusal hands back the author's own keys rather than a schema sketch they
// then have to fill in.
func envVarsHint(env *EnvMap) string {
	parts := make([]string, 0, env.Len())
	for _, k := range env.Keys() {
		v, _ := env.Get(k)
		parts = append(parts, pytext.Repr(k)+": "+pytext.Repr(v))
	}
	return strings.Join(parts, ", ")
}

// packBindMountProblems applies §3.1 requirements 1 and 3 to every bind mount.
func (m *Manifest) packBindMountProblems(manifestPath string) []string {
	var out []string
	for i, bm := range m.HostBindMounts {
		field := fmt.Sprintf("host_bind_mounts[%d]", i)
		if prob := packBindHostProblem(manifestPath, field, bm.Host); prob != "" {
			out = append(out, prob)
		}
		if !bm.Readonly {
			out = append(out, packWritableBindProblem(manifestPath, field, bm))
		}
	}
	return out
}

// packBindHostProblem constrains a pack-shipped `host_bind_mounts[].host` to the
// same namespace the `mount` contribution kind uses (§3.1 requirement 1).
//
// DRAFT 1 AUDITED THE WRONG AXIS. It checked rw-versus-ro and missed PATH SCOPE,
// which is where a loophole bind really is the back door around `mount` that the
// design forbids. Compare the two on both axes:
//
//   - `mount` refuses absolute paths, ".." and ":" (packdecl.appendPathProblems),
//     so it is HOME-RELATIVE ONLY — and packload.HonoredMounts refuses EVERY mount
//     when the pack may not access the host ("a FETCHED pack cannot read your host
//     home").
//   - a loophole's `host` is any non-empty string, and `$HOME`, ".." and "/" all
//     pass expandEnv on the way to `-v <host>:<container>:ro`.
//
// So the vocabulary a pack gets here is: a path inside its OWN module dir
// ({loophole_dir}/...), or a path relative to the user's home. `$VAR` expansion is
// refused as well as absolute paths, because a manifest saying "${XDG_RUNTIME_DIR}"
// names an absolute path one indirection later — refusing the literal "/" while
// admitting the variable that expands to one would be a rule about spelling.
//
// A BUNDLED loophole keeps the wider vocabulary, and must: `audio` names
// /run/user/<uid>/pulse, which no home-relative path can reach.
func packBindHostProblem(manifestPath, field, host string) string {
	fix := " A pack-shipped loophole may name a path inside its own module dir" +
		" ('" + TokenLoopholeDir + "/<file>', which is content the pack ships and" +
		" packstage already vetted) or a path relative to your home ('Documents/x')" +
		" — the same namespace the `mount` contribution kind uses. For a host path" +
		" outside both, the loophole has to be bundled with yolo, or declare a" +
		" `host_daemon` that mediates the access (loophole-packaging.md §3.1)."
	clause := packPathScopeClause(host)
	if clause == "" {
		return ""
	}
	return fmt.Sprintf("%s: %s.host = %s %s.%s",
		manifestPath, field, pytext.Repr(host), clause, fix)
}

// packPathScopeClause classifies one path-bearing value against the shapes a
// pack-shipped loophole may name, returning the clause that says what is wrong (no
// leading field, no trailing fix) or "" when the value is inside the namespace.
//
// SHARED between `host_bind_mounts[].host` and `ca_cert` deliberately: they are the
// two fields whose value becomes a `-v <host>:...` argument, so one classifier is
// what keeps them from disagreeing about whether "${HOME}/x" is absolute. The FIX
// differs per field (the legal namespaces are not the same — a bind host may be
// home-relative, a ca_cert may name '{state}') so the caller supplies that half.
func packPathScopeClause(value string) string {
	switch {
	case strings.Contains(value, "$"):
		return "expands an environment variable, and a pack-shipped loophole may not:" +
			" '${XDG_RUNTIME_DIR}' names an absolute host path one indirection later, so" +
			" admitting the variable while refusing the literal would be a rule about" +
			" spelling"
	case strings.HasPrefix(value, "/"):
		return "is an absolute host path, and a pack-shipped loophole may not name one"
	case hasDotDotSegment(value):
		return "contains a '..' segment, which walks out of the namespace it is relative to"
	case strings.Contains(value, ":"):
		// The same reason packdecl refuses it: the container runtime parses a colon
		// as the mount-option separator, so part of the path silently becomes a flag.
		return "contains ':', which the container runtime parses as the mount-option" +
			" separator — part of the path would silently become a flag"
	}
	return ""
}

// packCACertProblems path-scopes a pack-shipped `ca_cert` (§3.1 requirement 1, on the
// field draft 1's table and its first implementation both left out).
//
// IT IS THE SHARPEST OF THE PATH-BEARING FIELDS, not the mildest, which is why an
// absolute one cannot be admitted while an absolute bind host is refused. A ca_cert
// does everything a `:ro` bind does — internal/loopholes' RuntimeArgsFor emits
// `-v <ca_cert>:<jail module dir>/ca.crt:ro` when nothing else already carries it —
// and then one thing more: the container-side path is joined into
// `-e NODE_EXTRA_CA_CERTS`, so every node client in the jail TRUSTS that file as a
// certificate authority. An absolute value would let a pack name any file on the
// machine, and the resolver hands it through as-is (an absolute ca_cert deliberately
// discards module_path, or filepath.Join would produce '<module>/<abs>').
//
// The legal shapes are the pack's own content and its own state dir: a plain relative
// path (which the resolver joins onto the staged module dir, and packstage vetted that
// tree), or '{state}/x' — StateDirFor(<name>) under yolo's own state tree, which is
// name-keyed and therefore survives restaging. That second one is what makes a
// pack-shipped CA possible at all: a CA regenerated on every launch would break every
// long-lived TLS client in the jail.
//
// A BUNDLED loophole keeps the wider vocabulary, and the broker needs it: it names
// '{state}/ca.crt' (inside the subset anyway) while `audio` shows the general case.
func (m *Manifest) packCACertProblems(manifestPath string) []string {
	if !m.CACertSet {
		return nil
	}
	// A LEADING '{state}' is a path yolo chooses, so the scope question is about the text
	// AFTER it: '{state}/ca.crt' is in scope while '{state}/../../x' walks out of it.
	// Stripped as a PREFIX only, so a token spelled mid-path cannot launder an otherwise
	// absolute value. Note which token is NOT handled here: '{loophole_dir}' is not
	// substituted in `ca_cert` at all (the resolver joins a relative value onto the module
	// dir directly, and the token's legal fields are host_daemon.cmd, doctor_cmd and
	// host_bind_mounts[].host), so it needs no special case — and the message deliberately
	// offers a plain relative path rather than a spelling that resolves to nothing.
	probe, _ := stripPathToken(m.CACert, TokenState)
	clause := packPathScopeClause(probe)
	if clause == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s: 'ca_cert' = %s %s. A pack-shipped loophole may name"+
		" a certificate it SHIPS (a plain relative path like 'ca.crt', which resolves inside"+
		" its own module dir) or one inside its own state dir ('%s/ca.crt', which yolo owns"+
		" and which survives restaging). The file is bind-mounted from your host AND joined"+
		" into NODE_EXTRA_CA_CERTS, so it is trusted by every node client in the jail —"+
		" naming an arbitrary host path would hand the jail a CA you never chose. For"+
		" anything outside both, the loophole has to be bundled with yolo"+
		" (loophole-packaging.md §3.1)",
		manifestPath, pytext.Repr(m.CACert), clause, TokenState)}
}

// stripPathToken removes a leading token and the '/' after it, reporting whether it was
// there. Separate from a bare TrimPrefix because the remainder must not keep the
// separator: '{state}/ca.crt' minus the token is '/ca.crt', which every absolute-path
// check would then reject — and rejecting the one spelling the design requires is worse
// than not checking at all.
func stripPathToken(value, token string) (string, bool) {
	if !strings.HasPrefix(value, token) {
		return value, false
	}
	return strings.TrimPrefix(strings.TrimPrefix(value, token), "/"), true
}

// hasDotDotSegment reports a ".." PATH SEGMENT, not the substring: "..hidden" and
// "a..b" are ordinary names, and refusing them would reject working manifests for
// looking suspicious.
func hasDotDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// packWritableBindProblem keeps §3.1 requirement 3 — `readonly: false` stays
// refused — and says WHAT THE REFUSAL ACTUALLY COVERS, which is narrower than
// draft 1 claimed.
//
// MEASURED TWICE IN THIS REPO: a read-only bind of an AF_UNIX socket is fully
// connectable and BIDIRECTIONAL. The kernel's read-only check exempts inodes that
// are not REG/DIR/LNK, which is the well-known docker.sock:ro result. So this
// refusal buys NOTHING for a socket — a pack can bind a host socket `:ro` (a
// container socket, ssh-agent, gpg-agent, the PipeWire daemon) and get unrestricted
// read-write access to whatever is behind it. What it covers is REGULAR FILES AND
// DIRECTORIES.
//
// Written down here rather than left to be re-derived, because the inference runs
// the other way so naturally: review once argued that a `:ro` socket bind "passes
// no audio", and the audio example was priced with a cost it does not have. Two
// lenses contradicted each other and the measurement decided it. The refusal is a
// no-op for sockets in BOTH directions — it neither protects nor breaks.
func packWritableBindProblem(manifestPath, field string, bm HostBindMount) string {
	return fmt.Sprintf(
		"%s: %s.readonly = false — a pack-shipped loophole may not ask for a WRITABLE"+
			" host bind; omit the key, which defaults to true. If %s is an AF_UNIX socket"+
			" you lose nothing: a read-only bind of a socket is fully connectable and"+
			" bidirectional (measured — the kernel exempts non-REG/DIR/LNK inodes from the"+
			" read-only check), so this rule only ever covers regular files and"+
			" directories. If the pack genuinely has to WRITE a host file, declare a"+
			" `host_daemon` that mediates it (loophole-packaging.md §3.1)",
		manifestPath, field, pytext.Repr(bm.Host))
}

// packPublishesProblems enforces §2.1's REVIEW RULING: a pack-shipped loophole may
// only say `publishes: "socket"`.
//
// The transport is a property of the FRAMEWORK, not of the loophole, and §2.3's
// enforcement asymmetry is why. On the server side every security-critical property
// is invisible to yolo — the endpoint file's mode, whether the key persists, whether
// the token compare is constant-time, whether the pre-allocation length is capped —
// so shipping other people's TLS-server code to strangers' machines is a materially
// different proposition from a hand-written config entry on one machine. Under
// `publishes: "socket"` a third party cannot get any of those wrong because they
// never write them: they bind a plain AF_UNIX socket and yolo runs the front.
//
// `publishes: "endpoint"` stays available to a BUNDLED loophole (all three use it),
// because that is yolo's own code publishing yolo's own credential.
//
// THE DEFAULT IS REFUSED TOO, and that is deliberate rather than an oversight: an
// absent `publishes` decodes to PublishesEndpoint, so a pack-shipped daemon that
// says nothing about publication has declared the mode it may not have. Since the
// fix is identical either way ("write publishes: socket"), the manifest needs no
// declared-versus-defaulted bit to carry a better message.
func (m *Manifest) packPublishesProblems(manifestPath string) []string {
	if m.HostDaemon == nil || m.HostDaemon.Publishes == PublishesSocket {
		return nil
	}
	return []string{fmt.Sprintf(
		"%s: 'host_daemon.publishes' is %s, and a pack-shipped loophole must declare"+
			" \"publishes\": \"socket\" — the transport belongs to the framework, not to the"+
			" loophole. Bind a plain AF_UNIX socket at the path yolo substitutes into"+
			" '%s' and yolo runs the TLS front over it and publishes the endpoint file"+
			" for you, so the endpoint's file mode, its key persistence, its"+
			" constant-time compare and its length cap are yolo's code rather than"+
			" yours. Self-publishing stays available to a loophole BUNDLED with yolo,"+
			" which is yolo's own code publishing yolo's own credential"+
			" (loophole-packaging.md §2.1)",
		manifestPath, pytext.Repr(m.HostDaemon.Publishes), "{socket}")}
}
