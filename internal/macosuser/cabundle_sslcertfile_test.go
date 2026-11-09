package macosuser

// cabundle_sslcertfile_test.go pins the macos-user half of OQ-PS15's "regardless of how that trust
// got there" for the bundle the launching user's SSL_CERT_FILE names: it is merged after the
// keychains' CAs, a certificate the keychains already hold keeps the keychain's name, an unreadable
// bundle warns with the next step and keeps the keychains' CAs, and with SSL_CERT_FILE unset no
// bundle is read. The container launch on a macOS host makes the same read (internal/cli/run
// TestAMacHostTrustsTheBundleItsSSLCertFileNames). Driven through RunMacosUser, so dropping the
// read from ComposeCATrust or its notes from printCATrust fails here.

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// sslCertFileDeps is caDeps with the launching user's SSL_CERT_FILE naming path, whose content is
// body ("" is a file that cannot be read), recording every file write and every path read.
func sslCertFileDeps(t *testing.T, f keychainFixture, path, body string) (Deps, map[string]string, *[]string) {
	t.Helper()
	var rec []string
	d := caDeps(t, &rec, f)
	d.Getenv = func(k string) string {
		if k == "SSL_CERT_FILE" {
			return path
		}
		return ""
	}
	var reads []string
	profileRead := d.ReadFile
	d.ReadFile = func(p string) (string, bool) {
		reads = append(reads, p)
		if path != "" && p == path {
			if body == "" {
				return "", false
			}
			return body, true
		}
		return profileRead(p)
	}
	contents := map[string]string{}
	d.WriteHostFile = func(p, content string, mode os.FileMode) bool {
		rec = append(rec, fmt.Sprintf("write:%s %#o", p, mode))
		contents[p] = content
		return true
	}
	return d, contents, &reads
}

// sessionKey is the launch's session key, read off the profile it installed.
func sessionKey(t *testing.T, contents map[string]string) string {
	t.Helper()
	for p := range contents {
		if rest, ok := strings.CutPrefix(p, stateDir+"/profile-"); ok {
			return strings.TrimSuffix(rest, ".sb")
		}
	}
	t.Fatalf("no profile installed")
	return ""
}

// THE BUNDLE SSL_CERT_FILE NAMES IS THE SANDBOX'S TRUST TOO: its CA is in the session's bundle and
// extras after the keychain's, named with the bundle's path on the notice stream; the keychain's CA
// it repeats is named once, by the keychain; its expired certificate is counted in a note.
func TestAMacosUserLaunchTrustsTheBundleItsSSLCertFileNames(t *testing.T) {
	f := newKeychainFixture(t)
	now := time.Now()
	corp := testCAPEM(t, "Zscaler Root CA", true, now.Add(-time.Hour), now.Add(24*time.Hour), x509.ExtKeyUsageServerAuth)
	stale := testCAPEM(t, "Stale Root CA", true, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	path := "/Users/lee/certs/corp-bundle.pem"
	d, contents, _ := sslCertFileDeps(t, f, path, corp+f.trusted+stale)
	var out, notices bytes.Buffer
	d.Out, d.Notices = &out, &notices
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d\nout:\n%s\nnotices:\n%s", rc, out.String(), notices.String())
	}
	key := sessionKey(t, contents)
	if got, want := contents[CABundleFile(key, "")], testRoots+f.kept()+corp; got != want {
		t.Errorf("the session's bundle is not the profile's roots, the keychain's CAs, then the bundle's CA:\n%s", got)
	}
	if got, want := contents[CAExtrasFile(key, "")], f.kept()+corp; got != want {
		t.Errorf("the session's extras are not the keychain's CAs, then the bundle's CA:\n%s", got)
	}
	for _, want := range []string{
		"Trusting 3 certificate authorities, 2 from this Mac's System keychain " +
			"(Example Corp Inspection CA, Example Corp Any-Purpose CA) and 1 from the CA bundle at " + path + " (Zscaler Root CA).",
		"1 certificate in " + path + " was left out of the sandbox's trust",
	} {
		if !strings.Contains(notices.String(), want) {
			t.Errorf("the notice stream lacks %q:\n%s", want, notices.String())
		}
		if strings.Contains(out.String(), want) {
			t.Errorf("%q reached Out, the launch's stdout:\n%s", want, out.String())
		}
	}
}

// AN UNREADABLE BUNDLE NARROWS, NEVER REFUSES: the launch warns with the next step and still trusts
// the keychain's CAs.
func TestAMacosUserLaunchWithAnUnreadableSSLCertFileWarnsAndKeepsTheKeychain(t *testing.T) {
	f := newKeychainFixture(t)
	path := "/Users/lee/certs/missing.pem"
	d, contents, _ := sslCertFileDeps(t, f, path, "")
	var out, notices bytes.Buffer
	d.Out, d.Notices = &out, &notices
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d\nout:\n%s\nnotices:\n%s", rc, out.String(), notices.String())
	}
	if got := contents[CAExtrasFile(sessionKey(t, contents), "")]; got != f.kept() {
		t.Errorf("an unreadable SSL_CERT_FILE changed the keychain's CAs:\n%s", got)
	}
	for _, want := range []string{"could not read this host's CA bundle (SSL_CERT_FILE names " + path,
		"Point SSL_CERT_FILE at a readable bundle, or unset it", "Trusting 2 certificate authorities from this Mac's System keychain"} {
		if !strings.Contains(notices.String(), want) {
			t.Errorf("the notice stream lacks %q:\n%s", want, notices.String())
		}
	}
}

