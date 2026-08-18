package svcendpoint

// readguard_test.go covers the ONE failure mode in this package that does not end
// in an error: Read opening something that is not a file it can read to the end.
//
// Every other malformed shape (endpointfile_test.go's table) ends in a value a
// caller can act on. A writer-less fifo ends in nothing at all — the goroutine sits
// in open(2) and no deadline anywhere in this package or its callers can reach it,
// because none of them has been entered yet. That is why these assertions are
// TIME-BOUNDED rather than plain equality checks: a regression here HANGS, and a
// hung test surfaces minutes later as a package-wide timeout with no name attached
// to it, so the deadline has to be the failure.

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// readWithin runs Read on its own goroutine and fails the test if it has not
// returned by the deadline.
//
// The goroutine is deliberately not cleaned up on the failure path: if Read is
// wedged in open(2) there is nothing that can un-wedge it, and a leaked goroutine in
// a test binary that is already failing costs nothing. What matters is that the
// FAILURE IS NAMED — "Read never returned for a named pipe" is a diagnosis, and
// "panic: test timed out after 10m0s" is not.
func readWithin(t *testing.T, path string, budget time.Duration) (Endpoint, error) {
	t.Helper()
	type result struct {
		ep  Endpoint
		err error
	}
	done := make(chan result, 1)
	go func() {
		ep, err := Read(path)
		done <- result{ep, err}
	}()
	select {
	case r := <-done:
		return r.ep, r.err
	case <-time.After(budget):
		t.Fatalf("Read(%s) did not return within %s — it opened the path instead of "+
			"stat'ing it first. This is not a slow read: opening a writer-less fifo "+
			"blocks in open(2) forever, and every caller of this function (the boot "+
			"probe, the in-jail OAuth terminator, `yolo check`, the readiness polls) "+
			"is then wedged with nothing printed.", path, budget)
		panic("unreachable")
	}
}

