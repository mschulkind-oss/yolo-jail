package packload_test

// loopholesource_test.go pins the `loophole` kind's TOTAL claim enumeration — the
// load-bearing rule of docs/design/loophole-packaging.md §3.3 — and the resolution
// behaviour around it.
//
// WHAT THE ENUMERATION IS FOR CHANGED ON 2026-09-04, and the rule got MORE load-bearing
// rather than less. It used to feed two things: the footprint, and the claim set the
// fetched-pack approval prompt showed and the lockfile stored. The blocker was that
// `packMayAccessHost` returned TRUE on an EMPTY set, so a loophole declaring only
// `host_bind_mounts` + `host_devices` and emitting no claims crossed unprompted. OQ-TP9
// (docs/design/trust-paths.md) deleted the prompt, the lockfile record and the gate — and
// left the footprint as the ONLY place a user learns a pack reaches their machine. So a
// crossing that emits no claim is no longer "waved through a gate"; it is a crossing
// NOBODY IS TOLD ABOUT. Every test below that asserts a claim EXISTS is asserting that.
//
// The claims are read through the FOOTPRINT here, because that is where they are now
// consumed — see disclosedCrossings.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// disclosedCrossings renders this pack's `loophole` claims as one line each — the footprint
// TARGET (the collision key) plus its DETAIL (what a user reads) — sorted.
//
// It replaces packload.Pack.LoopholeHostAccessClaims, which produced a second, raw
// rendering of the same set for the approval prompt and the lockfile. OQ-TP9 deleted both
// consumers, so the display rendering is the only one left; joining target and detail is
// what keeps these assertions able to see everything the raw string used to carry.
func disclosedCrossings(p *packload.Pack) []string {
	var out []string
	for _, c := range packload.FootprintOf(p).Claims {
		if c.Kind == packdecl.KindLoophole {
			out = append(out, c.Target+" "+c.Detail)
		}
	}
	sort.Strings(out)
	return out
}

