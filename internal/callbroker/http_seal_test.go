package callbroker

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// frontFixtureCertificate is a P-256 self-signed DER with one IP SAN.
func frontFixtureCertificate(t *testing.T, ip string, serverAuth bool) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture-front"},
		IPAddresses: []net.IP{net.ParseIP(ip)}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature}
	if serverAuth {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestSelectedHTTPFrontSealGatesOrdinaryFrames(t *testing.T) {
	var requests atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	h, f, generation := selectedPreparedHTTPReadFixture(t, origin, executor)
	frame := selectedHTTPRawFrames(t, h.Handle)
	call := HTTPCall{Method: "GET", Path: "/project"}
	act := func(gen any) (int, string) {
		payload := map[string]any{"op": "act", "http": call}
		if gen != nil {
			payload["front_generation"] = gen
		}
		return frame(payload)
	}
	if rc, out := act(generation); rc != 64 || !strings.Contains(out, "has not sealed") {
		t.Fatalf("unsealed generation reached classification: rc=%d out=%q", rc, out)
	}
	if rc, _ := act(nil); rc != 64 {
		t.Fatalf("selected-front frame without front_generation was accepted: rc=%d", rc)
	}
	prepared, err := os.ReadFile(f.inputPath + ".prepared")
	if err != nil {
		t.Fatal(err)
	}
	seal := func(gen, address string, cert []byte) (int, string) {
		return frame(map[string]any{"op": "seal", "front_generation": gen,
			"front": map[string]any{"address": address, "certificate_base64": base64.StdEncoding.EncodeToString(cert)}})
	}
	good := frontFixtureCertificate(t, "127.0.0.1", true)
	for name, tc := range map[string]struct {
		gen, address string
		cert         []byte
	}{
		"wrong generation":  {strings.Repeat("b", 32), "127.0.0.1:41000", good},
		"not loopback":      {generation, "10.0.0.1:41000", good},
		"port zero":         {generation, "127.0.0.1:0", good},
		"SAN mismatch":      {generation, "127.0.0.1:41000", frontFixtureCertificate(t, "127.0.0.2", true)},
		"no ServerAuth":     {generation, "127.0.0.1:41000", frontFixtureCertificate(t, "127.0.0.1", false)},
		"not a certificate": {generation, "127.0.0.1:41000", []byte("not-der")},
	} {
		if rc, _ := seal(tc.gen, tc.address, tc.cert); rc != 64 {
			t.Fatalf("%s: bad seal accepted: rc=%d", name, rc)
		}
		if _, err := os.Stat(f.inputPath + ".prepared.seal"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: refused seal left a record: %v", name, err)
		}
	}
	rc, reply := seal(generation, "127.0.0.1:41000", good)
	var answer struct {
		TokenSHA256      string `json:"token_sha256"`
		CredentialHeader string `json:"credential_header"`
		CredentialPrefix string `json:"credential_prefix"`
	}
	if rc != 0 || json.Unmarshal([]byte(reply), &answer) != nil || answer.CredentialHeader != "Authorization" || answer.CredentialPrefix != "Bearer " {
		t.Fatalf("valid seal refused: rc=%d reply=%q", rc, reply)
	}
	token, err := os.ReadFile(f.inputPath + ".prepared.token")
	sum := sha256.Sum256(token)
	if err != nil || !svcendpoint.IsToken(string(token)) || hex.EncodeToString(sum[:]) != answer.TokenSHA256 {
		t.Fatalf("token leaf does not match the sealed hash: err=%v", err)
	}
	raw, err := os.ReadFile(f.inputPath + ".prepared.seal")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := callbrokerinput.DecodePreparedHTTPFrontSeal(raw)
	if err != nil || sealed.Generation != generation || sealed.BrokerStart != h.start || sealed.Address != "127.0.0.1:41000" ||
		bytes.Contains(raw, token) || strings.Contains(string(raw), answer.TokenSHA256) {
		t.Fatalf("seal does not bind the live start and address without the token: %+v err=%v", sealed, err)
	}
	if after, _ := os.ReadFile(f.inputPath + ".prepared"); !bytes.Equal(after, prepared) {
		t.Fatal("sealing mutated the prepared record")
	}
	if rc, _ := seal(generation, "127.0.0.1:41000", good); rc != 64 {
		t.Fatal("a second seal was accepted")
	}
	if rc, _ := act(strings.Repeat("b", 32)); rc != 64 || requests.Load() != 0 {
		t.Fatalf("wrong front_generation reached the origin: rc=%d requests=%d", rc, requests.Load())
	}
	smuggled := call
	smuggled.Headers = []HTTPHeader{{Name: "X-Echo", Values: []string{string(token)}}}
	if rc, _ := frame(map[string]any{"op": "act", "http": smuggled, "front_generation": generation}); rc != 64 || requests.Load() != 0 {
		t.Fatalf("frame carrying the sealed token was classified: rc=%d", rc)
	}
	// base64 hides the raw token from a search of the frame alone.
	inBody := call
	inBody.BodyPresent, inBody.Body = true, append(append([]byte(`{"note":"`), token...), `"}`...)
	if rc, out := frame(map[string]any{"op": "act", "http": inBody, "front_generation": generation}); rc != 64 || !strings.Contains(out, "outside its field") || requests.Load() != 0 {
		t.Fatalf("body carrying the sealed token was classified: rc=%d out=%q requests=%d", rc, out, requests.Load())
	}
	for _, leaf := range []string{".prepared.token", ".prepared.seal"} {
		if info, err := os.Lstat(f.inputPath + leaf); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s is not a private regular file: %v %v", leaf, info, err)
		}
	}
	if rc, out := act(generation); rc != 0 || requests.Load() != 1 {
		t.Fatalf("sealed generation did not send exactly once: rc=%d out=%q requests=%d", rc, out, requests.Load())
	}
}

