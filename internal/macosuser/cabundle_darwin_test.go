//go:build darwin

package macosuser

import (
	"os"
	"strings"
	"testing"
	"time"
)

// cabundle_darwin_test.go RUNS THE REAL KEYCHAIN READ a launch makes (cabundle.go), on whatever
// Mac runs it (ci.yml's check-macos runs `go test -short ./...` on macos-latest): `security
// find-certificate -a -p` over the System keychain as this user, with no sudo, then the parse
// and filter the launch applies. It asserts only that the read works unprivileged and that
// every block it returns is a certificate; what a runner's keychain holds is not this test's to
// say, so the counts and the time it took are logged, the cost
// docs/design/provisioner-sets.md §16.6 item 8 asked a Mac to record.
func TestTheSystemKeychainIsReadableWithoutSudo(t *testing.T) {
	if _, err := os.Stat(systemKeychainPath); err != nil {
		t.Skipf("this Mac has no %s: %v", systemKeychainPath, err)
	}
	start := time.Now()
	out, err := readSystemKeychainReal()
	read := time.Since(start)
	if err != nil {
		t.Fatalf("reading the System keychain as this user failed: %v", err)
	}
	blocks := strings.Count(out, "-----BEGIN CERTIFICATE-----")
	candidates, left := keychainCACandidates(out, time.Now())
	if len(candidates)+left > blocks {
		t.Errorf("%d candidates and %d left out from %d certificate blocks", len(candidates), left, blocks)
	}
	start = time.Now()
	trusted := 0
	for _, c := range candidates {
		if verifyCAReal(c.PEM) {
			trusted++
		}
	}
	t.Logf("System keychain: %d certificate(s) exported in %s; %d TLS CA candidate(s), %d left "+
		"out by the filter; macOS trusts %d of the candidates for TLS (verify-cert took %s)",
		blocks, read.Round(time.Millisecond), len(candidates), left, trusted,
		time.Since(start).Round(time.Millisecond))
}
