package check

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// checkLoopholes surfaces loophole discovery + each
// loophole's own self-check. Bad manifests warn; non-zero self-checks fail.
func (o *Options) checkLoopholes(r *reporter) {
	if o.inJail() {
		r.ok("Inside jail — loophole checks skipped (managed by host)")
		return
	}
	// FIRST, before any loophole is reported: a still-populated retired directory
	// (OQ-LP10) means loopholes that WERE running host daemons are now inert. `yolo
	// check` is the command someone runs when that happens, and the discovery-time
	// stderr line is easy to scroll past — so it gets a graded row of its own, with the
	// whole migration instruction as the detail.
	if stranded := loopholes.RetiredUserLoopholes(); len(stranded) > 0 {
		r.warn("retired loopholes directory still holds "+
			fmt.Sprintf("%d module(s): %s", len(stranded), strings.Join(stranded, ", ")),
			loopholes.RetiredUserLoopholeNotice())
	}
	// ValidateSet, not ValidateLoopholes: the SAME walk, plus the ORIGIN GATE the
	// entries cannot carry — a ValidateEntry is a manifest, not a set. Both halves are
	// load-bearing here, and only the second one was missing: the walker already reads
	// the recorded pack modules, so a pack-shipped loophole was already LISTED, but the
	// doctor face went through the package-level RunDoctorChecks, which refuses every
	// SourcePack record by construction (a slice carries no gate). An APPROVED pack's
	// self-check therefore reported "could not run" and its output never reached the
	// screen.
	//
	// That costs nothing today, because the one pack-shipped loophole (audio-alsa)
	// declares no doctor_cmd — and it costs everything the moment the activation sprint
	// moves the only two that DO declare one, the broker and host-processes, out of
	// bundled_loopholes/ (docs/design/loophole-activation.md OQ-A12). On the day that
	// conversion lands this section would print a cheerful all-green while the broker's
	// cert freshness, liveness and self-check went unreported — on exactly the command a
	// user reaches for when a loophole has silently stopped.
	//
	// It is also the honest completion of docs/design/pack-code-separation.md's doctor
	// ruling: `check` reads loophole health through the manifest surface rather than
	// hand-rolled Go. Nothing about WHAT a doctor_cmd does or WHEN it runs moves — the
	// origin and placement gates still live in the callee, where a slice cannot forget
	// them, and an unapproved pack is still refused WITH its reason (below, in the
	// RC==nil branch). Only which loopholes this section can see changes.
	entries, set := loopholes.ValidateSet(true)
	if len(entries) == 0 {
		r.ok(fmt.Sprintf("No loopholes installed (install one as a pack; %s is "+
			"selected implicitly when it exists)", paths.LocalPackDir()))
		return
	}
	// `enabled` is writable at workspace scope (loophole-packaging.md §4.3b), so
	// the disclosure is the only protection left for a default-on loophole: a
	// workspace-sourced disable must WARN and name the file, never render as a
	// green line. Only a disable from the loophole's own manifest is an ok.
	//
	// The ON direction is disclosed the same way, for the newer reason
	// (docs/design/loophole-activation.md OQ-A13). R5 dates from when a workspace
	// `enabled: true` was INERT — manifests defaulted to on, so the weak scope could
	// only subtract. R2 flipped that default and made this key the ACTIVATION VERB,
	// and what a workspace enable rendered as here was the greenest line in the
	// section: `[PASS] loophole X: disabled`, read off the manifest default (this
	// walk resolves no config), with the file that overrode it named nowhere.
	//
	// Both lines are READABILITY, not a control, and the ON wording is held to that:
	// it names the file holding the switch and stops. The config-approval diff is the
	// mechanism that asks a human (docs/design/config-safety.md); a check row implying
	// review would be worth less than no row.
	//
	// A workspace file that merely RESTATES the manifest default is disclosed too.
	// The launch-time twin cannot suppress that case — its LoopholeInfo carries no
	// default — and two disclosures contradicting each other over one file is worse
	// than one redundant line. An explicit `enabled` in an agent-editable file is a
	// deliberate act either way, so this stays off ordinary launches.
	switches := config.WorkspaceLoopholeSwitches(o.Workspace)
	// The USER's switch, read separately from the disclosure above and for a different
	// job. `switches` answers "did an agent-editable file touch this", which is what
	// decides whether to WARN; this answers "will the loophole run", which is what
	// decides the ROW. Conflating them is how the section came to report the manifest
	// default as fact: OQ-A13 fixed the workspace half and left the user half standing,
	// so `loopholes.audio.enabled: true` in ~/.config/yolo-jail/config.jsonc — the
	// remedy audio's own manifest prescribes, and the one `yolo loopholes enable`
	// prints — rendered as `[PASS] loophole audio: disabled` with the doctor_cmd
	// unrun. R2's flipped default is what made that the ordinary path rather than an
	// exotic one.
	//
	// This adds no disclosure, which is OQ-A13's ruling and not an omission: a
	// user-scope enable draws no line, because a line under every enabled loophole on
	// every run is how the one that matters gets skimmed past. Only the verdict moves.
	userSwitches := loopholeConfigBlock(o.Workspace)
	for _, e := range entries {
		if e.Err != "" {
			r.warn("loophole "+filepath.Base(e.Path)+": invalid manifest", e.Err)
			continue
		}
		lp := e.Loophole
		sw, wsScoped := switches[lp.Name]
		if wsScoped && !sw.Enabled {
			r.warn("loophole "+lp.Name+": disabled by "+sw.File+" (workspace scope)",
				"An agent-editable file turned an installed loophole off; jails "+
					"launched from this workspace run without it. Re-enable it there, "+
					"or move the override to "+paths.UserConfigPath()+".")
			continue
		}
		// The ON row DISCLOSES and then falls through, where the OFF row above stops.
		// That asymmetry is the point rather than an oversight: off means there is
		// nothing left to measure, on means the loophole is about to run and its
		// self-check is the next thing a reader wants. Ending the iteration here would
		// undo OQ-A12 — the reason every loophole's doctor_cmd is reported at all — for
		// exactly the loopholes whose activation is least expected.
		enabled := lp.Enabled
		if v, set := loopholes.ConfigEnabledOverride(userSwitches, lp.Name); set {
			enabled = v
		}
		if wsScoped && sw.Enabled {
			r.warn("loophole "+lp.Name+": enabled by "+sw.File+" (workspace scope)",
				"An agent-editable file turned an installed loophole ON; jails "+
					"launched from this workspace run WITH it. This row says where the "+
					"switch lives — it is not a record that anyone reviewed it. Turn it "+
					"off there, or move the override to "+paths.UserConfigPath()+".")
			enabled = true
		}
		if !enabled {
			r.ok("loophole " + lp.Name + ": disabled")
			continue
		}
		if !lp.RequirementsMet() {
			reason, _ := lp.InactiveReason()
			r.ok("loophole " + lp.Name + ": inactive (" + reason + ")")
			continue
		}
		if len(lp.DoctorCmd) == 0 {
			r.ok("loophole " + lp.Name + ": no self-check declared")
			continue
		}
		// set.RunDoctorChecks, not the package-level function: the Set is what carries
		// the origin gate, and the ungated door is nailed shut rather than documented —
		// through it a pack-shipped record comes back RC=nil however approved its pack
		// actually is. An UNAPPROVED pack still comes back RC=nil, from the callee, and
		// lands in the RC==nil branch with the refusal as its note; that is visible
		// rather than silent, which is the whole difference between "withheld" and
		// "declares no self-check".
		results := set.RunDoctorChecks([]*loopholes.Loophole{lp}, 10*time.Second)
		res := results[0]
		switch {
		case res.RC != nil && *res.RC == 0:
			r.ok("loophole " + lp.Name + ": self-check ok")
			reportSelfCheckLines(r, lp.Name, res.Output)
		case res.RC == nil:
			out := res.Output
			if out == "" {
				out = "command missing"
			}
			r.warn("loophole "+lp.Name+": self-check could not run", out)
		default:
			if graded := reportSelfCheckLines(r, lp.Name, res.Output); graded == 0 {
				r.fail(fmt.Sprintf("loophole %s: self-check failed (rc=%d)", lp.Name, *res.RC), "no output")
			}
		}
		// OUTSIDE the switch, and deliberately: the daemon-liveness block is the
		// first thing you want when the self-check FAILED (expired shared creds
		// are usually a dead or wedged broker), and it used to hang off the rc=0
		// branch only — where the freshness grading also lived, so a bad grade
		// could not coexist with a non-zero rc. Now that the grading is behind
		// doctor_cmd, a bad grade IS a non-zero rc, and gating liveness on rc=0
		// would hide it in exactly the case it is diagnostic.
		if lp.Name == brokerLoopholeName {
			o.reportBrokerDaemon(r)
		}
	}
}