func TestLegacyHTTPInputRefusesGenerationAndSeal(t *testing.T) {
	var requests atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	h, f := selectedHTTPReadFixture(t, origin, executor)
	frame := selectedHTTPRawFrames(t, h.Handle)
	call := HTTPCall{Method: "GET", Path: "/project"}
	if rc, _ := frame(map[string]any{"op": "act", "http": call, "front_generation": strings.Repeat("a", 32)}); rc != 64 || requests.Load() != 0 {
		t.Fatalf("legacy input accepted a front_generation: rc=%d requests=%d", rc, requests.Load())
	}
	cert := base64.StdEncoding.EncodeToString(frontFixtureCertificate(t, "127.0.0.1", true))
	if rc, _ := frame(map[string]any{"op": "seal", "front_generation": strings.Repeat("a", 32),
		"front": map[string]any{"address": "127.0.0.1:41000", "certificate_base64": cert}}); rc != 64 {
		t.Fatalf("legacy input accepted a seal: rc=%d", rc)
	}
	for _, leaf := range []string{".prepared.seal", ".prepared.token"} {
		if _, err := os.Stat(f.inputPath + leaf); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("legacy seal left %s: %v", leaf, err)
		}
	}
	if rc, out := frame(map[string]any{"op": "act", "http": call}); rc != 0 || requests.Load() != 1 {
		t.Fatalf("legacy frame without front_generation was refused: rc=%d out=%q requests=%d", rc, out, requests.Load())
	}
}

// TestHTTPFrontSealRefusesLeftoverLeavesOnDisk pins the on-disk guards with no
// in-memory seal to answer first: a leftover token, seal or link at either leaf
// refuses, and a seal write that fails after the token removes the token.
func TestHTTPFrontSealRefusesLeftoverLeavesOnDisk(t *testing.T) {
	good := frontFixtureCertificate(t, "127.0.0.1", true)
	request := httpFrontSealRequest{Address: "127.0.0.1:41000", CertificateBase64: base64.StdEncoding.EncodeToString(good)}
	noLeaves := func(t *testing.T, f *onceFixture, leaves ...string) {
		t.Helper()
		for _, leaf := range leaves {
			if _, err := os.Lstat(f.inputPath + leaf); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refused seal left %s: %v", leaf, err)
			}
		}
	}
	for _, tc := range []struct {
		name, leaf string
		link       bool
	}{
		{"leftover token", ".prepared.token", false},
		{"leftover seal", ".prepared.seal", false},
		{"dangling link at token", ".prepared.token", true},
		{"dangling link at seal", ".prepared.seal", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, f, generation := selectedPreparedHTTPReadFixture(t, "https://fixture.example", httpExecutor{})
			path := f.inputPath + tc.leaf
			if tc.link {
				if err := os.Symlink(f.inputPath+".missing-target", path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("leftover"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := h.seal(h.input.FrontJailID, generation, request); !errors.Is(err, errFrontAlreadySealed) {
				t.Fatalf("seal over a leftover %s was not refused as existing: %v", tc.leaf, err)
			}
			other := ".prepared.seal"
			if tc.leaf == other {
				other = ".prepared.token"
			}
			noLeaves(t, f, other, ".missing-target")
			if after, err := os.Lstat(path); err != nil || (after.Mode()&os.ModeSymlink != 0) != tc.link {
				t.Fatalf("the leftover %s was replaced: %v", tc.leaf, err)
			}
		})
	}
	t.Run("seal write fails after the token", func(t *testing.T) {
		h, f, generation := selectedPreparedHTTPReadFixture(t, "https://fixture.example", httpExecutor{})
		h.beforeSealWrite = func() {
			if err := os.WriteFile(f.inputPath+".prepared.seal", []byte("raced"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := h.seal(h.input.FrontJailID, generation, request); err == nil {
			t.Fatal("seal succeeded over a raced seal leaf")
		}
		noLeaves(t, f, ".prepared.token")
		if h.front.seal != nil || h.front.token != "" {
			t.Fatal("failed seal left in-memory front state")
		}
	})
	t.Run("fresh handler after a seal", func(t *testing.T) {
		h, f, generation := selectedPreparedHTTPReadFixture(t, "https://fixture.example", httpExecutor{})
		if _, err := h.seal(h.input.FrontJailID, generation, request); err != nil {
			t.Fatalf("first seal refused: %v", err)
		}
		fresh, err := newHTTPHandler(f.inputPath, f.home, f.store, h.audit)
		if err != nil {
			t.Fatal(err)
		}
		fresh.scopePath, fresh.bound, fresh.start = h.scopePath, h.bound, h.start
		if _, err := fresh.seal(fresh.input.FrontJailID, generation, request); !errors.Is(err, errFrontAlreadySealed) {
			t.Fatalf("a handler with no in-memory seal resealed over the leaves: %v", err)
		}
	})
}
