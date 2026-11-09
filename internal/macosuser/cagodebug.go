package macosuser

// cagodebug.go closes the separate account's Go gap (OQ-PS16 as ruled 2026-10-23, PS-D17 in
// docs/design/provisioner-sets.md): a Go program in the sandbox trusts the same certificate
// authorities as the tools that read a CA file, the login keychain's included.
//
// THE GAP. A Go program on darwin verifies TLS through the Security framework, and in the
// separate-account mode it asks as the sandbox account, whose trust settings are not yours. So a CA
// you marked "Always Trust" in your login keychain reached curl and git (the session's CA bundle,
// cabundle.go) but not gh.
//
// THE MECHANISM: the bundle, not a keychain. Since Go 1.27, crypto/x509 on darwin loads its roots
// from SSL_CERT_FILE when that is set and verifies with Go's own verifier instead of the Security
// framework, under the GODEBUG setting x509sslcertoverrideplatform, which defaults to 1 for a main
// module declaring go 1.27 or later and to 0 below it (SOURCED: the Go 1.27 release notes and
// golang/go#77865). The launch already points SSL_CERT_FILE at the session's bundle, so it sets
// GODEBUG=x509sslcertoverrideplatform=1 beside it: every Go program built by a Go 1.27 or later
// toolchain then trusts exactly the bundle, whatever its go.mod declares. One built by an older
// toolchain ignores the setting and still asks macOS; the disclosure names that, with the check.
//
// WHY NOT A KEYCHAIN (the candidates PS-D17 weighed). Trust settings in the sandbox account's own
// user domain cannot be written without a GUI authorization panel, so never from a launch: Apple's
// SecTrustSettingsSetTrustSettings documentation says per-user changes prompt for the user's
// credentials and are "not possible ... when not running in a GUI environment" (SOURCED). The admin
// domain needs root, which the one-rule sudo contract never grants a launch (privilege.go), and is
// machine-wide rather than a yolo-owned keychain file. This mechanism writes no keychain, runs no
// sudo, opens no Seatbelt rule, and has no state to reconcile: the bundle is composed afresh from
// the host's keychains at every launch, so a CA the host stops trusting is gone at the next one.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

const (
	// godebugVar is Go's runtime settings variable, comma-separated name=value pairs.
	godebugVar = "GODEBUG"
	// goCAFileSetting is the GODEBUG setting that makes a Go 1.27+ program on darwin verify against
	// SSL_CERT_FILE instead of the Security framework.
	goCAFileSetting = "x509sslcertoverrideplatform"
	// goCAFileMinVersion is the first Go release that reads the setting.
	goCAFileMinVersion = "1.27"
)

// goCAFileState is what a launch did about Go's trust in the separate account.
type goCAFileState int

const (
	// goCAFileNotWanted: run-as-you mode, no bundle of ours, or nothing kept beyond the public
	// roots, so the platform verifier already trusts what the bundle would.
	goCAFileNotWanted goCAFileState = iota
	// goCAFileSet: GODEBUG carries the setting at 1, ours or the caller's.
	goCAFileSet
	// goCAFilePinnedOff: the caller's GODEBUG set the setting to something other than 1, which the
	// launch keeps.
	goCAFilePinnedOff
)

// withGoCAFileTrust adds x509sslcertoverrideplatform=1 to GODEBUG in env when the separate
// account's SSL_CERT_FILE names this session's bundle (bundleFile, non-empty only when the launch
// kept a CA and the caller named no bundle of their own). A caller's own GODEBUG keeps every
// setting it has, and one that already names the setting is left as it is. env is not modified.
func withGoCAFileTrust(env *jsonx.OrderedMap, ca CATrust, bundleFile string) *jsonx.OrderedMap {
	if !ca.SeparateAccount || bundleFile == "" {
		return env
	}
	if v, ok := env.Get("SSL_CERT_FILE"); !ok || asStr(v) != bundleFile {
		return env
	}
	current := ""
	if v, ok := env.Get(godebugVar); ok {
		current = asStr(v)
	}
	if _, named := godebugValue(current, goCAFileSetting); named {
		return env
	}
	value := goCAFileSetting + "=1"
	if strings.TrimSpace(current) != "" {
		value = strings.TrimRight(current, ",") + "," + value
	}
	return withEnvVar(env, godebugVar, value)
}