// loopholeConfigBlock returns the merged (user + workspace) `loopholes` block, or nil
// when there is no config or no block — which ConfigEnabledOverride reads as "nobody
// answered", leaving each manifest's own default standing.
//
// MERGED, not workspace-only, and that is the whole point of it existing beside
// config.WorkspaceLoopholeSwitches. The two answer different questions: the switches map
// is provenance (which agent-editable FILE holds the key, so a disclosure can name it),
// this is the decision (what a launch will actually do). Reading provenance where the
// decision was wanted is what left the user-scope half of the row wrong after OQ-A13
// fixed the workspace half.
//
// LOOSE: an unreadable or invalid config yields nil rather than an error, because the
// config's own validity is a different section's report and `yolo check` must not lose
// the loophole rows over it.
func loopholeConfigBlock(workspace string) *jsonx.OrderedMap {
	cfg := loadConfigLoose(workspace)
	if cfg == nil {
		return nil
	}
	blockV, ok := cfg.Get("loopholes")
	if !ok {
		return nil
	}
	block, isMap := blockV.(*jsonx.OrderedMap)
	if !isMap {
		return nil
	}
	return block
}

// reportSelfCheckLines renders a self-check's own graded output — "FAIL:" as a
// fail, "NOTE:" as a warn, "OK:" as a pass — and returns how many lines it
// rendered. The trailing colon-less "OK" summary is not a graded line; the
// caller's "self-check ok" header covers that.
//
// Core used to render only the FAIL lines, and only when rc was non-zero, which
// meant a passing self-check's own findings never reached the screen. That is
// precisely what forced `yolo check` to re-implement the broker's shared-creds
// freshness grading in Go against `claudeAiOauth.expiresAt` (deleted with this
// change): the number existed, but there was no way for a loophole to REPORT a
// healthy-but-informative measurement through the doctor_cmd seam it already
// declared. Rendering the whole protocol is what makes doctor_cmd the extension
// point it was always documented to be — for every loophole, not just this one.
func reportSelfCheckLines(r *reporter, name, output string) int {
	lines := nixdiag.SplitSelfCheckLines(output)
	for _, l := range lines {
		switch l.Grade {
		case nixdiag.GradeFail:
			r.fail("loophole "+name+": "+l.Title, l.Detail)
		case nixdiag.GradeNote:
			r.warn("loophole "+name+": "+l.Title, l.Detail)
		case nixdiag.GradeOK:
			r.ok("loophole " + name + ": " + l.Title)
		}
	}
	return len(lines)
}

