package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE macos-user BACKEND-PARITY FIXES, ASSERTED ON THE HARDWARE — docs/design/backend-parity.md
// §5 rows #6, #7, #9 and #14, as the code states them TODAY.
//
// Every one of these was unit-tested and mutation-checked on Linux on 2026-08-24 and never ran
// on a Mac, and three of the four have since been REWRITTEN by later work, so each test below
// asserts the current behavior rather than the 2026-08-24 fix:
//
//	#6  TestMacosUserReportsOnlyPlatformInertLoopholes  the backend axis is gone (every host daemon starts); the platform axis and the jail-half decline remain
//	#7  TestMacosUserStartsAConfigDeclaredLoophole      a config-declared loophole is STARTED here now, and its endpoint reaches the sandbox
//	#9  TestMacosUserSaysResourcesAreIgnoredAndRelocatesTheCache  resources still warns and is ignored; cache_relocations is delivered now; the third warning is retired
//	#14 TestMacosUserDeliversHostBytesByCopy            reads-host and host_files sources cross by copy, a DIRECTORY source too since 2026-10-05
//
// Unlike the Apple Container file these are REAL ASSERTIONS, not experiments: macos-user.yml
// runs on a GitHub-hosted runner every night, and each assertion is a claim the code already
// makes in a comment or a launch line. A red here is either a defect in that claim or a claim
// that needs correcting, and the message says which comment to read.
//
// WHAT AN ARGV CANNOT SHOW is the target in every case, because the argv half is already pinned
// on Linux (backendwarns_test.go, loopholeinert_test.go, jaildaemondecline_test.go,
// internal/macosuser): the launch lines reaching a real launch's output, the platform gate
// keeping a daemon from spawning on darwin, a 0600 endpoint file being readable by the sandbox
// account through its ACL grant (never executed before — backend-parity.md OQ-BP-5), the limits
// a sandboxed process really has, and the bytes that arrive in the sandbox home.
//
// ONE LAUNCH PER TEST, for macosusertools_test.go's reason: every launch builds a native floor.

// macosUserServiceEnvVar is the variable a loopback-TLS service's endpoint file is named by in
// the sandbox: YOLO_SERVICE_<NAME>_ENDPOINT, with the name upper-cased and every run of
// non-alphanumerics collapsed to one underscore (run.serviceEnvSlug is the authority).
func macosUserServiceEnvVar(name string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			under = false
		} else if !under && b.Len() > 0 {
			b.WriteByte('_')
			under = true
		}
	}
	return paths.ServiceEnvVarPrefix + strings.TrimSuffix(b.String(), "_") + paths.ServiceEnvVarSuffix
}

// macosUserRunProbe launches script and fails the test when the launch did not run it. opts
// reach the launch as runMacosUser's do.
func macosUserRunProbe(t *testing.T, fix, ws, script string, opts ...runOption) result {
	t.Helper()
	r := runMacosUser(t, ws, script, opts...)
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END ===") {
		t.Fatalf("backend-parity %s: the macos-user launch did not run its probe (rc %d), so "+
			"nothing below can be read as a result.\nstdout:\n%s\nstderr:\n%s",
			fix, r.rc, r.stdout, r.stderr)
	}
	return r
}

