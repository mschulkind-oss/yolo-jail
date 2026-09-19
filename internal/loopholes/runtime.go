package loopholes

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// warnSink/infof are the package's log sinks.
//
// warnSink writes to STDERR by default. It used to be a no-op "for callers that
// install a sink", and in the whole tree no caller ever did — so every warning
// this package emitted went nowhere. That is tolerable for "skipped a bind mount"
// and NOT tolerable for "this loophole failed to load and is therefore absent",
// which is otherwise invisible at launch (see loadModuleDirs). A warning nobody can
// read is not a diagnostic.
//
// infof stays a no-op: its one use is a routine in-jail device skip that happens
// on every launch and says nothing actionable.
var (
	warnSink = func(msg string) {
		fmt.Fprintln(os.Stderr, "warning: "+msg)
	}
	infof = func(format string, args ...any) {}

	// saidWarnings is the set of lines already said, and it is why warnf below is a
	// function rather than the swappable var it used to be: the dedup has to sit ABOVE
	// the sink, or a test that installs its own sink would measure a different rule than
	// the one that ships.
	saidWarnings = map[string]bool{}
)

// warnf reports a diagnostic, SAYING EACH DISTINCT LINE ONCE.
//
// The rule is here because A LAUNCH DISCOVERS ONCE PER CONSUMER, not once. Each host-side
// consumer builds its OWN Set through NewHostSet — the briefing, the broker gate, the
// argv's broker gate, the runtime args and the daemon spawn each do — and every
// construction re-walks the module dirs and re-warns about them. The convergence
// (docs/reference/loophole-system.md#selection-and-discovery) collapsed seven independent
// ASSEMBLIES into one
// constructor; it did not collapse them into one RESOLUTION, and was never meant to. So a
// launch said each of its diagnostics once per pass: measured on a real host 2026-09-09,
// four missing module dirs printed twenty lines, which buries four facts in a count.
//
// NOT SUPPRESSION: the first occurrence of every distinct line is always said, so a
// genuinely absent module dir is still reported exactly as loudly as OQ-A9 requires. What
// is dropped is a repetition of a line already on screen, which carried no new fact.
//
// PER PROCESS, which is per launch for every way the host runs this (one `yolo`, one
// launch). The one exception is the in-process capture sub-launch, and it costs nothing:
// each launch's messages name its OWN staging root, so two launches collide on a line only
// when they are reporting the same missing directory — the same fact, said once.
func warnf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if saidWarnings[msg] {
		return
	}
	saidWarnings[msg] = true
	warnSink(msg)
}

// resetSaidWarnings forgets what has been said. For tests, which must each measure the
// rule from a clean slate — a line another test already said would otherwise be silent
// here, which is a false green in the direction that matters.
func resetSaidWarnings() { saidWarnings = map[string]bool{} }

// (podman) path; pass "container" for Apple Container (which skips any loophole
// declaring `intercepts`). It is side-effect free and idempotent.
//
// A SourcePack record is NEVER HONORED here, whatever the caller intended: with no
// origin gate in hand its declarations are dropped. Same door, same nail as
// RunDoctorChecks below — see gateAdmitsCrossing. A caller that DID evaluate the gate
// says so by going through Set.RuntimeArgsFor.
func RuntimeArgsFor(loopholes []*Loophole, runtime string) []string {
	return runtimeArgsFor(loopholes, runtime, nil, nil)
}

// RuntimeArgsFor builds the container args for the given records WITH THIS SET'S ORIGIN
// GATE applied: a pack-contributed loophole's binds, devices, intercepts, CA and jail_env
// reach the argv only when the caller recorded that its pack's host access is approved.
// Everything else behaves exactly as the package-level function.
func (s Set) RuntimeArgsFor(from []*Loophole, runtime string) []string {
	return runtimeArgsFor(from, runtime, &s, nil)
}