// WITH SSL_CERT_FILE UNSET, the keychains alone: no bundle is read, never a Linux distribution path.
func TestAMacosUserLaunchWithNoSSLCertFileReadsNoBundle(t *testing.T) {
	f := newKeychainFixture(t)
	d, _, reads := sslCertFileDeps(t, f, "", "")
	var notices bytes.Buffer
	d.Notices = &notices
	d.Out = &bytes.Buffer{}
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, notices.String())
	}
	for _, p := range *reads {
		if strings.HasPrefix(p, "/etc/") {
			t.Errorf("with no SSL_CERT_FILE the launch read %s", p)
		}
	}
	if strings.Contains(notices.String(), "CA bundle (") {
		t.Errorf("with no SSL_CERT_FILE the launch names a host CA bundle:\n%s", notices.String())
	}
}

// withProfileRoots has the tool profile's CA bundle read as roots, a real certificate, in place of
// caDeps' unparseable stub, so the filter against the profile's roots has something to match.
func withProfileRoots(d Deps, roots string) Deps {
	prev := d.ReadFile
	d.ReadFile = func(p string) (string, bool) {
		if p == testProfile+"/"+profileCABundleRel {
			return roots, true
		}
		return prev(p)
	}
	return d
}

// A BUNDLE CERTIFICATE THE PROFILE'S ROOTS ALREADY CARRY IS NOT ADDED (nor named) again, matched by
// its DER: one with the same name but another key is a different certificate, and is kept.
func TestTheSSLCertFileBundleSkipsWhatTheProfileRootsCarry(t *testing.T) {
	f := newKeychainFixture(t)
	now := time.Now()
	root := testCAPEM(t, "Public Root CA", true, now.Add(-time.Hour), now.Add(24*time.Hour), x509.ExtKeyUsageServerAuth)
	twin := testCAPEM(t, "Public Root CA", true, now.Add(-time.Hour), now.Add(24*time.Hour), x509.ExtKeyUsageServerAuth)
	d, _, _ := sslCertFileDeps(t, f, "/Users/lee/certs/corp-bundle.pem", root+twin)
	d = withProfileRoots(d, root)
	ca := ComposeCATrust(d, &Darwin{ProfilePath: testProfile})
	var got []string
	for _, c := range ca.Kept {
		switch c.PEM {
		case root:
			got = append(got, "root")
		case twin:
			got = append(got, "twin")
		}
	}
	if strings.Join(got, ",") != "twin" {
		t.Errorf("from the bundle the launch kept %v, want the same-name certificate with another key alone", got)
	}
}

// THE DISCLOSURE NAMES THE BUNDLE AS A SOURCE IT READ: with nothing added, the line says the
// keychain(s) and the bundle added nothing, and the keychains' left-out count stays theirs.
func TestTheTrustLineNamesTheSSLCertFileBundleAsASource(t *testing.T) {
	f := newKeychainFixture(t)
	now := time.Now()
	root := testCAPEM(t, "Public Root CA", true, now.Add(-time.Hour), now.Add(24*time.Hour), x509.ExtKeyUsageServerAuth)
	path := "/Users/lee/certs/corp-bundle.pem"
	for _, tc := range []struct {
		name         string
		login        bool
		holder, left string
	}{
		{"system", false, "this Mac's System keychain and the bundle SSL_CERT_FILE names (" + path +
			") add no certificate authority", " in this Mac's System keychain were left out"},
		{"system and login", true, "this Mac's System keychain, your login keychain and the bundle " +
			"SSL_CERT_FILE names (" + path + ") add no certificate authority", " in this Mac's System keychain and your login keychain were left out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, _ := sslCertFileDeps(t, f, path, root)
			d = withProfileRoots(d, root)
			d.VerifyCA = verifyOnly()
			if tc.login {
				d.ReadLoginKeychain = func() (string, error) { return "", nil }
			}
			var notices bytes.Buffer
			d.Out, d.Notices = &bytes.Buffer{}, &notices
			if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
				t.Fatalf("rc = %d\n%s", rc, notices.String())
			}
			for _, want := range []string{tc.holder, tc.left} {
				if !strings.Contains(notices.String(), want) {
					t.Errorf("the notice stream lacks %q:\n%s", want, notices.String())
				}
			}
			// Nothing was trusted, so the left-out count is not "more" than anything.
			if strings.Contains(notices.String(), " more certificate") {
				t.Errorf("the left-out count says \"more\" though no CA was trusted:\n%s", notices.String())
			}
		})
	}
}

// AN UNREADABLE SYSTEM KEYCHAIN WITH THE BUNDLE READ says the sandbox adds none of the System
// keychain's CAs, not that it adds only the login keychain's: the bundle's are what remain.
func TestAnUnreadableSystemKeychainBesideTheSSLCertFileBundleSaysNoneOfItsCAs(t *testing.T) {
	f := newKeychainFixture(t)
	now := time.Now()
	corp := testCAPEM(t, "Zscaler Root CA", true, now.Add(-time.Hour), now.Add(24*time.Hour), x509.ExtKeyUsageServerAuth)
	d, _, _ := sslCertFileDeps(t, f, "/Users/lee/certs/corp-bundle.pem", corp)
	d.ReadSystemKeychain = func() (string, error) { return "", fmt.Errorf("security: SecKeychainOpen failed") }
	var notices bytes.Buffer
	d.Out, d.Notices = &bytes.Buffer{}, &notices
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, notices.String())
	}
	want := "could not read this Mac's System keychain (security: SecKeychainOpen failed), so the " +
		"sandbox adds none of its certificate authorities."
	if !strings.Contains(notices.String(), want) {
		t.Errorf("the notice stream lacks %q:\n%s", want, notices.String())
	}
}
