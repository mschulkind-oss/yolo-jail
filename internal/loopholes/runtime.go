package loopholes

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// warnf/infof are the package's log sinks.
//
// warnf writes to STDERR by default. It used to be a no-op "for callers that
// install a sink", and in the whole tree no caller ever did — so every warning
// this package emitted went nowhere. That is tolerable for "skipped a bind mount"
// and NOT tolerable for "this loophole failed to load and is therefore absent",
// which is otherwise invisible at launch (see loadFromDir). A warning nobody can
// read is not a diagnostic.
//
// infof stays a no-op: its one use is a routine in-jail device skip that happens
// on every launch and says nothing actionable.
var (
	warnf = func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
	}
	infof = func(format string, args ...any) {}
)

// (podman) path; pass "container" for Apple Container (which skips any loophole
// declaring `intercepts`). It is side-effect free and idempotent.
//
// A SourcePack record is NEVER HONORED here, whatever the caller intended: with no
// origin gate in hand its declarations are dropped. Same door, same nail as
// RunDoctorChecks below — see gateAdmitsCrossing. A caller that DID evaluate the gate
// says so by going through Set.RuntimeArgsFor.
func RuntimeArgsFor(loopholes []*Loophole, runtime string) []string {
	return runtimeArgsFor(loopholes, runtime, nil)
}

// RuntimeArgsFor builds the container args for the given records WITH THIS SET'S ORIGIN
// GATE applied: a pack-contributed loophole's binds, devices, intercepts, CA and jail_env
// reach the argv only when the caller recorded that its pack's host access is approved.
// Everything else behaves exactly as the package-level function.
func (s Set) RuntimeArgsFor(from []*Loophole, runtime string) []string {
	return runtimeArgsFor(from, runtime, &s)
}

// gateAdmitsCrossing is THE origin gate for a pack-shipped loophole's host crossings —
// the enforcement half of docs/design/loophole-packaging.md §4.3 G3, which says an
// unapproved fetched pack's loophole is "not discovered at all".
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
	// execution: it is p.MayAccessHost, which packMayAccessHost already decided for the
	// whole pack. A pack that may not read the host certainly may not bind-mount one of
	// its directories into a UID-0 jail.
	return gate.MayRunHostCode(m)
}

func runtimeArgsFor(loopholes []*Loophole, runtime string, gate *Set) []string {
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
		if !gateAdmitsCrossing(m, gate, "RuntimeArgsFor") {
			continue
		}
		// Apple Container does not support --add-host (apple/container#673), so a
		// loophole that needs one is skipped whole there.
		//
		// The key is the INTERCEPT LIST, not a transport string. It used to be
		// `Transport == "tls-intercept"`, which worked only because one value
		// happened to imply the other; `intercepts` is what actually produces the
		// --add-host flags twenty lines below, so keying on it makes the skip and
		// the thing skipped the same fact. That is also what let "tls-intercept"
		// retire (docs/design/loophole-transport.md §7.4): it was the field's only
		// behavioural reader.
		if runtime == "container" && len(m.Intercepts) > 0 {
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
// rather than documented (docs/design/loophole-packaging.md §5.1 — "RunDoctorChecks must
// take only loopholes whose origin gate has been evaluated").
//
// Neither is a loophole whose doctor_cmd or module dir lives where an AGENT writes: §4.3a's
// PLACEMENT rule applies at this face too, narrowed to the jail-home tree because no doctor
// caller carries a workspace. See runDoctorChecks for why both gates are in the callee.
//
// The reason it is enforced HERE, in the callee, is that two of the doctor call sites are
// `yolo check` and `yolo loopholes status` — commands users and AGENTS.md treat as
// READ-ONLY PREFLIGHT — and neither has pack resolution, a lockfile or packMayAccessHost
// anywhere in reach. A rule they were merely asked to follow is a rule the next call site
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
		timeout = 10 * time.Second
	}
	results := []DoctorResult{}
	for _, m := range loopholes {
		if m.Source == SourcePack && (gate == nil || !gate.MayRunHostCode(m)) {
			results = append(results, DoctorResult{Loophole: m, RC: nil,
				Output: "not run: a pack-shipped loophole's self-check is host execution, " +
					"and this pack's host access is not approved for it " +
					"(`yolo pack install` records the approval)"})
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

func runOne(argv []string, timeout time.Duration) (*int, string) {
	// A doctor_cmd of the form ["yolo","internal","daemon",<name>,"--self-check"]
	// re-execs the running yolo binary rather than resolving "yolo" on PATH.
	argv = execx.SelfExecArgv(argv)
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