// TestReadRefusesNonRegularFiles is the hazard itself, one shape per subtest.
//
// MEASURED 2026-08-18 on this host: os.ReadFile of a writer-less fifo does not
// return in 3s or ever, while os.Stat of the same path answers immediately with
// p---------. The fifo case is therefore the only one of the four that would
// actually hang today; the other three (directory, unix socket, device node) fail
// with an errno instead. They are here anyway because the fix is one gate and the
// ATTRIBUTION is shared: EISDIR and ENXIO are errors this package does not claim,
// so before the gate they reached callers untyped — and internal/entrypoint's
// classifyReachability sends everything it cannot attribute into faultUnreachable,
// the one class OQ-R2's fatal refuses a launch over. A directory at the endpoint
// path has nothing to do with the network.
func TestReadRefusesNonRegularFiles(t *testing.T) {
	cases := []struct {
		name string
		make func(t *testing.T, dir string) string
		// want is a word the message must carry: "not a regular file" would leave
		// the reader to go and stat it themselves, and the specific shape is
		// usually the whole diagnosis — "named pipe" says somebody ran mkfifo.
		want string
	}{
		{
			name: "a fifo nobody is writing to",
			make: func(t *testing.T, dir string) string {
				p := filepath.Join(dir, "fifo.endpoint")
				if err := syscall.Mkfifo(p, 0o600); err != nil {
					t.Skipf("mkfifo unavailable here: %v", err)
				}
				return p
			},
			want: "named pipe",
		},
		{
			name: "a directory",
			make: func(t *testing.T, dir string) string {
				p := filepath.Join(dir, "dir.endpoint")
				if err := os.Mkdir(p, 0o700); err != nil {
					t.Fatal(err)
				}
				return p
			},
			want: "directory",
		},
		{
			name: "a unix socket",
			make: func(t *testing.T, _ string) string {
				// Its own /tmp-rooted directory: a t.TempDir()-rooted socket path
				// can overrun sun_path, and the failure then reads as a bare
				// "bind: invalid argument" naming neither the limit nor the fix.
				p := filepath.Join(privateSocketDir(t), "sock.endpoint")
				assertSockPathFits(t, p)
				ln, err := net.Listen("unix", p)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = ln.Close() })
				return p
			},
			want: "unix socket",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.make(t, privateDir(t))

			_, err := readWithin(t, path, 10*time.Second)
			if err == nil {
				t.Fatalf("Read(%s) succeeded on a %s", path, tc.want)
			}
			// ErrEndpointMalformed, and the choice is load-bearing rather than
			// cosmetic: internal/entrypoint's classifyReachability maps exactly
			// ErrEndpointMissing and ErrEndpointMalformed onto faultUnpublished and
			// EVERYTHING ELSE onto faultUnreachable. Since OQ-R4 both classes refuse
			// the launch, so what an untyped error here costs is no longer the
			// severity but the DIAGNOSIS: "a fifo sits where an endpoint belongs"
			// would be reported as a transport failure and printed under the
			// paragraph about pasta's forwarding, sending the reader after a network
			// stack for a local file.
			if !errors.Is(err, ErrEndpointMalformed) {
				t.Errorf("Read(%s) error = %v, want ErrEndpointMalformed", tc.want, err)
			}
			// And it must NOT be the missing case. ErrEndpointMissing carries a
			// second promise — errors.Is(err, syscall.ENOENT) — that
			// internal/oauthterminator's frozen two-layer attribution gates on, and
			// a fifo is not an absent file.
			if errors.Is(err, ErrEndpointMissing) {
				t.Errorf("a %s that EXISTS was reported as a missing endpoint: %v", tc.want, err)
			}
			if errors.Is(err, syscall.ENOENT) {
				t.Errorf("a %s must not read as ENOENT — internal/oauthterminator "+
					"attributes ENOENT to the relay layer: %v", tc.want, err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not name the offending file", err)
			}
			// The assertion is on the PHRASE, not on the bare word, and that is not
			// fussiness: t.TempDir names its directory after the test, so "directory"
			// is already a substring of the path this error quotes. Checked against
			// the word alone, the directory case passed with fileKindName replaced by
			// a constant "not a regular file" — dead coverage that looked green,
			// measured while mutation-testing this.
			if !strings.Contains(err.Error(), "is a "+tc.want) {
				t.Errorf("error %q does not name what is actually at the path (%q)", err, tc.want)
			}

			// Dial and Probe both go through Read, which is the whole point of
			// putting the gate there: every caller inherits it, including the ones
			// that never touch internal/entrypoint.
			dialed := make(chan error, 1)
			go func() {
				conn, derr := Dial(path, time.Second)
				if conn != nil {
					_ = conn.Close()
				}
				dialed <- derr
			}()
			select {
			case derr := <-dialed:
				if !errors.Is(derr, ErrEndpointMalformed) {
					t.Errorf("Dial on a %s = %v, want ErrEndpointMalformed", tc.want, derr)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("Dial(%s) never returned — the gate is not on the path Dial takes", path)
			}

			probed := make(chan bool, 1)
			go func() { probed <- Probe(path) }()
			select {
			case ok := <-probed:
				if ok {
					t.Errorf("Probe reported a %s as a healthy endpoint", tc.want)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("Probe(%s) never returned", path)
			}
		})
	}
}

