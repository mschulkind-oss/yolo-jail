package callbrokerinput

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func validFrontSealFixture(t *testing.T) []byte {
	t.Helper()
	der := []byte("fixture-der-bytes")
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
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePreparedHTTPFrontSeal([]byte(raw)); err == nil {
				t.Fatalf("invalid seal accepted: %s", raw)
			}
		})
	}
}
