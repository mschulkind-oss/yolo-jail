package integration

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// TestMacosUserTrustsSystemKeychainCA: A CERTIFICATE AUTHORITY THE MAC'S SYSTEM OR LOGIN KEYCHAIN TRUSTS
// FOR TLS IS TRUSTED INSIDE THE SANDBOX, and the launch names it (internal/macosuser/cabundle.go;
// docs/design/provisioner-sets.md PS-D10, PS-D14, PS-D17).
//
// THE CA IS ONE THIS MAC ALREADY TRUSTS. The test never writes a keychain: the suite takes no
// privilege `yolo yolo-account create` does not install, and adding a trusted root to the System keychain
// needs root (OQ-DM1 in docs/design/macos-user-suite-on-a-developer-mac.md, ruled 2026-10-18). It
// reads the System keychain as the invoking user, the way a launch does, through the launch's own
// composition (macosuser.ComposeCATrust over macosuser.RealDeps: `security find-certificate`, then
// `security verify-cert` per candidate), and takes the CAs that composition keeps. A Mac whose
// System keychain holds none — an unmanaged Mac, and the GitHub runner — skips, countably, saying
// so; an MDM-managed Mac, the case the feature exists for, runs it.
//
// WHAT IT ASSERTS, with no key for any of those CAs and so no server one signed to connect to:
//
//   - the five bundle variables name the session's CA bundle, and NODE_EXTRA_CA_CERTS its extras
//     file;
//   - the sandbox account can READ both, and each holds every CA the composition kept;
//   - the launch's output and launch.log name each kept CA, and the disclosure names no keychain
//     certificate the composition left out.
//
// ⚠ What it no longer measures: a TLS handshake through the bundle. That needs a server whose
// leaf the CA signed, which needs the CA's key, which a CA already on the machine does not hand
// over. That the tools read the variables is measured in a Linux jail (PS-D10's MEASURED clause).
func TestMacosUserTrustsSystemKeychainCA(t *testing.T) {
	requireMacosUser(t)
	kept, leftOut := catrustKeychainCAs(t)
	if len(kept) == 0 {
		macosUserSkipCountably(t, macosUserGateLaunch, fmt.Sprintf("neither this Mac's System keychain nor "+
			"your login keychain holds a certificate authority macOS trusts for TLS (%d System "+
			"keychain certificate(s) read, every one left out), "+
			"and this test only reads it: it never adds one, which would need root (OQ-DM1). A Mac "+
			"whose MDM or administrator installed a TLS CA in the System keychain runs it",
			len(leftOut)))
	}
	for _, c := range kept {
		t.Logf("a CA trusted for TLS, from %s: %s", c.Source, c.Name)
	}
	// BOTH MODES (PS-D15): the second account reads the files through its one ACL grant, and
	// run-as-you reads files the user wrote, by the exact paths its profile opens.
	for _, mode := range []string{"account", "self"} {
		t.Run(mode, func(t *testing.T) {
			packHome(t, `{"macos_user_mode": "`+mode+`"}`)
			macosUserCATrustLaunch(t, mode, kept, leftOut)
		})
	}
}