// godebugValue is the value GODEBUG gives name, read exactly as Go's runtime reads it
// (internal/godebug's parse): entries split on ',' with nothing trimmed, so " name=1" names a
// different setting; the name runs to the entry's first '='; a later entry wins; and a value's
// text ends at its first '#', the rest being a bisect pattern. found says it names it at all.
func godebugValue(godebug, name string) (string, bool) {
	value, found := "", false
	for _, kv := range strings.Split(godebug, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k == name {
			value, _, _ = strings.Cut(v, "#")
			found = true
		}
	}
	return value, found
}

// goCAFileStateOf reads what the rendered env file says about Go's trust, so the disclosure names
// what the sandbox really gets.
func goCAFileStateOf(ca CATrust, envContent, bundleFile, follows string) goCAFileState {
	if !ca.SeparateAccount || bundleFile == "" || follows != "" {
		return goCAFileNotWanted
	}
	godebug, _ := sandboxEnvFileValue(envContent, godebugVar)
	switch v, named := godebugValue(godebug, goCAFileSetting); {
	case !named:
		return goCAFileNotWanted
	case v == "1":
		return goCAFileSet
	default:
		return goCAFilePinnedOff
	}
}

// goCAFileLine is the separate account's disclosure of Go's trust, or "" when there is nothing to
// say: n is how many CAs the bundle adds, and fromLogin names those from your login keychain. A
// setting the launch made is always named (a launch has no quiet mode); the limit that remains, a
// Go program built before Go 1.27, is named with the command that tells, and only when a CA from
// your login keychain is one that program would miss.
func goCAFileLine(state goCAFileState, n int, fromLogin []string) string {
	login := strings.Join(fromLogin, ", ")
	switch state {
	case goCAFileSet:
		line := "[dim]The agent runs as a separate account, so " + godebugVar + " sets " +
			goCAFileSetting + "=1: a Go program built with Go " + goCAFileMinVersion + " or later, " +
			"such as gh, verifies against that bundle and so trusts the same " +
			plural(n, "certificate authority", "certificate authorities") + "."
		if len(fromLogin) > 0 {
			line += " One built with an older Go still asks macOS, as that account, and does not trust " +
				"the " + plural(len(fromLogin), "one", "ones") + " from your login keychain (" + login +
				"); `go version <path to the program>` names the Go that built it, and a release built " +
				"with Go " + goCAFileMinVersion + " or later trusts " + plural(len(fromLogin), "it", "them") + "." +
				" Any other program that asks macOS for trust instead of reading a CA file misses " +
				plural(len(fromLogin), "it", "them") + " too; point it at the bundle SSL_CERT_FILE names " +
				"if it can read one."
		}
		return line + "[/dim]"
	case goCAFilePinnedOff:
		if len(fromLogin) == 0 {
			return ""
		}
		return "[yellow]Note: your configured environment sets " + godebugVar + " " + goCAFileSetting +
			" to something other than 1[/yellow], so Go programs, such as gh, and any other program " +
			"that asks macOS for trust, ask it as the " +
			"sandbox account and do not trust the " +
			plural(len(fromLogin), "certificate authority", "certificate authorities") +
			" from your login keychain (" + login + "); tools that read a CA file, such as curl, git, " +
			"Node and Python, do. Remove that setting from `env.set`, or the file under `env.files` that sets it, and launch again to have Go " +
			"programs trust " + plural(len(fromLogin), "it", "them") + " too."
	}
	return ""
}
