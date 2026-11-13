package integration

// THE ORBSTACK NATIVE PROOF — component D of docs/research/orbstack-as-a-podman-host-plan.md.
//
// yolo reaches OrbStack through its existing Podman path: a rootless Podman inside an OrbStack
// Linux machine, selected by a named Podman connection (CONTAINER_CONNECTION). Three things only
// that endpoint can answer, and no Linux run or nested jail stands in for any of them:
//
//   - OrbStack's SSH proxy (port 32222) ending a forwarded channel when the client half-closes
//     stdin, which hung the launch before the jail-main client held its input open (B0,
//     d0feeb3b8). TestOrbStackALaunchOutputsAndItsSessionsSeeEOF asserts the launch prints and
//     that the first session and an attach still see EOF from a closed stdin.
//   - The Mac workspace reaching the jail through OrbStack's shared folders, and a write from the
//     jail landing on the Mac as the Mac user (TestOrbStackTheWorkspaceIsSharedBothWays).
//   - The Mac's loopback reaching the jail as host.orb.internal (component C,
//     internal/cli/run/orbstackroute.go): TestOrbStackHostServicesAreReachable dials every
//     enabled loopback-TLS endpoint from inside the jail and reads the boot witness.
//
// THE NAMING IS PART OF THE GATE, as for TestAppleContainer…: .github/workflows/orbstack.yml
// selects `-run '^TestOrbStack'` from `go test -list` and refuses an empty selection, and
// requireOrbStack enforces the prefix. Undeclared, every test here skips; declared
// (YOLO_TEST_ORBSTACK), a missing or wrong endpoint fails, since a skip would read as a pass.
//
// Nothing here creates, changes or removes an OrbStack machine or Podman connection: that is
// OQ-ORB1's (docs/research/orbstack-as-a-podman-host.md#OQ-ORB1). Nothing prints a connection's
// identity file or an endpoint's token.

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"syscall"
	"testing"
)

// orbStackDeclareEnv is how the workflow says "this run was scheduled to exercise OrbStack".
const orbStackDeclareEnv = "YOLO_TEST_ORBSTACK"

// orbStackTestPrefix is the required name prefix (see the header).
const orbStackTestPrefix = "TestOrbStack"

// orbStackSSHPort is OrbStack's own SSH endpoint. The research's workaround tunneled to the
// machine's sshd on 2222 instead; a proof through that tunnel proves the workaround, not the
// supported path.
const orbStackSSHPort = "32222"

// orbStackKernelMarker is what OrbStack puts in its VM's kernel release (the same marker
// internal/cli/run/orbstackroute.go identifies the endpoint by).
const orbStackKernelMarker = "-orbstack"

// requireOrbStack gates a test on the selected Podman endpoint being a rootless OrbStack
// machine, reached through OrbStack's SSH proxy, and returns the connection name.
func requireOrbStack(t *testing.T) string {
	t.Helper()
	if !strings.HasPrefix(t.Name(), orbStackTestPrefix) {
		t.Fatalf("%s is behind requireOrbStack and is not named %s…; the workflow selects "+
			"`-run '^%s'`, so it would never run. Rename it.", t.Name(), orbStackTestPrefix, orbStackTestPrefix)
	}
	if testing.Short() {
		t.Skip("skipping OrbStack native test (-short)")
	}
	declared := os.Getenv(orbStackDeclareEnv) != ""
	conn := os.Getenv("CONTAINER_CONNECTION")
	why := orbStackUnavailable(conn)
	if why == "" {
		return conn
	}
	if declared {
		t.Fatalf("%s is set, so this run was scheduled to exercise OrbStack, and it cannot: %s.",
			orbStackDeclareEnv, why)
	}
	t.Skipf("skipping OrbStack native test: %s (set %s in a job that must run them)", why, orbStackDeclareEnv)
	return ""
}