// macosUserCATrustLaunch is one launch of TestMacosUserTrustsSystemKeychainCA, in the mode the
// isolated user config names.
func macosUserCATrustLaunch(t *testing.T, mode string, kept []macosuser.TrustedCA, leftOut []string) {
	t.Helper()
	ws := macosUserWorkspace(t, `{}`)
	r := runMacosUserAs(t, mode, ws, `echo "=== VARS ==="; `+
		`echo "NIX=${NIX_SSL_CERT_FILE-}"; echo "SSL=${SSL_CERT_FILE-}"; echo "REQ=${REQUESTS_CA_BUNDLE-}"; `+
		`echo "CURLCA=${CURL_CA_BUNDLE-}"; echo "GIT=${GIT_SSL_CAINFO-}"; echo "NODE=${NODE_EXTRA_CA_CERTS-}"; echo "GODEBUG=${GODEBUG-}"; `+
		`echo "=== BUNDLE ==="; cat "$CURL_CA_BUNDLE"; echo "BUNDLE_RC=$?"; `+
		`echo "=== EXTRAS ==="; cat "$NODE_EXTRA_CA_CERTS"; echo "EXTRAS_RC=$?"; echo "=== END ==="`)
	if r.rc != 0 {
		t.Fatalf("the macos-user launch failed (rc %d):\n%s", r.rc, lastLines(r.combined(), 40))
	}
	vars := hd10Fields(section(r.stdout, "=== VARS ===", "=== BUNDLE ==="))
	for _, k := range []string{"NIX", "SSL", "REQ", "CURLCA", "GIT"} {
		if !strings.HasPrefix(vars[k], macosuser.StateDir()+"/env/") || !strings.HasSuffix(vars[k], ".ca-bundle.crt") {
			t.Errorf("%s names %q, not the session's CA bundle", k, vars[k])
		}
	}
	if !strings.HasPrefix(vars["NODE"], macosuser.StateDir()+"/env/") || !strings.HasSuffix(vars["NODE"], ".extra-ca.pem") {
		t.Errorf("NODE_EXTRA_CA_CERTS names %q, not the session's extra-CA file", vars["NODE"])
	}
	// THE SEPARATE ACCOUNT'S GO PROGRAMS (PS-D17): told to verify against the bundle there, and
	// never in run-as-you mode, which asks macOS as you. What a Go 1.27+ client then does with the
	// setting needs a server signed by a kept CA, which this test cannot have (see above).
	hasGo := strings.Contains(","+vars["GODEBUG"]+",", ",x509sslcertoverrideplatform=1,")
	if mode == "account" && !hasGo {
		t.Errorf("the separate-account sandbox's GODEBUG is %q, without x509sslcertoverrideplatform=1",
			vars["GODEBUG"])
	}
	if mode == "self" && hasGo {
		t.Errorf("the run-as-you sandbox's GODEBUG is %q, which names a setting only the separate "+
			"account needs", vars["GODEBUG"])
	}
	for _, f := range []struct{ name, start, end, rc string }{
		{"the CA bundle", "=== BUNDLE ===", "=== EXTRAS ===", "BUNDLE_RC=0"},
		{"the extra-CA file", "=== EXTRAS ===", "=== END ===", "EXTRAS_RC=0"},
	} {
		body := section(r.stdout, f.start, f.end)
		if !strings.Contains(body, f.rc) {
			t.Errorf("the sandbox could not read %s:\n%s", f.name, lastLines(body, 10))
			continue
		}
		for _, c := range kept {
			if !catrustHoldsCert(body, c.PEM) {
				t.Errorf("%s the sandbox read does not hold %s", f.name, c.Name)
			}
		}
	}
	logBody, _ := os.ReadFile(filepath.Join(ws, ".yolo", "launch.log"))
	for where, text := range map[string]string{"the launch's output": r.combined(), "launch.log": string(logBody)} {
		disclosure := catrustDisclosure(text)
		if disclosure == "" {
			t.Errorf("%s has no line naming the CAs it took from this Mac's keychains:\n%s",
				where, lastLines(text, 40))
			continue
		}
		for _, c := range kept {
			if !strings.Contains(disclosure, c.Name) {
				t.Errorf("%s does not name the CA it trusted (%s):\n%s", where, c.Name, disclosure)
			}
		}
		for _, name := range leftOut {
			if strings.Contains(disclosure, name) {
				t.Errorf("%s names %s, which the keychain holds and the launch must leave out:\n%s",
					where, name, disclosure)
			}
		}
	}
}

// catrustKeychainCAs is the launch's own composition over this Mac's System keychain: the CAs it
// keeps, and the names of the certificates it leaves out that no kept CA shares (a shared name
// cannot tell the two apart in the disclosure). It reads only; nothing here can write a keychain.
func catrustKeychainCAs(t *testing.T) (kept []macosuser.TrustedCA, leftOut []string) {
	t.Helper()
	// ComposeCATrust needs a tool profile with a bundle to append to; a stand-in is enough, since
	// only the keychain half is asked.
	profile := t.TempDir()
	bundle := filepath.Join(profile, "etc", "ssl", "certs", "ca-bundle.crt")
	if err := os.MkdirAll(filepath.Dir(bundle), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, []byte("# stand-in public roots\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ca := macosuser.ComposeCATrust(macosuser.RealDeps(nil, nil, false), &macosuser.Darwin{ProfilePath: profile})
	if ca.ReadError != "" {
		t.Fatalf("the launch's own read of the System keychain failed (%s); a launch would warn and "+
			"trust the public roots alone, so there is nothing for this test to measure. Check "+
			"`/usr/bin/security find-certificate -a -p /Library/Keychains/System.keychain` as yourself",
			ca.ReadError)
	}
	keptNames := map[string]bool{}
	for _, c := range ca.Kept {
		keptNames[c.Name] = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/security", "find-certificate", "-a", "-p",
		"/Library/Keychains/System.keychain").Output()
	if err != nil {
		t.Fatalf("reading the System keychain as yourself: %v", err)
	}
	seen := map[string]bool{}
	for rest := out; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		name := cert.Subject.CommonName
		if name == "" {
			name = cert.Subject.String()
		}
		if !keptNames[name] && !seen[name] && !catrustNameInside(name, ca.Kept) {
			seen[name] = true
			leftOut = append(leftOut, name)
		}
	}
	return ca.Kept, leftOut
}

// catrustNameInside reports whether name is part of a kept CA's name, which the disclosure then
// carries whether or not the certificate it names was left out.
func catrustNameInside(name string, kept []macosuser.TrustedCA) bool {
	for _, c := range kept {
		if strings.Contains(c.Name, name) {
			return true
		}
	}
	return false
}

// catrustHoldsCert reports whether text carries the certificate pemText encodes, compared by DER
// so line wrapping cannot decide it.
func catrustHoldsCert(text, pemText string) bool {
	want, _ := pem.Decode([]byte(pemText))
	if want == nil {
		return false
	}
	for rest := []byte(text); ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return false
		}
		if bytes.Equal(block.Bytes, want.Bytes) {
			return true
		}
	}
}

// catrustDisclosure is every line of text in which the launch names the CAs it took from the
// System keychain (cabundle.go's printCATrust), joined.
func catrustDisclosure(text string) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "from this Mac's System keychain") || strings.Contains(line, "from your login keychain") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return strings.Join(out, "\n")
}
