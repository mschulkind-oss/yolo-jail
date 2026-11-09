package macosuser

// cabundle.go is CORPORATE CA TRUST inside the macos-user sandbox (docs/design/provisioner-sets.md
// §16.3, PS-D10 and PS-D14, built on OQ-PS14 and OQ-PS15 as ruled 2026-10-23: the host's trust,
// the login keychain's included, is the sandbox's).
//
// An employer that inspects TLS traffic has its MDM install its own certificate authority into
// the Mac's System keychain. Go programs, mise and pip ask macOS and see it. Nix curl, nix git,
// nix Python and nix Node read a FILE instead, and the profile's file is Mozilla's public roots
// alone, so behind such a CA every one of them failed TLS in the sandbox.
//
// At each launch the host-side yolo, which runs as the human and outside the sandbox:
//
//  1. reads the System keychain, which is world-readable, and the launching user's own login
//     keychain, with `security find-certificate` and no sudo; it never writes a keychain, and
//     every Seatbelt keychain deny stays;
//  2. keeps each unexpired CA whose extended key usage allows TLS servers, and then only those
//     `security verify-cert -p ssl` confirms macOS trusts (Homebrew's ca-certificates filter);
//  3. writes, per session beside the env file, a bundle of the profile's public roots plus the kept
//     CAs, and a second file of the kept CAs alone;
//  4. points NIX_SSL_CERT_FILE, SSL_CERT_FILE, REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE and
//     GIT_SSL_CAINFO at the bundle, and NODE_EXTRA_CA_CERTS at the second file (Node reads that
//     variable as ONE file it adds to its own roots, so it is never a colon-joined list);
//  5. names every CA it took, and the keychain it came from, at launch (a launch has no quiet mode).
//
// The bundle the launching user's SSL_CERT_FILE names, when they set one, is this Mac's trust too:
// its OpenSSL programs (nix curl, git, Python) read it. Its in-date certificates the profile's roots
// do not already carry are kept after the keychains' CAs, named with the bundle's path
// (ReadSSLCertFileTrust, the same read a container launch on a macOS host makes, PS-D16). A
// bundle that cannot be read warns with the next step and keeps the keychains' CAs.
//
// A System keychain that cannot be read warns, names env_sources, and still points the variables
// at the profile's public roots; a login keychain that cannot be read is one note, and the launch
// trusts the System keychain's CAs and the SSL_CERT_FILE bundle's. None ever refuses a launch.
//
// THE CALLER'S OWN BUNDLE WINS, FOR ALL FIVE (applyCATrust). Each tool reads its own of the five
// first, so a user who set one of them, say SSL_CERT_FILE in env_sources, would have curl, git and
// nix's OpenSSL ignore it if the other four kept a default of ours. When the launch's env layers
// set any of the five, every one they left unset takes that value, and NODE_EXTRA_CA_CERTS, which
// Node adds to its own roots and is none of the five, still names the keychain's CAs, as a default.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/catrust"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const (
	// systemKeychainPath is the Mac's System keychain, where an MDM installs a CA. Reads under
	// /Library/Keychains are denied INSIDE the sandbox (seatbelt.go); the launcher reads it outside.
	systemKeychainPath = "/Library/Keychains/System.keychain"
	// securityBin is Apple's keychain tool, spelled absolutely like every binary here.
	securityBin = "/usr/bin/security"
	// profileCABundleRel is the CA bundle inside the tool profile: nixpkgs' cacert, linked by the
	// profile's buildEnv (flake.nix yoloNoncontainerProfile, every path linked).
	profileCABundleRel = "etc/ssl/certs/ca-bundle.crt"
	// caBundleSuffix and caExtrasSuffix name a session's two CA files beside its env file.
	caBundleSuffix = ".ca-bundle.crt"
	caExtrasSuffix = ".extra-ca.pem"
	// NodeExtraCAVar is the one variable that names the extra CAs alone.
	NodeExtraCAVar = "NODE_EXTRA_CA_CERTS"
)

// systemKeychainExportArgv is the System keychain read a launch makes (readSystemKeychainReal):
// every certificate in it (-a), as PEM (-p), run as the invoking user with no sudo.
func systemKeychainExportArgv() []string {
	return []string{securityBin, "find-certificate", "-a", "-p", systemKeychainPath}
}