// TestReadRefusesAnOversizedFile is the same argument one step along. os.ReadFile
// has no ceiling, so a file something in the read-write host-services directory grew
// without bound is an OOM in whatever read it — PID 1 during boot, or the agent's
// OAuth terminator mid-session — rather than an error anybody can act on.
//
// The file is SPARSE: what the gate reads is the declared size, and materialising a
// megabyte to prove it would only slow the suite down.
func TestReadRefusesAnOversizedFile(t *testing.T) {
	path := filepath.Join(privateDir(t), "huge.endpoint")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxEndpointFileSize + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = readWithin(t, path, 10*time.Second)
	if !errors.Is(err, ErrEndpointMalformed) {
		t.Errorf("Read(oversized) error = %v, want ErrEndpointMalformed", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the offending file", err)
	}
	// The SIZE has to appear, and that is not pedantry about wording — it is the
	// only thing separating "refused to read a megabyte" from "read the megabyte
	// and then failed to parse it". Both produce ErrEndpointMalformed (a megabyte
	// of NUL bytes is one whitespace-free field, so Parse rejects it too), and only
	// one of them is the ceiling doing its job. No other error in this package
	// mentions this number.
	if !strings.Contains(err.Error(), strconv.Itoa(MaxEndpointFileSize+1)) {
		t.Errorf("error %q does not name the file's size — the megabyte was read "+
			"and then rejected by Parse, which is the OOM this cap exists to stop", err)
	}
	if Probe(path) {
		t.Error("Probe accepted an oversized endpoint file")
	}
}

// TestReadAcceptsAFileAtTheCeiling pins which side of MaxEndpointFileSize is
// refused. A ceiling nobody tests the boundary of is a ceiling that quietly becomes
// off-by-one, and the failure would be silent in the direction that matters: a
// legitimate endpoint refused reads to every caller as an unpublished service.
func TestReadAcceptsAFileAtTheCeiling(t *testing.T) {
	ep := sampleEndpoint(t)
	line := ep.Format()
	// Pad with trailing whitespace to exactly the ceiling. strings.Fields ignores
	// it, so this is still a well-formed publication — just an absurdly large one.
	padded := line + strings.Repeat(" ", MaxEndpointFileSize-len(line))
	if len(padded) != MaxEndpointFileSize {
		t.Fatalf("padding arithmetic is wrong: %d bytes, want %d", len(padded), MaxEndpointFileSize)
	}
	path := filepath.Join(privateDir(t), "atceiling.endpoint")
	if err := os.WriteFile(path, []byte(padded), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readWithin(t, path, 10*time.Second)
	if err != nil {
		t.Fatalf("Read refused a file OF EXACTLY MaxEndpointFileSize bytes: %v — the "+
			"cap is `>`, not `>=`", err)
	}
	if got.Token != ep.Token || got.HostPort != ep.HostPort {
		t.Errorf("Read returned the wrong endpoint: %+v", got.HostPort)
	}
}

// TestReadStillReadsAnOrdinaryEndpoint is the guard's other half: a gate that
// refuses the healthy case is a worse outage than the one it closes, and every
// failure it would cause looks like "the daemon never published".
func TestReadStillReadsAnOrdinaryEndpoint(t *testing.T) {
	ep := sampleEndpoint(t)
	path := filepath.Join(privateDir(t), "good.endpoint")
	if err := Publish(path, ep); err != nil {
		t.Fatal(err)
	}

	got, err := readWithin(t, path, 10*time.Second)
	if err != nil {
		t.Fatalf("Read on a freshly published endpoint: %v", err)
	}
	if got.HostPort != ep.HostPort || got.Token != ep.Token {
		t.Errorf("Read round trip changed the endpoint: %q vs %q", got.HostPort, ep.HostPort)
	}
	if !Probe(path) {
		t.Error("Probe rejected a complete, published endpoint")
	}

	// A SYMLINK to a regular file is still a regular file, and must stay readable.
	// The gate stats through the link (os.Stat, not os.Lstat) on purpose: os.ReadFile
	// followed links before it existed, so refusing them here would be a silent
	// behaviour change dressed up as a safety fix.
	link := filepath.Join(privateDir(t), "link.endpoint")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readWithin(t, link, 10*time.Second); err != nil {
		t.Errorf("Read through a symlink to a regular endpoint file: %v", err)
	}

	// A DANGLING symlink is the missing case, not the malformed one — os.Stat
	// reports ENOENT for it exactly as os.ReadFile did, so the ENOENT attribution
	// internal/oauthterminator gates on survives the gate.
	dangling := filepath.Join(privateDir(t), "dangling.endpoint")
	if err := os.Symlink(filepath.Join(privateDir(t), "nothing-here"), dangling); err != nil {
		t.Fatal(err)
	}
	_, err = readWithin(t, dangling, 10*time.Second)
	if !errors.Is(err, ErrEndpointMissing) || !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Read(dangling symlink) = %v, want both ErrEndpointMissing and ENOENT", err)
	}
}
