package entrypoint

// catrust.go is a CONTAINER JAIL'S TLS TRUST (docs/design/provisioner-sets.md §16.3, PS-D16; OQ-PS14
// and OQ-PS15 as ruled 2026-10-23: "if it was trusted on the host, it should be trusted in the
// jail, regardless of how that trust got there").
//
// The launcher reads the host's trust on the host (a Linux host's CA bundle; a macOS host's System
// and login keychains) and hands the jail ONE composed file of those CAs, each labelled with its
// source (catrust.Render), named by YOLO_HOST_CA_FILE: never a host directory. This boot step joins
// the image's public roots, the host's CAs the image does not already carry, and every loophole CA
// into $HOME/.yolo-ca-bundle.crt, writes the CAs beyond the public roots alone into
// $HOME/.config/yolo-extra-ca.pem for Node, decides the six variables, and names what it added.
//
// THE CALLER'S OWN BUNDLE WINS, FOR ALL FIVE, the rule PS-D10 states for macos-user: each tool
// reads its own of NIX_SSL_CERT_FILE, SSL_CERT_FILE, REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE and
// GIT_SSL_CAINFO first, so when the launch's env layers (env_sources, a profile, a pack: the user
// env file) set any of them, every one they left unset names the first such value, in that order,
// and NODE_EXTRA_CA_CERTS, which Node adds to its own roots and reads as ONE file, names the
// extras, to which a file the user names there is appended, never put in place of the loophole
// CAs. The boot used to set its four unconditionally after the user env file was read, and
// the shell set them again before sourcing it, so a user's SSL_CERT_FILE lost to the image's.

import (
	"bytes"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/catrust"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const (
	// HostCAFileEnv names the in-jail path of the file of the host's CAs the launcher composed
	// (catrust.Render). Unset: the launcher composed none, and the jail trusts its image's public
	// roots and the loophole CAs alone.
	HostCAFileEnv = "YOLO_HOST_CA_FILE"
	// HostCAFileContainerPath is where the podman launcher binds that file, read-only. Apple
	// Container writes it into the home it binds instead (HostCAFileHomeRel).
	HostCAFileContainerPath = "/run/yolo-host-ca.pem"
	// HostCAFileHomeRel is the file's home-relative path on Apple Container.
	HostCAFileHomeRel = ".config/yolo-host-ca.pem"
	// caBundleRel is the combined bundle, a single-file bind on podman (paths.HomeFileMountpoints).
	caBundleRel = ".yolo-ca-bundle.crt"
	// ExtraCAFileRel is the CAs beyond the image's public roots alone, which NODE_EXTRA_CA_CERTS
	// names: one file, never a colon-joined list (Node reads that variable as ONE path).
	ExtraCAFileRel = ".config/yolo-extra-ca.pem"
	// CAEnvFileRel is the boot's decided CA variables as plain exports, which every jail shell
	// sources (GenerateBashrc), so an interactive shell names what the agent's environment names.
	CAEnvFileRel   = ".config/yolo-ca-env.sh"
	nodeExtraCAVar = "NODE_EXTRA_CA_CERTS"
)

