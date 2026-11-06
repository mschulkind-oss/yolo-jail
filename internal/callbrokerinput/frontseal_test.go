package callbrokerinput

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func frontSealCertificate(t *testing.T, ip string, usage x509.ExtKeyUsage) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture-front"},
		IPAddresses: []net.IP{net.ParseIP(ip)}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func validFrontSealFixture(t *testing.T) []byte {
	t.Helper()
	return frontSealWithCertificate(t, frontSealCertificate(t, "127.0.0.1", x509.ExtKeyUsageServerAuth))
}

func frontSealWithCertificate(t *testing.T, der []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(der)
	seal := PreparedHTTPFrontSeal{Version: PreparedHTTPFrontSealVersion, Generation: strings.Repeat("a", 32),
		Container: "fixture-container", FrontJailID: "fixture-front", LaunchID: "fixture-launch",
		BrokerStart: "fixture-start", Loophole: "tasks", DeclarationHash: "sha256:fixture",
		Address: "127.0.0.1:41000", CertificateSHA256: hex.EncodeToString(sum[:]), CertificateDER: der}
	data, err := json.Marshal(seal)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPreparedHTTPFrontSealDecodesStrictly(t *testing.T) {
	valid := string(validFrontSealFixture(t))
	if _, err := DecodePreparedHTTPFrontSeal([]byte(valid)); err != nil {
		t.Fatalf("valid seal refused: %v", err)
	}
	for name, raw := range map[string]string{
		"unknown key":          strings.Replace(valid, `"version"`, `"token":"x","version"`, 1),
		"exact duplicate":      strings.Replace(valid, `"address":`, `"address":"127.0.0.1:1","address":`, 1),
		"case-fold duplicate":  strings.Replace(valid, `"address":`, `"Address":"127.0.0.1:1","address":`, 1),
		"case-fold alias":      strings.Replace(valid, `"generation":`, `"GENERATION":`, 1),
		"bad version":          strings.Replace(valid, PreparedHTTPFrontSealVersion, "yolo.http-front-seal/2", 1),
		"malformed generation": strings.Replace(valid, strings.Repeat("a", 32), "not-a-generation", 1),
		"uppercase generation": strings.Replace(valid, strings.Repeat("a", 32), strings.Repeat("A", 32), 1),
		"certificate hash":     strings.Replace(valid, `"certificate_sha256":"`, `"certificate_sha256":"00`, 1),
		"nested value":         strings.Replace(valid, `"loophole":"tasks"`, `"loophole":{"name":"tasks"}`, 1),
		"trailing JSON":        valid + `{}`,
		"not loopback":         strings.Replace(valid, "127.0.0.1:41000", "10.0.0.1:41000", 1),
		"missing container":    strings.Replace(valid, `"container":"fixture-container"`, `"container":""`, 1),
		"non-object":           `[]`,
		"non-string loophole":  strings.Replace(valid, `"loophole":"tasks"`, `"loophole":7`, 1),
		"port zero":            strings.Replace(valid, "127.0.0.1:41000", "127.0.0.1:0", 1),
		// The DER hash matches in each of these; only the certificate itself is wrong.
		"not a certificate": string(frontSealWithCertificate(t, []byte("fixture-der-bytes"))),
		"SAN names another IP": string(frontSealWithCertificate(t,
			frontSealCertificate(t, "127.0.0.2", x509.ExtKeyUsageServerAuth))),
		"no ServerAuth": string(frontSealWithCertificate(t,
			frontSealCertificate(t, "127.0.0.1", x509.ExtKeyUsageClientAuth))),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePreparedHTTPFrontSeal([]byte(raw)); err == nil {
				t.Fatalf("invalid seal accepted: %s", raw)
			}
		})
	}
}