// reportBrokerDaemon reports the broker liveness block (after a green
// self-check): live / not running / stale PID / unresponsive.
func (o *Options) reportBrokerDaemon(r *reporter) {
	status := o.brokerStatus()
	switch {
	case status.pidLive && status.pingOK:
		r.ok(fmt.Sprintf("loophole claude-oauth-broker: daemon live (pid=%d, ping ok)", status.pid))
	case !status.pidPresent:
		r.warn("loophole claude-oauth-broker: daemon not running",
			"First `yolo run` will spawn it; "+
				"`yolo broker status` reports state, "+
				"`yolo broker restart` cycles.")
	case !status.pidLive:
		r.fail(fmt.Sprintf("loophole claude-oauth-broker: stale PID file, pid %d not running", status.pid),
			"Run `yolo broker restart` to clean up and respawn.")
	default:
		socketState := "missing"
		if status.socketExists {
			socketState = "present"
		}
		r.fail(fmt.Sprintf("loophole claude-oauth-broker: daemon unresponsive (pid=%d, socket %s, ping failed)", status.pid, socketState),
			"Run `yolo broker restart` — typical after a "+
				"binary upgrade; old code still loaded in memory.")
	}
}

// checkBrokerRelay probes one jail's broker relay end-to-end THROUGH THE HOP THE
// JAIL USES — read the endpoint file, pin its certificate, present its token, then
// ping the singleton behind it — naming the failing LAYER.
//
// Probing the relay's own Unix socket instead would test a path no jail travels:
// that socket is host-only now, so it can be perfectly healthy while the jail's
// half is unpublished, stale, or mismatched. The prober can authenticate at all
// only because it runs as the uid that published the file and reads the same 0600
// file the jail does — a property that exists because the token lives there rather
// than in the jail's environment. It never prints the file's contents.
func (o *Options) checkBrokerRelay(r *reporter, label, endpointPath, rt, cname string) {
	if !o.PathExists(endpointPath) {
		r.fail(label+": relay endpoint missing",
			fmt.Sprintf("Expected %s.  The per-jail relay never started, its front "+
				"failed to publish, or its host-services dir was removed.  Any `yolo` "+
				"invocation against this jail respawns it.", endpointPath))
		return
	}
	if !svcendpoint.Probe(endpointPath) {
		r.fail(label+": relay endpoint incomplete",
			fmt.Sprintf("%s exists but does not parse as a complete endpoint "+
				"(address, certificate, token).  It was truncated or written by an "+
				"older yolo; any `yolo` invocation against this jail republishes it.",
				endpointPath))
		return
	}
	// DialLocal, not Dial: the published address names the container runtime's
	// gateway, which a jail resolves and this host does not. Same port, same pinned
	// certificate, same token — only the name substituted. That substitution is why
	// the PASS below labels itself host-side; see hostSideProbeCaveat.
	conn, err := svcendpoint.DialLocal(endpointPath, 2*time.Second)
	if err != nil {
		if errors.Is(err, svcendpoint.ErrAuthRejected) {
			r.fail(label+": relay rejected this jail's token",
				fmt.Sprintf("The relay named by %s refused the token in that file, so "+
					"the file is stale relative to the running relay (it restarted and "+
					"republished, or a predecessor's file was left behind).  Any `yolo` "+
					"invocation against this jail republishes it.", endpointPath))
			return
		}
		r.fail(label+": relay endpoint dead",
			fmt.Sprintf("Dialing the relay named by %s failed: %s.  The relay process "+
				"exited; any `yolo` invocation against this jail respawns it.", endpointPath, err))
		return
	}
	ok := brokerPingConn(conn, 2*time.Second)
	_ = conn.Close()
	if ok {
		if v := o.relayEndpointVisibleInJail(rt, cname); v != nil && !*v {
			r.fail(label+": relay ok on host, endpoint invisible in-jail",
				"The host-services dir was recreated after the container mounted "+
					"it (host /tmp cleanup or a teardown/startup race): the "+
					"jail's bind mount still points at the old, deleted "+
					"directory, so in-jail auth requests 502 even though the "+
					"host-side relay answers.  That directory now holds this jail's "+
					"CREDENTIAL, so re-reading cannot recover it either.  Relaunch "+
					"the jail to remount the directory.")
		} else {
			r.ok(label + ": relay ok (cert-pinned, token-authenticated), broker answers " +
				"through it — host-side, says nothing about in-jail reachability")
		}
	} else {
		r.fail(label+": relay up, broker unreachable",
			"The relay authenticated and accepted, but the singleton broker did not "+
				"answer the proxied ping.  Check `yolo broker status` / "+
				"`yolo broker restart`.")
	}
}

