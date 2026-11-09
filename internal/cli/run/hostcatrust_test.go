package run

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/catrust"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// hostCATestCA mints one self-signed CA with the common name cn and returns it as the host read
// would, labelled with source.
func hostCATestCA(t *testing.T, cn, source string) catrust.CA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return catrust.CA{Name: cn, Source: source, PEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

// stubHostTrust replaces the host read for one test.
func stubHostTrust(t *testing.T, ht hostTrust) {
	t.Helper()
	prev := readHostTrust
	readHostTrust = func() hostTrust { return ht }
	t.Cleanup(func() { readHostTrust = prev })
}

func testPrinter(buf *bytes.Buffer) printer {
	return (&Options{}).pr(buf)
}

// A fresh podman launch hands its jail the host's CAs as ONE read-only file and the variable
// naming it: the whole launch runs, so deleting either call site (run.go's prepareHostCATrust,
// assemble.go's hostCAArgs) fails this.
func TestALaunchHandsTheJailTheHostsCAsAsOneFile(t *testing.T) {
	assertHostCAsHanded(t, false)
}

// A SEALED build carries the host's CAs too (seal.go's crossing table, PS-D16): a CA certificate is
// public, the maintainer ruled "every jail", and the macos-user sealed capture composes the same
// trust. The seal's allowlist test (TestASealedLaunchHandsTheJailNoCrossing) names the variable.
func TestASealedLaunchCarriesTheHostsCAs(t *testing.T) {
	assertHostCAsHanded(t, true)
}

func assertHostCAsHanded(t *testing.T, sealed bool) {
	t.Helper()
	ca := hostCATestCA(t, "Example Corp Inspection CA", catrust.HostBundleSource("/etc/ssl/certs/ca-certificates.crt"))
	stubHostTrust(t, hostTrust{cas: []catrust.CA{ca}})
	argv, ws, _, _ := sealedLaunch(t, sealed)

	if !slices.Contains(argvValues(argv, "-e"), entrypoint.HostCAFileEnv+"="+entrypoint.HostCAFileContainerPath) {
		t.Errorf("the launch does not name the host's CA file to the jail; -e values: %v", argvValues(argv, "-e"))
	}
	src, ok := sealedBindSources(argv)[entrypoint.HostCAFileContainerPath]
	if !ok {
		t.Fatalf("the launch binds nothing at %s; -v values: %v", entrypoint.HostCAFileContainerPath, argvValues(argv, "-v"))
	}
	if !strings.HasPrefix(src, ws+string(filepath.Separator)) || filepath.Base(src) != hostCAFileName {
		t.Errorf("the host's CA file is bound from %s, want the launch's own %s under %s", src, hostCAFileName, ws)
	}
	for _, v := range argvValues(argv, "-v") {
		if strings.HasSuffix(v, ":"+entrypoint.HostCAFileContainerPath+":ro") {
			goto bound
		}
	}
	t.Errorf("the host's CA file is not bound read-only: %v", argvValues(argv, "-v"))
bound:
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	got := catrust.ParseLabelled(string(body))
	if len(got) != 1 || got[0].Name != ca.Name || got[0].Source != ca.Source || got[0].PEM != ca.PEM {
		t.Errorf("the composed file holds %+v, want the one host CA with its source", got)
	}
}

// THE HOST-TRUST NOTES GO TO STDERR, never to stdout: `yolo run -- <cmd>`'s stdout IS the command's
// stdout, so a note there is read as the command's output. On a Mac with no readable login keychain
// (every CI runner's temp HOME) the notes printed ahead of `rg -c`'s count and failed
// integration's TestHostComposedBriefingIsNotDeliveredTwice with a correct count of 1, darwin only,
// because a Linux host's bundle usually yields no note at all. The whole launch runs, so this
// fails if the call site in runContainer hands prepareHostCATrust a stdout printer again.
func TestHostTrustNotesNeverReachTheCommandsStdout(t *testing.T) {
	const note = "Note: could not read your login keychain (no login keychain)"
	ca := hostCATestCA(t, "Example Corp Inspection CA", catrust.SourceSystemKeychain)
	stubHostTrust(t, hostTrust{cas: []catrust.CA{ca}, notes: []string{note, "3 certificate(s) were left out"}})
	_, _, _, stdout, stderr := sealCrossingsFixture.launchStreams(t, false)
	if strings.Contains(stdout, "login keychain") || strings.Contains(stdout, "left out") {
		t.Errorf("a host-trust note reached the launcher's stdout, which a jail command's output "+
			"shares:\nstdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, note) {
		t.Errorf("the host-trust note was not printed on stderr:\nstderr:\n%s", stderr)
	}
}

// AN UNREADABLE LINUX HOST BUNDLE NAMES THE VERB THAT NAMES ANOTHER, not a JSON fragment for
// env_sources that a TOML config cannot take.
func TestAnUnreadableLinuxHostBundleNamesTheVerb(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("the Linux host read; a Mac reads its keychains")
	}
	t.Setenv("SSL_CERT_FILE", filepath.Join(t.TempDir(), "absent.pem"))
	ht := readHostTrustReal()
	if len(ht.notes) != 1 || !strings.Contains(ht.notes[0], "`yolo config set env.set.SSL_CERT_FILE <that file>`") ||
		strings.Contains(ht.notes[0], "env_sources") || strings.Contains(ht.notes[0], "{\"") {
		t.Errorf("the unreadable bundle's note does not name the verb: %q", ht.notes)
	}
}

// A LINUX HOST'S BUNDLE says how many certificates it left out, never silently (PS-D16): the
// host's own read, through the host's SSL_CERT_FILE, which a Linux host's OpenSSL honors too.
func TestALinuxHostSaysWhatItsBundleLeftOut(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("the Linux host read; a Mac reads its keychains")
	}
	ca := hostCATestCA(t, "Example Corp Inspection CA", "x")
	bundle := filepath.Join(t.TempDir(), "bundle.pem")
	body := ca.PEM + "-----BEGIN CERTIFICATE-----\nbm90IGEgY2VydA==\n-----END CERTIFICATE-----\n"
	if err := os.WriteFile(bundle, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", bundle)
	ht := readHostTrustReal()
	if len(ht.cas) != 1 || ht.cas[0].Source != catrust.HostBundleSource(bundle) {
		t.Fatalf("the host read kept %+v, want the one CA from %s", ht.cas, bundle)
	}
	if len(ht.notes) != 1 || !strings.Contains(ht.notes[0], "1 certificate in "+bundle+" was left out") {
		t.Errorf("the left-out certificate is not said: %q", ht.notes)
	}
}

// A MAC'S SSL_CERT_FILE IS ITS TRUST TOO: a container launch on a macOS host reads the bundle the
// host's SSL_CERT_FILE names beside the keychains, as a Linux host does (OQ-PS15, "regardless of
// how that trust got there"). The macOS podman nightly's TestAContainerJailTrustsTheHostsCAs failed
// because the darwin read took the keychains alone, so the CA the host bundle held never reached
// the jail and NODE_EXTRA_CA_CERTS was left unset. Runs on Linux by stubbing the host's OS and
// keychains.
func TestAMacHostTrustsTheBundleItsSSLCertFileNames(t *testing.T) {
	keychainCA := hostCATestCA(t, "Example Corp Keychain CA", catrust.SourceSystemKeychain)
	prevGOOS, prevReaders := hostGOOS, hostKeychainReaders
	hostGOOS = "darwin"
	hostKeychainReaders = func() macosuser.KeychainReaders {
		return macosuser.KeychainReaders{
			System: func() (string, error) { return keychainCA.PEM, nil },
			Login:  func() (string, error) { return "", errors.New("no login keychain at /nonexistent") },
			Verify: func(string) bool { return true },
		}
	}
	t.Cleanup(func() { hostGOOS, hostKeychainReaders = prevGOOS, prevReaders })

	bundleCA := hostCATestCA(t, "Example Corp Integration CA", "x")
	bundle := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(bundle, []byte(bundleCA.PEM), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", bundle)
	ht := readHostTrustReal()
	var sources []string
	for _, c := range ht.cas {
		sources = append(sources, c.Name+" <- "+c.Source)
	}
	want := []string{
		"Example Corp Keychain CA <- " + catrust.SourceSystemKeychain,
		"Example Corp Integration CA <- " + catrust.HostBundleSource(bundle),
	}
	if !slices.Equal(sources, want) {
		t.Errorf("a Mac's host read kept %q, want %q", sources, want)
	}

	// An unreadable SSL_CERT_FILE warns, naming the next step, and keeps the keychains' CAs.
	t.Setenv("SSL_CERT_FILE", filepath.Join(t.TempDir(), "missing.pem"))
	ht = readHostTrustReal()
	if len(ht.cas) != 1 || ht.cas[0].Name != keychainCA.Name {
		t.Errorf("an unreadable SSL_CERT_FILE dropped the keychain's CA: %+v", ht.cas)
	}
	if !slices.ContainsFunc(ht.notes, func(n string) bool {
		return strings.Contains(n, "could not read this host's CA bundle") && strings.Contains(n, "Point SSL_CERT_FILE")
	}) {
		t.Errorf("an unreadable SSL_CERT_FILE is not warned about with its next step: %q", ht.notes)
	}

	// With SSL_CERT_FILE unset a Mac reads its keychains alone: no Linux distribution path.
	t.Setenv("SSL_CERT_FILE", "")
	ht = readHostTrustReal()
	if len(ht.cas) != 1 || ht.cas[0].Name != keychainCA.Name {
		t.Errorf("with no SSL_CERT_FILE a Mac's host read kept %+v, want the keychain's CA alone", ht.cas)
	}
}

// A host with no CA to add gets no file and no variable, and every source it could not read is
// printed, naming the next step.
func TestAHostWithNoCAsHandsTheJailNothingAndSaysWhy(t *testing.T) {
	stubHostTrust(t, hostTrust{notes: []string{"Warning: could not read this host's CA bundle (none) — set SSL_CERT_FILE in env_sources"}})
	var buf bytes.Buffer
	ws := t.TempDir()
	if got := (&Options{}).prepareHostCATrust("podman", ws, testPrinter(&buf)); got != "" {
		t.Errorf("prepareHostCATrust = %q, want none", got)
	}
	if !strings.Contains(buf.String(), "could not read this host's CA bundle") {
		t.Errorf("the read failure was not printed: %q", buf.String())
	}
	if _, err := os.Stat(filepath.Join(ws, hostCAFileName)); !os.IsNotExist(err) {
		t.Errorf("a file was written with no CA to hold: %v", err)
	}
	if args := hostCAArgs("podman", ws, ""); args != nil {
		t.Errorf("hostCAArgs with no file = %v, want nothing", args)
	}
}

// Apple Container binds the workspace's home whole at /home/agent, so the file is written into
// that home and named there, with no bind of its own.
func TestAppleContainerGetsTheHostsCAsInItsHome(t *testing.T) {
	ca := hostCATestCA(t, "Example Corp Inspection CA", catrust.SourceSystemKeychain)
	stubHostTrust(t, hostTrust{cas: []catrust.CA{ca}})
	var buf bytes.Buffer
	ws := t.TempDir()
	got := (&Options{}).prepareHostCATrust("container", ws, testPrinter(&buf))
	want := "/home/agent/" + entrypoint.HostCAFileHomeRel
	if got != want {
		t.Fatalf("prepareHostCATrust = %q, want %q", got, want)
	}
	body, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(entrypoint.HostCAFileHomeRel)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), ca.PEM) || !strings.Contains(string(body), catrust.SourceSystemKeychain) {
		t.Errorf("the home copy does not hold the labelled CA:\n%s", body)
	}
	args := hostCAArgs("container", ws, got)
	if want := []string{"-e", entrypoint.HostCAFileEnv + "=" + want}; !slices.Equal(args, want) {
		t.Errorf("hostCAArgs = %v, want %v", args, want)
	}
}

