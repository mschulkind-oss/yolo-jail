package callbroker

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
)

// The host publishes a selected client's URL, token and trust only through the production
// operation, only to its own sealed live generation, and only for a published client/setup cell.
// With production's cell list (empty) nothing is published even after a valid seal.
func TestHTTPClientPublicationOnlyForASealedPublishedCell(t *testing.T) {
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h, f, generation := selectedPreparedHTTPReadFixture(t, origin, executor)
	if h.published == nil || h.published(callbrokerinput.HTTPClientCell{Pack: "client-pack", Bin: "fixture-http-client", RecipeIdentity: "sha256:fixture", Setup: "podman-self"}) {
		t.Fatal("the production handler does not consult the production cell list, or that list publishes the fixture cell")
	}
	var listed atomic.Bool
	want := callbrokerinput.HTTPClientCell{Pack: "client-pack", Bin: "fixture-http-client", RecipeIdentity: "sha256:fixture", Setup: "podman-self"}
	h.published = func(c callbrokerinput.HTTPClientCell) bool { return listed.Load() && c == want }
	frame, endpoint := selectedHTTPRawFramesAt(t, h.Handle)
	fetch := func(gen, bin string) (callbrokerinput.HTTPClientPublication, error) {
		return FetchHTTPClientPublication(context.Background(), endpoint, gen, bin)
	}

	listed.Store(true)
	if _, err := fetch(generation, "fixture-http-client"); err == nil || !strings.Contains(err.Error(), "has not sealed") {
		t.Fatalf("an unsealed generation was published: %v", err)
	}
	good := frontFixtureCertificate(t, "127.0.0.1", true)
	if rc, out := frame(map[string]any{"op": "seal", "front_generation": generation,
		"front": map[string]any{"address": "127.0.0.1:41000", "certificate_base64": base64.StdEncoding.EncodeToString(good)}}); rc != 0 {
		t.Fatalf("seal refused: rc=%d out=%q", rc, out)
	}
	token, err := os.ReadFile(f.inputPath + ".prepared.token")
	if err != nil {
		t.Fatal(err)
	}

	listed.Store(false)
	if _, err := fetch(generation, "fixture-http-client"); err == nil || !strings.Contains(err.Error(), "is not published under setup podman-self") {
		t.Fatalf("an unpublished cell was published: %v", err)
	}
	listed.Store(true)
	for name, tc := range map[string]struct{ gen, bin, want string }{
		"another client":     {generation, "other-client", "not a selected client"},
		"another generation": {strings.Repeat("b", 32), "fixture-http-client", "does not match this launch"},
	} {
		if _, err := fetch(tc.gen, tc.bin); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, bad := range []string{
		`{"op":"client","front_generation":"` + generation + `","client":"fixture-http-client","extra":"x"}`,
		`{"op":"client","front_generation":"` + generation + `","client":7}`,
		`{"op":"client","front_generation":"` + generation + `"}`,
	} {
		if validateHTTPBrokerFrame([]byte(bad)) == nil {
			t.Fatalf("ingress accepts the malformed publication frame %s", bad)
		}
	}
	if _, err := h.publish("another-jail", generation, "fixture-http-client"); err == nil || !strings.Contains(err.Error(), "this launch's jail") {
		t.Fatalf("another jail was published to: %v", err)
	}

	pub, err := fetch(generation, "fixture-http-client")
	if err != nil {
		t.Fatalf("the sealed, published cell was refused: %v", err)
	}
	if pub.Token != string(token) || pub.URL != "https://127.0.0.1:41000" || pub.CertificateBase64 != base64.StdEncoding.EncodeToString(good) ||
		pub.TokenVariable != "FIXTURE_API_TOKEN" || pub.URLVariable != "FIXTURE_BASE_URL" || pub.TrustVariable != "SSL_CERT_FILE" {
		t.Fatalf("publication does not carry the sealed token, address, certificate and the plan's variables: %+v", pub)
	}
	if again, err := fetch(generation, "fixture-http-client"); err != nil || again != pub {
		t.Fatalf("a second ask (an attach) did not adopt the same generation's values: %+v %v", again, err)
	}
	if audit := mustRead(t, filepath.Join(f.home, "audit.jsonl")); bytes.Contains(audit, token) || !bytes.Contains(audit, []byte(`"event":"publish"`)) {
		t.Fatal("publication is not audited, or its audit carries the token")
	}

	h.withdrawFront()
	if _, err := fetch(generation, "fixture-http-client"); err == nil || !strings.Contains(err.Error(), "withdrawn") {
		t.Fatalf("a withdrawn front was published: %v", err)
	}
}

// A legacy G3 input (no prepared generation) publishes nothing.
func TestLegacyHTTPInputPublishesNoClient(t *testing.T) {
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h, _ := selectedHTTPReadFixture(t, origin, executor)
	h.published = func(callbrokerinput.HTTPClientCell) bool { return true }
	frame := selectedHTTPRawFrames(t, h.Handle)
	if rc, out := frame(map[string]any{"op": "client", "front_generation": strings.Repeat("a", 32), "client": "fixture-http-client"}); rc != 64 || !strings.Contains(out, "no selected HTTP front generation") {
		t.Fatalf("legacy input published a client: rc=%d out=%q", rc, out)
	}
}

func TestHTTPClientPublicationValidate(t *testing.T) {
	ok := callbrokerinput.HTTPClientPublication{Token: "t", URL: "https://127.0.0.1:4000", CertificateBase64: "AA==",
		TokenVariable: "A", URLVariable: "B", TrustVariable: "C"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*callbrokerinput.HTTPClientPublication){
		"plain http":        func(p *callbrokerinput.HTTPClientPublication) { p.URL = "http://127.0.0.1:4000" },
		"not loopback":      func(p *callbrokerinput.HTTPClientPublication) { p.URL = "https://10.0.0.1:4000" },
		"repeated variable": func(p *callbrokerinput.HTTPClientPublication) { p.URLVariable = "A" },
		"bad variable":      func(p *callbrokerinput.HTTPClientPublication) { p.TrustVariable = "X=Y" },
		"no token":          func(p *callbrokerinput.HTTPClientPublication) { p.Token = "" },
		"newline token":     func(p *callbrokerinput.HTTPClientPublication) { p.Token = "a\nb" },
	} {
		p := ok
		mutate(&p)
		if p.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