// checkHostServiceLiveness verifies, for each running
// jail, that each external host_daemon's socket is alive.
func (o *Options) checkHostServiceLiveness(r *reporter) {
	if o.inJail() {
		// Say so, rather than returning silently. Every sibling section announces why
		// it stepped aside; this one left its header standing over an empty block,
		// which reads as "probed, nothing to report" in exactly the place where the
		// honest answer is "not askable from here" — the host's per-jail service
		// directory is not mounted in, so there is nothing to probe.
		r.ok("Inside jail — host-service liveness skipped (these probes run host-side)")
		return
	}
	entries := loopholes.ValidateLoopholes(true)
	// The same resolution the row above needs, for the same reason and with sharper
	// stakes: this walker reads no config at all, so the record's Enabled is the
	// manifest default. A loophole the user switched on has a daemon RUNNING — the
	// launch path resolved the config and spawned it — while this block skipped it and
	// printed "no host-side daemons to probe". A green line asserting there is nothing
	// to measure, over a live host process, on the command someone runs when that
	// process is the thing that broke.
	userSwitches := loopholeConfigBlock(o.Workspace)
	var externals []*loopholes.Loophole
	for _, e := range entries {
		lp := e.Loophole
		if lp == nil || e.Err != "" {
			continue
		}
		enabled := lp.Enabled
		if v, set := loopholes.ConfigEnabledOverride(userSwitches, lp.Name); set {
			enabled = v
		}
		if enabled && lp.RequirementsMet() && lp.HostDaemon != nil {
			externals = append(externals, lp)
		}
	}
	if len(externals) == 0 {
		r.ok("no host-side daemons to probe")
		return
	}
	rt := o.detectRuntimeForListing()
	if rt == "" {
		r.warn("no container runtime found — skipping liveness probe", "")
		return
	}
	cnames, listErr := o.listRunningJailNames(rt)
	if listErr != "" {
		r.warn("could not list running jails via "+rt, firstLine(listErr))
		return
	}
	if len(cnames) == 0 {
		r.ok("no jails running — nothing to probe")
		return
	}
	// Whether any loopback-TLS probe actually ran, so the caveat below appears only
	// under greens it qualifies. A run with nothing but AF_UNIX sockets has no
	// loopback hop to be wrong about — the socket is bind-mounted, not routed — and a
	// caveat printed there would train the reader to skip it where it matters.
	probedLoopbackTLS := false
	for _, cname := range cnames {
		socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
		for _, lp := range externals {
			label := fmt.Sprintf("loophole %s @ %s", lp.Name, cname)
			if lp.Name == brokerLoopholeName {
				probedLoopbackTLS = true
				o.checkBrokerRelay(r, label,
					filepath.Join(socketsDir, lp.Name+paths.ServiceEndpointExt), rt, cname)
				continue
			}
			if lp.Transport == loopholes.TransportLoopbackTLS {
				probedLoopbackTLS = true
				o.checkLoopbackTLSService(r, label, filepath.Join(socketsDir, lp.Name+paths.ServiceEndpointExt), lp.Name)
				continue
			}
			sockPath := filepath.Join(socketsDir, lp.Name+".sock")
			if !o.PathExists(sockPath) {
				r.fail(label+": no socket",
					fmt.Sprintf("Expected %s.  Daemon never started or "+
						"crashed at spawn.  Tail "+
						"~/.local/share/yolo-jail/logs/host-service-%s.log "+
						"for the reason; restart the jail to respawn.", sockPath, lp.Name))
				continue
			}
			conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
			if err != nil {
				r.fail(label+": socket dead",
					fmt.Sprintf("connect(%s) failed: %s.  "+
						"Daemon process likely exited; restart the jail.", sockPath, err))
				continue
			}
			_ = conn.Close()
			r.ok(label + ": socket accepting")
		}
	}
	if probedLoopbackTLS {
		r.dim(hostSideProbeCaveat)
	}
}

