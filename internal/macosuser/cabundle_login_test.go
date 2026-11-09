package macosuser

// cabundle_login_test.go pins OQ-PS15 as ruled 2026-10-23 (PS-D14): a CA the launching user trusted
// only in their own login keychain is trusted in the sandbox too, in both modes, and named at
// launch as coming from it; an unreadable login keychain degrades to the System keychain's CAs with
// a note, never a refusal. And the run-as-you delivery (PS-D15): the CA files are written as you,
// with no sudo and no access entry, the profile reads each by its exact path and still reaches
// trustd's agent, and every keychain deny stays.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"crypto/x509"

	"github.com/mschulkind-oss/yolo-jail/internal/catrust"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// loginFixture is a login keychain export: a CA only the user trusts, the System keychain's trusted
// CA again (named by the System keychain, which holds it first), and a CA macOS does not trust.
type loginFixture struct {
	userOnly, untrusted, export string
}

func newLoginFixture(t *testing.T, sys keychainFixture) loginFixture {
	t.Helper()
	now := time.Now()
	f := loginFixture{
		userOnly:  testCAPEM(t, "Staging CA", true, now.Add(-time.Hour), now.Add(24*time.Hour), x509.ExtKeyUsageServerAuth),
		untrusted: testCAPEM(t, "Distrusted CA", true, now.Add(-time.Hour), now.Add(24*time.Hour)),
	}
	f.export = f.userOnly + sys.trusted + f.untrusted
	return f
}