// CABundleVars are the five variables a jail points at one bundle, in the order nix's OpenSSL
// reads them (the first is the one the others follow).
var CABundleVars = []string{"NIX_SSL_CERT_FILE", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"}

// caTrust is what one boot composed.
type caTrust struct {
	bundle    string       // $HOME/.yolo-ca-bundle.crt
	extras    string       // the extras file, "" when there is nothing beyond the public roots
	hostFile  string       // the launcher's file of the host's CAs, "" when it composed none
	hostAdded []catrust.CA // the host's CAs the image's roots did not already carry
	vars      [][2]string  // the six variables as decided, in order
	follows   string       // the bundle variable the user's layers set, which the others follow
	// userNode is the NODE_EXTRA_CA_CERTS the user's layers set, "" for none; userNodeRead is
	// whether its certificates were appended to the extras file.
	userNode     string
	userNodeRead bool
}

// GenerateCABundle concatenates the image baseline ($SSL_CERT_FILE, unless it already IS our
// bundle), the host's CAs the baseline does not already hold ($YOLO_HOST_CA_FILE), and each path
// in $NODE_EXTRA_CA_CERTS (colon-separated, de-duplicated, the loophole CAs) into
// $HOME/.yolo-ca-bundle.crt (chmod 0o644), and the last two alone into the extras file. Always
// writes the bundle, even if empty. Returns the bundle path; deciding the variables is
// applyCATrust's.
func GenerateCABundle(e *Env) (string, error) {
	ca, err := generateCATrust(e, "")
	return ca.bundle, err
}

// generateCATrust composes the bundle and the extras file. userNode is the NODE_EXTRA_CA_CERTS the
// user's env layers set, "" for none: its certificates are APPENDED to the extras file, beside the
// host's and the loophole CAs, because Node reads that variable as ONE file, and pointing it at the
// user's file alone would drop the loophole CAs (the oauth broker's interception leaf among them).
func generateCATrust(e *Env, userNode string) (caTrust, error) {
	ca := caTrust{bundle: filepath.Join(e.Home, caBundleRel)}
	extrasPath := filepath.Join(e.Home, ExtraCAFileRel)

	var chunks, extraChunks [][]byte
	baseline := e.Getenv("SSL_CERT_FILE")
	var baselineDER [][]byte
	if baseline != "" && baseline != ca.bundle {
		if data := readBundleBytes(baseline); len(data) > 0 {
			chunks = append(chunks, data)
			baselineDER = pemDERs(data)
		}
	}

	// THE HOST'S CAs, minus what the image's public roots already carry: on a Linux host the
	// distribution's bundle is mostly the same Mozilla roots, and only what the host adds is
	// named at launch.
	if p := e.Getenv(HostCAFileEnv); p != "" {
		ca.hostFile = p
		if data := readBundleBytes(p); len(data) > 0 {
			for _, c := range catrust.ParseLabelled(string(data)) {
				der := c.DER()
				if containsDER(baselineDER, der) {
					continue
				}
				baselineDER = append(baselineDER, der)
				ca.hostAdded = append(ca.hostAdded, c)
				chunks = append(chunks, []byte(c.PEM))
				extraChunks = append(extraChunks, []byte(c.PEM))
			}
		}
	}

	if extras := e.Getenv(nodeExtraCAVar); extras != "" {
		seen := map[string]struct{}{}
		for _, raw := range splitPathList(extras) {
			p := trimSpace(raw)
			if p == "" || p == extrasPath {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			if data := readBundleBytes(p); len(data) > 0 {
				chunks = append(chunks, data)
				extraChunks = append(extraChunks, data)
			}
		}
	}

	// THE USER'S OWN NODE_EXTRA_CA_CERTS, appended for Node alone (a Zscaler root behind
	// env_sources is the usual case): never in place of the loophole CAs.
	if userNode != "" && userNode != extrasPath {
		ca.userNode = userNode
		if data := readBundleBytes(userNode); len(data) > 0 {
			extraChunks = append(extraChunks, data)
			ca.userNodeRead = true
		}
	}

	if err := os.MkdirAll(e.Home, 0o755); err != nil {
		return ca, err
	}
	// Write the body then chmod 0o644 (WriteInPlace truncates in place: on podman the file is a
	// single-file bind, whose inode must survive).
	if err := writeBytesMode(ca.bundle, joinPEMChunks(chunks), 0o644); err != nil {
		return ca, err
	}
	if len(extraChunks) > 0 {
		if err := writeBytesMode(extrasPath, joinPEMChunks(extraChunks), 0o644); err != nil {
			return ca, err
		}
		ca.extras = extrasPath
	} else {
		_ = os.Remove(extrasPath)
	}
	return ca, nil
}

// joinPEMChunks joins the chunks with "\n" (each right-trimmed of newlines), with a trailing NL
// when non-empty.
func joinPEMChunks(chunks [][]byte) []byte {
	trimmed := make([][]byte, len(chunks))
	for i, c := range chunks {
		trimmed[i] = bytes.TrimRight(c, "\n")
	}
	body := bytes.Join(trimmed, []byte("\n"))
	if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
		body = append(body, '\n')
	}
	return body
}

// pemDERs is every CERTIFICATE block's DER in data.
func pemDERs(data []byte) [][]byte {
	var out [][]byte
	for {
		var b *pem.Block
		b, data = pem.Decode(data)
		if b == nil {
			return out
		}
		if b.Type == "CERTIFICATE" {
			out = append(out, b.Bytes)
		}
	}
}

func containsDER(list [][]byte, der []byte) bool {
	for _, d := range list {
		if bytes.Equal(d, der) {
			return true
		}
	}
	return false
}

// userCAValues reads the six CA variables the launch's own env layers set, from the user env
// file the boot hydrated (userEnvFilePath): both line grammars, the env_sources default and the
// channel's plain form, since a pack's or a profile's value is the user's choice as much as a
// dotenv file's. The hydration itself skips a default the container's environment already sets,
// and the image sets SSL_CERT_FILE, so the file is read here directly.
func userCAValues(e *Env) map[string]string {
	data, err := os.ReadFile(userEnvFilePath(e))
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range splitLines(string(data)) {
		key, val, _, ok := parseExportLine(line)
		if !ok {
			continue
		}
		if key == nodeExtraCAVar || slices.Contains(CABundleVars, key) {
			out[key] = val
		}
	}
	return out
}

// decideCAVars is the six variables for this boot: with no bundle variable set by the user, all
// five name the bundle; with one set non-empty, every one they left unset names the first such
// value in CABundleVars order, a set one keeps its own (an empty value names no bundle, so it is
// kept and not followed); NODE_EXTRA_CA_CERTS names the extras file whenever there is one, which
// already holds the user's own file's certificates (generateCATrust), else keeps the user's
// value, else is left as it is.
func decideCAVars(ca caTrust, user map[string]string) (vars [][2]string, follows string) {
	target := ca.bundle
	for _, k := range CABundleVars {
		if v, ok := user[k]; ok && v != "" {
			follows, target = k, v
			break
		}
	}
	for _, k := range CABundleVars {
		v, ok := user[k]
		if !ok {
			v = target
		}
		vars = append(vars, [2]string{k, v})
	}
	if ca.extras != "" {
		vars = append(vars, [2]string{nodeExtraCAVar, ca.extras})
	} else if v, ok := user[nodeExtraCAVar]; ok {
		vars = append(vars, [2]string{nodeExtraCAVar, v})
	}
	return vars, follows
}

// applyCATrust is the generate_ca_bundle step: compose, decide, export into the boot's
// environment (so the agent the boot execs inherits it), write the shell's copy, and disclose.
func applyCATrust(e *Env) error {
	user := userCAValues(e)
	ca, err := generateCATrust(e, user[nodeExtraCAVar])
	if err != nil {
		return err
	}
	ca.vars, ca.follows = decideCAVars(ca, user)
	var sh strings.Builder
	sh.WriteString("# Generated by the jail's boot (generate_ca_bundle): the TLS trust variables.\n")
	for _, kv := range ca.vars {
		setEnvBoth(e, kv[0], kv[1])
		sh.WriteString("export " + kv[0] + "='" + strings.ReplaceAll(kv[1], "'", `'\''`) + "'\n")
	}
	if err := writeBytesMode(filepath.Join(e.Home, CAEnvFileRel), []byte(sh.String()), 0o644); err != nil {
		return err
	}
	for _, line := range caTrustLines(ca) {
		e.warn(line)
	}
	if ca.userNode != "" && !ca.userNodeRead {
		e.warn("Warning: your configured environment sets " + nodeExtraCAVar + " to " +
			richtext.Escape(termsafe.Visible(ca.userNode)) + ", which the jail could not read, so Node " +
			"does not add its certificates. Check the path where you set it (`env.set`, or a file under `env.files`): it is read inside the " +
			"jail, so it must be a path the jail sees, such as one under /workspace.")
	}
	if len(ca.hostAdded) == 0 {
		e.note("TLS in the jail: the host adds no certificate authority beyond the image's public roots" +
			hostFileNote(ca.hostFile))
	}
	return nil
}

func hostFileNote(p string) string {
	if p == "" {
		return " (the launcher composed no host file)"
	}
	return " (" + p + ")"
}

// caTrustLines is the launch's disclosure of the host CAs this jail trusts, each named with where
// on the host its trust came from: nothing when the host added none (the boot log records that).
func caTrustLines(ca caTrust) []string {
	n := len(ca.hostAdded)
	if n == 0 {
		return nil
	}
	esc := func(s string) string { return richtext.Escape(termsafe.Visible(s)) }
	authorities := "1 certificate authority"
	them := "it"
	if n != 1 {
		authorities = strconv.Itoa(n) + " certificate authorities"
		them = "them"
	}
	value := map[string]string{}
	for _, kv := range ca.vars {
		value[kv[0]] = kv[1]
	}
	node := nodeExtraCAVar + " keeps your own value, so Node does not add " + them + "."
	if v := value[nodeExtraCAVar]; ca.extras != "" && v == ca.extras {
		node = nodeExtraCAVar + " names " + ca.extras + ", so Node adds " + them + "."
		if ca.userNodeRead {
			node = nodeExtraCAVar + " names " + ca.extras + ", which also holds the certificates in your " +
				esc(ca.userNode) + ", so Node adds " + them + " and yours."
		}
	}
	if ca.follows != "" {
		return []string{"The host trusts " + authorities + catrust.SourcesPhrase(ca.hostAdded, "", esc) +
			". Your configured environment sets " + ca.follows + ", so every TLS variable you left " +
			"unset names " + esc(value[ca.follows]) + " too, and the jail does not add " + them +
			" there; " + node}
	}
	return []string{"Trusting " + authorities + catrust.SourcesPhrase(ca.hostAdded, "", esc) + ". " +
		strings.Join(CABundleVars, ", ") + " name " + ca.bundle + ", the image's public roots plus " +
		them + "; " + node}
}