// RuntimeArgsForWithJailDaemons is Set.RuntimeArgsFor with ADDITIONAL entries joined
// into the SAME YOLO_JAIL_DAEMONS payload — the doorway pack services walk
// (packdecl.KindService, docs/reference/wire-bridge.md §2.1). The env var is ONE frozen
// contract with ONE writer, and this method is what keeps it that way: a service's
// daemon ({name, cmd, restart}, the loophole JailDaemon shape verbatim) is appended
// here rather than emitted by a second `-e YOLO_JAIL_DAEMONS` further down the argv,
// where two writers would race on one variable and the loser would depend on the
// runtime's duplicate-flag resolution. The source-skew gate cannot see an env
// contract, which is exactly why the merge lives in the writer instead of beside it.
//
// The entries are appended AFTER the loopholes' own, in the order given — the run
// pipeline sorts them by service name before calling, so the composed payload is
// deterministic run to run.
func (s Set) RuntimeArgsForWithJailDaemons(from []*Loophole, runtime string, extraJailDaemons []JailDaemonSpec) []string {
	return runtimeArgsFor(from, runtime, &s, extraJailDaemons)
}

// JailDaemonSpec is ONE entry of the YOLO_JAIL_DAEMONS payload, before it is serialized:
// the supervisor's own {name, cmd, restart} shape (internal/supervisor's Spec), in a form a
// caller can READ rather than re-decode.
//
// It exists because the payload has TWO consumers and only one of them wants JSON. The
// container argv wants the bytes; a backend that will NOT run these daemons has to name each
// one it is declining (run.noteJailDaemonsDeclined). A decline printer that reached into
// []any and pulled a "name" key back out would be a second reader of the wire shape, which is
// the drift the "one env contract, one writer" rule exists to prevent — so the composed value
// is typed all the way to JailDaemonPayload, which is the only place the wire shape is built.
//
// Restart is carried VERBATIM. Each producer owns its own default (loopholedecl defaults a
// loophole's to "on-failure" at load; run.serviceJailDaemons defaults a service's when it
// composes the spec), so this type never invents one — a defaulting step here would change
// what an existing manifest puts on the argv.
type JailDaemonSpec struct {
	Name    string
	Cmd     []string
	Restart string
}

// JailDaemons composes THIS LAUNCH'S jail-daemon entries — every admitted record's own,
// followed by the extra entries in the order given — WITH THIS SET'S ORIGIN GATE APPLIED,
// exactly as Set.RuntimeArgsForWithJailDaemons applies it.
//
// THE ONE COMPOSER, and it is exported so that a caller which cannot assemble an argv at all
// can still ask what this launch's payload IS. macos-user is that caller: it has no container
// argv, so until this existed the payload was composed inside runtimeArgsFor and emitted only
// as `-e YOLO_JAIL_DAEMONS=`, which is a container flag — and a backend with no consumer for
// it started nothing and said nothing (docs/design/jail-daemon-on-macos-user-plan.md, the
// DEFAULT configuration: packs/claude `needs` both openai-auth and wire-bridge).
//
// NO UNGATED PACKAGE-LEVEL TWIN, unlike RuntimeArgsFor and ManifestHostDaemonSpecs. Those
// have one because they predate the gate and a caller may hold a plain slice; this is new, so
// the unsafe call is simply unrepresentable — a Set is the only way in, and gateAdmitsCrossing
// therefore always has a gate to consult.
func (s Set) JailDaemons(from []*Loophole, runtime string, extra []JailDaemonSpec) []JailDaemonSpec {
	return jailDaemonSpecs(from, runtime, &s, extra, "JailDaemons")
}

// JailDaemonPayload is THE WRITER of the YOLO_JAIL_DAEMONS wire shape: the JSON list
// internal/supervisor's ParseEnv reads, one object per spec, keys in the frozen order.
//
// It is the whole reason the type above is worth having. The shape used to be built in two
// places — this package's loop for a loophole's daemon and internal/cli/run's for a pack
// SERVICE's — with a comment in each asking the other to stay identical. One writer is how
// "a diff of the two halves' entries shows no structural difference" becomes a fact instead
// of a request.
func JailDaemonPayload(specs []JailDaemonSpec) []any {
	out := make([]any, 0, len(specs))
	for _, sp := range specs {
		spec := jsonx.NewOrderedMap()
		spec.Set("name", sp.Name)
		spec.Set("cmd", toAnySlice(sp.Cmd))
		spec.Set("restart", sp.Restart)
		out = append(out, spec)
	}
	return out
}