// THE LOGIN KEYCHAIN'S CAs, filtered and verified as the System keychain's are, labelled with their
// source, and a certificate both keychains hold is named once, by the System keychain.
func TestReadHostKeychainTrustAddsTheLoginKeychain(t *testing.T) {
	sys := newKeychainFixture(t)
	login := newLoginFixture(t, sys)
	var asked []string
	kt := ReadHostKeychainTrust(KeychainReaders{
		System: func() (string, error) { return sys.export, nil },
		Login:  func() (string, error) { return login.export, nil },
		Verify: func(p string) bool {
			asked = append(asked, p)
			return verifyOnly(sys.trusted, sys.anyPurpose, login.userOnly)(p)
		},
	}, time.Now())
	if !kt.LoginRead || kt.LoginError != "" || kt.SystemError != "" {
		t.Fatalf("reads: %+v", kt)
	}
	var got []string
	for _, c := range kt.Kept {
		got = append(got, c.Name+"|"+c.Source)
	}
	want := []string{
		"Example Corp Inspection CA|" + catrust.SourceSystemKeychain,
		"Example Corp Any-Purpose CA|" + catrust.SourceSystemKeychain,
		"Staging CA|" + catrust.SourceLoginKeychain,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("kept:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// System: 4 filtered + 1 rejected by verify; login: the distrusted CA rejected by verify.
	if kt.LeftOut != 6 {
		t.Errorf("LeftOut = %d, want 6", kt.LeftOut)
	}
	if len(asked) != 5 {
		t.Errorf("verify-cert asked about %d certificates, want 5 (the duplicate is asked once)", len(asked))
	}
}

// AN UNREADABLE LOGIN KEYCHAIN NARROWS TO THE SYSTEM KEYCHAIN'S CAs; an unreadable System keychain
// still reads the login keychain.
func TestAKeychainThatCannotBeReadNarrowsTheTrust(t *testing.T) {
	sys := newKeychainFixture(t)
	login := newLoginFixture(t, sys)
	verify := verifyOnly(sys.trusted, sys.anyPurpose, login.userOnly)
	kt := ReadHostKeychainTrust(KeychainReaders{
		System: func() (string, error) { return sys.export, nil },
		Login:  func() (string, error) { return "", errors.New("SecKeychainOpen: locked") },
		Verify: verify,
	}, time.Now())
	if kt.LoginRead || kt.LoginError == "" || len(kt.Kept) != 2 {
		t.Errorf("an unreadable login keychain: %+v", kt)
	}
	kt = ReadHostKeychainTrust(KeychainReaders{
		System: func() (string, error) { return "", errors.New("boom") },
		Login:  func() (string, error) { return login.export, nil },
		Verify: verify,
	}, time.Now())
	if kt.SystemError != "boom" || len(kt.Kept) != 2 || kt.Kept[0].Name != "Staging CA" {
		t.Errorf("an unreadable System keychain: %+v", kt)
	}
}

func TestTheLoginKeychainReadIsTheUsersOwn(t *testing.T) {
	if got, want := strings.Join(loginKeychainExportArgv("/Users/you/"+loginKeychainRel), " "),
		"/usr/bin/security find-certificate -a -p /Users/you/Library/Keychains/login.keychain-db"; got != want {
		t.Errorf("the login keychain read is %q, want %q", got, want)
	}
}

// loginCADeps is caDeps with a login keychain beside the System keychain.
func loginCADeps(t *testing.T, rec *[]string, sys keychainFixture, login loginFixture, contents map[string]string) Deps {
	d := caDeps(t, rec, sys)
	d.ReadLoginKeychain = func() (string, error) { return login.export, nil }
	d.VerifyCA = verifyOnly(sys.trusted, sys.anyPurpose, login.userOnly)
	d.WriteHostFile = func(path, content string, mode os.FileMode) bool {
		*rec = append(*rec, fmt.Sprintf("write:%s %#o", path, mode))
		contents[path] = content
		return true
	}
	return d
}

// THE LAUNCH'S CALL SITE, in the account mode: the login keychain's CA reaches the bundle and the
// extras, and the launch names each CA with its source. Fails if ComposeCATrust stops passing the
// login reader, or the disclosure stops naming sources.
func TestALaunchTrustsAndNamesTheLoginKeychainsCA(t *testing.T) {
	sys := newKeychainFixture(t)
	login := newLoginFixture(t, sys)
	var rec []string
	contents := map[string]string{}
	d := loginCADeps(t, &rec, sys, login, contents)
	var out bytes.Buffer
	d.Out = &out
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	var extras string
	for p, c := range contents {
		if strings.HasSuffix(p, caExtrasSuffix) {
			extras = c
		}
	}
	if extras != sys.kept()+login.userOnly {
		t.Errorf("the extras file is not the System keychain's CAs then the login keychain's:\n%s", extras)
	}
	want := "Trusting 3 certificate authorities, 2 from this Mac's System keychain (Example Corp " +
		"Inspection CA, Example Corp Any-Purpose CA) and 1 from your login keychain (Staging CA)."
	if !strings.Contains(out.String(), want) {
		t.Errorf("the launch does not say %q:\n%s", want, out.String())
	}
	if !strings.Contains(out.String(), "6 more certificate authorities in this Mac's System keychain and your login keychain were left out") {
		t.Errorf("the left-out count does not cover both keychains:\n%s", out.String())
	}
	// THE SEPARATE ACCOUNT'S GO PROGRAMS (PS-D17, cagodebug.go): told to verify against the bundle,
	// and what still asks macOS as the sandbox account named with the check.
	limit := "One built with an older Go still asks macOS, as that account, and does not trust the " +
		"one from your login keychain (Staging CA)"
	if !strings.Contains(out.String(), limit) {
		t.Errorf("the account-mode launch does not qualify the login keychain's CA:\n%s", out.String())
	}
}

// AN UNREADABLE LOGIN KEYCHAIN IS ONE NOTE WITH A NEXT STEP, and the launch goes on with the System
// keychain's CAs.
func TestALaunchWithALockedLoginKeychainNotesItAndGoesOn(t *testing.T) {
	sys := newKeychainFixture(t)
	login := newLoginFixture(t, sys)
	var rec []string
	contents := map[string]string{}
	d := loginCADeps(t, &rec, sys, login, contents)
	d.ReadLoginKeychain = func() (string, error) { return "", errors.New("no answer within 15s") }
	var out bytes.Buffer
	d.Out = &out
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("a locked login keychain refused the launch: rc = %d\n%s", rc, out.String())
	}
	for _, want := range []string{"could not read your login keychain (no answer within 15s)",
		"security unlock-keychain",
		"Trusting 2 certificate authorities from this Mac's System keychain: Example Corp Inspection CA"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the launch does not say %q:\n%s", want, out.String())
		}
	}
}

// THE TLS TRUST LINES ARE NOTICES, printed on Deps.Notices (the launch's stderr) and never on Out:
// Out is the launch's stdout, which the command's own output shares, and on a Mac these lines print
// on nearly every launch. The container launch moved the same keychain notes to stderr for the same
// reason (internal/cli/run TestHostTrustNotesNeverReachTheCommandsStdout).
func TestALaunchPrintsItsTLSTrustOnTheNoticeStreamNotStdout(t *testing.T) {
	sys := newKeychainFixture(t)
	login := newLoginFixture(t, sys)
	var rec []string
	d := loginCADeps(t, &rec, sys, login, map[string]string{})
	d.ReadLoginKeychain = func() (string, error) { return "", errors.New("no login keychain at /h/x") }
	var out, notices bytes.Buffer
	d.Out, d.Notices = &out, &notices
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d\nout:\n%s\nnotices:\n%s", rc, out.String(), notices.String())
	}
	for _, line := range []string{"could not read your login keychain", "Trusting 2 certificate authorities"} {
		if !strings.Contains(notices.String(), line) {
			t.Errorf("the notice stream lacks %q:\n%s", line, notices.String())
		}
		if strings.Contains(out.String(), line) {
			t.Errorf("%q reached Out, the launch's stdout:\n%s", line, out.String())
		}
	}
}

