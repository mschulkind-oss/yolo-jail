package broker

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

func outcomeFromEnsured(t *testing.T, ensured Ensured) hostservice.StartupOutcome {
	t.Helper()
	field := reflect.ValueOf(ensured).FieldByName("Outcome")
	if !field.IsValid() {
		t.Fatal("actual EnsureSingleton result did not retain typed startup outcome")
	}
	outcome, ok := field.Interface().(hostservice.StartupOutcome)
	if !ok {
		t.Fatalf("EnsureSingleton outcome has type %s", field.Type())
	}
	// Every known exit, pre-spawn and reuse included, carries the owner and this call's attempt.
	if outcome.Known() && (outcome.Owner != hostservice.StartupOwnerSingleton || outcome.Attempt == 0) {
		t.Fatalf("known %s outcome lacks owner/attempt attribution: %+v", outcome.Kind, outcome)
	}
	return outcome
}

func TestEnsureSingletonReturnsTypedOwnerOutcomeForActualReadinessAndReason(t *testing.T) {
	for _, tc := range []struct {
		name        string
		class       string
		ready       bool
		childExited bool
		malformed   bool
	}{
		{name: "ready without reason", ready: true},
		{name: "ready with configuration reason", class: "configuration", ready: true},
		{name: "configuration refusal", class: "configuration", childExited: true},
		{name: "dependency refusal", class: "dependency", childExited: true},
		{name: "permission refusal", class: "permission", childExited: true},
		{name: "internal refusal", class: "internal", childExited: true},
		{name: "malformed current attempt", childExited: true, malformed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &fakeState{alive: map[int]bool{}, reachOK: false, spawnPID: 77}
			deps := newFakeDeps(t, state)
			deps.Name = "fixture-singleton"
			deps.StartupReason = true
			var frame []byte
			if tc.class != "" {
				record := hostservice.StartupReason{Version: 1, Service: "fixture-singleton",
					Attempt: "current-attempt", Class: tc.class, Reason: "safe reason", Remedy: "safe remedy"}
				body, _ := json.Marshal(record)
				frame = append(frame, make([]byte, 4)...)
				binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
				frame = append(frame, body...)
			} else if tc.malformed {
				frame = []byte{0, 0, 0, 3, '{', 'x', '}'}
			}
			reasonConn := &bufferedStartupReasonConn{reader: bytes.NewReader(frame)}
			deps.SpawnWithReason = func([]string, string, string) (int, func() bool, net.Conn, string, error) {
				return 77, func() bool { return tc.childExited }, reasonConn, "current-attempt", nil
			}
			deps.PathExists = func(string) bool { return false }
			state.now = time.Now()
			deps.waitForSocketUntil = func(_ string, deadline time.Time, _ func() bool, results chan startupReasonResult) bool {
				// Wait for the real protocol reader to classify this attempt, then return the
				// result to EnsureSingleton as a readiness-boundary race would.
				select {
				case result := <-results:
					results <- result
				case <-time.After(time.Second):
					t.Fatal("actual singleton reason reader did not publish")
				}
				if !tc.ready {
					state.now = deadline
				}
				return tc.ready
			}

			ensured := EnsureSingleton(deps)
			outcome := outcomeFromEnsured(t, ensured)
			if outcome.Owner != hostservice.StartupOwnerSingleton || outcome.Service != deps.Name || outcome.Attempt == 0 {
				t.Fatalf("owner/service/current-attempt attribution = %+v", outcome)
			}
			if tc.ready {
				if outcome.Kind != hostservice.StartupKindSocketObserved || outcome.Readiness != hostservice.StartupReadinessObserved ||
					!outcome.Spawned || outcome.Kind == hostservice.StartupKindReady {
					t.Fatalf("path observation was overstated as accepted readiness: %+v", outcome)
				}
				if tc.class == "" {
					if outcome.ReasonRead.Kind != hostservice.StartupReasonReadNoRecord {
						t.Fatalf("ready-without-reason evidence = %+v", outcome.ReasonRead)
					}
				} else if outcome.ReasonRead.Kind != hostservice.StartupReasonReadRecord || outcome.ReasonClass != tc.class ||
					ensured.StartupReason != nil {
					t.Fatalf("ready singleton reason changed established refusal authority: result=%+v legacy=%+v", outcome, ensured.StartupReason)
				}
				return
			}
			if ensured.Started != outcome.Spawned {
				t.Fatalf("legacy Started=%v disagrees with typed Spawned=%v", ensured.Started, outcome.Spawned)
			}
			if outcome.Process != hostservice.StartupProcessExited || outcome.ProcessExitStatusKnown {
				t.Fatalf("singleton exit evidence invented or lost status: %+v", outcome)
			}
			if tc.class != "" {
				if outcome.Kind != hostservice.StartupKindCooperativeRefusal || outcome.ReasonClass != tc.class ||
					outcome.Reason != "safe reason" || outcome.Remedy != "safe remedy" ||
					outcome.ReasonRead.Kind != hostservice.StartupReasonReadRecord {
					t.Fatalf("cooperative class/result was not consumed by EnsureSingleton: %+v", outcome)
				}
				if outcome.ReasonRead.Reason+outcome.Remedy+outcome.Reason != "safe reasonsafe remedysafe reason" {
					t.Fatal("safe result text unexpectedly changed")
				}
			} else if outcome.ReasonRead.Kind != hostservice.StartupReasonReadChannelFault ||
				outcome.ReasonRead.Fault != hostservice.StartupReasonFaultMalformedRecord {
				t.Fatalf("malformed current attempt was collapsed: %+v", outcome.ReasonRead)
			}
		})
	}
}

