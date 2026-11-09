// Package catrust is the host's certificate-authority trust, read on the host and handed to a jail
// (docs/design/provisioner-sets.md §16.3, OQ-PS14 and OQ-PS15 as ruled 2026-10-23: "if it was
// trusted on the host, it should be trusted in the jail, regardless of how that trust got there").
//
// It is the pure half every backend shares: the filter that keeps a certificate a TLS client can
// anchor on (ParseCAs), the source each kept CA is named by at launch, the labelled file a container
// jail's boot reads the host's CAs from (Render, ParseLabelled), the disclosure's grouping by source
// (SourcesPhrase), and the Linux host's own bundle (ReadLinuxHostBundle). Reading a macOS keychain
// is macosuser's (cabundle.go), since only that package runs `security`.
//
// A CA certificate is public, so nothing here is a credential: a jail gets the composed file, never
// a host directory.
package catrust

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The sources a CA is disclosed by. A Linux host's bundle is named with its path (HostBundleSource).
const (
	SourceSystemKeychain = "this Mac's System keychain"
	SourceLoginKeychain  = "your login keychain"
)

// HostBundleSource is the source a CA read from a Linux host's bundle at path is disclosed by.
func HostBundleSource(path string) string { return "the CA bundle at " + path }

// CA is one certificate authority the host trusts: the name a launch discloses it by, its PEM
// block, and where on the host its trust came from.
type CA struct {
	Name   string
	PEM    string
	Source string
}

// DER is the certificate's DER bytes, nil when the PEM does not decode.
func (c CA) DER() []byte {
	b, _ := pem.Decode([]byte(c.PEM))
	if b == nil {
		return nil
	}
	return b.Bytes
}

// ParseCAs reads PEM text (a `security find-certificate -a -p` export, a host's bundle) and
// returns each certificate that can be a TLS trust anchor, once, labelled source, with how many it
// left out: a block that does not parse, a certificate that is not a CA, one outside its validity
// at now, and one whose extended key usage names neither TLS servers nor any purpose. A non-
// certificate block (a key) is skipped and not counted. What the host's own trust settings say is
// the caller's to ask afterwards (macOS: `security verify-cert`).
func ParseCAs(pemText, source string, now time.Time) (kept []CA, left int) {
	return parse(pemText, source, now, true)
}

// ParseBundle reads a host's own CA bundle file and keeps EVERY certificate in it that parses and
// is within its validity at now, once, labelled source, with how many it left out. A
// distribution's bundle is the host's trust decision already (an administrator who drops a
// self-signed server certificate into it means it to be trusted), so neither CA-ness nor extended
// key usage is second-guessed here, as ParseCAs does for a keychain export (PS-D16).
func ParseBundle(pemText, source string, now time.Time) (kept []CA, left int) {
	return parse(pemText, source, now, false)
}

func parse(pemText, source string, now time.Time, anchorsOnly bool) (kept []CA, left int) {
	rest := []byte(pemText)
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
		if err != nil || now.Before(cert.NotBefore) || now.After(cert.NotAfter) ||
			anchorsOnly && (!cert.IsCA || !AllowsServerAuth(cert)) {
			left++
			continue
		}
		if slices.ContainsFunc(seen, func(der []byte) bool { return bytes.Equal(der, cert.Raw) }) {
			continue
		}
		seen = append(seen, cert.Raw)
		kept = append(kept, CA{Name: Name(cert), PEM: string(pem.EncodeToMemory(&pem.Block{
			Type: "CERTIFICATE", Bytes: block.Bytes})), Source: source})
	}
}

// AllowsServerAuth reports whether a CA's extended key usage permits TLS server certificates: no
// EKU at all (unconstrained), ServerAuth, or Any.
func AllowsServerAuth(c *x509.Certificate) bool {
	if len(c.ExtKeyUsage) == 0 && len(c.UnknownExtKeyUsage) == 0 {
		return true
	}
	return slices.Contains(c.ExtKeyUsage, x509.ExtKeyUsageServerAuth) ||
		slices.Contains(c.ExtKeyUsage, x509.ExtKeyUsageAny)
}