// orbStackUnavailable is "" when conn names a rootless OrbStack endpoint behind OrbStack's SSH
// proxy, and otherwise why not.
func orbStackUnavailable(conn string) string {
	if goruntime.GOOS != "darwin" {
		return "OrbStack is a macOS host and this is " + goruntime.GOOS
	}
	if conn == "" {
		return "CONTAINER_CONNECTION names no Podman connection; the proof must name one, never take the default"
	}
	if _, err := exec.LookPath("podman"); err != nil {
		return "no `podman` on PATH"
	}
	out, err := exec.Command("podman", "system", "connection", "list", "--format", "json").Output()
	if err != nil {
		return "`podman system connection list` failed: " + err.Error()
	}
	var conns []struct {
		Name string `json:"Name"`
		URI  string `json:"URI"`
	}
	if err := json.Unmarshal(out, &conns); err != nil {
		return "`podman system connection list --format json` did not parse: " + err.Error()
	}
	uri := ""
	for _, c := range conns {
		if c.Name == conn {
			uri = c.URI
		}
	}
	if uri == "" {
		return "no Podman connection is named " + conn
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "ssh" {
		return "connection " + conn + "'s URI is not an ssh:// URI"
	}
	if u.Port() != orbStackSSHPort {
		return "connection " + conn + " goes through port " + u.Port() + ", not OrbStack's SSH proxy on " +
			orbStackSSHPort + " (a tunnel to the machine's own sshd proves the workaround, not the supported path)"
	}
	cmd := exec.Command("podman", "info", "--format", "{{.Host.Security.Rootless}}|{{.Host.Kernel}}")
	cmd.Env = append(os.Environ(), "CONTAINER_CONNECTION="+conn)
	facts, err := cmd.Output()
	if err != nil {
		return "`podman info` through " + conn + " failed: " + err.Error()
	}
	rootless, kernel, _ := strings.Cut(strings.TrimSpace(string(facts)), "|")
	if rootless != "true" {
		return "the endpoint behind " + conn + " is not rootless"
	}
	if !strings.Contains(kernel, orbStackKernelMarker) {
		return "the endpoint behind " + conn + " runs kernel " + kernel + ", not OrbStack's"
	}
	return ""
}

// orbStackEnv is every launch's runtime selection: the podman-self setup, on the named
// connection, so the launch cannot fall through to a default Podman Machine or Apple Container.
func orbStackEnv(conn string) runOption {
	return withEnv("YOLO_SETUP=podman-self", "CONTAINER_CONNECTION="+conn)
}

// TestOrbStackALaunchOutputsAndItsSessionsSeeEOF is the SSH-proxy proof. The harness's stdin is
// closed (/dev/null), so `cat` returning is the session seeing EOF; the marker after it reaching
// this process is the launch's output surviving OrbStack's half-close. Then a second launch
// attaches to the held jail and must see EOF the same way.
func TestOrbStackALaunchOutputsAndItsSessionsSeeEOF(t *testing.T) {
	conn := requireOrbStack(t)
	requireJail(t)
	dir := writeProject(t, `{}`)

	first := runYolo(t, dir, `cat; echo FIRST-AFTER-EOF-$((40+2))`, orbStackEnv(conn))
	if first.rc != 0 || !strings.Contains(first.stdout, "FIRST-AFTER-EOF-42") {
		t.Fatalf("a fresh launch through %s did not print after its session's EOF (rc %d):\n%s",
			conn, first.rc, lastLines(first.combined(), 60))
	}

	holder := startYoloBackground(t, "HOLD", dir, `echo HOLD-IN-$((40+2)); `+
		`for _ in $(seq 1 1500); do [ -f /workspace/.orb-release ] && break; sleep 0.2; done; echo HOLD-OUT-$((40+2))`,
		"YOLO_SETUP=podman-self", "CONTAINER_CONNECTION="+conn)
	t.Cleanup(func() { writeRelease(t, dir, ".orb-release") })
	awaitOutput(t, holder, regexp.MustCompile(`HOLD-IN-42`))

	attach := runYolo(t, dir, `cat; echo ATTACH-AFTER-EOF-$((40+2))`, orbStackEnv(conn))
	if !strings.Contains(attach.combined(), "Attaching to existing jail") {
		t.Fatalf("the second launch did not attach to the held jail, so attach EOF was not measured:\n%s",
			lastLines(attach.combined(), 60))
	}
	if attach.rc != 0 || !strings.Contains(attach.stdout, "ATTACH-AFTER-EOF-42") {
		t.Errorf("an attach through %s did not see EOF from its closed stdin (rc %d):\n%s",
			conn, attach.rc, lastLines(attach.combined(), 60))
	}
	writeRelease(t, dir, ".orb-release")
	awaitOutput(t, holder, regexp.MustCompile(`HOLD-OUT-42`))
	if rc := holder.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("the held first session ended %d:\n%s", rc, lastLines(holder.combined(), 60))
	}
}