// THE RUN-AS-YOU DELIVERY: the launch writes both CA files as you, with no sudo and no access entry,
// and they hold the login keychain's CA too. Fails if the self branch of caTrustFilePlans stops
// stripping the grants, or the self launch stops composing the trust.
func TestASelfModeLaunchWritesTheCAFilesAsYouWithNoGrant(t *testing.T) {
	sys := newKeychainFixture(t)
	login := newLoginFixture(t, sys)
	var rec []string
	contents := map[string]string{}
	d := loginCADeps(t, &rec, sys, login, contents)
	d.SandboxUserExists = func() bool { return false }
	d.RunAsGrant = func() bool { return false }
	d.PathIsDir = func(p string) bool { return p != SandboxHome() }
	var out bytes.Buffer
	d.Out = &out
	if rc := RunMacosUser(d, selfModeOpts("/Users/you/code/alpha")); rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wrote := 0
	for _, r := range rec {
		if strings.Contains(r, sudoBin) || strings.Contains(r, "chmod +a") {
			t.Errorf("the run-as-you launch ran %s", r)
		}
		if strings.HasPrefix(r, "write:") && (strings.Contains(r, caBundleSuffix) || strings.Contains(r, caExtrasSuffix)) {
			wrote++
			if !strings.HasSuffix(r, " 0600") {
				t.Errorf("a CA file is not written 0600: %s", r)
			}
		}
	}
	if wrote != 2 {
		t.Errorf("the run-as-you launch wrote %d CA files, want 2:\n%s", wrote, strings.Join(rec, "\n"))
	}
	if strings.Contains(out.String(), "separate account") {
		t.Errorf("the run-as-you launch says the separate account's limit, which it does not have:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1 from your login keychain (Staging CA)") {
		t.Errorf("the run-as-you launch does not name the login keychain's CA:\n%s", out.String())
	}
}

// THE RUN-AS-YOU PROFILE reads each CA file by its exact path, still reaches trustd's per-user
// agent (and not its system daemon), and keeps every keychain deny: the System and login keychain files and securityd.
func TestTheSelfModeProfileReadsTheCAFilesAndReachesTrustd(t *testing.T) {
	ca := CATrust{ProfileBundle: "/p/bundle", Kept: []TrustedCA{{Name: "Corp CA"}}, Bundle: "x", Extras: "x"}
	plan := BuildRunPlanWithDaemons("/Users/you/code/alpha", jsonx.NewOrderedMap(), nil, []string{"claude"},
		"/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), nil, nil,
		selfModeWireBridgeDaemons("/opt/yolo/bin/darwin-arm64"), FloorStage{},
		PlanSession{Mode: ModeSelf, HostHome: "/Users/you", HostUser: "you",
			DarwinUserTempDir: "/private/var/folders/ab/xyz/T/", CATrust: ca})
	if plan.CABundleFile == "" || plan.CAExtrasFile == "" {
		t.Fatalf("the fixture plan names no CA files: %q %q", plan.CABundleFile, plan.CAExtrasFile)
	}
	block := profileBlock(plan.Seatbelt, "staging-root-read-allow")
	for _, f := range []string{plan.CABundleFile, plan.CAExtrasFile} {
		if !strings.Contains(block, "(subpath "+sbplStr(f)+")") {
			t.Errorf("the self-mode profile does not read %s:\n%s", f, block)
		}
	}
	lookups := profileBlock(plan.Seatbelt, "mach-lookup-allow")
	if !strings.Contains(lookups, `(global-name "com.apple.trustd.agent")`) {
		t.Errorf("the self-mode profile does not reach trustd's agent:\n%s", lookups)
	}
	// NOT trustd's system daemon, a root process, until a Mac shows a Go client failing without it
	// (PS-D15; the runbook's trust check).
	if strings.Contains(lookups, `(global-name "com.apple.trustd")`) {
		t.Errorf("the self-mode profile reaches com.apple.trustd, which no Mac run has shown a need for:\n%s", lookups)
	}
	for _, deny := range []string{
		`(deny file-read* file-write* (subpath "/Library/Keychains"))`,
		`(subpath "/Users/you/Library/Keychains")`,
		`(global-name "com.apple.SecurityServer")`,
		`(global-name "com.apple.securityd")`,
	} {
		if !strings.Contains(plan.Seatbelt, deny) {
			t.Errorf("the self-mode profile lost %s", deny)
		}
	}
	for _, f := range plan.caTrustFilePlans() {
		if len(f.grant) != 0 {
			t.Errorf("a run-as-you CA file carries a grant: %v", f.grant)
		}
	}
	if problems := PlanInvariants(plan); len(problems) != 0 {
		t.Errorf("the self-mode plan with CA files has problems:\n  %s", strings.Join(problems, "\n  "))
	}
}
