package macosuser

// cabundle.go is CORPORATE CA TRUST inside the macos-user sandbox (docs/design/provisioner-sets.md
// §16.3 and PS-D10, built on OQ-PS14 (a) and OQ-PS15 (a) as leaned).
//
// An employer that inspects TLS traffic has its MDM install its own certificate authority into
// the Mac's System keychain. Go programs, mise and pip ask macOS and see it. Nix curl, nix git,
// nix Python and nix Node read a FILE instead, and the profile's file is Mozilla's public roots
// alone, so behind such a CA every one of them failed TLS in the sandbox.
//
// At each launch the host-side yolo, which runs as the human and outside the sandbox:
//
//  1. reads the System keychain, which is world-readable, with `security find-certificate` and no
//     sudo; it never writes a keychain, and both Seatbelt keychain denies stay;
//  2. keeps each unexpired CA whose extended key usage allows TLS servers, and then only those
//     `security verify-cert -p ssl` confirms macOS trusts (Homebrew's ca-certificates filter);
//  3. writes, per session beside the env file, a bundle of the profile's public roots plus the kept
//     CAs, and a second file of the kept CAs alone;
//  4. points NIX_SSL_CERT_FILE, SSL_CERT_FILE, REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE and
//     GIT_SSL_CAINFO at the bundle, and NODE_EXTRA_CA_CERTS at the second file (Node reads that
//     variable as ONE file it adds to its own roots, so it is never a colon-joined list);
//  5. names every CA it took, at launch (a launch has no quiet mode).
//
// A keychain that cannot be read warns, names env_sources, and still points the variables at the
// profile's public roots: it never refuses a launch.
//
// THE CALLER'S OWN BUNDLE WINS, FOR ALL FIVE (applyCATrust). Each tool reads its own of the five
// first, so a user who set one of them, say SSL_CERT_FILE in env_sources, would have curl, git and
// nix's OpenSSL ignore it if the other four kept a default of ours. When the launch's env layers
// set any of the five, every one they left unset takes that value, and NODE_EXTRA_CA_CERTS, which
// Node adds to its own roots and is none of the five, still names the keychain's CAs, as a default.

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"slices"
	"strings"
	"time"

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

// TrustedCA is one certificate authority taken from the System keychain: the name the launch
// discloses it by, and its PEM block.
type TrustedCA struct {
	Name string
	PEM  string
}

// CATrust is what one launch composed for TLS trust in the sandbox. The zero value composed
// nothing (no tool profile: a dry run, a capture with no packages), and sets no variable.
type CATrust struct {
	// ProfileBundle is the tool profile's own bundle, Mozilla's public roots. "" means no trust
	// was composed and no variable is set.
	ProfileBundle string
	// MissingProfileBundle is a profile bundle path that could not be read, which also sets no
	// variable: pointing one at a file that is not there would break TLS rather than narrow it.
	MissingProfileBundle string
	// ReadError is why the keychain was not read, or "" when it was.
	ReadError string
	// Kept are the CAs the launch trusts beyond the public roots, in keychain order.
	Kept []TrustedCA
	// LeftOut counts the CAs the keychain held that were left out: expired, not for TLS servers,
	// or not trusted by macOS for TLS.
	LeftOut int
	// Bundle is the profile's roots plus Kept, and Extras is Kept alone; both "" when nothing was
	// kept, in which case the variables name ProfileBundle itself and no file is written.
	Bundle string
	Extras string
}

// ComposeCATrust composes this launch's trust from the materialized tool profile and the System
// keychain. A READ OF THE HOST, so it runs in the orchestrator, never in the pure plan builder.
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
	ca := CATrust{ProfileBundle: profile}
	if deps.ReadSystemKeychain == nil {
		ca.ReadError = "this build has no keychain reader wired"
		return ca
	}
	out, err := deps.ReadSystemKeychain()
	if err != nil {
		ca.ReadError = err.Error()
		return ca
	}
	candidates, left := keychainCACandidates(out, time.Now())
	for _, c := range candidates {
		if deps.VerifyCA != nil && deps.VerifyCA(c.PEM) {
			ca.Kept = append(ca.Kept, c)
		} else {
			left++
		}
	}
	ca.LeftOut = left
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

// keychainCACandidates parses `security find-certificate -a -p` output and returns the
// certificates that can be TLS trust anchors, each once, with how many it left out: a block that
// does not parse, a certificate that is not a CA, one outside its validity at now, and one whose
// extended key usage names neither TLS servers nor any purpose. What macOS's own trust settings
// say is asked afterwards, one certificate at a time (Deps.VerifyCA).
func keychainCACandidates(pemOut string, now time.Time) (kept []TrustedCA, left int) {
	rest := []byte(pemOut)
	var seen [][]byte
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return kept, left
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || now.Before(cert.NotBefore) || now.After(cert.NotAfter) ||
			!allowsServerAuth(cert) {
			left++
			continue
		}
		if slices.ContainsFunc(seen, func(der []byte) bool { return bytes.Equal(der, cert.Raw) }) {
			continue
		}
		seen = append(seen, cert.Raw)
		kept = append(kept, TrustedCA{Name: caName(cert), PEM: string(pem.EncodeToMemory(block))})
	}
}