// THE LAUNCHER WRITES NOTHING ON STDOUT during a launch: `yolo run -- <cmd>`'s stdout IS the
// command's stdout (the session runs with the process's own stream, never Options.Stdout), so
// every line the launcher prints — a warning, a refusal, a note, a progress line, a disclosure —
// goes to stderr, where launch.log still records it (launchlog.go). The whole launch runs over a
// fixture whose config makes it warn, so a call site that hands a stdout printer a launch line
// anywhere on the container path fails this.
func TestALaunchPrintsNothingOnTheCommandsStdout(t *testing.T) {
	ca := hostCATestCA(t, "Example Corp Inspection CA", catrust.SourceSystemKeychain)
	stubHostTrust(t, hostTrust{cas: []catrust.CA{ca}, notes: []string{"3 certificate(s) were left out"}})
	_, _, _, stdout, stderr := sealCrossingsFixture.launchStreams(t, false)
	if stdout != "" {
		t.Errorf("the launcher printed on stdout, which the jail command's output shares:\nstdout:\n%s", stdout)
	}
	// The fixture's device does not exist, so the config validator warns, and the device line
	// discloses the KVM passthrough: both reached stdout before the launcher's lines moved.
	for _, want := range []string{"device path does not exist", "KVM passthrough: /dev/kvm"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the launch did not print %q on stderr, so it no longer shows where a launch "+
				"line goes:\nstderr:\n%s", want, stderr)
		}
	}
}