// checkLoopbackTLSService probes one loopback-TLS host service end-to-end, naming
// the failing LAYER.
//
// The prober runs HOST-SIDE and as the same uid that published the file, so it can
// read the endpoint and authenticate exactly as the jail does — a property that only
// exists because the token lives in the file rather than in the jail's environment.
// It never prints the file's contents: that file is this jail's credential.
func (o *Options) checkLoopbackTLSService(r *reporter, label, endpointPath, name string) {
	if !o.PathExists(endpointPath) {
		r.fail(label+": no endpoint published",
			fmt.Sprintf("Expected %s.  Daemon never started or "+
				"crashed at spawn.  Tail "+
				"~/.local/share/yolo-jail/logs/host-service-%s.log "+
				"for the reason; restart the jail to respawn.", endpointPath, name))
		return
	}
	if !svcendpoint.Probe(endpointPath) {
		r.fail(label+": endpoint file incomplete",
			fmt.Sprintf("%s exists but does not parse as a complete endpoint "+
				"(address, certificate, token).  It was truncated or written by an "+
				"older yolo; restart the jail to republish it.", endpointPath))
		return
	}
	// DialLocal, not Dial: the published address names the runtime's gateway, which
	// a jail resolves and this host does not. Same port, same pinned cert, same
	// token — only the name substituted. That substitution is why the PASS below
	// labels itself host-side; see hostSideProbeCaveat.
	conn, err := svcendpoint.DialLocal(endpointPath, 2*time.Second)
	if err != nil {
		detail := fmt.Sprintf("Dialing the listener named by %s failed: %s.  "+
			"Daemon process likely exited; restart the jail.", endpointPath, err)
		if errors.Is(err, svcendpoint.ErrAuthRejected) {
			detail = fmt.Sprintf("The listener named by %s rejected the token in that "+
				"file.  The file is stale relative to the running daemon (it restarted "+
				"and republished, or a predecessor's file was left behind); restart the "+
				"jail.", endpointPath)
		}
		r.fail(label+": listener unreachable", detail)
		return
	}
	_ = conn.Close()
	r.ok(label + ": endpoint accepting (cert-pinned, token-authenticated) — host-side, " +
		"says nothing about in-jail reachability")
}

