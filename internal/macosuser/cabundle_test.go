package macosuser

// cabundle_test.go pins the sandbox's corporate CA trust (cabundle.go), with certificates
// crypto/x509 makes here: the filter keeps a CA macOS trusts for TLS and drops an expired one, a
// leaf, a client-only CA and one macOS rejects; the bundle is the profile's roots plus the kept
// CAs; the env file sets the five bundle variables, and NODE_EXTRA_CA_CERTS only when there are
// extras; the files are written dir, then tee, then the read ACE, and swept with the env file; and
// the disclosure names each CA. The launch and the capture are driven through RunMacosUser,
// RunCaptureAct and RunCapturePlan, so deleting either's wiring fails a test here.

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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// testCAPEM makes one self-signed certificate as PEM.
func testCAPEM(t *testing.T, cn string, isCA bool, notBefore, notAfter time.Time, eku ...x509.ExtKeyUsage) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"Example Corp"}},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  isCA,
		BasicConstraintsValid: true,
		ExtKeyUsage:           eku,
	}
	if isCA {
		tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	} else {
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// keychainFixture is a System keychain export: a CA macOS trusts, a second one whose extended key
// usage is Any (which allows TLS servers), one macOS does not trust, an expired CA, one not yet
// valid, a leaf, a client-auth-only CA, a duplicate of the trusted one, and a block that is not a
// certificate.
type keychainFixture struct {
	trusted, anyPurpose, rejected, expired, notYetValid, leaf, clientOnly string
	export                                                                string
}

func newKeychainFixture(t *testing.T) keychainFixture {
	t.Helper()
	now := time.Now()
	f := keychainFixture{
		trusted:     testCAPEM(t, "Example Corp Inspection CA", true, now.Add(-time.Hour), now.Add(24*time.Hour), x509.ExtKeyUsageServerAuth),
		anyPurpose:  testCAPEM(t, "Example Corp Any-Purpose CA", true, now.Add(-time.Hour), now.Add(24*time.Hour), x509.ExtKeyUsageAny),
		rejected:    testCAPEM(t, "Untrusted Lab CA", true, now.Add(-time.Hour), now.Add(24*time.Hour)),
		expired:     testCAPEM(t, "Expired CA", true, now.Add(-48*time.Hour), now.Add(-24*time.Hour)),
		notYetValid: testCAPEM(t, "Not Yet Valid CA", true, now.Add(24*time.Hour), now.Add(48*time.Hour)),
		leaf:        testCAPEM(t, "leaf.example.com", false, now.Add(-time.Hour), now.Add(24*time.Hour), x509.ExtKeyUsageServerAuth),
		clientOnly:  testCAPEM(t, "Client Only CA", true, now.Add(-time.Hour), now.Add(24*time.Hour), x509.ExtKeyUsageClientAuth),
	}
	f.export = f.trusted + f.anyPurpose + f.rejected + f.expired + f.notYetValid + f.leaf + f.clientOnly +
		f.trusted + "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"
	return f
}

// kept is what a launch over the fixture trusts beyond the public roots, in keychain order.
func (f keychainFixture) kept() string { return f.trusted + f.anyPurpose }

// verifyOnly is a VerifyCA that trusts exactly these PEMs.
func verifyOnly(pemBlocks ...string) func(string) bool {
	return func(p string) bool { return slices.Contains(pemBlocks, p) }
}

const testProfile = "/nix/store/000mock-yolo-noncontainer-profile"
const testRoots = "-----BEGIN CERTIFICATE-----\nMOZILLA\n-----END CERTIFICATE-----\n"

// caDeps is mockDeps with a tool profile carrying a CA bundle and a keychain export.
func caDeps(t *testing.T, rec *[]string, f keychainFixture) Deps {
	t.Helper()
	d := mockDeps(rec)
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		return &Darwin{PathPrefix: []string{testProfile + "/bin"}, System: "aarch64-darwin", ProfilePath: testProfile}, true, nil
	}
	d.ReadFile = func(p string) (string, bool) {
		if p == testProfile+"/"+profileCABundleRel {
			return testRoots, true
		}
		return "", true
	}
	d.ReadSystemKeychain = func() (string, error) { return f.export, nil }
	d.VerifyCA = verifyOnly(f.trusted, f.anyPurpose)
	return d
}