// RuntimeArgsWithJailDaemons is Set.RuntimeArgsFor emitting a payload the CALLER already
// composed (with Set.JailDaemons) instead of composing one itself.
//
// For the launch that composes the payload ABOVE its backend dispatch, so that both arms read
// one value: the container arm hands it back here to be serialized onto the argv, and the
// native arm declines each entry by name. Composing it twice would work — the composer is
// pure — but then "one payload per launch" would be a property of two call sites passing the
// same arguments rather than of the launch.
func (s Set) RuntimeArgsWithJailDaemons(from []*Loophole, runtime string, specs []JailDaemonSpec) []string {
	return runtimeArgsWith(from, runtime, &s, specs)
}

// gateAdmitsCrossing is THE origin gate for a pack-shipped loophole's host crossings —
// the enforcement half of docs/reference/loophole-system.md#three-predicates-and-what-each-one-means,
// whose design required that an unapproved fetched pack's loophole be "not discovered
// at all".
//
// # The gate was computed and then not enforced, which is the defect this closes
//
// `HostExecApproved` is decided per module in internal/cli/run (where the pack's origin
// and the approval lockfile are reachable) and carried in on DiscoverOptions.PackModules.
// It had exactly ONE production reader — runDoctorChecks. RuntimeArgsFor filtered on
// FromConfig and Active; ManifestHostDaemonSpecs on FromConfig, HostDaemon and Active.
// Neither consulted the gate, so an UNAPPROVED fetched pack's daemon entered the spawn
// list and ran, and its bind mounts, devices, intercepts and CA reached the container argv
// — while packMayAccessHost correctly answered false and the run package's comment said
// the two were "the SAME gate, not a second one that could disagree". True of the
// decision; false of its enforcement.
//
// # Why it is enforced in the CALLEE, and why the ungated entry points refuse
//
// The same argument RunDoctorChecks already makes: a SLICE CARRIES NO GATE, so the only
// place the check cannot be forgotten is inside the function that acts on the records.
// Both of these are exported and take a plain []*Loophole, so a caller assembling records
// any other way (SetOf, ValidateLoopholes' entries, a hand-built slice) would otherwise
// walk straight past the boundary. Making the unsafe call unrepresentable is worth more
// than a rule the next call site has to know about.
//
// # Why a refused record is still DISCOVERED and LISTED
//
// G3's "not discovered at all" is about what CROSSES, and this is where the design's
// wording and its visibility requirement are reconciled: nothing of the loophole reaches
// the jail, while `yolo loopholes list`/`status` still show it — as `unapproved`, which is
// the state a user has to be able to see. A pack loophole missing from the list is
// indistinguishable from one that failed to stage, and the fix ("`yolo pack install`
// records the approval") is not discoverable from an absence.
//
// # Who reports it
//
// A gate that evaluated FALSE is silent HERE, because the caller holding the gate is the
// one that reports it once, with the reason and the fix (run.stagePacks' HonoredLoopholes
// refusal — the loophole equivalent of HonoredMounts/HonoredInstalls). Duplicating it
// here would print the same fact twice per launch.
//
// A gate that was NEVER EVALUATED is different and does warn: that is a caller which
// reached a host crossing without an origin decision, which is a programming error rather
// than a user's unapproved pack, and a silently-degraded jail is exactly how the original
// defect stayed invisible.
func gateAdmitsCrossing(m *Loophole, gate *Set, what string) bool {
	if m.Source != SourcePack {
		return true
	}
	if gate == nil {
		warnf("loophole %s: %s withheld — this is a PACK-shipped loophole and the caller "+
			"evaluated no origin gate, so its host access cannot be honored "+
			"(use loopholes.NewHostSet / Set.%s)", m.Name, what, what)
		return false
	}
	// MayRunHostCode is the one decision, and it governs the READS as well as the
	// execution. ⚠ It used to be `p.MayAccessHost`, decided per pack by
	// `packMayAccessHost` — BOTH were deleted on 2026-09-04 with the fetched-pack
	// approval gate (docs/design/trust-paths.md OQ-TP9), because selecting a pack means
	// writing user-scope config as the host user, which already exceeds what the gate
	// withheld. `HostExecApproved` is now always true from production, and its false arm
	// means "the caller assembled a []*Loophole without resolving packs" — API misuse,
	// not consent. This gate still refuses that, which is why it stays.
	return gate.MayRunHostCode(m)
}

