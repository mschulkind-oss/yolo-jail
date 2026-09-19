package entrypoint

// portforwardskip_test.go pins the one branch of startContainerPortForwarding that
// used to decide silently: a forward the user configured, dropped because the jail's
// own 127.0.0.1:<port> was already taken.
//
// Both tests drive the REAL call site rather than the namer alone, because the namer
// alone would stay green with `if portInUse(localPort) { continue }` back in place —
// which is what it was. The bind probe is real (the test process holds the port), and
// only the HOLDER's identity comes from a fixture /proc, because that is the half a
// test cannot arrange on a live machine.

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// withFixtureProc points this package's procRoot at a hand-built /proc for one test.
func withFixtureProc(t *testing.T, root string) {
	t.Helper()
	old := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = old })
}

// writeHolderProc builds the minimum /proc that attributes one LISTEN socket on port
// to one process: the table row, the fd link pointing at its inode, and the two name
// files. hexPort is the kernel's spelling, which is why the callers pass hex.
func writeHolderProc(t *testing.T, root, hexPort string, pid int, comm, argv string) {
	t.Helper()
	const inode = "318411330"
	mk := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	for _, dir := range []string{mk("net"), mk(strconv.Itoa(pid), "fd")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	row := "   0: 0100007F:" + hexPort + " 00000000:0000 0A 00000000:00000000 " +
		"00:00000000 00000000     0        0 " + inode + " 2 000000006fad1d5e 100 0 0 10 0"
	body := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt" +
		"   uid  timeout inode\n" + row + "\n"
	if err := os.WriteFile(mk("net", "tcp"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// A real /proc/<pid>/fd/N is a symlink to "socket:[<inode>]", which is not a
	// path — DirSource.ReadLink returns the target text unresolved, so a dangling
	// symlink is exactly the right fixture.
	if err := os.Symlink("socket:["+inode+"]", mk(strconv.Itoa(pid), "fd", "3")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mk(strconv.Itoa(pid), "comm"), []byte(comm+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mk(strconv.Itoa(pid), "cmdline"),
		[]byte(strings.ReplaceAll(argv, " ", "\x00")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// holdPort binds a real port so the bind probe genuinely refuses, and returns it in
// both decimal and the kernel's hex spelling.
func holdPort(t *testing.T) (port int, hexPort string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	_, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err = strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return port, strings.ToUpper(strconv.FormatInt(int64(port), 16))
}

// forwardEnv is an Env that will reach the skip branch and nothing after it.
func forwardEnv(t *testing.T, buf *strings.Builder, local, host int) *Env {
	t.Helper()
	return &Env{
		Home:   t.TempDir(),
		Stderr: buf,
		Vars: map[string]string{
			"YOLO_FORWARD_HOST_PORTS": `["` + strconv.Itoa(local) + ":" + strconv.Itoa(host) + `"]`,
		},
	}
}

// THE DEFECT: a dropped forward with an unrelated holder. The user asked for a
// forward, the jail does not have one, and the symptom without this line is a
// connection refused hours later in an unrelated place.
func TestSkippedForwardNamesWhatTookThePort(t *testing.T) {
	port, hexPort := holdPort(t)
	root := t.TempDir()
	writeHolderProc(t, root, hexPort, 4242, "redis-server", "redis-server *:"+strconv.Itoa(port))
	withFixtureProc(t, root)

	var buf strings.Builder
	startContainerPortForwarding(forwardEnv(t, &buf, port, 5432))

	out := buf.String()
	for _, want := range []string{
		"Warning",
		"not forwarding local port " + strconv.Itoa(port) + " -> host 5432",
		"will not exist in this jail",
		"held by pid 4242",
		"redis-server",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("skipped-forward report %q missing %q", out, want)
		}
	}
}

// THE OTHER REGISTER. A re-entered container whose earlier socat is still up is
// working as designed, and a warning there is the cried-wolf line that teaches a
// reader to skip this text. The holder is matched on the LISTEN SPEC, not on the
// binary name, so the benign register cannot be claimed by a socat forwarding some
// other port.
func TestAForwardAlreadyEstablishedIsNotAWarning(t *testing.T) {
	port, hexPort := holdPort(t)
	root := t.TempDir()
	writeHolderProc(t, root, hexPort, 77, "socat",
		"socat TCP-LISTEN:"+strconv.Itoa(port)+",bind=127.0.0.1,fork,reuseaddr "+
			"UNIX-CONNECT:/tmp/yolo-fwd/port-"+strconv.Itoa(port)+".sock")
	withFixtureProc(t, root)

	var buf strings.Builder
	startContainerPortForwarding(forwardEnv(t, &buf, port, port))

	out := buf.String()
	if !strings.Contains(out, "already established") {
		t.Errorf("an existing forward was not reported as one: %q", out)
	}
	if strings.Contains(out, "Warning") {
		t.Errorf("an existing forward was reported as a fault: %q", out)
	}
	// And it is still said out loud. A benign register is not a silent one — this is
	// the line that tells a reader why their new forward config did not take effect
	// in a container they re-entered.
	if !strings.Contains(out, "held by pid 77") {
		t.Errorf("the established forward was not identified: %q", out)
	}
}

// THE TRI-STATE, at the one place it can mislead here: an unreadable /proc must not
// be reported as "nothing holds it". The bind probe already proved something does,
// so a sentence claiming otherwise would contradict the refusal it is explaining.
func TestSkippedForwardSaysWhenItCouldNotLook(t *testing.T) {
	port, _ := holdPort(t)
	withFixtureProc(t, filepath.Join(t.TempDir(), "absent"))

	var buf strings.Builder
	startContainerPortForwarding(forwardEnv(t, &buf, port, port))

	out := buf.String()
	if !strings.Contains(out, "could not be determined") {
		t.Errorf("an unreadable /proc = %q, want the could-not-ask answer", out)
	}
	if !strings.Contains(out, "net/tcp") {
		t.Errorf("the could-not-ask answer does not name the read that failed: %q", out)
	}
	if strings.Contains(out, "no LISTEN socket") {
		t.Errorf("an unreadable table was reported as an absent listener: %q", out)
	}
	// Still a warning: the forward is gone whether or not the holder is nameable.
	if !strings.Contains(out, "Warning") {
		t.Errorf("a dropped forward was not reported as a fault: %q", out)
	}
}

// The budget property. A boot that skips nothing must not read /proc at all — the
// attribution walk is the expensive half, and putting it on the healthy path would
// pay for a diagnostic nobody needs on every launch.
func TestAHealthyForwardTakesNoProcSnapshot(t *testing.T) {
	// A port nothing holds: the bind probe succeeds, so the namer is never called.
	// procRoot points at a path that does not exist, and a snapshot taken from it
	// would leave its gaps in the output.
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portText, _ := net.SplitHostPort(free.Addr().String())
	port, _ := strconv.Atoi(portText)
	if err := free.Close(); err != nil {
		t.Fatal(err)
	}
	withFixtureProc(t, filepath.Join(t.TempDir(), "absent"))

	var buf strings.Builder
	startContainerPortForwarding(forwardEnv(t, &buf, port, port))

	out := buf.String()
	if strings.Contains(out, "could not be determined") || strings.Contains(out, "net/tcp") {
		t.Errorf("a free port cost a /proc snapshot: %q", out)
	}
}