// THE FILTER, then macOS's own word: a trusted TLS CA is kept, once, and so is one whose extended
// key usage is Any; an expired CA, one not yet valid, a leaf, a client-only CA and a
// non-certificate block are left out before any verify; a CA macOS rejects is left out by the
// verify. The bundle is the profile's roots plus the kept CAs.
func TestComposeCATrustKeepsOnlyTrustedTLSCAs(t *testing.T) {
	f := newKeychainFixture(t)
	d := caDeps(t, nil, f)
	var asked []string
	trust := verifyOnly(f.trusted, f.anyPurpose)
	d.VerifyCA = func(p string) bool { asked = append(asked, p); return trust(p) }
	ca := ComposeCATrust(d, &Darwin{ProfilePath: testProfile})
	if ca.ReadError != "" || ca.ProfileBundle != testProfile+"/"+profileCABundleRel {
		t.Fatalf("ComposeCATrust = %+v", ca)
	}
	if len(ca.Kept) != 2 || ca.Kept[0].Name != "Example Corp Inspection CA" || ca.Kept[0].PEM != f.trusted ||
		ca.Kept[1].Name != "Example Corp Any-Purpose CA" || ca.Kept[1].PEM != f.anyPurpose {
		t.Fatalf("kept %+v, want the trusted CA and the any-purpose one, in keychain order", ca.Kept)
	}
	if ca.LeftOut != 5 {
		t.Errorf("LeftOut = %d, want 5 (rejected, expired, not yet valid, leaf, client-only)", ca.LeftOut)
	}
	if len(asked) != 3 {
		t.Errorf("verify-cert was asked about %d certificates, want 3 (the filter runs first, and "+
			"a duplicate is asked once)", len(asked))
	}
	for name, p := range map[string]string{"an expired CA": f.expired, "a CA not yet valid": f.notYetValid} {
		if slices.Contains(asked, p) {
			t.Errorf("%s reached verify-cert; the validity filter must leave it out first", name)
		}
	}
	if ca.Bundle != testRoots+f.kept() || ca.Extras != f.kept() {
		t.Errorf("bundle = %q, extras = %q", ca.Bundle, ca.Extras)
	}
}

// THE TWO KEYCHAIN COMMANDS, exactly. Each runs only on a Mac, so nothing else on Linux would see a
// flag go missing, and two would fail silently there: without -l, macOS judges each CA as the
// server certificate under test and rejects every one, so the launch keeps no CA on any Mac;
// without -R offline, every launch asks the network about revocation.
func TestTheKeychainCommandsAreTheOnesALaunchRuns(t *testing.T) {
	if got, want := strings.Join(systemKeychainExportArgv(), " "),
		"/usr/bin/security find-certificate -a -p /Library/Keychains/System.keychain"; got != want {
		t.Errorf("the keychain read is %q, want %q", got, want)
	}
	if got, want := strings.Join(verifyCAArgv("/tmp/yolo-ca-1.pem"), " "),
		"/usr/bin/security verify-cert -l -L -R offline -p ssl -c /tmp/yolo-ca-1.pem"; got != want {
		t.Errorf("the trust question is %q, want %q", got, want)
	}
}

// NO TOOL PROFILE, NO TRUST, and nothing read: a dry run and a capture with no packages. A
// profile with no bundle sets nothing either, rather than naming a file that is not there.
func TestComposeCATrustNeedsTheProfilesBundle(t *testing.T) {
	f := newKeychainFixture(t)
	d := caDeps(t, nil, f)
	read := false
	d.ReadSystemKeychain = func() (string, error) { read = true; return f.export, nil }
	if ca := ComposeCATrust(d, nil); ca.ProfileBundle != "" || read {
		t.Errorf("no profile composed %+v (keychain read: %v)", ca, read)
	}
	d.ReadFile = func(string) (string, bool) { return "", true }
	ca := ComposeCATrust(d, &Darwin{ProfilePath: testProfile})
	if ca.ProfileBundle != "" || ca.MissingProfileBundle == "" || read {
		t.Errorf("a profile with no bundle composed %+v (keychain read: %v)", ca, read)
	}
	env, bundle, extras, follows := applyCATrust(jsonx.NewOrderedMap(), ca, "k")
	if env.Len() != 0 || bundle != "" || extras != "" || follows != "" {
		t.Errorf("a missing profile bundle set variables: %v", env.Keys())
	}
}