func TestEnsureSingletonDistinguishesPreparationAndPostStopMigrationFailures(t *testing.T) {
	t.Run("preparation after publication", func(t *testing.T) {
		state := &fakeState{alive: map[int]bool{42: true}, reachOK: true, spawnPID: 77}
		deps := newFakeDeps(t, state)
		writePID(t, deps, 42)
		var published, spawned bool
		deps.PublishSettings = func() error { published = true; return nil }
		deps.PrepareLocked = func() (func() error, error) { return nil, os.ErrPermission }
		deps.Spawn = func([]string, string) (int, func() bool, error) { spawned = true; return 77, nil, nil }
		outcome := outcomeFromEnsured(t, EnsureSingleton(deps))
		if !published || spawned || outcome.Kind != hostservice.StartupKindPreparationFailed ||
			outcome.Phase != hostservice.StartupPhasePreparation || outcome.Spawned {
			t.Fatalf("preparation outcome/publish/spawn = %+v published=%v spawned=%v", outcome, published, spawned)
		}
	})

	t.Run("migration after stop", func(t *testing.T) {
		state := &fakeState{alive: map[int]bool{42: true}, reachOK: true, spawnPID: 77}
		deps := newFakeDeps(t, state)
		writePID(t, deps, 42)
		var published, spawned, stopped bool
		deps.PublishSettings = func() error { published = true; return nil }
		deps.Kill = func(pid int, sig syscall.Signal) error {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.killed = append(state.killed, struct {
				pid int
				sig syscall.Signal
			}{pid, sig})
			state.alive[pid] = false
			stopped = true
			return nil
		}
		deps.PrepareLocked = func() (func() error, error) {
			return func() error { return errors.New("injected migration failure") }, nil
		}
		deps.Spawn = func([]string, string) (int, func() bool, error) { spawned = true; return 77, nil, nil }
		outcome := outcomeFromEnsured(t, EnsureSingleton(deps))
		if !published || !stopped || spawned || outcome.Kind != hostservice.StartupKindMigrationFailed ||
			outcome.Phase != hostservice.StartupPhaseMigration || outcome.Spawned {
			t.Fatalf("migration outcome/publish/stop/spawn = %+v published=%v stopped=%v spawned=%v", outcome, published, stopped, spawned)
		}
	})
}

func TestEnsureSingletonDistinguishesLockPublicationAndSpawnFailures(t *testing.T) {
	t.Run("lock", func(t *testing.T) {
		state := &fakeState{alive: map[int]bool{}}
		deps := newFakeDeps(t, state)
		deps.LockPath = t.TempDir()
		outcome := outcomeFromEnsured(t, EnsureSingleton(deps))
		if outcome.Kind != hostservice.StartupKindLockFailed || outcome.Phase != hostservice.StartupPhaseLock {
			t.Fatalf("lock outcome = %+v", outcome)
		}
	})
	t.Run("publication", func(t *testing.T) {
		state := &fakeState{alive: map[int]bool{}}
		deps := newFakeDeps(t, state)
		deps.PublishSettings = func() error { return os.ErrPermission }
		outcome := outcomeFromEnsured(t, EnsureSingleton(deps))
		if outcome.Kind != hostservice.StartupKindPublicationFailed || outcome.Phase != hostservice.StartupPhasePublication {
			t.Fatalf("publication outcome = %+v", outcome)
		}
	})
	t.Run("spawn", func(t *testing.T) {
		state := &fakeState{alive: map[int]bool{}, spawnErr: os.ErrNotExist}
		deps := newFakeDeps(t, state)
		deps.waitForSocketUntil = func(string, time.Time, func() bool, chan startupReasonResult) bool {
			t.Fatal("readiness ran after spawn failed")
			return false
		}
		deps.Spawn = func([]string, string) (int, func() bool, error) { return 0, nil, os.ErrNotExist }
		outcome := outcomeFromEnsured(t, EnsureSingleton(deps))
		if outcome.Kind != hostservice.StartupKindDaemonStartFailed || outcome.Phase != hostservice.StartupPhaseSpawn || outcome.Spawned {
			t.Fatalf("spawn outcome = %+v", outcome)
		}
	})
}

func TestEnsureSingletonReuseIsNotAcceptedEndpointEvidence(t *testing.T) {
	state := &fakeState{alive: map[int]bool{42: true}, reachOK: true}
	deps := newFakeDeps(t, state)
	writePID(t, deps, 42)
	if err := os.WriteFile(deps.SocketPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	outcome := outcomeFromEnsured(t, EnsureSingleton(deps))
	if outcome.Kind != hostservice.StartupKindReused || !outcome.Reused || outcome.Readiness != hostservice.StartupReadinessUnknown ||
		outcome.Spawned || outcome.Kind == hostservice.StartupKindReady {
		t.Fatalf("reuse result invented accepted endpoint readiness: %+v", outcome)
	}
}
