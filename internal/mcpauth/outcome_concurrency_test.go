package mcpauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOutcomeTwoBrokersAdoptOneSingleUseToken(t *testing.T) {
	f := outcomeFixture(t)
	other := f.newBroker(f.broker.Canonical.Dir)
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, broker := range []*OnceBroker{f.broker, other} {
		go func(b *OnceBroker) { <-start; done <- b.Pass(context.Background(), f.src, []OnceServer{f.server}) }(broker)
	}
	close(start)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("record-lock collision did not settle")
		}
	}
	record := f.record(other)
	if f.as.callCount() != 1 || record == nil || record.Generation != 1 {
		t.Fatal("losing broker redeemed or failed to reread the adopted record")
	}
	outcome := fixtureOutcome(t, f, other)
	if outcome.Code != OutcomeAdopted || outcome.Generation != 1 {
		t.Fatalf("wrong winning capture observation: %+v", outcome)
	}
	noOutcomeSecrets(t, outcome, f.events, "")
}

func TestOutcomeLatestNativeSignInAdvancesGeneration(t *testing.T) {
	f := outcomeFixture(t)
	f.pass(f.broker)
	f.as.mu.Lock()
	f.as.valid["rt-new"] = true
	f.as.mu.Unlock()
	entry := f.entry()
	entry["accessToken"] = "at-new"
	entry["refreshToken"] = "rt-new"
	entry["expiresAt"] = f.clock.Now().Add(2 * time.Hour).UnixMilli()
	entry["serverName"] = "plugin:newer:slack"
	f.writeEntry(entry)
	f.pass(f.broker)
	outcome := fixtureOutcome(t, f, f.newBroker(f.broker.Canonical.Dir))
	if outcome.Code != OutcomeAdopted || outcome.Generation != 2 || outcome.NativeName != "plugin:newer:slack" {
		t.Fatalf("new native sign-in not observed: %+v", outcome)
	}
	data, _ := json.Marshal(outcome)
	if string(data) == "" {
		t.Fatal("empty observation")
	}
	for _, secret := range []string{"rt-new", "at-new"} {
		sum := sha256.Sum256([]byte(secret))
		if strings.Contains(string(data), secret) || strings.Contains(string(data), hex.EncodeToString(sum[:])) {
			t.Fatal("new sign-in leaked through note")
		}
	}
}

func TestOutcomeSourceWritebackPendingKeepsCommittedObservation(t *testing.T) {
	f := outcomeFixture(t)
	lock := filepath.Join(f.dir, f.src.Store.LockDir)
	if err := os.Mkdir(lock, 0755); err != nil {
		t.Fatal(err)
	}
	f.pass(f.broker)
	if f.record(f.broker) == nil || f.entry()["refreshToken"] != "rt-1" {
		t.Fatal("fixture did not leave writeback pending")
	}
	outcome := fixtureOutcome(t, f, f.broker)
	if outcome.Code != OutcomeAdopted {
		t.Fatalf("pending writeback lost committed adoption: %+v", outcome)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	f.pass(f.broker)
	if f.entry()["refreshToken"] != nil || f.as.callCount() != 1 {
		t.Fatal("pending writeback redeemed the spent source token")
	}
}