// THE ENV FILE: the five bundle variables name the session's bundle and NODE_EXTRA_CA_CERTS the
// extras alone (one path, never a list); with nothing kept, the five name the profile's own bundle
// and NODE_EXTRA_CA_CERTS is not set. (A bundle the user's own layers name:
// TestAUserBundleVariableIsWhatEveryBundleVariableNames.)
func TestTheEnvFileNamesTheBundleAndExtrasOnlyWithExtras(t *testing.T) {
	f := newKeychainFixture(t)
	ca := ComposeCATrust(caDeps(t, nil, f), &Darwin{ProfilePath: testProfile})
	plan := BuildRunPlanWithDaemons("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(),
		mockDarwin(), nil, JailDaemons{}, FloorStage{}, PlanSession{ID: "0123456789abcdef", CATrust: ca})
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("invariants:\n%s", strings.Join(problems, "\n"))
	}
	key := SessionKey(plan.Cname, plan.SessionID)
	if plan.CABundleFile != CABundleFile(key, "") || plan.CAExtrasFile != CAExtrasFile(key, "") {
		t.Fatalf("CA files %s, %s are not the session's", plan.CABundleFile, plan.CAExtrasFile)
	}
	if plan.CABundleContent != ca.Bundle || plan.CAExtrasContent != ca.Extras {
		t.Errorf("the plan writes other content than it composed")
	}
	for _, k := range CABundleVars {
		if !SandboxEnvFileSets(plan.EnvFileContent, k, plan.CABundleFile) {
			t.Errorf("the env file does not set %s=%s:\n%s", k, plan.CABundleFile, plan.EnvFileContent)
		}
	}
	if plan.CAFollows != "" {
		t.Errorf("CAFollows = %q with no bundle variable in the caller's layers", plan.CAFollows)
	}
	if !SandboxEnvFileSets(plan.EnvFileContent, NodeExtraCAVar, plan.CAExtrasFile) {
		t.Errorf("the env file does not point %s at the extras alone", NodeExtraCAVar)
	}
	for _, f := range []string{plan.CABundleFile, plan.CAExtrasFile} {
		if !removesFile(plan.EnvFileRemoveCommands, f) {
			t.Errorf("%s is not swept with the env file", f)
		}
	}

	none := ca
	none.Kept, none.Bundle, none.Extras = nil, "", ""
	plain := BuildRunPlanWithDaemons("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{}, nil, mockDarwin(), nil,
		JailDaemons{}, FloorStage{}, PlanSession{ID: "0123456789abcdef", CATrust: none})
	if plain.CABundleFile != "" || plain.CAExtrasFile != "" {
		t.Errorf("a launch that kept no CA writes CA files: %s %s", plain.CABundleFile, plain.CAExtrasFile)
	}
	for _, k := range CABundleVars {
		if !SandboxEnvFileSets(plain.EnvFileContent, k, none.ProfileBundle) {
			t.Errorf("with nothing kept, %s does not name the profile's bundle", k)
		}
	}
	if SandboxEnvFileKeysInclude(plain.EnvFileContent, NodeExtraCAVar) {
		t.Errorf("with nothing kept, %s is set", NodeExtraCAVar)
	}
}

// THE LAUNCH'S CALL SITES, through RunMacosUser: the keychain is read, the CA files are written
// after the env file, each directory first (when the env file did not already make it), then tee,
// then its read ACE, and both are removed when the session ends; the launch names the CA it took.
// Fails if ComposeCATrust, the CA install or the disclosure is dropped from the orchestrator.
func TestALaunchWritesTheCABundleBesideItsEnvFileAndNamesItsCAs(t *testing.T) {
	f := newKeychainFixture(t)
	var rec []string
	d := caDeps(t, &rec, f)
	contents := map[string]string{}
	d.InstallRootFile = func(path, content, mode string) bool {
		rec = append(rec, "install:"+path+" "+mode)
		contents[path] = content
		return true
	}
	var out bytes.Buffer
	d.Out = &out
	ws := "/Users/Shared/yolo/proj"
	if rc := RunMacosUser(d, newOpts(ws)); rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	var key string
	for _, r := range rec {
		if rest, ok := strings.CutPrefix(r, "install:"+stateDir+"/profile-"); ok {
			key = strings.TrimSuffix(strings.Fields(rest)[0], ".sb")
		}
	}
	if key == "" {
		t.Fatalf("no profile installed:\n%s", strings.Join(rec, "\n"))
	}
	envFile, bundle, extras := SandboxEnvFile(key, ""), CABundleFile(key, ""), CAExtrasFile(key, "")
	joined := strings.Join(rec, "\n")
	order := []string{
		"install:" + envFile + " 0600",
		"install:" + bundle + " 0600",
		"run:sudo " + chmodBin + " +a user:" + SandboxUser + " allow " + sandboxFileReadRights + " " + bundle,
		"install:" + extras + " 0600",
		"run:sudo " + chmodBin + " +a user:" + SandboxUser + " allow " + sandboxFileReadRights + " " + extras,
		"internal darwin-bootstrap",
	}
	at := -1
	for _, want := range order {
		i := strings.Index(joined, want)
		if i < 0 || i < at {
			t.Fatalf("the launch does not run %q after the steps before it:\n%s", want, joined)
		}
		at = i
	}
	if contents[bundle] != testRoots+f.kept() || contents[extras] != f.kept() {
		t.Errorf("the CA files carry other bytes than the composition")
	}
	if !SandboxEnvFileSets(contents[envFile], "SSL_CERT_FILE", bundle) ||
		!SandboxEnvFileSets(contents[envFile], "NIX_SSL_CERT_FILE", bundle) ||
		!SandboxEnvFileSets(contents[envFile], NodeExtraCAVar, extras) {
		t.Errorf("the env file the launch wrote does not name its CA files:\n%s", contents[envFile])
	}
	for _, f := range []string{bundle, extras} {
		if !strings.Contains(joined, "run:sudo "+rmBin+" -f "+f) {
			t.Errorf("%s is not removed when the session ends", f)
		}
	}
	for _, want := range []string{"Trusting 2 certificate authorities from this Mac's System keychain: " +
		"Example Corp Inspection CA, Example Corp Any-Purpose CA.", "NIX_SSL_CERT_FILE, SSL_CERT_FILE, REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE, " +
		"GIT_SSL_CAINFO name " + bundle, NodeExtraCAVar + " names " + extras, "5 more in it were left out"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the launch does not say %q:\n%s", want, out.String())
		}
	}
}