// writeLoopholePack writes a pack whose pack.json declares one `loophole` contribution per
// module, with each module's manifest.jsonc body supplied by the caller. Returns the root.
func writeLoopholePack(t *testing.T, modules map[string]string) string {
	t.Helper()
	root := t.TempDir()
	var entries []string
	for name, manifest := range modules {
		dir := filepath.Join(root, "loopholes", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if manifest != "" {
			if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"),
				[]byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		entries = append(entries, `{"kind":"loophole","from":"loopholes/`+name+`"}`)
	}
	pj := `{"name":"acme","contributes":[` + strings.Join(entries, ",") + `]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func loadPack(t *testing.T, root string) *packload.Pack {
	t.Helper()
	p, problems := packload.LoadDir(root, "acme")
	if len(problems) > 0 {
		t.Fatalf("loading pack: %v", problems)
	}
	return p
}

// hasClaimContaining reports whether any claim contains every one of subs.
func hasClaimContaining(claims []string, subs ...string) bool {
	for _, c := range claims {
		all := true
		for _, sub := range subs {
			if !strings.Contains(c, sub) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// THE central case: a loophole with NO daemon and NO intercepts, declaring only bind
// mounts and a device, must still emit a claim for each. This is the exact manifest whose
// zero-claim footprint got a fetched pack the user's SSH keys and the whole host
// filesystem with no prompt (loophole-packaging.md §3.3).
func TestBindMountsAndDevicesEachEmitAClaim(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"nice": `{
	  "name": "nice",
	  "transport": "none",
	  "host_bind_mounts": [
	    {"host": "$HOME/.ssh", "container": "/ctx/keys"},
	    {"host": "/", "container": "/ctx/root"}
	  ],
	  "host_devices": ["/dev/snd"]
	}`})
	claims := disclosedCrossings(loadPack(t, root))
	if len(claims) != 3 {
		t.Fatalf("claims = %v (%d), want 3 — one per bind mount and one per device. A "+
			"crossing with no claim is a crossing nobody is told about: since OQ-TP9 the "+
			"footprint is the only place this is disclosed", claims, len(claims))
	}
	if !hasClaimContaining(claims, "$HOME/.ssh", "/ctx/keys") {
		t.Errorf("no claim names the ~/.ssh bind: %v", claims)
	}
	if !hasClaimContaining(claims, "/ctx/root") {
		t.Errorf("no claim names the root-filesystem bind: %v", claims)
	}
	if !hasClaimContaining(claims, "/dev/snd") {
		t.Errorf("no claim names the device passthrough — a device node is not weaker than a "+
			"read-write bind mount: %v", claims)
	}
}

// An intercept claims EVEN WITH transport:"none" and no daemon: it runs no host code and
// still installs a CA trusted by every TLS client in the jail.
func TestInterceptClaimsWithNoDaemon(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"proxy": `{
	  "name": "proxy",
	  "transport": "none",
	  "intercepts": [{"host": "api.acme.com"}, {"host": "cdn.acme.com"}]
	}`})
	claims := disclosedCrossings(loadPack(t, root))
	if len(claims) != 2 {
		t.Fatalf("claims = %v, want one per intercept even with no host_daemon", claims)
	}
	for _, host := range []string{"api.acme.com", "cdn.acme.com"} {
		if !hasClaimContaining(claims, host, "CA") {
			t.Errorf("intercept claim for %s must name the CA it installs: %v", host, claims)
		}
	}
}

// The base claim is HOST EXECUTION, it folds doctor_cmd in, and it says so in words —
// "RUNS" and "on your machine" — because ReviewWorthy is one boolean and a host read must
// not read the same as running code as the user.
func TestDaemonClaimSpellsOutHostExecution(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"acme-proxy": `{
	  "name": "acme-proxy",
	  "host_daemon": {"cmd": ["python3", "{loophole_dir}/acme-daemon.py", "--socket", "{socket}"],
	                  "publishes": "socket"},
	  "doctor_cmd": ["python3", "{loophole_dir}/doctor.py"]
	}`})
	claims := disclosedCrossings(loadPack(t, root))
	if len(claims) != 1 {
		t.Fatalf("claims = %v, want ONE base claim — doctor_cmd is host execution too, so it "+
			"joins the daemon's claim rather than getting its own line", claims)
	}
	c := claims[0]
	for _, want := range []string{"RUNS", "on your machine", "acme-daemon.py", "doctor.py"} {
		if !strings.Contains(c, want) {
			t.Errorf("base claim %q is missing %q", c, want)
		}
	}
}

// G2a: the claim string is the RAW argv — placeholders UNEXPANDED, nothing elided.
//
// Both halves are load-bearing, and both fail catastrophically rather than cosmetically.
// An ELIDED argv collapses two different daemons onto one approved claim. An EXPANDED
// {loophole_dir} makes the approved string machine-specific, so it never matches an
// approval recorded elsewhere and re-prompts forever — and promptYesNo fails closed on a
// non-TTY, so packMayAccessHost then refuses the loophole permanently.
func TestClaimStringIsRawArgv(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"acme": `{
	  "name": "acme",
	  "host_daemon": {"cmd": ["python3", "{loophole_dir}/very/deeply/nested/acme-daemon.py",
	                          "--flag-one", "--flag-two", "--flag-three"],
	                  "publishes": "socket"}
	}`})
	p := loadPack(t, root)
	claims := disclosedCrossings(p)
	if len(claims) != 1 {
		t.Fatalf("claims = %v, want 1", claims)
	}
	c := claims[0]
	if strings.Contains(c, "…") || strings.Contains(c, "...") {
		t.Errorf("claim %q elides — it is the ONLY place a user sees this argv, and an "+
			"ellipsis is where the interesting flag hides", c)
	}
	if !strings.Contains(c, "{loophole_dir}") {
		t.Errorf("claim %q does not carry the RAW {loophole_dir} token — an expanded one is a "+
			"staging-specific absolute path, so the collision key differs per machine and "+
			"the line a reader checks against the manifest no longer matches it", c)
	}
	if strings.Contains(c, root) {
		t.Errorf("claim %q contains the pack's absolute staging root — it must read the same "+
			"on another machine", c)
	}
	for _, flag := range []string{"--flag-one", "--flag-two", "--flag-three"} {
		if !strings.Contains(c, flag) {
			t.Errorf("claim %q dropped %s — nothing is elided", c, flag)
		}
	}
	// STABLE: the same tree read twice yields byte-identical claims, or the collision pass
	// is a coin flip.
	if again := disclosedCrossings(p); again[0] != c {
		t.Errorf("claims are not stable across reads: %q vs %q", c, again[0])
	}
}

// The argv rendering is INJECTIVE: two different argvs must never produce one claim
// string. Nothing execs the claim (the spawn reads the argv list), so this is not about a
// shell — it is about the claim being a comparison key. A bare space join collapses
// ["sh","-c","a b"] onto ["sh","-c","a","b"], which is the same failure an ellipsis
// causes, reached by accident instead of by decision.
func TestDaemonArgvRenderingIsInjective(t *testing.T) {
	claimFor := func(t *testing.T, cmdJSON string) string {
		t.Helper()
		root := writeLoopholePack(t, map[string]string{"acme": `{
		  "name": "acme",
		  "host_daemon": {"cmd": ` + cmdJSON + `, "publishes": "socket"}
		}`})
		claims := disclosedCrossings(loadPack(t, root))
		if len(claims) != 1 {
			t.Fatalf("claims = %v, want 1", claims)
		}
		return claims[0]
	}
	oneArg := claimFor(t, `["sh", "-c", "a b"]`)
	twoArgs := claimFor(t, `["sh", "-c", "a", "b"]`)
	if oneArg == twoArgs {
		t.Errorf("two different argvs render to the SAME claim (%q) — approving one would "+
			"silently approve the other, which is what makes the claim a comparison key "+
			"rather than a sentence", oneArg)
	}
}

// A socket bind is its OWN claim class, distinct from a file/dir mount, and it says
// read-write. `:ro` is no boundary for an AF_UNIX socket — measured twice in this repo (the
// well-known docker.sock:ro result) — so a claim calling it read-only would be false.
func TestSocketBindIsItsOwnReadWriteClaimClass(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"ipc": `{
	  "name": "ipc",
	  "transport": "none",
	  "host_bind_mounts": [
	    {"host": "${XDG_RUNTIME_DIR}/pulse/native", "container": "/run/pulse/native",
	     "readonly": false},
	    {"host": "$HOME/agent.sock", "container": "/run/agent.sock", "readonly": true},
	    {"host": "{loophole_dir}/asound.conf", "container": "/etc/asound.conf",
	     "readonly": true}
	  ]
	}`})
	claims := disclosedCrossings(loadPack(t, root))
	if len(claims) != 3 {
		t.Fatalf("claims = %v, want one per bind", claims)
	}
	// readonly:false — bidirectional by the manifest's own admission.
	if !hasClaimContaining(claims, "/run/pulse/native", "read-write") {
		t.Errorf("a readonly:false bind must be claimed as read-write host IPC: %v", claims)
	}
	// A `:ro` bind whose basename names a socket is STILL IPC: the kernel's read-only check
	// exempts non-REG/DIR/LNK inodes, so `:ro` buys nothing here.
	if !hasClaimContaining(claims, "/run/agent.sock", "read-write") {
		t.Errorf("a `:ro` bind of a .sock must still be claimed read-write — `:ro` is not a "+
			"boundary for an AF_UNIX socket: %v", claims)
	}
	// A plain file mount is the read class — and its text still carries the socket caveat,
	// because the discriminator is coarser than a stat (see bindIsIPC).
	if !hasClaimContaining(claims, "/etc/asound.conf", "read-only") {
		t.Errorf("a `:ro` file bind should be claimed as a read: %v", claims)
	}
	if !hasClaimContaining(claims, "/etc/asound.conf", "SOCKET") {
		t.Errorf("the mount class must still warn that a SOCKET at that path would be "+
			"read-write regardless of `:ro`, since the class is inferred and not stated: %v",
			claims)
	}
}

// `broker_ip` IS WHAT THE INTERCEPT POINTS AT, so it is folded INTO the intercept claim.
//
// The measured hole: two manifests differing ONLY in broker_ip produced the identical approved
// claim string. RuntimeArgsFor emits `--add-host <intercept.host>:<broker_ip>`, so approving an
// intercept against `host-gateway` (yolo's own front, which is what the default means) and then
// moving the pin silently redirects that hostname to an arbitrary address — inside a jail that
// now trusts the loophole's CA for it — with no re-prompt, because the approved string never
// mentioned where it pointed.
func TestBrokerIPIsFoldedIntoTheInterceptClaim(t *testing.T) {
	claimFor := func(t *testing.T, brokerIP string) string {
		t.Helper()
		body := `{"name":"proxy","transport":"none","intercepts":[{"host":"api.acme.com"}]`
		if brokerIP != "" {
			body += `,"broker_ip":"` + brokerIP + `"`
		}
		root := writeLoopholePack(t, map[string]string{"proxy": body + "}"})
		claims := disclosedCrossings(loadPack(t, root))
		if len(claims) != 1 {
			t.Fatalf("claims = %v, want 1", claims)
		}
		return claims[0]
	}
	defaulted := claimFor(t, "")
	moved := claimFor(t, "10.13.37.4")
	if defaulted == moved {
		t.Errorf("two manifests differing only in broker_ip render to ONE claim (%q). Approving "+
			"an intercept pointed at yolo's own front would then silently approve the same "+
			"hostname pointed anywhere the author later chooses", defaulted)
	}
	// The claim NAMES the address, so the approval prompt says where the hostname goes.
	if !strings.Contains(moved, "10.13.37.4") {
		t.Errorf("the intercept claim does not name the address it points the hostname at: %q", moved)
	}
	// And the DEFAULT is spelled out rather than omitted: "resolves to whatever yolo's default
	// is" is not something a reader of a lockfile can check, and an omitted default would make
	// the defaulted and the explicitly-defaulted manifests two different approvals.
	if !strings.Contains(defaulted, "host-gateway") {
		t.Errorf("the defaulted intercept claim does not name the default address: %q", defaulted)
	}
	if explicit := claimFor(t, "host-gateway"); explicit != defaulted {
		t.Errorf("an explicit broker_ip equal to the default is a DIFFERENT claim from an "+
			"absent one (%q vs %q) — the two declare the same thing, so re-prompting on the "+
			"spelling would be a rule about spelling", explicit, defaulted)
	}
}

// `ca_cert` IS A CROSSING, so it emits its own claim — §3.3's total enumeration reproduced
// on the key draft 1 and its first implementation both missed.
//
// The measured hole: `ca_cert` appeared in NO claim while RuntimeArgsFor emitted
// `-v <CACert>:<containerCA>:ro` for it and joined the container path into
// `-e NODE_EXTRA_CA_CERTS`. So `{"transport":"none","ca_cert":"..."}` — no daemon, no
// intercepts, no binds — produced ZERO claims, `packMayAccessHost` took its `len(want)==0`
// branch ("the gate is moot") and a fetched pack bind-mounted an arbitrary host path into a
// UID-0 jail AND had every node client in that jail trust it as a CA, with no prompt ever.
func TestCACertEmitsItsOwnClaim(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"cahole": `{
	  "name": "cahole",
	  "transport": "none",
	  "ca_cert": "ca.crt"
	}`})
	claims := disclosedCrossings(loadPack(t, root))
	if len(claims) != 1 {
		t.Fatalf("claims = %v (%d), want 1 — a ca_cert with nothing else declared is still a "+
			"crossing: it is bind-mounted from the host and joined into NODE_EXTRA_CA_CERTS. "+
			"With no claim, packMayAccessHost returns TRUE on the empty set", claims, len(claims))
	}
	c := claims[0]
	// The CAPABILITY, not the file. A CA in NODE_EXTRA_CA_CERTS is trusted by every node
	// client in the jail — the same standing capability the intercept claim's text exists to
	// disclose — and it lands there even for a module-relative cert with no intercepts.
	for _, want := range []string{"ca.crt", "TRUSTS", "every node client in the jail"} {
		if !strings.Contains(c, want) {
			t.Errorf("ca_cert claim %q is missing %q — the claim has to disclose the "+
				"capability (a trusted CA), not merely name a path that gets mounted", c, want)
		}
	}
	// And the FOOTPRINT target is separately keyed, like every other crossing, so it is
	// separately approvable rather than folded onto the loophole's base claim.
	fp := packload.FootprintOf(loadPack(t, root))
	var targets []string
	for _, cl := range fp.Claims {
		if cl.Kind == "loophole" {
			targets = append(targets, cl.Target)
		}
	}
	if len(targets) != 1 || !strings.HasPrefix(targets[0], "cahole:ca:") {
		t.Errorf("footprint targets = %v, want one keyed cahole:ca:<path> — every other "+
			"crossing is <name>:<discriminator>", targets)
	}
	// RAW, like every other claim: an absolute or {state} path is carried verbatim, never
	// resolved against this machine.
	if strings.Contains(c, root) {
		t.Errorf("ca_cert claim %q names the staging root — the approval must compare equal "+
			"across machines", c)
	}
}

// Two DIFFERENT ca_certs are two different claims. Approving one must not approve the other:
// the path is the risk-bearing fact for a read, and the CA a pack installs is a read whose
// target the user has to be able to recognize.
func TestCACertClaimDistinguishesThePath(t *testing.T) {
	claimFor := func(t *testing.T, caCert string) string {
		t.Helper()
		root := writeLoopholePack(t, map[string]string{"cahole": `{
		  "name": "cahole", "transport": "none", "ca_cert": ` + caCert + `
		}`})
		claims := disclosedCrossings(loadPack(t, root))
		if len(claims) != 1 {
			t.Fatalf("claims = %v, want 1", claims)
		}
		return claims[0]
	}
	mine := claimFor(t, `"{state}/ca.crt"`)
	theirs := claimFor(t, `"{loophole_dir}/vendor-ca.crt"`)
	if mine == theirs {
		t.Errorf("two different ca_cert paths render to ONE claim (%q) — approving a pack's "+
			"own bundled CA would silently approve any other file it later names", mine)
	}
	if !strings.Contains(mine, "{state}") {
		t.Errorf("claim %q expanded {state} — a resolved path is machine-specific and would "+
			"re-prompt forever", mine)
	}
}

// state_files needs NO claim: it resolves under yolo's own state tree, not a path the user
// would recognise as theirs. A jail_daemon likewise — it runs inside the container, which
// is the one place a pack's code was always allowed to run.
func TestStateFilesAndJailDaemonMakeNoClaim(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"statey": `{
	  "name": "statey",
	  "transport": "none",
	  "state_files": ["ca.crt", "sub/dir/token"],
	  "jail_daemon": {"cmd": ["{jail_loophole_dir}/in-jail.sh"]}
	}`})
	claims := disclosedCrossings(loadPack(t, root))
	if len(claims) != 0 {
		t.Errorf("claims = %v, want none: state_files stays inside yolo's state tree and a "+
			"jail_daemon runs in the container — neither crosses to the host", claims)
	}
}

// A `from` naming a directory the pack does not contain is REFUSED BY NAME, never a silent
// skip — the precedent `skills`' non-conventional `from` set. A missing `from` is a
// pack.json error one layer up (packdecl), which is why the two are different mechanisms.
func TestAbsentModuleDirIsRefusedByName(t *testing.T) {
	root := t.TempDir()
	pj := `{"name":"acme","contributes":[{"kind":"loophole","from":"loopholes/ghost"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	mods, refusals, warnings := loadPack(t, root).LoopholeModules()
	if len(mods) != 0 {
		t.Errorf("mods = %+v, want none for an absent module dir", mods)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v — an absent `from` is a pack.json-level REFUSAL, not the "+
			"discovery layer's warn-and-continue", warnings)
	}
	if len(refusals) != 1 || !strings.Contains(refusals[0], "loopholes/ghost") {
		t.Fatalf("refusals = %v, want one naming the missing dir", refusals)
	}
}

// An unloadable manifest is a WARNING (discovery's layer), and it must NOT produce an
// empty claim set — that is the grant-everything case. It fails closed with an
// "UNENUMERABLE" claim instead.
func TestUnreadableManifestFailsClosedRatherThanClaimingNothing(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"broken": `{ this is not json at all `})
	p := loadPack(t, root)
	mods, refusals, warnings := p.LoopholeModules()
	if len(refusals) != 0 {
		t.Errorf("refusals = %v — an unloadable manifest is the discovery layer's business "+
			"(warn-and-continue), not a pack.json refusal", refusals)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one naming the module", warnings)
	}
	if len(mods) != 1 || mods[0].Decl != nil {
		t.Fatalf("mods = %+v, want one module with no decoded manifest", mods)
	}
	claims := disclosedCrossings(p)
	if len(claims) == 0 {
		t.Fatal("an unreadable manifest produced ZERO claims — the module is still " +
			"DISCOVERED, so a pack whose manifest this build cannot parse would show a clean " +
			"footprint while its loophole crossed")
	}
	if !hasClaimContaining(claims, "UNREADABLE") {
		t.Errorf("claims = %v, want one saying the declaration is unreadable", claims)
	}
	if hasClaimContaining(claims, root) {
		t.Errorf("the fail-closed claim %v names an absolute staging path — it must use the "+
			"DECLARED `from` so it compares equal across machines", claims)
	}
}

// Two module dirs whose basenames agree are two loopholes with one NAME: a self-collision
// the generic Collisions pass cannot see (it skips single-pack groups), and one that would
// silently share every claim target, the state dir and the endpoint.
func TestOnePackCannotShipTwoLoopholesWithOneName(t *testing.T) {
	root := t.TempDir()
	for _, sub := range []string{"a/acme", "vendor/acme"} {
		dir := filepath.Join(root, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"),
			[]byte(`{"name":"acme","transport":"none"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pj := `{"name":"p","contributes":[{"kind":"loophole","from":"a/acme"},` +
		`{"kind":"loophole","from":"vendor/acme"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, "p")
	if len(problems) > 0 {
		t.Fatalf("loading pack: %v", problems)
	}
	_, refusals, _ := p.LoopholeModules()
	if len(refusals) != 1 || !strings.Contains(refusals[0], "acme") {
		t.Fatalf("refusals = %v, want one naming the duplicated loophole name", refusals)
	}
}

// Two PACKS shipping one loophole name is a collision keyed on the NAME. The generic
// exclusive loop cannot catch it: the claim targets carry a discriminator
// (`acme:device:/dev/snd`), so it would compare the two packs' bind mounts instead.
func TestTwoPacksOneLoopholeNameCollides(t *testing.T) {
	var packs []*packload.Pack
	for _, pack := range []string{"one", "two"} {
		root := t.TempDir()
		dir := filepath.Join(root, "loopholes", "acme")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Deliberately DIFFERENT crossings, so no claim target is shared and only a
		// name-keyed pass can see the conflict.
		body := `{"name":"acme","transport":"none","host_devices":["/dev/` + pack + `"]}`
		if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		pj := `{"name":"` + pack + `","contributes":[{"kind":"loophole","from":"loopholes/acme"}]}`
		if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(pj), 0o644); err != nil {
			t.Fatal(err)
		}
		p, problems := packload.LoadDir(root, pack)
		if len(problems) > 0 {
			t.Fatalf("loading pack %s: %v", pack, problems)
		}
		packs = append(packs, p)
	}
	cols := packload.LoopholeNameCollisions(packs)
	if len(cols) != 1 || cols[0].Target != "acme" {
		t.Fatalf("LoopholeNameCollisions = %+v, want one on the name `acme`", cols)
	}
	if len(cols[0].Packs) != 2 {
		t.Errorf("collision must name both packs: %+v", cols[0])
	}
	// And Collisions() must surface it, so `pack footprint` reports it.
	found := false
	for _, c := range packload.Collisions(packs) {
		if c.Target == "acme" {
			found = true
		}
	}
	if !found {
		t.Error("Collisions() does not include the loophole name collision — `pack footprint` " +
			"would report two packs shipping one loophole name as clean")
	}
}

// A pack shipping THREE loopholes is ordinary: exclusivity is per NAME, not per pack — the
// same rule `program` has per `bin`.
func TestThreeLoopholesInOnePackIsOrdinary(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{
		"one":   `{"name":"one","transport":"none","host_devices":["/dev/one"]}`,
		"two":   `{"name":"two","transport":"none","host_devices":["/dev/two"]}`,
		"three": `{"name":"three","transport":"none","host_devices":["/dev/three"]}`,
	})
	p := loadPack(t, root)
	mods, refusals, warnings := p.LoopholeModules()
	if len(refusals) != 0 || len(warnings) != 0 {
		t.Fatalf("refusals=%v warnings=%v — one pack, three DIFFERENT names is not a "+
			"collision", refusals, warnings)
	}
	if len(mods) != 3 {
		t.Fatalf("mods = %d, want 3", len(mods))
	}
	if claims := disclosedCrossings(p); len(claims) != 3 {
		t.Errorf("claims = %v, want one per loophole's device", claims)
	}
	if cols := packload.LoopholeNameCollisions([]*packload.Pack{p}); len(cols) != 0 {
		t.Errorf("collisions = %+v, want none", cols)
	}
}

// ONE FOOTPRINT CARRIES EVERY PRODUCER — pack.json's own contributions AND the loophole
// modules whose declarations live outside it.
//
// It was TestMergedHostAccessClaimsUnionsEveryProducer, over packload.Pack.HostAccessClaims:
// the union both gates compared, sorted and deduplicated because it was a lockfile key.
// OQ-TP9 deleted the gates and the helper; the union PROPERTY is what survives, one layer
// over, because the footprint is now the only report a user gets and a producer missing
// from it is a crossing with no reader at all.
func TestTheFootprintCarriesEveryProducersCrossings(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "loopholes", "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"),
		[]byte(`{"name":"acme","host_daemon":{"cmd":["/bin/acmed"],"publishes":"socket"}}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	pj := `{"name":"acme","contributes":[` +
		`{"kind":"reads-host","host":".config/acme/key","into":"key"},` +
		`{"kind":"loophole","from":"loopholes/acme"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, "acme")
	if len(problems) > 0 {
		t.Fatalf("loading pack: %v", problems)
	}
	var lines []string
	for _, c := range packload.FootprintOf(p).Claims {
		if !c.ReviewWorthy {
			continue
		}
		lines = append(lines, string(c.Kind)+" "+c.Target+" "+c.Detail)
	}
	if !hasClaimContaining(lines, "reads-host", ".config/acme/key") {
		t.Errorf("the footprint dropped pack.json's own producer: %v", lines)
	}
	if !hasClaimContaining(lines, "loophole", "acmed") {
		t.Errorf("the footprint dropped the loophole producer, whose declaration lives in a "+
			"file OUTSIDE pack.json — that is the producer a contributions-only walk misses, "+
			"and it is the one whose crossing is host EXECUTION: %v", lines)
	}
}

// A pack with no `loophole` contribution claims nothing and reports nothing — the kind must
// cost a pack that does not use it exactly zero.
func TestNoLoopholeContributionIsSilent(t *testing.T) {
	root := t.TempDir()
	pj := `{"name":"plain","contributes":[{"kind":"env","vars":{"X":"1"}}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, "plain")
	if len(problems) > 0 {
		t.Fatalf("loading pack: %v", problems)
	}
	mods, refusals, warnings := p.LoopholeModules()
	if len(mods) != 0 || len(refusals) != 0 || len(warnings) != 0 {
		t.Errorf("mods=%v refusals=%v warnings=%v, want all empty", mods, refusals, warnings)
	}
	if claims := disclosedCrossings(p); len(claims) != 0 {
		t.Errorf("claims = %v, want none", claims)
	}
	if probs := p.LoopholeDeclProblems(); len(probs) != 0 {
		t.Errorf("problems = %v, want none", probs)
	}
}

// `pack lint`'s STRICT read reports a typo'd manifest key, which the gate's tolerant read
// deliberately does not. Both halves are the point: an author must hear that a key declares
// nothing, and a gate must not refuse a manifest over a key only a newer build knows.
func TestLintReadsLoopholeManifestStrictly(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"typo": `{
	  "name": "typo",
	  "transport": "none",
	  "host_deamon": {"cmd": ["/bin/true"]}
	}`})
	p := loadPack(t, root)
	probs := p.LoopholeDeclProblems()
	if len(probs) == 0 || !hasClaimContaining(probs, "host_deamon") {
		t.Fatalf("LoopholeDeclProblems = %v, want the misspelled key named — a typo is "+
			"otherwise a declaration that silently does nothing", probs)
	}
	// The GATE stays tolerant, so the typo does not turn into a permanent refusal.
	if _, refusals, warnings := p.LoopholeModules(); len(refusals) != 0 || len(warnings) != 0 {
		t.Errorf("the tolerant read refused a manifest over an unknown key "+
			"(refusals=%v warnings=%v) — that would make a newer-build key re-prompt for "+
			"approval, and promptYesNo fails closed on a non-TTY", refusals, warnings)
	}
}

// `pack lint` MUST APPLY THE PACK-SHIPPED SUBSET, or the authoring seam is kinder than every
// launch: the author sees a green tick and their loophole is refused on every machine that
// installs it — with the refusal arriving as a launch warning about a manifest they were told
// was fine. The subset's whole point is one set of rules with one answer.
//
// Measured before this: `yolo pack lint` on a pack declaring `jail_env` plus a `readonly:false`
// bind of ${XDG_RUNTIME_DIR} printed "pack ok — 2 file(s) stage".
func TestLintAppliesThePackShippedSubset(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"acme": `{
	  "name": "acme",
	  "transport": "none",
	  "jail_env": {"PULSE_SERVER": "unix:/run/pulse/native"},
	  "host_bind_mounts": [{"host": "/run/user/1000/pulse", "container": "/run/pulse",
	                        "readonly": false}],
	  "ca_cert": "/etc/ssl/certs/ca-certificates.crt"
	}`})
	probs := loadPack(t, root).LoopholeDeclProblems()
	for _, want := range []string{"jail_env", "absolute host path", "readonly = false", "ca_cert"} {
		if !hasClaimContaining(probs, want) {
			t.Errorf("`pack lint` does not report %q — the author lints clean and every launch "+
				"refuses, which is the report/gate disagreement the subset exists to prevent. "+
				"Got: %v", want, probs)
		}
	}
}

// And it stays TOLERANT of an unknown key in the same read: a pack crosses the version
// boundary by construction, so `pack lint` on a newer build's key must report the key as
// unknown (the strict decode's job) rather than have the subset check vanish.
func TestLintReportsTheSubsetAndTheTypoTogether(t *testing.T) {
	root := writeLoopholePack(t, map[string]string{"acme": `{
	  "name": "acme",
	  "transport": "none",
	  "host_deamon": {"cmd": ["/bin/true"]},
	  "jail_env": {"A": "1"}
	}`})
	probs := loadPack(t, root).LoopholeDeclProblems()
	if !hasClaimContaining(probs, "host_deamon") {
		t.Errorf("the misspelled key is not reported: %v", probs)
	}
	if !hasClaimContaining(probs, "jail_env") {
		t.Errorf("the subset violation is not reported alongside it — an author fixing two "+
			"things should not need two edit-check cycles: %v", probs)
	}
}
