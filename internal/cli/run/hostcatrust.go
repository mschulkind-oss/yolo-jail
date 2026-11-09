package run

// hostcatrust.go is THE HOST'S CA TRUST FOR A CONTAINER JAIL (docs/design/provisioner-sets.md
// §16.3, PS-D16; OQ-PS14 and OQ-PS15 as ruled 2026-10-23: "in general, for the certificate
// authorities ... I do want to take the host set", and "if it was trusted on the host, it should
// be trusted in the jail, regardless of how that trust got there").
//
// A fresh launch reads the host's trust ON THE HOST, as the invoking user:
//
//   - a Linux host: its own CA bundle, the file the host's OpenSSL reads (SSL_CERT_FILE when the
//     host sets it, else the distribution's bundle; catrust.ReadLinuxHostBundle);
//   - a macOS host (podman machine, Apple Container): the System keychain and the user's login
//     keychain, exported and verified exactly as a macos-user launch does
//     (macosuser.ReadHostKeychainTrust), plus the bundle SSL_CERT_FILE names when the host sets it.
//
// It writes the CAs that pass into ONE file, each labelled with its source (catrust.Render), and
// hands the jail that file alone, never a host directory: a read-only bind on podman, a copy in
// the home Apple Container binds. YOLO_HOST_CA_FILE names it in the jail, whose boot joins it with
// the image's public roots and the loophole CAs (entrypoint/catrust.go) and names what it added.
// A CA certificate is public, so nothing here is a credential. A host whose trust cannot be read
// warns and launches with the image's roots: never a refusal.
//
// An attach never reaches here: the running jail keeps the trust it booted with.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/catrust"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// hostCAFileName is the composed file's name beneath wsState on podman, which binds it read-only
// at entrypoint.HostCAFileContainerPath.
const hostCAFileName = "host-ca.pem"

// hostTrust is what one read of the host's trust found: the CAs, and each line the launch prints
// about a source it could not read.
type hostTrust struct {
	cas   []catrust.CA
	notes []string
}

// readHostTrust is the host read, a seam for a test; production reads the host this launch runs on.
var readHostTrust = readHostTrustReal

// hostGOOS and hostKeychainReaders are the host readHostTrustReal reads, seams so a Linux test can
// drive a macOS host's read with stubbed keychains.
var (
	hostGOOS            = runtime.GOOS
	hostKeychainReaders = macosuser.RealKeychainReaders
)

func readHostTrustReal() hostTrust {
	now := time.Now()
	esc := func(s string) string { return richtext.Escape(termsafe.Visible(s)) }
	if hostGOOS == "darwin" {
		kt := macosuser.ReadHostKeychainTrust(hostKeychainReaders(), now)
		var notes []string
		if kt.SystemError != "" {
			notes = append(notes, "[yellow]Warning: could not read this Mac's System keychain ("+
				esc(kt.SystemError)+")[/yellow], so the jail does not trust its certificate authorities. "+
				"A certificate authority your network needs can be named: put a bundle holding it and "+
				"the public roots in the workspace and run `yolo config set env.set.SSL_CERT_FILE <that file>`, "+
				"which the other four TLS variables follow.")
		}
		if kt.LoginError != "" {
			notes = append(notes, "[yellow]Note: could not read your login keychain ("+esc(kt.LoginError)+
				")[/yellow], so a certificate authority you trusted only there is not trusted in the jail "+
				"this launch; the ones in this Mac's System keychain are still trusted. If the keychain is "+
				"locked, unlock it (`security unlock-keychain`) and launch again.")
		}
		if kt.LeftOut > 0 {
			notes = append(notes, fmt.Sprintf("[dim]%d %s in this Mac's keychains %s left "+
				"out of the jail's trust: expired, not for TLS servers, or not trusted by macOS for TLS.[/dim]",
				kt.LeftOut, plural(kt.LeftOut, "certificate", "certificates"), plural(kt.LeftOut, "was", "were")))
		}
		// The bundle the host's SSL_CERT_FILE names is this Mac's trust too: its OpenSSL programs
		// (nix curl, git, Python) read it, so a CA trusted there is trusted in the jail beside the
		// keychains', as on a Linux host and in a macos-user sandbox ("regardless of how that trust
		// got there", OQ-PS15). Unset, nothing is read: never a Linux distribution path.
		st := macosuser.ReadSSLCertFileTrust(os.Getenv, os.ReadFile, now)
		notes = append(notes, macosuser.SSLCertFileNotes(st, "the jail")...)
		return hostTrust{cas: catrust.Merge(kt.Kept, st.CAs), notes: notes}
	}
	hb, err := catrust.ReadLinuxHostBundle(os.Getenv, os.ReadFile, now)
	if err != nil {
		return hostTrust{notes: []string{"[yellow]Warning: could not read this host's CA bundle (" +
			esc(err.Error()) + ")[/yellow], so the jail trusts its image's public roots and the " +
			"loophole CAs alone. A certificate authority your network needs can be named: put a " +
			"bundle holding it and the public roots in the workspace and run " +
			"`yolo config set env.set.SSL_CERT_FILE <that file>`, which the other four TLS variables follow."}}
	}
	var notes []string
	if hb.LeftOut > 0 {
		notes = append(notes, fmt.Sprintf("[dim]%d %s in %s %s left out of the jail's "+
			"trust: out of date, or not readable as a certificate.[/dim]", hb.LeftOut,
			plural(hb.LeftOut, "certificate", "certificates"), esc(hb.Path), plural(hb.LeftOut, "was", "were")))
	}
	return hostTrust{cas: hb.CAs, notes: notes}
}

// prepareHostCATrust reads the host's trust, writes the composed file where rt reaches it, prints
// each source it could not read, and returns the file's in-jail path, or "" when the host
// contributed no CA (the jail then gets no file and no variable). A file that cannot be written
// warns and launches without it.
func (o *Options) prepareHostCATrust(rt, wsState string, out printer) string {
	ht := readHostTrust()
	for _, n := range ht.notes {
		out.print(n)
	}
	if len(ht.cas) == 0 {
		return ""
	}
	rel, inJail := hostCAFileName, entrypoint.HostCAFileContainerPath
	if rt == "container" { // parity: HonoredBy — Apple Container binds wsState whole at /home/agent and ignores `:ro`, so the file is written into that home instead of bound
		rel, inJail = entrypoint.HostCAFileHomeRel, "/home/agent/"+entrypoint.HostCAFileHomeRel
	}
	if err := writeFileBeneathMode(wsState, filepath.FromSlash(rel), []byte(catrust.Render(ht.cas)), 0o644); err != nil {
		out.printf("[yellow]Warning: could not write the host's certificate authorities for the jail "+
			"(%s)[/yellow], so it trusts its image's public roots alone this launch; the next launch "+
			"writes the file again.", richtext.Escape(err.Error()))
		return ""
	}
	return inJail
}

// hostCAArgs is the argv that hands the jail the composed file: on podman its read-only bind, and
// on both backends the variable naming it. Nothing when the launch composed none.
func hostCAArgs(rt, wsState, inJail string) []string {
	if inJail == "" {
		return nil
	}
	var args []string
	if rt != "container" { // parity: HonoredBy — Apple Container reads the copy prepareHostCATrust wrote into the home it binds
		args = append(args, "-v", filepath.Join(wsState, hostCAFileName)+":"+inJail+":ro")
	}
	return append(args, "-e", entrypoint.HostCAFileEnv+"="+inJail)
}