// TestOrbStackTheWorkspaceIsSharedBothWays: a Mac file is readable in the jail, and a file the
// jail writes appears on the Mac owned by the Mac user. ReadMachineShares returns unknown for a
// named connection, so only a real bind and write is evidence.
func TestOrbStackTheWorkspaceIsSharedBothWays(t *testing.T) {
	conn := requireOrbStack(t)
	requireJail(t)
	dir := writeProject(t, `{}`)
	if err := os.WriteFile(filepath.Join(dir, "from-mac.txt"), []byte("MAC-SIDE-42\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := runYolo(t, dir, `cat /workspace/from-mac.txt; echo JAIL-SIDE-42 > /workspace/from-jail.txt`, orbStackEnv(conn))
	if r.rc != 0 || !strings.Contains(r.stdout, "MAC-SIDE-42") {
		t.Fatalf("the jail did not read the Mac workspace (rc %d):\n%s", r.rc, lastLines(r.combined(), 60))
	}
	path := filepath.Join(dir, "from-jail.txt")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "JAIL-SIDE-42") {
		t.Fatalf("the jail's write did not reach the Mac at %s: %v %q", path, err, data)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		t.Errorf("the jail's file is owned by uid %d on the Mac, want the Mac user's %d", st.Uid, os.Getuid())
	}
}

// orbStackEndpointProbe prints, for every enabled loopback-TLS endpoint the jail was handed,
// its advertised host and whether a TCP connect to it succeeded. Only the address field of an
// endpoint file is read; its token never leaves the jail.
const orbStackEndpointProbe = `echo "DISPOSITION=$YOLO_HOST_LOOPBACK"
n=0
for v in $(env | sed -n 's/^\(YOLO_SERVICE_[A-Z0-9_]*_ENDPOINT\)=.*/\1/p'); do
  f=$(printenv "$v"); [ -f "$f" ] || continue
  addr=$(cut -d' ' -f1 < "$f"); host=${addr%:*}; port=${addr##*:}
  if (exec 3<>"/dev/tcp/$host/$port") 2>/dev/null; then r=ok; else r=refused; fi
  echo "ENDPOINT $v host=$host dial=$r"; n=$((n+1))
done
echo "ENDPOINTS=$n"`

// TestOrbStackHostServicesAreReachable is component C's native proof: the jail is told
// `requested`, every enabled endpoint names host.orb.internal and dials, and the boot witness
// agrees.
func TestOrbStackHostServicesAreReachable(t *testing.T) {
	conn := requireOrbStack(t)
	requireJail(t)
	dir := writeProjectWithPacks(t, `{}`, "claude")

	r := runYolo(t, dir, orbStackEndpointProbe, orbStackEnv(conn))
	if r.rc != 0 {
		t.Fatalf("the probe launch failed (rc %d):\n%s", r.rc, lastLines(r.combined(), 60))
	}
	if !strings.Contains(r.stdout, "DISPOSITION=requested\n") {
		t.Errorf("the jail was not told `requested` on an OrbStack endpoint:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "ENDPOINTS=0\n") {
		t.Fatalf("the jail was handed no loopback-TLS endpoint, so reachability was not measured:\n%s",
			lastLines(r.combined(), 60))
	}
	for _, line := range strings.Split(r.stdout, "\n") {
		if !strings.HasPrefix(line, "ENDPOINT ") {
			continue
		}
		if !strings.Contains(line, "host=host.orb.internal ") || !strings.HasSuffix(line, "dial=ok") {
			t.Errorf("an endpoint is not reachable through host.orb.internal: %s", line)
		}
	}
	witness := ""
	for _, name := range []string{"boot.log", "boot.session.log"} {
		data, _ := os.ReadFile(filepath.Join(dir, ".yolo", "logs", name))
		if m := regexp.MustCompile(`reachability: (\d+)/(\d+) enabled service\(s\) reachable, disposition=\S+`).
			FindStringSubmatch(string(data)); m != nil {
			witness = m[0]
			if m[1] != m[2] {
				t.Errorf("the boot witness disagrees with the dials: %s", m[0])
			}
		}
	}
	if witness == "" {
		t.Errorf("no boot log under %s recorded the reachability witness", filepath.Join(dir, ".yolo", "logs"))
	} else {
		t.Logf("witness: %s", witness)
	}
}