// A KEYCHAIN THAT CANNOT BE READ WARNS AND NEVER REFUSES: the launch goes on, the variables name
// the profile's public roots, no CA file is written, and the warning names env_sources.
func TestAnUnreadableKeychainWarnsAndPointsAtThePublicRoots(t *testing.T) {
	f := newKeychainFixture(t)
	var rec []string
	d := caDeps(t, &rec, f)
	d.ReadSystemKeychain = func() (string, error) { return "", errors.New("security: SecKeychainOpen failed") }
	contents := map[string]string{}
	d.InstallRootFile = func(path, content, mode string) bool {
		rec = append(rec, "install:"+path)
		contents[path] = content
		return true
	}
	var out bytes.Buffer
	d.Out = &out
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("an unreadable keychain refused the launch: rc = %d\n%s", rc, out.String())
	}
	roots := testProfile + "/" + profileCABundleRel
	for _, want := range []string{"could not read this Mac's System keychain (security: SecKeychainOpen failed)",
		roots, `in env_sources: put a bundle holding it and the public roots in the workspace and set ` +
			`{"SSL_CERT_FILE": "<that file>"}, which the other four follow, and ` +
			`{"NODE_EXTRA_CA_CERTS": "<that file>"} for Node.`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the warning does not carry %q:\n%s", want, out.String())
		}
	}
	for path, content := range contents {
		if strings.HasSuffix(path, caBundleSuffix) || strings.HasSuffix(path, caExtrasSuffix) {
			t.Errorf("a launch with no keychain wrote %s", path)
		}
		if strings.HasSuffix(path, ".env") && !SandboxEnvFileSets(content, "SSL_CERT_FILE", roots) {
			t.Errorf("the env file does not point SSL_CERT_FILE at the public roots:\n%s", content)
		}
	}
}

// THE CAPTURE'S CALL SITE: a capture plan carrying trust writes the same two files, after its env
// file, and removes them with the rest of its litter. Fails if RunCapturePlan stops installing
// them or BuildCapturePlan stops planning them.
func TestACaptureWritesTheSameCAFiles(t *testing.T) {
	f := newKeychainFixture(t)
	opts := testCaptureOptions()
	opts.CATrust = ComposeCATrust(caDeps(t, nil, f), &Darwin{ProfilePath: testProfile})
	plan := BuildCapturePlan(opts)
	if plan.CABundleFile != CABundleFile(plan.Cname, "") || plan.CAExtrasFile != CAExtrasFile(plan.Cname, "") {
		t.Fatalf("capture CA files: %s %s", plan.CABundleFile, plan.CAExtrasFile)
	}
	if !SandboxEnvFileSets(plan.EnvFileContent, "SSL_CERT_FILE", plan.CABundleFile) ||
		!SandboxEnvFileSets(plan.EnvFileContent, NodeExtraCAVar, plan.CAExtrasFile) {
		t.Errorf("the capture's env file does not name its CA files:\n%s", plan.EnvFileContent)
	}
	for _, p := range []string{plan.CABundleFile, plan.CAExtrasFile} {
		if !removesFile(plan.CleanupCommands, p) {
			t.Errorf("the capture's cleanup does not remove %s", p)
		}
	}
	var rec []string
	d := mockDeps(&rec)
	if rc := RunCapturePlan(d, plan); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	joined := strings.Join(rec, "\n")
	env, bundle := strings.Index(joined, "install:"+plan.EnvFile), strings.Index(joined, "install:"+plan.CABundleFile)
	if env < 0 || bundle < env || !strings.Contains(joined, "install:"+plan.CAExtrasFile) {
		t.Errorf("the capture does not write its CA files after its env file:\n%s", joined)
	}
}