// loginKeychainRel is the launching user's login keychain under their home: the file name macOS
// has used since 10.12 (the older `login.keychain` is not read).
const loginKeychainRel = "Library/Keychains/login.keychain-db"

// loginKeychainExportArgv is the login keychain read a launch makes (readLoginKeychainReal): every
// certificate in the keychain at path (-a), as PEM (-p), as the invoking user, whose keychain it
// is. Certificates are not secret items, so the read needs no unlock (UNMEASURED: a Mac session
// confirms it never prompts; the reader's timeout and closed stdin are what keep a launch from
// waiting on one if it does).
func loginKeychainExportArgv(path string) []string {
	return []string{securityBin, "find-certificate", "-a", "-p", path}
}

// verifyCAArgv is the trust question asked of the one CA in the file at path (verifyCAReal):
// under the ssl policy (-p ssl), with the certificate taken as a CA (-l; without it macOS judges
// a CA as the server certificate under test and rejects every one), local certificates only (-L)
// and no network revocation check (-R offline). Homebrew's ca-certificates formula asks the same.
func verifyCAArgv(path string) []string {
	return []string{securityBin, "verify-cert", "-l", "-L", "-R", "offline", "-p", "ssl", "-c", path}
}

// CABundleVars are the variables a launch points at its CA bundle: the four the container's
// bundle sets (generate_ca_bundle), plus NIX_SSL_CERT_FILE, which nix's OpenSSL reads first.
var CABundleVars = []string{"NIX_SSL_CERT_FILE", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"}

// CABundleFile is a session's CA bundle: <stateDir>/env/<key>.ca-bundle.crt.
func CABundleFile(key, sd string) string { return sessionEnvDirFile(key, sd, caBundleSuffix) }

// CAExtrasFile is a session's extra-CA file: <stateDir>/env/<key>.extra-ca.pem.
func CAExtrasFile(key, sd string) string { return sessionEnvDirFile(key, sd, caExtrasSuffix) }

// sessionEnvDirFile is <sd>/env/<key><suffix>, or "" for an empty key.
func sessionEnvDirFile(key, sd, suffix string) string {
	if sd == "" {
		sd = stateDir
	}
	if key == "" {
		return ""
	}
	return sd + "/" + sandboxEnvLeaf + "/" + key + suffix
}

// TrustedCA is one certificate authority taken from a keychain: the name the launch discloses it
// by, its PEM block, and the keychain it came from (catrust.SourceSystemKeychain or
// catrust.SourceLoginKeychain; "" reads as the System keychain).
type TrustedCA = catrust.CA

// CATrust is what one launch composed for TLS trust in the sandbox. The zero value composed
// nothing (no tool profile: a dry run, a capture with no packages), and sets no variable.
type CATrust struct {
	// ProfileBundle is the tool profile's own bundle, Mozilla's public roots. "" means no trust
	// was composed and no variable is set.
	ProfileBundle string
	// MissingProfileBundle is a profile bundle path that could not be read, which also sets no
	// variable: pointing one at a file that is not there would break TLS rather than narrow it.
	MissingProfileBundle string
	// ReadError is why the System keychain was not read, or "" when it was.
	ReadError string
	// LoginRead says the launching user's login keychain was read too, and LoginReadError why it
	// was not when the launch tried (OQ-PS15 as ruled: a CA trusted only there is trusted in the
	// sandbox too). Neither is set when the launch wires no login reader.
	LoginRead      bool
	LoginReadError string
	// SeparateAccount says the sandbox runs as the second account rather than as you (mode.go).
	// There, a program that asks macOS for trust (a Go program, such as gh) asks it as that
	// account, so the launch also has Go 1.27+ programs verify against the bundle (PS-D17,
	// cagodebug.go) and the disclosure names what still asks macOS. Set by the caller, which knows
	// the mode.
	SeparateAccount bool
	// Kept are the CAs the launch trusts beyond the public roots: the System keychain's in its
	// order, then the login keychain's not already among them.
	Kept []TrustedCA
	// LeftOut counts the CAs the keychains held that were left out: expired, not for TLS servers,
	// or not trusted by macOS for TLS.
	LeftOut int
	// HostBundle is the read of the bundle the launching user's SSL_CERT_FILE names (zero when it
	// is unset). Its CAs the profile's roots and the keychains do not already carry are in Kept,
	// after the keychains'; its own left-out count and read error are said by SSLCertFileNotes.
	HostBundle SSLCertFileTrust
	// Bundle is the profile's roots plus Kept, and Extras is Kept alone; both "" when nothing was
	// kept, in which case the variables name ProfileBundle itself and no file is written.
	Bundle string
	Extras string
}

// KeychainReaders are the three host reads a keychain export makes: the System keychain, the
// launching user's login keychain, and macOS's own trust verdict on one CA. Each runs as the
// invoking user with no sudo, and none writes a keychain. A nil System or Login reader is a read
// this build does not make; nil Verify trusts nothing beyond the public roots.
type KeychainReaders struct {
	System func() (string, error)
	Login  func() (string, error)
	Verify func(pem string) bool
}

// RealKeychainReaders are the production reads (`security`, real.go), for a caller outside this
// package: a container launch on a macOS host exports the same trust (PS-D16).
func RealKeychainReaders() KeychainReaders {
	return KeychainReaders{System: readSystemKeychainReal, Login: readLoginKeychainReal, Verify: verifyCAReal}
}

// KeychainTrust is what one export of the host's keychains found.
type KeychainTrust struct {
	// Kept are the CAs macOS trusts for TLS, each labelled with the keychain it came from: the
	// System keychain's first, then the login keychain's not already among them.
	Kept []TrustedCA
	// LeftOut counts the certificates left out: not a TLS CA, outside their validity, or not
	// trusted by macOS for TLS.
	LeftOut int
	// SystemError and LoginError are why a keychain was not read ("" when it was, or when this
	// build wires no login reader); LoginRead says the login keychain was read.
	SystemError, LoginError string
	LoginRead               bool
}

// ReadHostKeychainTrust exports the CAs this Mac trusts for TLS from the System keychain and the
// launching user's login keychain (OQ-PS14 and OQ-PS15 as ruled 2026-10-23: "if it was trusted on
// the host, it should be trusted in the jail, regardless of how that trust got there"). Every
// candidate passes the same filter (catrust.ParseCAs) and then `security verify-cert -p ssl`, which
// runs as the invoking user and so honors the user trust-settings domain as well as the admin and
// system ones: a CA the user marked "Always Trust" passes, one they distrusted does not. A keychain
// that cannot be read is recorded and the other is still read: a failure narrows the trust and
// never refuses (an unreadable login keychain leaves the System keychain's CAs, with a note).
func ReadHostKeychainTrust(r KeychainReaders, now time.Time) KeychainTrust {
	var kt KeychainTrust
	var candidates []TrustedCA
	if r.System == nil {
		kt.SystemError = "this build has no keychain reader wired"
	} else if out, err := r.System(); err != nil {
		kt.SystemError = err.Error()
	} else {
		candidates, kt.LeftOut = catrust.ParseCAs(out, catrust.SourceSystemKeychain, now)
	}
	if r.Login != nil {
		if out, err := r.Login(); err != nil {
			kt.LoginError = err.Error()
		} else {
			kt.LoginRead = true
			c, left := catrust.ParseCAs(out, catrust.SourceLoginKeychain, now)
			kt.LeftOut += left
			candidates = catrust.Merge(candidates, c)
		}
	}
	for _, c := range candidates {
		if r.Verify != nil && r.Verify(c.PEM) {
			kt.Kept = append(kt.Kept, c)
		} else {
			kt.LeftOut++
		}
	}
	return kt
}

// SSLCertFileTrust is what one read of the bundle the launching user's SSL_CERT_FILE names found.
// The zero value is a launch whose user set no SSL_CERT_FILE.
type SSLCertFileTrust struct {
	// Path is the file SSL_CERT_FILE names, "" when it is unset.
	Path string
	// CAs are its in-date certificates, each named with the bundle (catrust.HostBundleSource).
	CAs []TrustedCA
	// LeftOut counts its certificates left out: out of date, or not readable as a certificate.
	LeftOut int
	// Error is why it was not read, or "" when it was.
	Error string
}

// ReadSSLCertFileTrust reads the bundle SSL_CERT_FILE names on this Mac, which its OpenSSL
// programs trust, so a CA trusted there is trusted in the jail beside the keychains' ("regardless
// of how that trust got there", OQ-PS15). With SSL_CERT_FILE unset it reads nothing, and never a
// Linux distribution's path. Every in-date certificate is kept, as on a Linux host: the file IS
// the user's trust decision, so no verify-cert is asked. Both backends on a macOS host call it: a
// container launch (PS-D16) and a macos-user launch (ComposeCATrust).
func ReadSSLCertFileTrust(getenv func(string) string, readFile func(string) ([]byte, error), now time.Time) SSLCertFileTrust {
	if getenv == nil || readFile == nil {
		return SSLCertFileTrust{}
	}
	p := strings.TrimSpace(getenv("SSL_CERT_FILE"))
	if p == "" {
		return SSLCertFileTrust{}
	}
	hb, err := catrust.ReadLinuxHostBundle(func(string) string { return p }, readFile, now)
	if err != nil {
		return SSLCertFileTrust{Path: p, Error: err.Error()}
	}
	return SSLCertFileTrust{Path: hb.Path, CAs: hb.CAs, LeftOut: hb.LeftOut}
}

// SSLCertFileNotes are the lines a launch prints about the bundle SSL_CERT_FILE names: a warning
// with the next step when it could not be read, and how many certificates it left out. place is
// where the trust goes ("the jail", "the sandbox").
func SSLCertFileNotes(st SSLCertFileTrust, place string) []string {
	esc := func(s string) string { return richtext.Escape(termsafe.Visible(s)) }
	switch {
	case st.Error != "":
		return []string{"[yellow]Warning: could not read this host's CA bundle (" + esc(st.Error) +
			")[/yellow], so " + place + " does not trust the certificate authorities in it this launch; the " +
			"ones in this Mac's keychains are still trusted. Point SSL_CERT_FILE at a readable bundle, or unset it, and launch again."}
	case st.LeftOut > 0:
		return []string{fmt.Sprintf("[dim]%d %s in %s %s left out of %s's "+
			"trust: out of date, or not readable as a certificate.[/dim]", st.LeftOut,
			plural(st.LeftOut, "certificate", "certificates"), esc(st.Path), plural(st.LeftOut, "was", "were"), place)}
	}
	return nil
}

// ComposeCATrust composes this launch's trust from the materialized tool profile and the host's
// keychains (ReadHostKeychainTrust). A READ OF THE HOST, so it runs in the orchestrator, never in
// the pure plan builder.
func ComposeCATrust(deps Deps, darwin *Darwin) CATrust {
	if darwin == nil || darwin.ProfilePath == "" {
		return CATrust{}
	}
	profile := strings.TrimRight(darwin.ProfilePath, "/") + "/" + profileCABundleRel
	roots := ""
	if deps.ReadFile != nil {
		if body, ok := deps.ReadFile(profile); ok {
			roots = body
		}
	}
	if strings.TrimSpace(roots) == "" {
		return CATrust{MissingProfileBundle: profile}
	}
	kt := ReadHostKeychainTrust(KeychainReaders{System: deps.ReadSystemKeychain,
		Login: deps.ReadLoginKeychain, Verify: deps.VerifyCA}, time.Now())
	ca := CATrust{ProfileBundle: profile, ReadError: kt.SystemError, LoginRead: kt.LoginRead,
		LoginReadError: kt.LoginError, Kept: kt.Kept, LeftOut: kt.LeftOut}
	// THE BUNDLE SSL_CERT_FILE NAMES, after the keychains' CAs, less what the profile's roots or the
	// keychains already carry: a bundle that is mostly Mozilla's roots adds, and names, only the rest.
	var readFile func(string) ([]byte, error)
	if deps.ReadFile != nil {
		readFile = func(p string) ([]byte, error) {
			if body, ok := deps.ReadFile(p); ok && body != "" {
				return []byte(body), nil
			}
			return nil, fmt.Errorf("cannot read %s", p)
		}
	}
	ca.HostBundle = ReadSSLCertFileTrust(deps.Getenv, readFile, time.Now())
	if len(ca.HostBundle.CAs) > 0 {
		base, _ := catrust.ParseBundle(roots, "", time.Now())
		base = append(base, ca.Kept...)
		ca.Kept = append(ca.Kept, catrust.Merge(base, ca.HostBundle.CAs)[len(base):]...)
	}
	if len(ca.Kept) == 0 {
		return ca
	}
	var extras strings.Builder
	for _, c := range ca.Kept {
		extras.WriteString(c.PEM)
	}
	ca.Extras = extras.String()
	ca.Bundle = strings.TrimRight(roots, "\n") + "\n" + ca.Extras
	return ca
}

// applyCATrust sets the CA variables on a copy of env and returns the copy, with the session's CA
// files that a variable names (each "" when none does, so nothing writes it) and, when the
// caller's layers set a bundle variable, which one the others follow. env itself is untouched.
//
// ONE BUNDLE FOR THE FIVE, the caller's when they named one. Each tool reads its own of the five
// first: nix's OpenSSL NIX_SSL_CERT_FILE and only then SSL_CERT_FILE, curl CURL_CA_BUNDLE, git
// GIT_SSL_CAINFO, requests REQUESTS_CA_BUNDLE and then CURL_CA_BUNDLE. So a default of ours under a
// user's single variable is not a default but what those tools read instead of it. Measured
// 2026-10-05 against a private CA on loopback, with nix curl 8.22 (OpenSSL 3.6.4), git 2.55 and
// `openssl s_client`: SSL_CERT_FILE alone, naming the CA, verifies in all three; the same with
// the other four naming the public roots fails verification in all three. So when the caller's
// layers set any of the five to a non-empty value, every one they left unset takes the FIRST such
// value in CABundleVars order (the order nix's OpenSSL reads them), a variable they set keeps its
// own, and the session's bundle is not written, since nothing would name it. An empty value names
// no bundle, so it is kept for its own variable and not followed. NODE_EXTRA_CA_CERTS is not one
// of the five: it ADDS one file to the roots Node starts from (nix Node's are OpenSSL's, so the
// five; a downloaded Node's are its own), so it still names the extras, as a default under any
// value the caller set.
func applyCATrust(env *jsonx.OrderedMap, ca CATrust, key string) (out *jsonx.OrderedMap, bundleFile, extrasFile, follows string) {
	if ca.ProfileBundle == "" {
		return env, "", "", ""
	}
	target := ca.ProfileBundle
	if ca.Bundle != "" {
		bundleFile, extrasFile = CABundleFile(key, ""), CAExtrasFile(key, "")
		target = bundleFile
	}
	if k, v, ok := callerCABundle(env); ok {
		follows, target, bundleFile = k, v, ""
	}
	out = env
	for _, k := range CABundleVars {
		out = withEnvDefault(out, k, target)
	}
	if extrasFile != "" {
		if envSets(env, NodeExtraCAVar) {
			extrasFile = ""
		} else {
			out = withEnvVar(out, NodeExtraCAVar, extrasFile)
		}
	}
	// THE SEPARATE ACCOUNT'S GO PROGRAMS verify against the same bundle (cagodebug.go, PS-D17).
	out = withGoCAFileTrust(out, ca, bundleFile)
	return out, bundleFile, extrasFile, follows
}

// callerCABundle is the first of CABundleVars that env sets to a non-empty value, with that value.
func callerCABundle(env *jsonx.OrderedMap) (key, value string, ok bool) {
	if env == nil {
		return "", "", false
	}
	for _, k := range CABundleVars {
		if v, set := env.Get(k); set && asStr(v) != "" {
			return k, asStr(v), true
		}
	}
	return "", "", false
}

// envSets reports whether env sets key at all.
func envSets(env *jsonx.OrderedMap, key string) bool {
	if env == nil {
		return false
	}
	_, set := env.Get(key)
	return set
}

// caTrustContents is what to write into each of a plan's CA files: the composed bundle into the
// bundle file and the kept CAs alone into the extras file, each "" when its file is not written.
func caTrustContents(ca CATrust, bundleFile, extrasFile string) (bundle, extras string) {
	if bundleFile != "" {
		bundle = ca.Bundle
	}
	if extrasFile != "" {
		extras = ca.Extras
	}
	return bundle, extras
}

// withEnvDefault is withEnvVar for a key the caller's layers have not set; a set key is left as
// it is, and env is returned unchanged.
func withEnvDefault(env *jsonx.OrderedMap, key, value string) *jsonx.OrderedMap {
	if envSets(env, key) {
		return env
	}
	return withEnvVar(env, key, value)
}

// caTrustFiles are a plan's two CA files as installable files: the directory commands only when
// the env file did not already prepare the same directory, then each file's read ACE after it is
// written (installSessionFiles' order).
func caTrustFiles(envFile, bundleFile, bundle, extrasFile, extras string) []sessionFilePlan {
	var out []sessionFilePlan
	for _, f := range [][2]string{{bundleFile, bundle}, {extrasFile, extras}} {
		if f[0] == "" {
			continue
		}
		dir := SandboxEnvDirCommands(f[0], "")
		if (envFile != "" && pathParent(envFile) == pathParent(f[0])) || len(out) > 0 {
			dir = nil
		}
		out = append(out, sessionFilePlan{path: f[0], content: f[1], dir: dir,
			grant: SandboxEnvGrantCommands(f[0], "")})
	}
	return out
}

// installCATrustFiles writes a session's CA files as the host user, 0600, each with the sandbox
// account's read ACE, and runs no sudo: the env file's installer, with messages of its own. A failure refuses the
// launch, as the env file's does: its variables already name these files, and a bundle that is
// not there, or that the sandbox cannot read, breaks every TLS client in the sandbox rather than
// narrowing it. Each refusal names the next step (caTrustRetry).
func installCATrustFiles(deps Deps, out printer, files []sessionFilePlan) bool {
	for _, f := range files {
		for _, cmd := range f.dir {
			if deps.Run(cmd) != 0 {
				out.printf("[bold red]Could not prepare the session environment directory "+
					"(%s).[/bold red] %s", strings.Join(cmd, " "), caTrustRetry)
				return false
			}
		}
		if !deps.WriteHostFile(f.path, f.content, 0o600) {
			out.printf("[bold red]Could not write the session's CA bundle %s.[/bold red] %s",
				f.path, caTrustRetry)
			return false
		}
		for _, cmd := range f.grant {
			if deps.Run(cmd) != 0 {
				out.printf("[bold red]Could not grant %s read on %s (%s).[/bold red] %s",
					SandboxUser, f.path, strings.Join(cmd, " "), caTrustRetry)
				return false
			}
		}
	}
	return true
}

// caTrustRetry is the next step every refusal of installCATrustFiles names.
const caTrustRetry = "The sandbox's TLS variables name this file, so the launch stops here; run it " +
	"again, and if this repeats, `yolo run --dry-run` prints every command it runs."

// printCATrust is the launch's disclosure of what it trusts for TLS in the sandbox, read off the
// plan, so the variables it names are the ones the env file really sets. follows is the bundle
// variable the launch's own env layers set (applyCATrust), which the other four then name too:
// the line says so, and that the keychain's CAs then reach Node alone, or nothing. Each CA is
// named with the keychain it came from (catrust.SourcesPhrase).
func printCATrust(out printer, ca CATrust, envContent, bundleFile, extrasFile, follows string) {
	if ca.MissingProfileBundle != "" {
		out.printf("[yellow]Warning: the sandbox's tool profile has no CA bundle at %s[/yellow], "+
			"so no TLS variable is set and each tool in the sandbox uses its own default roots. "+
			"This is a yolo bug rather than a config error; `yolo run --dry-run` prints the plan.",
			ca.MissingProfileBundle)
		return
	}
	if ca.ProfileBundle == "" {
		return
	}
	esc := func(s string) string { return richtext.Escape(termsafe.Visible(s)) }
	n := len(ca.Kept)
	// A KEYCHAIN THAT COULD NOT BE READ, said on a line of its own whenever another source still
	// added a CA, so the main line below stays true: the login keychain's note always, and the
	// System keychain's warning when the login keychain's CAs are what remain.
	if ca.LoginReadError != "" {
		out.printf("[yellow]Note: could not read your login keychain (%s)[/yellow], so a certificate "+
			"authority you trusted only there is not trusted in the sandbox this launch; the ones in "+
			"this Mac's System keychain are still trusted. If the keychain is locked, unlock it (`security "+
			"unlock-keychain`) and launch again.", esc(ca.LoginReadError))
	}
	// THE BUNDLE SSL_CERT_FILE NAMES: a warning when it could not be read, and what it left out,
	// worded as the container launch words them (SSLCertFileNotes).
	for _, line := range SSLCertFileNotes(ca.HostBundle, "the sandbox") {
		out.print(line)
	}
	bundleRead := ca.HostBundle.Path != "" && ca.HostBundle.Error == ""
	if ca.ReadError != "" && n > 0 {
		rest := "only your login keychain's certificate authorities"
		if bundleRead {
			rest = "none of its certificate authorities"
		}
		out.printf("[yellow]Warning: could not read this Mac's System keychain (%s)[/yellow], so "+
			"the sandbox adds %s.", esc(ca.ReadError), rest)
	}
	kept := make([]TrustedCA, 0, n)
	for _, c := range ca.Kept {
		if c.Source == "" {
			c.Source = catrust.SourceSystemKeychain
		}
		kept = append(kept, c)
	}
	names := make([]string, 0, n)
	var fromLogin []string
	for _, c := range kept {
		names = append(names, esc(c.Name))
		if c.Source == catrust.SourceLoginKeychain {
			fromLogin = append(fromLogin, esc(c.Name))
		}
	}
	// THE SEPARATE ACCOUNT'S GO PROGRAMS, after the main line (PS-D17, cagodebug.go): the GODEBUG
	// setting that has a Go 1.27+ program verify against the bundle, and what still asks macOS as
	// the sandbox account. A caller's own bundle keeps PS-D14's line: the keychains' CAs are in no
	// bundle of ours then.
	switch state := goCAFileStateOf(ca, envContent, bundleFile, follows); {
	case state != goCAFileNotWanted:
		if line := goCAFileLine(state, n, fromLogin); line != "" {
			defer out.print(line)
		}
	case ca.SeparateAccount && len(fromLogin) > 0:
		defer out.printf("[dim]The agent runs as a separate account, so the %s from your login "+
			"keychain (%s) %s trusted only by tools that read a CA file, such as curl, git, Node and "+
			"Python; programs that ask macOS instead, such as gh, do not trust %s.[/dim]",
			plural(len(fromLogin), "certificate authority", "certificate authorities"),
			strings.Join(fromLogin, ", "), plural(len(fromLogin), "is", "are"),
			plural(len(fromLogin), "it", "them"))
	}
	// What was read, as the subject of "adds no …" and "trusts …".
	// A keychain that could not be read is no part of the subject: the warning above says so,
	// and crediting it here would say the opposite.
	var read []string
	if ca.ReadError == "" {
		read = append(read, "this Mac's System keychain")
	}
	if ca.LoginRead {
		read = append(read, "your login keychain")
	}
	keychains := len(read)
	if bundleRead {
		// The bundle joins the subject; the left-out count below stays the keychains' own.
		read = append(read, "the bundle SSL_CERT_FILE names ("+esc(ca.HostBundle.Path)+")")
	}
	holder := "This Mac's System keychain"
	switch len(read) {
	case 0:
	case 1:
		holder = read[0]
	case 2:
		holder = read[0] + " and " + read[1]
	default:
		holder = strings.Join(read[:len(read)-1], ", ") + " and " + read[len(read)-1]
	}
	holder = strings.ToUpper(holder[:1]) + holder[1:]
	verb, trusts := "adds", "trusts"
	if len(read) > 1 {
		verb, trusts = "add", "trust"
	}
	// The keychains alone hold the left-out count, so they are named, never a pronoun that the
	// bundle could seem to share.
	kcHolder := "this Mac's keychains"
	switch keychains {
	case 1:
		kcHolder = read[0]
	case 2:
		kcHolder = read[0] + " and " + read[1]
	}
	left := ""
	if ca.LeftOut > 0 {
		more := ""
		if n > 0 {
			more = " more"
		}
		left = fmt.Sprintf(" %d%s %s in %s %s left out: expired, not for TLS servers, or not "+
			"trusted by macOS for TLS.", ca.LeftOut, more,
			plural(ca.LeftOut, "certificate authority", "certificate authorities"), kcHolder,
			plural(ca.LeftOut, "was", "were"))
	}
	authorities := fmt.Sprintf("%d certificate %s", n, plural(n, "authority", "authorities"))
	node := ""
	if v, ok := sandboxEnvFileValue(envContent, NodeExtraCAVar); ok && v == extrasFile && extrasFile != "" {
		node = " " + NodeExtraCAVar + " names " + extrasFile + ", which holds only these."
	}
	if follows != "" {
		// THE CALLER'S BUNDLE, which every one of the five names that they did not set otherwise.
		yours := func(lead string) string {
			return lead + " configured environment sets " + follows + ", so " + caVarsClause(envContent) + "."
		}
		switch {
		case ca.ReadError != "" && n == 0:
			out.printf("[yellow]Warning: could not read this Mac's System keychain (%s)[/yellow], so "+
				"the sandbox adds none of its certificate authorities. %s",
				esc(ca.ReadError), yours("Your"))
		case n == 0:
			out.printf("[dim]TLS in the sandbox: %s %s %s no certificate "+
				"authority.%s[/dim]", yours("your"), holder, verb, left)
		case node != "":
			out.printf("Trusting %s%s.%s %s Every other tool trusts only what those variables name.%s",
				authorities, catrust.SourcesPhrase(kept, " in Node only", esc), node, yours("Your"), left)
		default:
			out.printf("%s %s %s for TLS (%s), and the sandbox does not add %s. %s %s keeps your "+
				"own value too.%s", holder, trusts, authorities,
				strings.Join(names, ", "), plural(n, "it", "them"), yours("Your"), NodeExtraCAVar, left)
		}
		return
	}
	target := ca.ProfileBundle
	if bundleFile != "" {
		target = bundleFile
	}
	var ours, theirs []string
	for _, k := range CABundleVars {
		if v, ok := sandboxEnvFileValue(envContent, k); ok && v == target {
			ours = append(ours, k)
		} else {
			theirs = append(theirs, k)
		}
	}
	// vars is the subject of a sentence, with its verb: "SSL_CERT_FILE names", "A, B name", or
	// "no variable names"; Vars is the same at a sentence's start.
	vars := strings.Join(ours, ", ") + " " + plural(len(ours), "names", "name")
	if len(ours) == 0 {
		vars = "no variable names"
	}
	Vars := strings.ToUpper(vars[:1]) + vars[1:]
	tail := ""
	if len(theirs) > 0 {
		tail = " " + strings.Join(theirs, ", ") + " " + plural(len(theirs), "keeps", "keep") +
			" the empty value your own environment set."
	}
	switch {
	case ca.ReadError != "" && n == 0:
		out.printf("[yellow]Warning: could not read this Mac's System keychain (%s)[/yellow], so "+
			"the sandbox trusts only the public roots in its tool profile: %s %s.%s A "+
			"certificate authority your network needs can be named: put a bundle holding it and the "+
			"public roots in the workspace, run `yolo config set env.set.SSL_CERT_FILE <that file>`, "+
			"which the other four follow, and `yolo config set env.set.%s <that file>` for Node.",
			esc(ca.ReadError), vars, target, tail, NodeExtraCAVar)
	case n == 0:
		out.printf("[dim]TLS in the sandbox: %s the public roots in its tool profile (%s); "+
			"%s %s no certificate authority.%s%s[/dim]",
			vars, target, strings.ToLower(holder[:1])+holder[1:], verb, left, tail)
	default:
		if node == "" {
			node = " " + NodeExtraCAVar + " keeps your own value, so Node does not add " +
				plural(n, "it", "them") + "."
		}
		out.printf("Trusting %s%s. %s %s, the tool "+
			"profile's public roots plus %s.%s%s%s", authorities, catrust.SourcesPhrase(kept, "", esc), Vars,
			target, plural(n, "it", "them"), node, left, tail)
	}
}

// caVarsClause says what the five bundle variables name in a rendered env file, grouped by value
// in CABundleVars order: "NIX_SSL_CERT_FILE, SSL_CERT_FILE name /a; GIT_SSL_CAINFO names /b". A
// value is the launch's env layers', so it is printed as text.
func caVarsClause(envContent string) string {
	var values []string
	byValue := map[string][]string{}
	for _, k := range CABundleVars {
		v, _ := sandboxEnvFileValue(envContent, k)
		if _, seen := byValue[v]; !seen {
			values = append(values, v)
		}
		byValue[v] = append(byValue[v], k)
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		ks := byValue[v]
		parts = append(parts, strings.Join(ks, ", ")+" "+plural(len(ks), "names", "name")+" "+
			richtext.Escape(termsafe.Visible(v)))
	}
	return strings.Join(parts, "; ")
}

// plural picks the word for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
