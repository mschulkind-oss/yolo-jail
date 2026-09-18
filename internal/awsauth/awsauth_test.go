package awsauth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestElidedPrintsTheProtocolShapeAndNoSecret(t *testing.T) {
	cred := Credential{
		AccessKeyID: "ASIAVISIBLE", SecretAccessKey: "zzsecretzz", SessionToken: "zztokenzz",
		ExpiresAtMS: time.Unix(1_700_003_600, 0).UnixMilli(),
	}
	encoded, err := json.Marshal(cred.Elided())
	if err != nil {
		t.Fatal(err)
	}
	// The FOUR FIELD NAMES are spelled out, so a reader of `--self-check` can see
	// the protocol shape — including that the token is called Token — without
	// seeing a secret.
	for _, key := range []string{"AccessKeyId", "SecretAccessKey", "Token", "Expiration"} {
		if !strings.Contains(string(encoded), `"`+key+`"`) {
			t.Errorf("the elided view omits %q: %s", key, encoded)
		}
	}
	for _, secret := range []string{"ASIAVISIBLE", "zzsecretzz", "zztokenzz"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the elided view carries %q: %s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), Fingerprint("ASIAVISIBLE")) {
		t.Errorf("the elided view does not identify the key by fingerprint: %s", encoded)
	}
}

func TestFingerprintIsStableAndNonReversible(t *testing.T) {
	if Fingerprint("") != "(none)" {
		t.Errorf("Fingerprint(\"\") = %q", Fingerprint(""))
	}
	a, b := Fingerprint("ASIAONE"), Fingerprint("ASIAONE")
	if a != b {
		t.Error("Fingerprint is not stable")
	}
	if strings.Contains(a, "ASIAONE") || len(a) != 8 {
		t.Errorf("Fingerprint = %q, want 8 hex characters of a digest", a)
	}
}

func TestCompleteRequiresAllFourFields(t *testing.T) {
	full := Credential{AccessKeyID: "a", SecretAccessKey: "s", SessionToken: "t", ExpiresAtMS: 1}
	if !full.Complete() {
		t.Error("a four-field credential is not complete")
	}
	for name, mutate := range map[string]func(Credential) Credential{
		"no key id":  func(c Credential) Credential { c.AccessKeyID = ""; return c },
		"no secret":  func(c Credential) Credential { c.SecretAccessKey = ""; return c },
		"no token":   func(c Credential) Credential { c.SessionToken = ""; return c },
		"no expiry":  func(c Credential) Credential { c.ExpiresAtMS = 0; return c },
		"past epoch": func(c Credential) Credential { c.ExpiresAtMS = -1; return c },
	} {
		if mutate(full).Complete() {
			t.Errorf("%s: reported complete", name)
		}
	}
}

// TestTheDefaultsCarryTheUnitsTheDesignFixed: three numbers with reasons, kept here
// so a change to any of them is a change to a test rather than a silent drift.
func TestTheDefaultsCarryTheUnitsTheDesignFixed(t *testing.T) {
	if RemintLead != 10*time.Minute {
		t.Errorf("RemintLead = %v, want 10m — twice the SDK's own 5-minute window", RemintLead)
	}
	if MintDuration != time.Hour {
		t.Errorf("MintDuration = %v, want 1h — the role-chaining ceiling, not raisable", MintDuration)
	}
	if len(SessionName) < 2 || len(SessionName) > 64 {
		t.Errorf("SessionName = %q, outside STS's 2-64 character constraint", SessionName)
	}
}