// THE NAMES A LAUNCH PRINTS ARE THE KEYCHAIN'S, and the keychain is not the launch's: a common
// name carrying markup or a terminal escape is printed as text.
func TestTheDisclosureEscapesACAsName(t *testing.T) {
	ca := CATrust{ProfileBundle: "/p/bundle", Kept: []TrustedCA{{Name: "[red]Evil\x1b[2J CA"}}, Bundle: "x", Extras: "x"}
	for _, user := range []*jsonx.OrderedMap{nil, userCAEnv("SSL_CERT_FILE", "/x/[bold]corp\x1b[2J.pem")} {
		env, bundle, extras, follows := applyCATrust(user, ca, "k")
		var out bytes.Buffer
		printCATrust(printer{w: &out}, ca, SandboxEnvFileContent(env), bundle, extras, follows)
		if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "red]Evil") {
			t.Errorf("the CA's name was interpreted rather than printed:\n%q", out.String())
		}
		if user != nil && !strings.Contains(out.String(), "bold]corp") {
			t.Errorf("the user's bundle path was interpreted rather than printed:\n%q", out.String())
		}
	}
}

// THE CAPTURE ACT'S CALL SITES: RunCaptureAct composes a launch's trust from the capture's tool
// profile, names it as a launch does, and writes both CA files; its dry run reads no keychain.
// Fails if RunCaptureAct stops composing the trust or stops disclosing it.
func TestACaptureActComposesAndNamesTheLaunchsTrust(t *testing.T) {
	f := newKeychainFixture(t)
	c := &captureDeps{}
	d := c.deps()
	reads := 0
	d.ReadFile = func(p string) (string, bool) {
		if p == testProfile+"/"+profileCABundleRel {
			return testRoots, true
		}
		return "", true
	}
	d.ReadSystemKeychain = func() (string, error) { reads++; return f.export, nil }
	d.VerifyCA = verifyOnly(f.trusted, f.anyPurpose)
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts := testCaptureOptions()
	opts.CaptureRoot = filepath.Join(tmp, "captures")
	opts.Darwin = &Darwin{PathPrefix: []string{testProfile + "/bin"}, System: "aarch64-darwin", ProfilePath: testProfile}

	if rc := RunCaptureAct(d, opts, filepath.Join(tmp, "dry"), true); rc != 0 {
		t.Fatalf("dry run = %d\n%s", rc, c.out.String())
	}
	if reads != 0 || len(c.ran) != 0 {
		t.Errorf("a dry run read the keychain %d time(s) and ran %v", reads, c.ran)
	}

	c.out.Reset()
	realRun := d.Run
	d.Run = func(argv []string) int {
		rc := realRun(argv)
		if strings.Contains(strings.Join(argv, " "), "capture-run") {
			if err := os.MkdirAll(filepath.Join(opts.CaptureRoot, "probetool", "out", "tree"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return rc
	}
	if rc := RunCaptureAct(d, opts, filepath.Join(tmp, "store", "out"), false); rc != 0 {
		t.Fatalf("RunCaptureAct = %d\n%s", rc, c.out.String())
	}
	if reads != 1 {
		t.Errorf("the capture read the keychain %d time(s), want once", reads)
	}
	cname := BuildCapturePlan(opts).Cname
	for _, p := range []string{CABundleFile(cname, ""), CAExtrasFile(cname, "")} {
		if !slices.Contains(c.files, p) {
			t.Errorf("the capture did not write %s: %v", p, c.files)
		}
	}
	want := "Trusting 2 certificate authorities from this Mac's System keychain: " +
		"Example Corp Inspection CA, Example Corp Any-Purpose CA."
	if !strings.Contains(c.out.String(), want) {
		t.Errorf("the capture does not say %q:\n%s", want, c.out.String())
	}
}

// corpBundle is a bundle the user's own env layers name, in the workspace.
const corpBundle = "/Users/Shared/yolo/proj/corp.pem"

// userCAEnv is a caller's env layer setting the bundle variables given as key, value pairs.
func userCAEnv(kv ...string) *jsonx.OrderedMap {
	env := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(kv); i += 2 {
		env.Set(kv[i], kv[i+1])
	}
	return env
}

// A USER'S ONE BUNDLE VARIABLE IS WHAT ALL FIVE NAME. Each tool reads its own of the five first
// (nix's OpenSSL NIX_SSL_CERT_FILE before SSL_CERT_FILE, curl CURL_CA_BUNDLE, git
// GIT_SSL_CAINFO, requests REQUESTS_CA_BUNDLE), so defaulting the other four to the composed
// bundle under a user's SSL_CERT_FILE made curl, git and nix OpenSSL ignore it, where before the
// CA trust no launch set any of them and the user's value worked. The other four take its value,
// the session's bundle is not written (nothing names it), and NODE_EXTRA_CA_CERTS, which Node
// adds to its own roots and is none of the five, still names the keychain's CAs.
func TestAUserBundleVariableIsWhatEveryBundleVariableNames(t *testing.T) {
	f := newKeychainFixture(t)
	ca := ComposeCATrust(caDeps(t, nil, f), &Darwin{ProfilePath: testProfile})
	plan := BuildRunPlanWithDaemons("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{},
		userCAEnv("SSL_CERT_FILE", corpBundle), mockDarwin(), nil, JailDaemons{}, FloorStage{},
		PlanSession{ID: "0123456789abcdef", CATrust: ca})
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("invariants:\n%s", strings.Join(problems, "\n"))
	}
	for _, k := range CABundleVars {
		if !SandboxEnvFileSets(plan.EnvFileContent, k, corpBundle) {
			t.Errorf("with the user's SSL_CERT_FILE set, %s does not name it:\n%s", k, plan.EnvFileContent)
		}
	}
	key := SessionKey(plan.Cname, plan.SessionID)
	if plan.CABundleFile != "" || plan.CABundleContent != "" {
		t.Errorf("the plan writes a CA bundle no variable names: %s", plan.CABundleFile)
	}
	if plan.CAExtrasFile != CAExtrasFile(key, "") || plan.CAExtrasContent != f.kept() {
		t.Errorf("the extras file is %q (content %d bytes), want %s with the kept CAs",
			plan.CAExtrasFile, len(plan.CAExtrasContent), CAExtrasFile(key, ""))
	}
	if !SandboxEnvFileSets(plan.EnvFileContent, NodeExtraCAVar, CAExtrasFile(key, "")) {
		t.Errorf("%s does not still name the keychain's CAs for Node:\n%s", NodeExtraCAVar, plan.EnvFileContent)
	}
	if !removesFile(plan.EnvFileRemoveCommands, CAExtrasFile(key, "")) {
		t.Errorf("the extras file is not swept with the env file")
	}
}

// THE FIRST ONE THE USER SET LEADS, in CABundleVars order (the order nix's OpenSSL reads them), and
// a variable the user set keeps its own value; a user's NODE_EXTRA_CA_CERTS is kept, and then the
// extras file is not written either; an empty value names no bundle, so it is not followed.
func TestTheBundleVariablesFollowTheFirstOneTheUserSet(t *testing.T) {
	f := newKeychainFixture(t)
	ca := ComposeCATrust(caDeps(t, nil, f), &Darwin{ProfilePath: testProfile})
	plan := func(env *jsonx.OrderedMap) RunPlan {
		return BuildRunPlanWithDaemons("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(), []string{"claude"},
			[]string{"claude"}, "/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{}, env, mockDarwin(), nil,
			JailDaemons{}, FloorStage{}, PlanSession{ID: "0123456789abcdef", CATrust: ca})
	}
	p := plan(userCAEnv("GIT_SSL_CAINFO", "/b.pem", "NIX_SSL_CERT_FILE", "/a.pem", NodeExtraCAVar, "/n.pem"))
	for k, want := range map[string]string{"NIX_SSL_CERT_FILE": "/a.pem", "SSL_CERT_FILE": "/a.pem",
		"REQUESTS_CA_BUNDLE": "/a.pem", "CURL_CA_BUNDLE": "/a.pem", "GIT_SSL_CAINFO": "/b.pem", NodeExtraCAVar: "/n.pem"} {
		if !SandboxEnvFileSets(p.EnvFileContent, k, want) {
			t.Errorf("%s is not %s:\n%s", k, want, p.EnvFileContent)
		}
	}
	if p.CABundleFile != "" || p.CAExtrasFile != "" {
		t.Errorf("a plan whose every CA variable is the user's writes %q and %q", p.CABundleFile, p.CAExtrasFile)
	}
	if p.CAFollows != "NIX_SSL_CERT_FILE" {
		t.Errorf("CAFollows = %q, want the first bundle variable the user set", p.CAFollows)
	}

	empty := plan(userCAEnv("SSL_CERT_FILE", ""))
	key := SessionKey(empty.Cname, empty.SessionID)
	if !SandboxEnvFileSets(empty.EnvFileContent, "SSL_CERT_FILE", "") ||
		!SandboxEnvFileSets(empty.EnvFileContent, "NIX_SSL_CERT_FILE", CABundleFile(key, "")) || empty.CAFollows != "" {
		t.Errorf("an empty SSL_CERT_FILE was followed, or lost (CAFollows %q):\n%s", empty.CAFollows, empty.EnvFileContent)
	}
}

// THE LAUNCH'S CALL SITE, through RunMacosUser, in the case the unreadable keychain's warning sends
// a user to: their SSL_CERT_FILE, from their own env, is what every bundle variable in the env file
// the launch wrote names, and the warning gives no advice they already took. With a readable
// keychain, the launch writes only the extras file, and says the other tools trust the user's bundle.
func TestALaunchWithAUserBundleNamesItForEveryTool(t *testing.T) {
	for _, tc := range []struct {
		name    string
		readErr error
		want    []string
	}{
		{"unreadable keychain", errors.New("boom"), []string{"could not read this Mac's System keychain (boom)",
			"Your configured environment sets SSL_CERT_FILE, so NIX_SSL_CERT_FILE, SSL_CERT_FILE, " +
				"REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE, GIT_SSL_CAINFO name " + corpBundle}},
		{"readable keychain", nil, []string{"Trusting 2 certificate authorities from this Mac's System keychain " +
			"in Node only: Example Corp Inspection CA, Example Corp Any-Purpose CA.",
			"Your configured environment sets SSL_CERT_FILE, so NIX_SSL_CERT_FILE, SSL_CERT_FILE, " +
				"REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE, GIT_SSL_CAINFO name " + corpBundle}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newKeychainFixture(t)
			var rec []string
			d := caDeps(t, &rec, f)
			if tc.readErr != nil {
				d.ReadSystemKeychain = func() (string, error) { return "", tc.readErr }
			}
			contents := map[string]string{}
			d.InstallRootFile = func(path, content, mode string) bool {
				rec = append(rec, "install:"+path)
				contents[path] = content
				return true
			}
			var out bytes.Buffer
			d.Out = &out
			o := newOpts("/Users/Shared/yolo/proj")
			o.SandboxEnv = userCAEnv("SSL_CERT_FILE", corpBundle)
			if rc := RunMacosUser(d, o); rc != 42 {
				t.Fatalf("rc = %d\n%s", rc, out.String())
			}
			wroteEnv := false
			for path, content := range contents {
				if strings.HasSuffix(path, caBundleSuffix) {
					t.Errorf("the launch wrote a CA bundle no variable names: %s", path)
				}
				if !strings.HasSuffix(path, ".env") {
					continue
				}
				wroteEnv = true
				for _, k := range CABundleVars {
					if !SandboxEnvFileSets(content, k, corpBundle) {
						t.Errorf("the env file the launch wrote does not set %s to the user's bundle:\n%s", k, content)
					}
				}
			}
			if !wroteEnv {
				t.Fatalf("the launch wrote no env file:\n%s", strings.Join(rec, "\n"))
			}
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("the launch does not say %q:\n%s", want, out.String())
				}
			}
			if strings.Contains(out.String(), "env_sources: put a bundle") {
				t.Errorf("the launch tells a user who set SSL_CERT_FILE to set it:\n%s", out.String())
			}
		})
	}
}

