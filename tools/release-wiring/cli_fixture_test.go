package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// This entry point exists only in the test binary, never the shipped CLI. Each
// invocation gets a separate process and explicit fixture roots; production
// client construction, verification and CLI dispatch are exercised unchanged.
func TestReleaseWiringCLIProcess(t *testing.T) {
	if os.Getenv("YOLO_TEST_CLI_PROCESS") != "1" {
		return
	}
	index := -1
	for i, arg := range os.Args {
		if arg == "--" {
			index = i
			break
		}
	}
	if index < 0 {
		fmt.Fprintln(os.Stderr, "fixture CLI has no argument boundary")
		os.Exit(2)
	}
	if path := os.Getenv("YOLO_TEST_FIXTURE_CA"); path != "" {
		body, err := os.ReadFile(path)
		roots := x509.NewCertPool()
		if err != nil || !roots.AppendCertsFromPEM(body) {
			fmt.Fprintln(os.Stderr, "fixture CLI certificate roots are unreadable or malformed")
			os.Exit(2)
		}
		if err := os.Setenv("GODEBUG", os.Getenv("GODEBUG")+",x509usefallbackroots=1"); err != nil {
			fmt.Fprintln(os.Stderr, "fixture CLI could not select explicit certificate roots")
			os.Exit(2)
		}
		x509.SetFallbackRoots(roots)
	}
	os.Args = append([]string{"release-wiring"}, os.Args[index+1:]...)
	main()
}

func fixtureCLIExecutable(t *testing.T) string {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "release-wiring")
	script := "#!/bin/sh\nYOLO_TEST_CLI_PROCESS=1 exec " + shquote.Join([]string{binary, "-test.run=^TestReleaseWiringCLIProcess$", "--"}) + " \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// Fixture controls are deliberately absent from the standalone production CLI.
// Even its valid fixture certificate and process marker must not add trust.
func TestStandaloneCLIHasNoFixtureTrustHook(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeJSON(w, []publishedRelease{{TagName: "v1.0.0"}})
	}))
	defer server.Close()
	cert := filepath.Join(t.TempDir(), "fixture.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, buildProductionCLI(t), "check-version-order")
	cmd.Env = append(os.Environ(), "GITHUB_REPOSITORY=owner/repo", "RELEASE_VERSION=1.0.1", "GH_TOKEN=offline-test-token", "GITHUB_API_URL="+server.URL, "YOLO_TEST_FIXTURE_CA="+cert, "YOLO_TEST_CLI_PROCESS=1", "SSL_CERT_FILE=", "SSL_CERT_DIR=")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "failed to verify certificate") || requests.Load() != 0 {
		t.Fatalf("standalone CLI accepted test-only trust or did not exercise TLS: err=%v requests=%d output=%s", err, requests.Load(), out)
	}
}

// The caller fixture must use only its explicit, process-local CA on every OS,
// without relying on Linux's SSL_CERT_FILE behavior or modifying a keychain.
func TestTrustedHelperCLIUsesOnlyExplicitFixtureRoots(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeJSON(w, []publishedRelease{{TagName: "v1.0.0"}})
	}))
	defer server.Close()
	root := t.TempDir()
	cert := filepath.Join(root, "fixture.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(42), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	wrong := filepath.Join(root, "unrelated.pem")
	if err := os.WriteFile(wrong, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, "malformed.pem")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	helper := buildTrustedHelper(t)
	for _, tc := range []struct {
		name, ca string
		wantOK   bool
	}{
		{"matching root", cert, true},
		{"unrelated root", wrong, false},
		{"malformed root", bad, false},
		{"missing root file", filepath.Join(root, "absent.pem"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := requests.Load()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, helper, "check-version-order")
			cmd.Env = append(os.Environ(), "GITHUB_REPOSITORY=owner/repo", "RELEASE_VERSION=1.0.1", "GH_TOKEN=offline-test-token", "GITHUB_API_URL="+server.URL, "YOLO_TEST_FIXTURE_CA="+tc.ca, "SSL_CERT_FILE=", "SSL_CERT_DIR=")
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.wantOK {
				t.Fatalf("explicit fixture trust: %v, output=%s", err, out)
			}
			if tc.wantOK {
				if requests.Load() != before+1 || !strings.Contains(string(out), "Version v1.0.1") {
					t.Fatalf("matching root did not reach the actual CLI/API: requests=%d output=%s", requests.Load()-before, out)
				}
			} else if requests.Load() != before {
				t.Fatal("untrusted fixture root reached the API")
			}
		})
	}
}