// Name is the name a CA is disclosed by: its subject's common name, or the whole subject when it
// has none.
func Name(c *x509.Certificate) string {
	if c.Subject.CommonName != "" {
		return c.Subject.CommonName
	}
	return c.Subject.String()
}

// Merge appends each CA of more whose certificate is not already in base or earlier in more,
// keeping order and labels: the first source to hold a certificate is the one it is named by.
func Merge(base []CA, more ...[]CA) []CA {
	out := slices.Clone(base)
	seen := make([][]byte, 0, len(out))
	for _, c := range out {
		seen = append(seen, c.DER())
	}
	for _, list := range more {
		for _, c := range list {
			der := c.DER()
			if der == nil || slices.ContainsFunc(seen, func(d []byte) bool { return bytes.Equal(d, der) }) {
				continue
			}
			seen = append(seen, der)
			out = append(out, c)
		}
	}
	return out
}

// sourceLabel is the comment line Render writes before each block, which ParseLabelled reads back.
// Text outside a BEGIN/END pair is ignored by OpenSSL and by encoding/pem, so the file is also an
// ordinary PEM bundle.
const sourceLabel = "# yolo-ca-source: "

// Render writes cas as one PEM file, each block preceded by its source as a comment line.
func Render(cas []CA) string {
	var b strings.Builder
	for _, c := range cas {
		b.WriteString(sourceLabel + oneLine(c.Source) + "\n")
		b.WriteString(strings.TrimRight(c.PEM, "\n") + "\n")
	}
	return b.String()
}

// ParseLabelled reads a file Render wrote back into CAs, each named from its certificate and
// labelled with the source line before it ("" when there is none). A block that does not parse as
// a certificate is skipped.
func ParseLabelled(text string) []CA {
	var out []CA
	source := ""
	rest := text
	for rest != "" {
		line, tail, _ := strings.Cut(rest, "\n")
		if s, ok := strings.CutPrefix(line, sourceLabel); ok {
			source = strings.TrimSpace(s)
			rest = tail
			continue
		}
		if strings.HasPrefix(line, "-----BEGIN ") {
			block, after := pem.Decode([]byte(rest))
			if block == nil {
				rest = tail
				continue
			}
			rest = string(after)
			if block.Type != "CERTIFICATE" {
				continue
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				continue
			}
			out = append(out, CA{Name: Name(cert), PEM: string(pem.EncodeToMemory(&pem.Block{
				Type: "CERTIFICATE", Bytes: block.Bytes})), Source: source})
			source = ""
			continue
		}
		rest = tail
	}
	return out
}

// oneLine keeps a source on its label line.
func oneLine(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }

// SourcesPhrase is how a disclosure names the kept CAs by source, each name passed through names
// (which escapes it for its sink), with qualifier (" in Node only", or "") placed after the source
// when there is one: " from <source><qualifier>: A, B" when every CA came from one source, and
// "<qualifier>, 2 from <source> (A, B) and 1 from <other> (C)" when they came from several, in the
// order each source first appears. "" for none.
func SourcesPhrase(kept []CA, qualifier string, names func(string) string) string {
	if len(kept) == 0 {
		return ""
	}
	var order []string
	bySource := map[string][]string{}
	for _, c := range kept {
		if _, ok := bySource[c.Source]; !ok {
			order = append(order, c.Source)
		}
		bySource[c.Source] = append(bySource[c.Source], names(c.Name))
	}
	if len(order) == 1 {
		return " from " + order[0] + qualifier + ": " + strings.Join(bySource[order[0]], ", ")
	}
	parts := make([]string, 0, len(order))
	for _, s := range order {
		parts = append(parts, fmt.Sprintf("%d from %s (%s)", len(bySource[s]), s, strings.Join(bySource[s], ", ")))
	}
	return qualifier + ", " + strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}