// THE CAPTURE FOLLOWS THE SAME RULE: a user's SSL_CERT_FILE in its env is what all five name.
// Fails if BuildCapturePlan stops applying the launch's rule, or writes a bundle nothing names.
func TestACaptureFollowsAUserBundleVariable(t *testing.T) {
	f := newKeychainFixture(t)
	opts := testCaptureOptions()
	opts.SandboxEnv.Set("SSL_CERT_FILE", corpBundle)
	opts.CATrust = ComposeCATrust(caDeps(t, nil, f), &Darwin{ProfilePath: testProfile})
	plan := BuildCapturePlan(opts)
	for _, k := range CABundleVars {
		if !SandboxEnvFileSets(plan.EnvFileContent, k, corpBundle) {
			t.Errorf("the capture's %s does not name the user's bundle:\n%s", k, plan.EnvFileContent)
		}
	}
	if plan.CABundleFile != "" || plan.CAExtrasFile != CAExtrasFile(plan.Cname, "") || plan.CAExtrasContent != f.kept() {
		t.Errorf("capture CA files: bundle %q, extras %q", plan.CABundleFile, plan.CAExtrasFile)
	}
	if problems := CapturePlanInvariants(plan); len(problems) > 0 {
		t.Errorf("invariants: %v", problems)
	}
}