// admitsJailSideEffects is the per-record skip the two loops below share: what this record
// puts in the jail at all, on this runtime, under this gate.
//
// ONE PREDICATE, TWO LOOPS. The mounts/flags loop and the jail-daemon composer walk the same
// records and must make the same skip, or a launch would compose a payload entry for a
// loophole whose `:ro` module mount it left off the argv — a daemon named at a path nothing
// mounted. Sharing the predicate is how that stays impossible rather than reviewed.
//
// `what` only reaches gateAdmitsCrossing's NEVER-EVALUATED warning, which is why the callers
// below both pass "RuntimeArgsFor" on the argv path: the two loops then produce the identical
// line for one ungated record and warnf says it once (see warnf's dedup), so extracting the
// composer left stderr byte-identical as well as the argv.
func admitsJailSideEffects(m *Loophole, runtime string, gate *Set, what string) bool {
	if m.FromConfig() {
		return false
	}
	if !m.Active() {
		return false
	}
	if !gateAdmitsCrossing(m, gate, what) {
		return false
	}
	// Apple Container does not support --add-host (apple/container#673), so a
	// loophole that needs one is skipped whole there.
	//
	// The key is the INTERCEPT LIST, not a transport string. It used to be
	// `Transport == "tls-intercept"`, which worked only because one value
	// happened to imply the other; `intercepts` is what actually produces the
	// --add-host flags in the loop below, so keying on it makes the skip and
	// the thing skipped the same fact. That is also what let "tls-intercept"
	// retire (docs/reference/loophole-transport.md §7.4): it was the field's only
	// behavioural reader.
	return !(runtime == "container" && len(m.Intercepts) > 0)
}

// jailDaemonSpecs is THE COMPOSER of this launch's jail-daemon entries — the body behind
// Set.JailDaemons and the one runtimeArgsFor calls, so there is exactly one.
func jailDaemonSpecs(loopholes []*Loophole, runtime string, gate *Set,
	extra []JailDaemonSpec, what string) []JailDaemonSpec {
	specs := []JailDaemonSpec{}
	for _, m := range loopholes {
		if !admitsJailSideEffects(m, runtime, gate, what) {
			continue
		}
		if m.JailDaemon == nil {
			continue
		}
		specs = append(specs, JailDaemonSpec{
			Name: m.Name, Cmd: m.JailDaemon.Cmd, Restart: m.JailDaemon.Restart,
		})
	}
	// Pack services' jail daemons join the loopholes' own entries, one list, one env
	// var (RuntimeArgsForWithJailDaemons carries the reasoning).
	return append(specs, extra...)
}

func runtimeArgsFor(loopholes []*Loophole, runtime string, gate *Set, extraJailDaemons []JailDaemonSpec) []string {
	// THE ARGV FIRST, then the payload, and the order is not cosmetic: both loops consult
	// gateAdmitsCrossing, and running the mounts loop first means its warnings come out in
	// exactly the sequence they did before the composer was extracted (the payload loop's
	// are then the same lines, which warnf drops). See admitsJailSideEffects.
	args := runtimeArgsWith(loopholes, runtime, gate, nil)
	specs := jailDaemonSpecs(loopholes, runtime, gate, extraJailDaemons, "RuntimeArgsFor")
	return append(args, jailDaemonEnvArgs(specs)...)
}