// TestMacosUserReportsOnlyPlatformInertLoopholes is fix #6 (`35448719`) as it stands today.
//
// The 2026-08-24 fix reported every loophole inert on this backend, because the arm returned
// before startLoopholes. Since 2026-09-17 the arm goes through startLoopholesDisclosed and
// starts every admitted host daemon, so backendInertReason has no macos-user case and the
// report is the PLATFORM axis alone (loopholeinert.go's header). What is inert here instead is
// the JAIL half, declined by name (jaildaemondecline.go). So, with `claude` and `journal`
// selected and journal switched on:
//
//   - journal declares `platforms: ["linux"]`: its line names the platform, and its host daemon
//     does NOT start, so the sandbox has no endpoint variable for it;
//   - claude's broker is NOT reported inert — it starts, and the sandbox has its endpoint;
//   - claude's jail daemons are declined by name.
func TestMacosUserReportsOnlyPlatformInertLoopholes(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude", "journal"], "loopholes": {"journal": {"enabled": true}}}`)
	ws := macosUserWorkspace(t, `{}`)
	journalVar := macosUserServiceEnvVar("journal")
	brokerVar := macosUserServiceEnvVar("claude-oauth-broker")
	r := macosUserRunProbe(t, "#6", ws, strings.Join([]string{
		`echo "=== ENV ==="`,
		`echo "JOURNAL=${` + journalVar + `-UNSET}"`,
		`echo "BROKER=${` + brokerVar + `-UNSET}"`,
		`echo "=== END ==="`,
	}, "\n"))
	out := r.combined()
	env := section(r.stdout, "=== ENV ===", "=== END ===")

	wantPlatform := "journal: loophole journal is unsupported on " + goruntime.GOOS + "/"
	if !strings.Contains(out, wantPlatform) {
		t.Errorf("the launch did not report journal inert by PLATFORM (want a line containing "+
			"%q). journal declares `platforms: [\"linux\"]` and was switched on, so the platform "+
			"producer (loopholes.PlatformInertNotes) owes one line here.\n%s", wantPlatform, out)
	}
	if !strings.Contains(env, "JOURNAL=UNSET") {
		t.Errorf("the sandbox has %s set, so journal's host daemon STARTED on darwin despite "+
			"declaring linux only — the platform gate did not hold at spawn.\n%s", journalVar, env)
	}
	if strings.Contains(out, "loophole claude-oauth-broker is inert") {
		t.Errorf("the launch reported claude-oauth-broker inert. macos-user starts every admitted "+
			"host daemon (run.go's macos-user arm), so the backend axis has nothing to say here "+
			"and a line saying it is the underclaim loopholeinert.go's header retracted.\n%s", out)
	}
	if strings.Contains(env, "BROKER=UNSET") {
		t.Errorf("the sandbox has no %s: claude's broker did not start or its endpoint was not "+
			"handed to the sandbox, so the pack whose loophole is NOT reported inert is inert "+
			"anyway.\n%s\nlaunch output:\n%s", brokerVar, env, out)
	}
	// The guest declines the daemons it does not run, each named with its reason
	// (noteMacosUserJailDaemonDeclines): the OAuth terminator (it needs a container's
	// --add-host and :443), the wire bridge's jail daemon (a pack service runs its host half
	// here), and since HS-D15 claude's OpenAI refresh adapter, whose doorway the launch opens
	// outside the sandbox instead (docs/design/host-notch-services.md HS-D15).
	for _, want := range []string{
		"Declined: these jail daemons do not run in the macos-user sandbox",
		"claude-oauth-broker: yolo-jaild oauth-terminator — ",
		"wire-bridge: yolo-jaild wire-bridge — ",
		"openai-auth-broker: yolo-jaild openai-auth-adapter --listen ",
		"(its doorway runs for this launch, outside the sandbox)",
		`Opened the "openai-auth-broker" doorway (pack "openai-auth"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch did not say %q (noteMacosUserJailDaemonDeclines, "+
				"startMacosUserDoorways).\n%s", want, out)
		}
	}
}

// TestMacosUserStartsAConfigDeclaredLoophole is fix #7 (`6a53a2a3`) as it stands today.
//
// The 2026-08-24 fix REPORTED a config-declared loophole inert on this backend. Since the
// lifecycle was generalised it is STARTED instead, like a pack's (startLoopholesDisclosed →
// startLoopholes, which walks the config's `loopholes` too). So the hardware question is the
// whole chain an argv cannot show: the user's command really runs on the host, its front
// publishes a 0600 endpoint file, the sandbox account can READ that file through the ACL grant
// macosuser.EndpointGrantCommands stages (OQ-BP-5 — `chmod +a` has never executed before), and
// the sandbox can reach the front on the launcher's loopback.
//
// The daemon is `nc -lkU`, macOS's own netcat binding the unix socket yolo names, which is the
// whole of what a `publishes: socket` config daemon owes. Only the endpoint's host:port field is
// ever printed — the file's last field is a bearer token.
func TestMacosUserStartsAConfigDeclaredLoophole(t *testing.T) {
	requireMacosUser(t)
	marker := filepath.Join(resolvedTempDir(t), "config-loophole-ran")
	const name = "yolo-it-config-svc"
	packHome(t, fmt.Sprintf(`{"loopholes": {%q: {"description": "backend-parity #7 probe", `+
		`"command": ["/bin/sh", "-c", "echo ran > %s; exec /usr/bin/nc -lkU {socket}"]}}}`, name, marker))
	ws := macosUserWorkspace(t, `{}`)
	envVar := macosUserServiceEnvVar(name)
	r := macosUserRunProbe(t, "#7", ws, strings.Join([]string{
		`ep="${` + envVar + `-}"`,
		`echo "=== EP ==="`,
		`echo "VAR=${ep:-UNSET}"`,
		`if [ -n "$ep" ] && head -c 1 "$ep" >/dev/null 2>&1; then echo READABLE; else echo UNREADABLE; fi`,
		`hp="$(cut -d' ' -f1 "$ep" 2>/dev/null)"; echo "HOSTPORT=$hp"`,
		`if [ -n "$hp" ] && (exec 3<>"/dev/tcp/${hp%:*}/${hp##*:}") 2>/dev/null; then echo DIAL-OK; else echo DIAL-FAILED; fi`,
		`echo "=== END ==="`,
	}, "\n"))
	out := r.combined()
	ep := section(r.stdout, "=== EP ===", "=== END ===")

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the config-declared loophole's command never ran on the host (%v). A "+
			"`loopholes.<name>.command` entry is started on this backend like a pack's host "+
			"daemon; a silent skip here is the defect #7 was filed for.\n%s", err, out)
	}
	if strings.Contains(out, "loophole "+name+" is") {
		t.Errorf("the launch reported the config-declared loophole inert, but this backend "+
			"starts it — the report and the lifecycle disagree:\n%s", out)
	}
	if strings.Contains(ep, "VAR=UNSET") {
		t.Fatalf("the sandbox has no %s, so the service's endpoint never reached it and "+
			"nothing below can be checked.\n%s\nlaunch output:\n%s", envVar, ep, out)
	}
	if !strings.Contains(ep, "READABLE") || strings.Contains(ep, "UNREADABLE") {
		t.Errorf("the sandbox account cannot READ the endpoint file. It is 0600 and the "+
			"publisher's, and the only thing that lets _yolojail read it is the ACL entry "+
			"macosuser.EndpointGrantCommands stages (backend-parity.md OQ-BP-5) — so the grant "+
			"did not take, and every loopback-TLS service on this backend is unreachable.\n%s", ep)
	}
	if !strings.Contains(ep, "DIAL-OK") {
		t.Errorf("the sandbox read the endpoint and could not connect to the host:port it "+
			"names. This backend shares the launcher's network stack by construction "+
			"(sharesLauncherNetns), so the front should answer on the loopback.\n%s", ep)
	}
}

// TestMacosUserSaysResourcesAreIgnoredAndRelocatesTheCache is fix #9 (`8ab03d2e`) as it stands
// today.
//
// One of its three warnings stands. `resources` still warns — for pids_limit alone now: io is set
// as the disk policy, cpus is honored cooperatively and memory by the sampled guard
// (internal/macosuser/orchestrator.go; TestMacosUserIOPriorityIsApplied and
// TestMacosUserMemoryGuard are their hardware tests). `cache_relocations` stopped warning on
// 2026-10-05, when it began to be DELIVERED here: a link the bootstrap lays at ~/.cache/<subdir>
// to the target, which the Seatbelt profile opens (docs/plans/cache-relocation.md, the macos-user
// section). So the hardware checks what each line SAYS: that the sandboxed process runs with the
// invoking user's own virtual-memory limit, and that a relocated cache subdir IS relocated — a
// file written there in the sandbox lands at the target, and the launch discloses the link rather
// than calling the key unimplemented. The third warning, pack `state` at scope:workspace being
// machine-wide, was retired when the per-workspace home layout shipped;
// TestMacosUserHomeTierIsPerWorkspace is its hardware test, so it is not repeated here.
//
// THE TARGET IS UNDER /Users/Shared, in a 0755 folder of the test's own, and does not exist
// before the launch, so yolo makes it and grants it the sandbox's access: a target under the
// runner's /var/folders temp dir, which this test used before, sits in a per-user directory the
// sandbox account cannot traverse, and the launch now refuses one (the DAC preflight).
// integration/macosuserrelocations_test.go has the cases this one does not: that what the sandbox
// wrote stays yours to delete, and the refusal of a populated target without the access.
func TestMacosUserSaysResourcesAreIgnoredAndRelocatesTheCache(t *testing.T) {
	requireMacosUser(t)
	target := macosUserRelocationTarget(t, sharedUsersDir)
	const subdir = "yolo-it-reloc"
	packHome(t, fmt.Sprintf(`{"cache_relocations": {%q: %q}}`, subdir, target))
	ws := macosUserWorkspace(t, `{"resources": {"pids_limit": 4096, "cpus": 2}}`)
	probeName := "probe-" + acParityNonce()

	r := macosUserRunProbe(t, "#9", ws, strings.Join([]string{
		`echo "=== LIMITS ==="; echo "ULIMIT_V=$(ulimit -v)"`,
		`echo "=== RELOC ==="; echo x > ~/.cache/` + subdir + `/` + probeName + ` && echo WROTE || echo WRITE-FAILED`,
		`echo "=== END ==="`,
	}, "\n"))
	out := r.combined()

	for _, want := range []string{
		"resources are NOT enforced on macos-user", "so pids_limit is read and ignored",
		"Cache relocation: ~/.cache/" + subdir + " → " + target,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch output lacks %q. The resources warning is the whole of what this "+
				"backend does with that key, and the relocation line is the disclosure of a folder "+
				"the sandbox writes (orchestrator.go), so a missing one is a silent change.\n%s", want, out)
		}
	}
	if strings.Contains(out, "cache_relocations are NOT implemented") {
		t.Errorf("the retired warning is still printed although the relocation is delivered:\n%s", out)
	}
	// cpus is acted on, so it is named on its own line and never on the ignored one.
	if strings.Contains(out, "so cpus") || strings.Contains(out, "cpus, pids_limit") ||
		!strings.Contains(out, "resources.cpus (2) is honored cooperatively") {
		t.Errorf("resources.cpus is honored cooperatively and must say so, not be called "+
			"ignored:\n%s", out)
	}

	hostV, err := exec.Command("/bin/bash", "-c", "ulimit -v").Output()
	if err != nil {
		t.Fatalf("reading this process's own virtual-memory limit: %v", err)
	}
	gotV := strings.TrimSpace(strings.TrimPrefix(
		strings.TrimSpace(section(r.stdout, "=== LIMITS ===", "=== RELOC ===")), "ULIMIT_V="))
	if gotV != strings.TrimSpace(string(hostV)) {
		t.Errorf("the sandbox's `ulimit -v` is %q and the launcher's is %q. The warning says "+
			"`resources` are read and ignored and the agent runs with the user's own limits; "+
			"a different limit means something now enforces one, and the warning is wrong.",
			gotV, strings.TrimSpace(string(hostV)))
	}

	if !strings.Contains(section(r.stdout, "=== RELOC ===", "=== END ==="), "WROTE") {
		t.Errorf("the sandbox could not write ~/.cache/%s, the relocated subdir:\n%s", subdir, r.stdout)
	}
	if _, err := os.Stat(filepath.Join(target, probeName)); err != nil {
		t.Errorf("a file the sandbox wrote to ~/.cache/%s is not at the relocation target %s (%v): "+
			"the link the bootstrap lays, or the profile's allow for the target, did not take", subdir, target, err)
	}
}

// TestMacosUserDeliversHostBytesByCopy is fix #14 (`4402e33a`) as it stands today.
//
// #14 warned that on this backend a pack `reads-host` surface renders from its DEFAULTS and a
// source-bearing `host_files` entry is dropped from the wire. Both warnings were RETIRED on
// 2026-09-13 because DP-L1 closed both gaps: host bytes now cross by COPY into a root-owned tree
// (internal/cli/run/macosctxtree.go, macosuser.StageCtxCommands), named to the sandbox by
// YOLO_CTX_ROOT. The one line that survived, for a host_files entry whose source is a DIRECTORY,
// went on 2026-10-05, when that shape began crossing by copy too, confined to its source
// (run's copyCtxTreeConfined). So this asserts all of it on the hardware: the user's own
// ~/.claude/settings.json is composed into the sandbox's, a FILE entry arrives with its bytes, a
// DIRECTORY entry arrives with its nested files, a link inside it that leads out of the source
// does not carry the file it points at, and the launch no longer says directories do not cross.
func TestMacosUserDeliversHostBytesByCopy(t *testing.T) {
	requireMacosUser(t)
	nonce := acParityNonce()
	packHome(t, `{"packs": ["claude"], "host_files": [`+
		`{"path": "~/.config/yolo-it-mu/probe.txt", "source": "~/yolo-it-mu-source.txt"}, `+
		`{"path": "~/.config/yolo-it-mu-dir/", "source": "~/yolo-it-mu-srcdir/"}]}`)
	home := os.Getenv("HOME")
	// RENDER-MARK PARITY made this backend label a host layer yolo has rendered, as the
	// container backends already did (notch-scoped-config-contributions.md NS-D3). So the
	// MACHINE's host-render mark, linked into this isolated home through the shared state dir,
	// would now drop the hand-written settings.json below as "yolo's own render" — and the
	// self-hosted Mac runs as the maintainer's own account, whose home has been host-rendered
	// (hostprovenanceisolation_test.go). TestAppleContainerReadsHostGrantArrives met the same
	// shape on 2026-09-25 and took the same fix.
	privateHostProvenance(t, home)
	if entrypoint.HostSurfaceRendered(home, claudeSettingsSurface) {
		t.Fatalf("#14: this home still carries a host-render mark for claude/settings, so the " +
			"launcher would drop its settings.json as yolo's own render — NOTHING WOULD BE " +
			"MEASURED about #14 (privateHostProvenance did not take)")
	}
	for path, body := range map[string]string{
		filepath.Join(home, ".claude", "settings.json"):             `{"yoloItReadsHostProbe": "` + nonce + `"}`,
		filepath.Join(home, "yolo-it-mu-source.txt"):                "HOSTFILE-" + nonce,
		filepath.Join(home, "yolo-it-mu-srcdir", "inner.txt"):       "DIR-" + nonce,
		filepath.Join(home, "yolo-it-mu-srcdir", "sub", "deep.txt"): "DEEP-" + nonce,
		filepath.Join(home, "yolo-it-mu-srcdir", "dark", "colors"):  "DARK-" + nonce,
		filepath.Join(home, "yolo-it-mu-outside", "secret.txt"):     "OUTSIDE-" + nonce,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// An owner-only file, whose bits must survive the world-readable staged copy (a 0644 key is
	// one OpenSSH refuses), and a folder link inside the source, which a container bind resolves.
	if err := os.WriteFile(filepath.Join(home, "yolo-it-mu-srcdir", "key"), []byte("KEY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dark", filepath.Join(home, "yolo-it-mu-srcdir", "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "yolo-it-mu-outside", "secret.txt"),
		filepath.Join(home, "yolo-it-mu-srcdir", "out-link")); err != nil {
		t.Fatal(err)
	}
	ws := macosUserWorkspace(t, `{}`)
	r := macosUserRunProbe(t, "#14", ws, strings.Join([]string{
		`echo "=== SETTINGS ==="; cat ~/.claude/settings.json 2>&1`,
		`echo "=== FILE ==="; cat ~/.config/yolo-it-mu/probe.txt 2>&1`,
		`echo "=== DIR ==="; cat ~/.config/yolo-it-mu-dir/inner.txt ~/.config/yolo-it-mu-dir/sub/deep.txt 2>&1`,
		`echo "=== LINK ==="; cat ~/.config/yolo-it-mu-dir/out-link 2>&1`,
		`echo "=== DIRLINK ==="; cat ~/.config/yolo-it-mu-dir/current/colors 2>&1`,
		`echo "=== MODE ==="; stat -f '%Lp' ~/.config/yolo-it-mu-dir/key 2>&1`,
		`echo "=== END ==="`,
	}, "\n"))
	out := r.combined()

	if got := section(r.stdout, "=== SETTINGS ===", "=== FILE ==="); !strings.Contains(got, nonce) {
		t.Errorf("the sandbox's ~/.claude/settings.json does not carry the user's own layer — it "+
			"rendered from its defaults, which is the gap #14 warned about and DP-L1 retired the "+
			"warning for.\n%s", got)
	}
	if got := section(r.stdout, "=== FILE ===", "=== DIR ==="); !strings.Contains(got, "HOSTFILE-"+nonce) {
		t.Errorf("a source-bearing host_files FILE entry did not arrive with its bytes. DP-L1 "+
			"delivers it by copy, and the warning that it is dropped was retired on that basis.\n%s", got)
	}
	dir := section(r.stdout, "=== DIR ===", "=== LINK ===")
	for _, want := range []string{"DIR-" + nonce, "DEEP-" + nonce} {
		if !strings.Contains(dir, want) {
			t.Errorf("a host_files DIRECTORY entry did not arrive with %q: the launch copies the tree "+
				"since 2026-10-05 (buildMacosCtxTree), and the line saying it does not was retired on "+
				"that basis.\n%s", want, dir)
		}
	}
	if got := section(r.stdout, "=== DIRLINK ===", "=== MODE ==="); !strings.Contains(got, "DARK-"+nonce) {
		t.Errorf("a folder link inside the source did not deliver its folder, as a container's bind "+
			"and boot copy do (copyCtxTreeConfined).\n%s", got)
	}
	if got := strings.TrimSpace(section(r.stdout, "=== MODE ===", "=== END ===")); got != "600" {
		t.Errorf("an owner-only file of a host_files DIRECTORY entry arrived with mode %q, want 600 "+
			"as on a container (entrypoint.HostFileDirModes)", got)
	}
	if got := section(r.stdout, "=== LINK ===", "=== DIRLINK ==="); strings.Contains(got, "OUTSIDE-"+nonce) {
		t.Errorf("a link inside the source that leads out of it carried the file it points at into "+
			"the sandbox — a host-file read nobody declared (copyCtxTreeConfined).\n%s", got)
	}
	if strings.Contains(out, "does not cross on macos-user") {
		t.Errorf("the launch still says a directory entry does not cross, and it does:\n%s", out)
	}
}