// allowsServerAuth reports whether a CA's extended key usage permits TLS server certificates: no
// EKU at all (unconstrained), ServerAuth, or Any.
func allowsServerAuth(c *x509.Certificate) bool {
	if len(c.ExtKeyUsage) == 0 && len(c.UnknownExtKeyUsage) == 0 {
		return true
	}
	return slices.Contains(c.ExtKeyUsage, x509.ExtKeyUsageServerAuth) ||
		slices.Contains(c.ExtKeyUsage, x509.ExtKeyUsageAny)
}

// caName is the name a CA is disclosed by: its subject's common name, or the whole subject when it
// has none.
func caName(c *x509.Certificate) string {
	if c.Subject.CommonName != "" {
		return c.Subject.CommonName
	}
	return c.Subject.String()
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

// installCATrustFiles writes a session's CA files root-owned 0600, each with the sandbox
// account's read ACE: the env file's installer, with messages of its own. A failure refuses the
// launch, as the env file's does: its variables already name these files, and a bundle that is
// not there, or that the sandbox cannot read, breaks every TLS client in the sandbox rather than
// narrowing it. Each refusal names the next step (caTrustRetry).
func installCATrustFiles(deps Deps, out printer, files []sessionFilePlan) bool {
	for _, f := range files {
		for _, cmd := range f.dir {
			if deps.Run(append([]string{"sudo"}, cmd...)) != 0 {
				out.printf("[bold red]Could not prepare the session environment directory "+
					"(%s).[/bold red] %s", strings.Join(cmd, " "), caTrustRetry)
				return false
			}
		}
		if !deps.InstallRootFile(f.path, f.content, "0600") {
			out.printf("[bold red]Could not write the session's CA bundle %s.[/bold red] %s",
				f.path, caTrustRetry)
			return false
		}
		for _, cmd := range f.grant {
			if deps.Run(append([]string{"sudo"}, cmd...)) != 0 {
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
	"again, and if this repeats, `yolo run --dry-run` prints every privileged command."

// printCATrust is the launch's disclosure of what it trusts for TLS in the sandbox, read off the
// plan, so the variables it names are the ones the env file really sets. follows is the bundle
// variable the launch's own env layers set (applyCATrust), which the other four then name too:
// the line says so, and that the keychain's CAs then reach Node alone, or nothing.
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
	left := ""
	if ca.LeftOut > 0 {
		left = fmt.Sprintf(" %d more in it %s left out: expired, not for TLS servers, or not "+
			"trusted by macOS for TLS.", ca.LeftOut, plural(ca.LeftOut, "was", "were"))
	}
	names := make([]string, 0, len(ca.Kept))
	for _, c := range ca.Kept {
		names = append(names, richtext.Escape(termsafe.Visible(c.Name)))
	}
	n := len(ca.Kept)
	authorities := fmt.Sprintf("%d certificate %s", n, plural(n, "authority", "authorities"))
	node := ""
	if v, ok := sandboxEnvFileValue(envContent, NodeExtraCAVar); ok && v == extrasFile && extrasFile != "" {
		node = " " + NodeExtraCAVar + " names " + extrasFile + ", those alone."
	}
	if follows != "" {
		// THE CALLER'S BUNDLE, which every one of the five names that they did not set otherwise.
		yours := func(lead string) string {
			return lead + " configured environment sets " + follows + ", so " + caVarsClause(envContent) + "."
		}
		switch {
		case ca.ReadError != "":
			out.printf("[yellow]Warning: could not read this Mac's System keychain (%s)[/yellow], so "+
				"the sandbox adds none of its certificate authorities. %s",
				richtext.Escape(termsafe.Visible(ca.ReadError)), yours("Your"))
		case n == 0:
			out.printf("[dim]TLS in the sandbox: %s This Mac's System keychain adds no certificate "+
				"authority.%s[/dim]", yours("your"), left)
		case node != "":
			out.printf("Trusting %s from this Mac's System keychain in Node only: %s.%s %s Every "+
				"other tool trusts only what those variables name.%s", authorities,
				strings.Join(names, ", "), node, yours("Your"), left)
		default:
			out.printf("This Mac's System keychain trusts %s for TLS (%s), and the sandbox does "+
				"not add %s. %s %s keeps your own value too.%s", authorities,
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
	vars := strings.Join(ours, ", ")
	if len(ours) == 0 {
		vars = "no variable"
	}
	tail := ""
	if len(theirs) > 0 {
		tail = " " + strings.Join(theirs, ", ") + " keep the empty value your own environment set."
	}
	switch {
	case ca.ReadError != "":
		out.printf("[yellow]Warning: could not read this Mac's System keychain (%s)[/yellow], so "+
			"the sandbox trusts only the public roots in its tool profile: %s name %s.%s A "+
			"certificate authority your network needs can be named in env_sources: put a bundle "+
			"holding it and the public roots in the workspace and set {\"SSL_CERT_FILE\": \"<that "+
			"file>\"}, which the other four follow, and {\"%s\": \"<that file>\"} for Node.",
			richtext.Escape(termsafe.Visible(ca.ReadError)), vars, target, tail, NodeExtraCAVar)
	case n == 0:
		out.printf("[dim]TLS in the sandbox: %s name the public roots in its tool profile (%s); "+
			"this Mac's System keychain adds no certificate authority.%s%s[/dim]",
			vars, target, left, tail)
	default:
		if node == "" {
			node = " " + NodeExtraCAVar + " keeps your own value, so Node does not add " +
				plural(n, "it", "them") + "."
		}
		out.printf("Trusting %s from this Mac's System keychain: %s. %s name %s, the tool "+
			"profile's public roots plus %s.%s%s%s", authorities, strings.Join(names, ", "), vars,
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
