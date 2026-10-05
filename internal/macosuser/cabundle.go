package macosuser

// cabundle.go is CORPORATE CA TRUST inside the macos-user sandbox (docs/design/provisioner-sets.md
// §16.3 and PS-D9, built on OQ-PS14 (a) and OQ-PS15 (a) as leaned).
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
// profile's public roots: it never refuses a launch. Each variable is a DEFAULT, under any value
// the user's own env layers set, as in a container, where the user's env file is read after the
// boot exports its bundle.

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

// applyCATrust sets the CA variables on a copy of env, each as a default under any value already
// there, and returns the copy with the session's two CA files (both "" when the variables name the
// profile's own bundle). env itself is untouched.
func applyCATrust(env *jsonx.OrderedMap, ca CATrust, key string) (out *jsonx.OrderedMap, bundleFile, extrasFile string) {
	if ca.ProfileBundle == "" {
		return env, "", ""
	}
	target := ca.ProfileBundle
	if ca.Bundle != "" {
		bundleFile, extrasFile = CABundleFile(key, ""), CAExtrasFile(key, "")
		target = bundleFile
	}
	out = env
	for _, k := range CABundleVars {
		out = withEnvDefault(out, k, target)
	}
	if extrasFile != "" {
		out = withEnvDefault(out, NodeExtraCAVar, extrasFile)
	}
	return out, bundleFile, extrasFile
}

// withEnvDefault is withEnvVar for a key the caller's layers have not set; a set key is left as
// it is, and env is returned unchanged.
func withEnvDefault(env *jsonx.OrderedMap, key, value string) *jsonx.OrderedMap {
	if env != nil {
		if _, set := env.Get(key); set {
			return env
		}
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
// not there breaks every TLS client in the sandbox rather than narrowing it.
func installCATrustFiles(deps Deps, out printer, files []sessionFilePlan) bool {
	for _, f := range files {
		for _, cmd := range f.dir {
			if deps.Run(append([]string{"sudo"}, cmd...)) != 0 {
				out.printf("[bold red]Could not prepare the session environment directory "+
					"(%s).[/bold red]", strings.Join(cmd, " "))
				return false
			}
		}
		if !deps.InstallRootFile(f.path, f.content, "0600") {
			out.printf("[bold red]Could not write the session's CA bundle %s.[/bold red] The "+
				"sandbox's TLS variables name it, so the launch stops here; run it again, and if "+
				"this repeats, `yolo run --dry-run` prints every privileged command.", f.path)
			return false
		}
		for _, cmd := range f.grant {
			if deps.Run(append([]string{"sudo"}, cmd...)) != 0 {
				out.printf("[bold red]Could not grant %s read on %s (%s).[/bold red]",
					SandboxUser, f.path, strings.Join(cmd, " "))
				return false
			}
		}
	}
	return true
}

// printCATrust is the launch's disclosure of what it trusts for TLS in the sandbox, read off the
// plan, so the variables it names are the ones the env file really sets: a variable the user's own
// env layers set keeps their value, and the line says so.
func printCATrust(out printer, ca CATrust, envContent, bundleFile, extrasFile string) {
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
		tail = " " + strings.Join(theirs, ", ") + " keep the value your own environment set."
	}
	left := ""
	if ca.LeftOut > 0 {
		left = fmt.Sprintf(" %d more in it %s left out: expired, not for TLS servers, or not "+
			"trusted by macOS for TLS.", ca.LeftOut, plural(ca.LeftOut, "was", "were"))
	}
	switch {
	case ca.ReadError != "":
		out.printf("[yellow]Warning: could not read this Mac's System keychain (%s)[/yellow], so "+
			"the sandbox trusts only the public roots in its tool profile: %s name %s.%s A "+
			"certificate authority your network needs can be named in env_sources: put a bundle "+
			"in the workspace and set {\"SSL_CERT_FILE\": \"<that file>\"}.",
			richtext.Escape(termsafe.Visible(ca.ReadError)), vars, target, tail)
	case len(ca.Kept) == 0:
		out.printf("[dim]TLS in the sandbox: %s name the public roots in its tool profile (%s); "+
			"this Mac's System keychain adds no certificate authority.%s%s[/dim]",
			vars, target, left, tail)
	default:
		names := make([]string, 0, len(ca.Kept))
		for _, c := range ca.Kept {
			names = append(names, richtext.Escape(termsafe.Visible(c.Name)))
		}
		node := ""
		if v, ok := sandboxEnvFileValue(envContent, NodeExtraCAVar); ok && v == extrasFile && extrasFile != "" {
			node = " " + NodeExtraCAVar + " names " + extrasFile + ", those alone."
		}
		out.printf("Trusting %d certificate %s from this Mac's System keychain: %s. %s name %s, "+
			"the tool profile's public roots plus %s.%s%s%s", len(ca.Kept),
			plural(len(ca.Kept), "authority", "authorities"), strings.Join(names, ", "), vars, target,
			plural(len(ca.Kept), "it", "them"), node, left, tail)
	}
}

// plural picks the word for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
