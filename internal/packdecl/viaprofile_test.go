package packdecl

import (
	"strings"
	"testing"
)

// viaprofile_test.go pins the manifest half of OQ-WG6/WG7: a profile's `via` names a
// service pack, a service's `via_address` is the loopback address its per-agent routes
// are served under, and each is refused in the shapes that would point an agent nowhere.

func TestAProfileViaDecodesAndCopies(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"v","contributes":[
	  {"kind":"profile","name":"pz","provider":"zai","via":"wire-bridge"}]}`))
	if len(probs) != 0 {
		t.Fatalf("a via profile should decode, got %v", probs)
	}
	if got := m.Profiles(); len(got) != 1 || got[0].Via != "wire-bridge" {
		t.Errorf("Profiles() = %+v, want via wire-bridge", got)
	}
	if got := m.ProfileFor("pz"); got == nil || got.Via != "wire-bridge" {
		t.Errorf("ProfileFor(pz) = %+v, want via wire-bridge", got)
	}
}

func TestAProfileViaMustBeAPackName(t *testing.T) {
	for _, bad := range []string{"a=b", "..", "../x", "/abs", "a:b"} {
		_, probs := Decode([]byte(`{"name":"v","contributes":[
		  {"kind":"profile","name":"pz","provider":"zai","via":"` + bad + `"}]}`))
		if !strings.Contains(strings.Join(probs, "\n"), "is not a pack name") {
			t.Errorf("via %q: problems %v, want the pack-name refusal", bad, probs)
		}
	}
}

func TestAServiceViaAddressDecodes(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"b","contributes":[{"kind":"service","name":"b",
	  "endpoint":"b.endpoint","jail_daemon":{"cmd":["yolo-jaild","b"]},
	  "via_address":"http://127.0.0.1:8216"}]}`))
	if len(probs) != 0 {
		t.Fatalf("a via_address should decode, got %v", probs)
	}
	if got := m.Services(); len(got) != 1 || got[0].ViaAddress != "http://127.0.0.1:8216" {
		t.Errorf("Services() = %+v", got)
	}
}

func TestViaAddressProblemRefusesWhatNoRouteCanServe(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:8216", "http://localhost:1", "http://[::1]:9"} {
		if p := ViaAddressProblem(ok); p != "" {
			t.Errorf("%q refused: %s", ok, p)
		}
	}
	for _, bad := range []string{
		"https://127.0.0.1:8216",  // TLS: the listener is plain loopback HTTP
		"http://10.0.0.1:8216",    // not loopback
		"http://127.0.0.1",        // no port
		"http://127.0.0.1:8216/x", // a path: the per-agent prefix is the whole path
		"http://u@127.0.0.1:8216", // userinfo
		"http://127.0.0.1:8216?q", // query
	} {
		if p := ViaAddressProblem(bad); p == "" {
			t.Errorf("%q accepted, want a refusal", bad)
		}
	}
}

func TestViaAddressIsOnlyAServiceField(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"v","contributes":[
	  {"kind":"profile","name":"pz","provider":"zai","via_address":"http://127.0.0.1:8216"}]}`))
	if !strings.Contains(strings.Join(probs, "\n"), `does not take "via_address"`) {
		t.Errorf("problems %v, want via_address refused on a profile", probs)
	}
}

// The call site, not only the predicate: a manifest carrying an unservable via_address
// is refused by Decode.
func TestDecodeRefusesAnUnservableViaAddress(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"b","contributes":[{"kind":"service","name":"b",
	  "endpoint":"b.endpoint","jail_daemon":{"cmd":["yolo-jaild","b"]},
	  "via_address":"http://10.0.0.1:8216"}]}`))
	if !strings.Contains(strings.Join(probs, "\n"), "via_address") {
		t.Errorf("problems %v, want the non-loopback via_address refused", probs)
	}
}
