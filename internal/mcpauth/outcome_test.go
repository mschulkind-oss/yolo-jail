package mcpauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func outcomeFixture(t *testing.T) *onceFixture {
	t.Helper()
	f := newOnceFixture(t)
	f.src.NativeName = func(_ string, raw json.RawMessage) string {
		var entry struct {
			Name string `json:"serverName"`
		}
		if json.Unmarshal(raw, &entry) != nil {
			return ""
		}
		return entry.Name
	}
	return f
}

func fixtureOutcome(t *testing.T, f *onceFixture, b *OnceBroker) Outcome {
	t.Helper()
	outcome, found, err := b.Canonical.ReadOutcome(f.server.Name, f.server.Identity)
	if err != nil || !found {
		t.Fatalf("capture observation unavailable: found=%v err=%v", found, err)
	}
	return outcome
}

func noOutcomeSecrets(t *testing.T, outcome Outcome, events []Event, warnings string) {
	t.Helper()
	data, err := json.Marshal(struct {
		Outcome  Outcome
		Events   []Event
		Warnings string
	}{outcome, events, warnings})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"at-1", "rt-1", "at-2", "rt-2", "at-3", "rt-3"} {
		sum := sha256.Sum256([]byte(secret))
		if strings.Contains(string(data), secret) || strings.Contains(string(data), hex.EncodeToString(sum[:])) {
			t.Fatal("capture note/log/audit leaked a fixture token or token hash")
		}
	}
	if strings.Contains(string(data), slackKey) {
		t.Fatal("store key/hash replaced the native name")
	}
}

func TestOutcomeIsWrittenUnderTheRecordLockAndReadAfterRestart(t *testing.T) {
	f := outcomeFixture(t)
	original := persistOutcome
	t.Cleanup(func() { persistOutcome = original })
	writes := 0
	persistOutcome = func(root *os.Root, name string, data []byte) error {
		lock, err := root.Open("records." + f.server.Identity + ".refresh.lock")
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			unix.Flock(int(lock.Fd()), unix.LOCK_UN)
			t.Fatal("capture observation written without its record lock")
		} else if !errors.Is(err, unix.EWOULDBLOCK) {
			t.Fatal(err)
		}
		writes++
		return original(root, name, data)
	}
	f.pass(f.broker)
	restarted := f.newBroker(f.broker.Canonical.Dir)
	outcome := fixtureOutcome(t, f, restarted)
	if writes != 1 || outcome.Code != OutcomeAdopted || outcome.NativeName != "plugin:slack:slack" || outcome.Generation != 1 || outcome.Version != 1 {
		t.Fatalf("wrong durable observation: %+v writes=%d", outcome, writes)
	}
	noOutcomeSecrets(t, outcome, f.events, "")
}

func TestOutcomeRotatedNotSavedIsObservedNotReconstructed(t *testing.T) {
	for _, writable := range []bool{true, false} {
		t.Run(map[bool]string{true: "writable note", false: "failed note"}[writable], func(t *testing.T) {
			f := outcomeFixture(t)
			var warnings strings.Builder
			f.broker.Warn = func(line string) { warnings.WriteString(line) }
			originalCommit, originalNote := persistCanonical, persistOutcome
			t.Cleanup(func() { persistCanonical = originalCommit; persistOutcome = originalNote })
			persistCanonical = func(*os.Root, string, CanonicalFile) error {
				return errors.New("injected canonical persistence failure")
			}
			if !writable {
				persistOutcome = func(*os.Root, string, []byte) error { return errors.New("injected observation persistence failure") }
			}
			if err := f.broker.Pass(context.Background(), f.src, []OnceServer{f.server}); err == nil {
				t.Fatal("successful rotation with failed canonical commit was hidden")
			}
			persistCanonical, persistOutcome = originalCommit, originalNote
			if f.as.callCount() != 1 || f.record(f.broker) != nil || f.entry()["refreshToken"] != "rt-1" {
				t.Fatal("failed commit stripped source or committed a record")
			}
			restarted := f.newBroker(f.broker.Canonical.Dir)
			outcome, found, err := restarted.Canonical.ReadOutcome(f.server.Name, f.server.Identity)
			if err != nil || found != writable {
				t.Fatalf("restart reconstructed a failed note: found=%v err=%v", found, err)
			}
			if found && outcome.Code != OutcomeRotatedNotSaved {
				t.Fatalf("rotation loss not recorded: %+v", outcome)
			}
			noOutcomeSecrets(t, outcome, f.events, warnings.String())
			f.pass(restarted) // actual issuer rejects the now-spent original token
			after := fixtureOutcome(t, f, restarted)
			if after.Code != OutcomeRefused || f.as.callCount() != 2 || f.record(restarted) != nil {
				t.Fatalf("restart did not record actual definitive refusal: %+v", after)
			}
			f.pass(restarted)
			if f.as.callCount() != 2 {
				t.Fatal("same poll retried a definitively refused token")
			}
			noOutcomeSecrets(t, after, f.events, warnings.String())
		})
	}
}

func TestOutcomeNoteFailureCannotFailCommittedAdoption(t *testing.T) {
	f := outcomeFixture(t)
	original := persistOutcome
	t.Cleanup(func() { persistOutcome = original })
	persistOutcome = func(*os.Root, string, []byte) error { return errors.New("injected note failure") }
	f.pass(f.broker)
	if f.record(f.broker) == nil || f.entry()["refreshToken"] != nil || f.entry()["accessToken"] != "at-2" {
		t.Fatal("note failure invalidated canonical commit or writeback")
	}
	if _, found, err := f.broker.Canonical.ReadOutcome(f.server.Name, f.server.Identity); err != nil || found {
		t.Fatal("failed note was reconstructed")
	}
}