// hostSideProbeCaveat is the footnote under every green above, printed ONCE per run.
//
// It is here because the greens cannot be made honest by dialling differently. Both
// probes use svcendpoint.DialLocal, which keeps the published PORT and substitutes
// 127.0.0.1 — where the daemons bind, and the one address a jail cannot use. That
// substitution is not a wiring mistake to fix: `yolo check` runs HOST-SIDE, and the
// ADVERTISED address (the runtime's gateway name) is only meaningful from inside a
// network namespace the runtime built. A host-side prober therefore cannot fail for
// the reason a jail's clients fail, which is how a total loopback-TLS outage sat under
// an all-green check for four days (docs/design/loopback-tls-reachability.md §7).
//
// So the output is made honest instead: each green says what it is, and this says
// where the answer it CANNOT give actually lives. Once, not per service — on a broken
// host every service fails for the same reason at the same instant, and a paragraph
// repeated under each one is a paragraph nobody reads (the same rule
// internal/entrypoint/reachability.go follows for its own shared explanation).
const hostSideProbeCaveat = "the probes above are HOST-SIDE: they dial 127.0.0.1, " +
	"where these daemons bind, so a green means the daemon answers — never that a JAIL " +
	"can reach it. Only the in-jail probe that runs at jail startup can say that " +
	"(docs/design/loopback-tls-reachability.md §7)."

// firstLine returns the first line of s, or "" when s is empty.
func firstLine(s string) string {
	if s == "" {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return s[:i]
		}
	}
	return s
}