// EVERY REFUSAL TO INSTALL A CA FILE NAMES THE NEXT STEP: the directory, the write and the read ACE
// each stop the launch, since the variables already name the file, and each says to run it again
// and how to see every privileged command. Driven through RunMacosUser for the ACE, the one a
// launch can reach after the env file prepared the directory; the directory branch is asked of
// the installer directly.
func TestARefusedCAFileInstallNamesTheNextStep(t *testing.T) {
	const next = "run it again, and if this repeats, `yolo run --dry-run` prints every privileged command"
	f := newKeychainFixture(t)
	var rec []string
	d := caDeps(t, &rec, f)
	run := d.Run
	d.Run = func(argv []string) int {
		rc := run(argv)
		if len(argv) > 1 && argv[1] == chmodBin && strings.HasSuffix(argv[len(argv)-1], caBundleSuffix) {
			return 1
		}
		return rc
	}
	var out bytes.Buffer
	d.Out = &out
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 1 {
		t.Fatalf("a failed read ACE on the CA bundle did not refuse: rc = %d\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "Could not grant "+SandboxUser+" read on") || !strings.Contains(out.String(), next) {
		t.Errorf("the refusal names no next step:\n%s", out.String())
	}

	out.Reset()
	d.Run = func([]string) int { return 1 }
	files := caTrustFiles("", "/var/yolo-jail/env/k.ca-bundle.crt", "x", "", "")
	if installCATrustFiles(d, printer{w: &out}, files) || !strings.Contains(out.String(), next) {
		t.Errorf("a failed directory command names no next step:\n%s", out.String())
	}
}

// THE CAPTURE ACT'S DISCLOSURE FOLLOWS THE SAME RULE: with the user's SSL_CERT_FILE in its env, it
// says the five name that file and the keychain's CAs reach Node alone. Fails if RunCaptureAct
// stops passing the plan's followed variable to the disclosure.
func TestACaptureActSaysTheBundleVariablesFollowTheUsers(t *testing.T) {
	f := newKeychainFixture(t)
	c := &captureDeps{}
	d := c.deps()
	d.ReadFile = func(p string) (string, bool) {
		if p == testProfile+"/"+profileCABundleRel {
			return testRoots, true
		}
		return "", true
	}
	d.ReadSystemKeychain = func() (string, error) { return f.export, nil }
	d.VerifyCA = verifyOnly(f.trusted, f.anyPurpose)
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts := testCaptureOptions()
	opts.SandboxEnv.Set("SSL_CERT_FILE", corpBundle)
	opts.CaptureRoot = filepath.Join(tmp, "captures")
	opts.Darwin = &Darwin{PathPrefix: []string{testProfile + "/bin"}, System: "aarch64-darwin", ProfilePath: testProfile}
	realRun := d.Run
	d.Run = func(argv []string) int {
		rc := realRun(argv)
		if strings.Contains(strings.Join(argv, " "), "capture-run") {
			if err := os.MkdirAll(filepath.Join(opts.CaptureRoot, "probetool", "out", "tree"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return rc
	}
	if rc := RunCaptureAct(d, opts, filepath.Join(tmp, "store", "out"), false); rc != 0 {
		t.Fatalf("RunCaptureAct = %d\n%s", rc, c.out.String())
	}
	for _, want := range []string{"in Node only: Example Corp Inspection CA, Example Corp Any-Purpose CA.",
		"Your configured environment sets SSL_CERT_FILE, so NIX_SSL_CERT_FILE, SSL_CERT_FILE, " +
			"REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE, GIT_SSL_CAINFO name " + corpBundle} {
		if !strings.Contains(c.out.String(), want) {
			t.Errorf("the capture does not say %q:\n%s", want, c.out.String())
		}
	}
	if slices.Contains(c.files, CABundleFile(BuildCapturePlan(opts).Cname, "")) {
		t.Errorf("the capture wrote a CA bundle no variable names")
	}
}