func TestOutcomeExpectedClientRequiresTheNativeClientField(t *testing.T) {
	for _, declared := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing value", false: "undeclared field"}[declared], func(t *testing.T) {
			f := outcomeFixture(t)
			f.server.ExpectedClientID = "client-1"
			if declared {
				entry := f.entry()
				delete(entry, "clientId")
				f.writeEntry(entry)
			} else {
				f.src.Fields.ClientID = ""
			}
			f.broker.Client.HTTP = &http.Client{Transport: failedOutcomeTransport{}}
			f.pass(f.broker)
			outcome := fixtureOutcome(t, f, f.broker)
			if outcome.Code != OutcomeClientMismatch || f.as.callCount() != 0 || f.record(f.broker) != nil {
				t.Fatalf("native client was guessed: %+v", outcome)
			}
		})
	}
}

type failedOutcomeTransport struct{}

func (failedOutcomeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("untrusted provider text: rt-1 at-1")
}

func TestOutcomeRecordsOnlyClosedProviderFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*onceFixture)
		code   OutcomeCode
		calls  int
	}{
		{name: "issuer mismatch", change: func(f *onceFixture) {
			entry := f.entry()
			entry["issuer"] = "https://other.invalid"
			f.writeEntry(entry)
		}, code: OutcomeIssuerMismatch},
		{name: "sender constrained token", change: func(f *onceFixture) { f.as.tokenType = "DPoP" }, code: OutcomeTokenType, calls: 1},
		{name: "provider code is not a token type", change: func(f *onceFixture) { f.as.fail = "dpop"; f.as.status = 400 }, code: OutcomeRefused, calls: 1},
		{name: "definitive provider refusal", change: func(f *onceFixture) { f.as.fail = "invalid_grant"; f.as.status = 400 }, code: OutcomeRefused, calls: 1},
		{name: "provider error text is not logged", change: func(f *onceFixture) { f.as.fail = "untrusted provider text rt-1 at-1"; f.as.status = 400 }, code: OutcomeRefused, calls: 1},
		{name: "transient token refusal", change: func(f *onceFixture) { f.as.fail = "temporarily_unavailable"; f.as.status = 503 }, code: OutcomeDeferredNetwork, calls: 1},
		{name: "transient discovery", change: func(f *onceFixture) { f.broker.Client.HTTP = &http.Client{Transport: failedOutcomeTransport{}} }, code: OutcomeDeferredNetwork},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := outcomeFixture(t)
			var warnings strings.Builder
			f.broker.Warn = func(line string) { warnings.WriteString(line) }
			tc.change(f)
			f.pass(f.broker)
			outcome := fixtureOutcome(t, f, f.newBroker(f.broker.Canonical.Dir))
			if outcome.Code != tc.code || f.as.callCount() != tc.calls || f.record(f.broker) != nil {
				t.Fatalf("wrong closed failure: %+v calls=%d", outcome, f.as.callCount())
			}
			noOutcomeSecrets(t, outcome, f.events, warnings.String())
		})
	}
}

func TestOutcomeMalformedUnsafeAndMissingSourcesNeverSignOut(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state SourceState
	}{{"malformed", SourceMalformed}, {"unsafe", SourceUnsafe}, {"absent", SourceAbsent}, {"unsafe-mode", SourceUnsafe}} {
		t.Run(tc.name, func(t *testing.T) {
			state := tc.state
			f := outcomeFixture(t)
			f.pass(f.broker)
			file := filepath.Join(f.dir, ".credentials.json")
			switch tc.name {
			case "unsafe-mode":
				if err := os.Chmod(file, 0644); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(file, []byte(`{"mcpOAuth":`), 0600); err != nil {
					t.Fatal(err)
				}
			case "unsafe":
				if err := os.Rename(file, file+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(".credentials.json.real", file); err != nil {
					t.Fatal(err)
				}
			case "absent":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := f.src.Store.Snapshot()
			if snapshot.State != state {
				t.Fatalf("classification=%s want=%s", snapshot.State, state)
			}
			f.pass(f.broker)
			record := f.record(f.broker)
			if record == nil || record.State != "live" || f.as.callCount() != 1 {
				t.Fatal("unparsed source signed out/refreshed or rewrote record")
			}
			outcome := fixtureOutcome(t, f, f.broker)
			want := OutcomeAdopted
			if state == SourceMalformed {
				want = OutcomeSourceMalformed
			}
			if state == SourceUnsafe {
				want = OutcomeSourceUnsafe
			}
			if outcome.Code != want {
				t.Fatalf("source observation=%+v", outcome)
			}
			noOutcomeSecrets(t, outcome, f.events, "")
		})
	}
}

func TestOutcomeReaderRefusesUnknownSchemaWithoutRepair(t *testing.T) {
	f := outcomeFixture(t)
	f.pass(f.broker)
	file := filepath.Join(f.broker.Canonical.Dir, outcomeFilename(f.server.Name, f.server.Identity))
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var outcome Outcome
	if err := json.Unmarshal(body, &outcome); err != nil {
		t.Fatal(err)
	}
	outcome.Version = 999
	body, _ = json.Marshal(outcome)
	if err := os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.broker.Canonical.ReadOutcome(f.server.Name, f.server.Identity); err == nil || found {
		t.Fatal("unknown schema treated as current observation")
	}
	after, err := os.ReadFile(file)
	if err != nil || string(after) != string(body) {
		t.Fatal("strict observation reader repaired unknown schema")
	}
}