// runtimeArgsWith is runtimeArgsFor over an ALREADY-COMPOSED payload. Pass nil for specs to
// get the argv without the env var at all, which is what runtimeArgsFor does before it
// composes (the var is appended last either way, so the two spellings agree byte for byte).
func runtimeArgsWith(loopholes []*Loophole, runtime string, gate *Set, specs []JailDaemonSpec) []string {
	args := []string{}
	trustedCAPaths := []string{}

	for _, m := range loopholes {
		if !admitsJailSideEffects(m, runtime, gate, "RuntimeArgsFor") {
			continue
		}
		containerDir := JailLoopholeDir(m.Name)

		for _, intercept := range m.Intercepts {
			args = append(args, "--add-host", intercept.Host+":"+m.BrokerIP)
		}

		stateContainer := "/var/lib/yolo-jail/loopholes/" + m.Name
		stateDirMounted := false
		stateFileMounted := map[string]bool{}
		dirMounted := false

		if m.JailDaemon != nil {
			args = append(args, "-v", m.Path+":"+containerDir+":ro")
			dirMounted = true
			if isDir(m.StateDir()) {
				if len(m.StateFiles) > 0 {
					// Least privilege (issue #33): only the DECLARED files cross.
					// The whole-dir mount below carried the broker CA's PRIVATE key
					// into every jail, where nothing reads it — signing is host-side
					// in internal/oauthbroker/cert.go — and 0600 is no barrier
					// because a jail's agent runs as UID 0 by design.
					for _, rel := range m.StateFiles {
						src := filepath.Join(m.StateDir(), rel)
						if !isFile(src) {
							// Never emit a -v for a missing source: the runtime
							// would materialize it as an empty DIRECTORY, shadowing
							// the file the jail daemon is waiting for.
							warnf("loophole %s: skipping state file, host source missing: %s", m.Name, src)
							continue
						}
						args = append(args, "-v", src+":"+stateContainer+"/"+rel+":ro")
						stateFileMounted[rel] = true
					}
				} else {
					args = append(args, "-v", m.StateDir()+":"+stateContainer+":ro")
					stateDirMounted = true
				}
			}
		}

		if m.HasCA() && m.CACertSet {
			containerCA := ""
			haveCA := false
			// The CA rides the state mount only when the state mount actually
			// carries it: the whole dir, or that exact file under state_files.
			if rel, ok := relativeTo(m.CACert, m.StateDir()); ok && (stateDirMounted || stateFileMounted[rel]) {
				containerCA = stateContainer + "/" + rel
				haveCA = true
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
	return append(args, jailDaemonEnvArgs(specs)...)
}

// jailDaemonEnvArgs is the `-e YOLO_JAIL_DAEMONS=` pair, or nothing at all for an empty
// payload — a launch with no jail daemon must not grow the variable merely because the
// composition ran.
func jailDaemonEnvArgs(specs []JailDaemonSpec) []string {
	if len(specs) == 0 {
		return nil
	}
	payload, err := jsonx.DumpsCompact(JailDaemonPayload(specs))
	if err != nil {
		// REPORTED AND DROPPED, in that order. The payload was the only thing telling
		// the in-jail supervisor these daemons exist, so a discarded error here is a
		// launch where every declared jail daemon silently does not run. Emitting a
		// half-written variable instead would trade the silence for a supervisor parse
		// failure in the jail's boot log, which is further from the reader than this
		// line is.
		//
		// ⚠ UNREACHABLE WITH TODAY'S PAYLOAD, and therefore UNTESTED: jsonx fails only
		// on an unsupported TYPE, and JailDaemonPayload emits strings, bools and a
		// []any of strings. It is a report rather than a `_` because the shape is
		// declared elsewhere (JailDaemonSpec) and the cost of being wrong about that is
		// a whole launch's daemons missing with nothing said.
		warnf("could not serialize the jail-daemon payload for %d declared daemon(s) "+
			"(%v); none will run in this jail", len(specs), err)
		return nil
	}
	return []string{"-e", "YOLO_JAIL_DAEMONS=" + payload}
}

// every active file-backed loophole with a host_daemon, shaped like the
// loopholes: config block. Returned as an insertion-ordered map so it serializes
// deterministically.
//
// A SourcePack record is NEVER ADMITTED here without an evaluated origin gate — this is
// the list startLoopholes spawns from, so it is the sharpest of the three gated surfaces.
// See gateAdmitsCrossing; Set.ManifestHostDaemonSpecs is the gated form.
func ManifestHostDaemonSpecs(loopholes []*Loophole) *jsonx.OrderedMap {
	return manifestHostDaemonSpecs(loopholes, nil)
}

// ManifestHostDaemonSpecs returns the daemon specs of the given records WITH THIS SET'S
// ORIGIN GATE applied: a pack-contributed daemon is admitted only when the caller recorded
// that its pack's host access is approved.
func (s Set) ManifestHostDaemonSpecs(from []*Loophole) *jsonx.OrderedMap {
	return manifestHostDaemonSpecs(from, &s)
}

func manifestHostDaemonSpecs(loopholes []*Loophole, gate *Set) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, m := range loopholes {
		if m.FromConfig() || m.HostDaemon == nil {
			continue
		}
		if !m.Active() {
			continue
		}
		if !gateAdmitsCrossing(m, gate, "ManifestHostDaemonSpecs") {
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

// RunDoctorChecks runs each loophole's doctor_cmd. timeout defaults to 10s when zero.
//
// A SourcePack record is NEVER EXECUTED here, whatever the caller intended: it comes back
// with RC=nil and an explanation instead. That is the ungated door being nailed shut
// rather than documented (docs/reference/loophole-system.md#selection-and-discovery —
// "RunDoctorChecks must take only loopholes whose origin gate has been evaluated").
//
// Neither is a loophole whose doctor_cmd or module dir lives where an AGENT writes: the
// PLACEMENT rule (docs/reference/loophole-system.md#the-placement-rule) applies at this
// face too, narrowed to the jail-home tree because no doctor
// caller carries a workspace. See runDoctorChecks for why both gates are in the callee.
//
// The reason it is enforced HERE, in the callee, is that two of the doctor call sites are
// `yolo check` and `yolo loopholes status` — commands users and AGENTS.md treat as
// READ-ONLY PREFLIGHT — and neither had pack resolution, a lockfile or (while it existed)
// `packMayAccessHost` anywhere in reach. A rule they were merely asked to follow is a rule the next call site
// does not know about; a slice carries no gate, so the only place the check cannot be
// forgotten is inside the function that spawns the process. A caller that DID evaluate the
// gate says so by going through Set.RunDoctorChecks below.
func RunDoctorChecks(loopholes []*Loophole, timeout time.Duration) []DoctorResult {
	return runDoctorChecks(loopholes, timeout, nil)
}

// RunDoctorChecks runs the doctor_cmd of each given record WITH THIS SET'S ORIGIN GATE
// applied: a pack-contributed loophole runs only when the caller recorded that its pack's
// host access is approved. Everything else behaves exactly as the package-level function —
// the §4.3a PLACEMENT rule included, since both entry points share one body.
func (s Set) RunDoctorChecks(from []*Loophole, timeout time.Duration) []DoctorResult {
	return runDoctorChecks(from, timeout, &s)
}

// runDoctorChecks is the shared body. gate nil means no gate was evaluated, which for a
// SourcePack record means "refuse", never "allow".
//
// TWO GATES, BOTH IN THE CALLEE, for one reason: a doctor_cmd is host execution and two of
// the three call sites (`yolo check`, `yolo loopholes status`) are commands users and
// AGENTS.md treat as READ-ONLY PREFLIGHT. The ORIGIN gate asks who shipped the code; the
// PLACEMENT gate (§4.3a) asks whether the named file lives where an agent rewrites it. They
// are independent — an embedded pack's own loophole passes the first and can fail the
// second — and both live here rather than at the call sites because a slice carries no
// judgement: a rule a caller is merely asked to apply is a rule the next call site does not
// know about. Measured before this gate existed: a hand-placed manifest whose doctor_cmd
// named a script an agent writes was EXECUTED by both preflight commands.
func runDoctorChecks(loopholes []*Loophole, timeout time.Duration, gate *Set) []DoctorResult {
	if timeout == 0 {
		timeout = doctorTimeoutDefault
	}
	results := []DoctorResult{}
	for _, m := range loopholes {
		if m.Source == SourcePack && (gate == nil || !gate.MayRunHostCode(m)) {
			results = append(results, DoctorResult{Loophole: m, RC: nil,
				Output: "not run: a pack-shipped loophole's self-check is host execution, " +
					"and this caller resolved no packs, so nothing vouches for the module " +
					"(a yolo bug, not a config problem — please report it)"})
			continue
		}
		// The PLACEMENT rule, at the doctor face. workspace is "" because no doctor caller
		// has one in hand (`yolo loopholes status` takes no workspace), which NARROWS the
		// rule to the jail-home tree rather than disabling it — a narrower answer beats no
		// answer, and refusing to check without a workspace is how the spawn face came to
		// be the rule's only caller for a batch.
		if probs := m.PlacementProblems(""); len(probs) > 0 {
			results = append(results, DoctorResult{Loophole: m, RC: nil,
				Output: "not run: " + strings.Join(probs, "\n")})
			continue
		}
		if len(m.DoctorCmd) == 0 {
			results = append(results, DoctorResult{Loophole: m, RC: nil, Output: ""})
			continue
		}
		rc, output := runOne(m.DoctorCmd, timeout)
		results = append(results, DoctorResult{Loophole: m, RC: rc, Output: output})
	}
	return results
}

// doctorTimeoutDefault bounds ONE loophole's doctor_cmd. Named rather than spelled
// inline at the zero-value branch above: the value is what the `timeout` report below
// states, and a magic literal in a path a `yolo check` runs is a duration no reader
// can cite.
const doctorTimeoutDefault = 10 * time.Second

func runOne(argv []string, timeout time.Duration) (*int, string) {
	// A doctor_cmd of the form ["yolo","internal","daemon",<name>,"--self-check"]
	// re-execs the running yolo binary rather than resolving "yolo" on PATH.
	argv = execx.SelfExecArgv(argv)
	cmd := exec.Command(argv[0], argv[1:]...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// SETSID SO THE DEADLINE BELOW CAN ACTUALLY REACH THE THING IT IS BOUNDING.
	// Without it the timeout killed the DIRECT CHILD only, and cmd.Wait() then blocked
	// until every grandchild still holding the captured stdout pipe exited — so a
	// doctor_cmd whose script forks (`sleep 30` from a shell is the whole
	// reproduction) burned the full hang against a 10-second deadline, which is the one
	// thing a deadline exists to prevent. MEASURED by
	// TestDoctorTimeoutNamesTheDeadlineItBurned, which asserts the WALL CLOCK and not
	// merely the message. Same Setsid, same reason, as a spawned host service's
	// (internal/cli/run's killServiceGroup).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		// FileNotFoundError / OSError -> returncode None, output = str(e).
		return nil, err.Error()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(timeout):
		// The DURATION is part of the answer. "timeout" alone left a reader unable to
		// tell a self-check that hung from one that is merely slower than the deadline
		// it cannot see, and the deadline is a caller's argument rather than a constant
		// a reader can look up.
		//
		// THE GROUP, not the child (Setsid at spawn, above): a doctor_cmd that forked
		// leaves its children holding the pipe this function is reading, and the <-done
		// below waits for that pipe to close. SIGKILL rather than SIGTERM because the
		// deadline has already passed — there is no grace left to give.
		//
		// The error is discarded because the process is unreachable either way: ESRCH
		// means it exited as the deadline passed, and the <-done below reaps it in both
		// cases — the RC stays nil, which is this function's word for "did not run to a
		// verdict".
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return nil, "timeout after " + timeout.String()
	case err := <-done:
		code := cmd.ProcessState.ExitCode()
		out := stdout.String()
		if out == "" {
			out = stderr.String()
		}
		out = strings.TrimSpace(out)
		// Wait's error is discarded because it is a RESTATEMENT of the exit code this
		// function already returns (an *ExitError carrying `code`), and a doctor_cmd
		// exiting non-zero is the reportable outcome, not an error of yolo's.
		_ = err
		rc := code
		return &rc, out
	}
}

// SetEnabled IS GONE, and nothing replaces it in this package. It rewrote `enabled`
// in a manifest under the hand-placed user loopholes dir — the only file yolo ever
// wrote on a user's behalf here, and the only source `yolo loopholes enable|disable`
// could serve. With that directory retired (retired.go, OQ-LP10) it had nothing left
// to write: a BUNDLED manifest is the binary's own content (and go:embed'd, so on an
// installed binary there is no file at all), and a PACK's manifest belongs to the
// pack, where a local rewrite would be silently reverted by the next `pack install`.
// Enable/disable state moves into config for every source; see CmdSetEnabled.
//
// OQ-A9 CLOSED THE OTHER HALF, and it is worth stating because the ruling reads like
// work still owed: the key this function used to write DOES NOT EXIST any more. The
// manifest's `enabled` is now `default_enabled` and is the PACK AUTHOR's default, not
// a user setting, so there is no longer a manifest field a `yolo loopholes enable`
// could legitimately target even if a writable manifest turned up. The only writable
// home for the user's answer is config, which is where CmdSetEnabled points —
// `loopholes.<name>.enabled`, unrenamed, because it was always the other key.

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

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
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
