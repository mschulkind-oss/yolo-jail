package hostservice

import "testing"

func TestStartupOutcomeZeroIsUnknownNotReady(t *testing.T) {
	var outcome StartupOutcome
	if outcome.Known() {
		t.Fatal("zero-value startup outcome is known")
	}
	if outcome.Kind != StartupKindUnknown || outcome.Readiness != StartupReadinessUnknown ||
		outcome.Owner != StartupOwnerUnknown || outcome.Process != StartupProcessUnknown {
		t.Fatalf("zero-value outcome is not explicitly unknown: %+v", outcome)
	}
}

func TestStartupOutcomeSeparatesSpawnFromAcceptedReadiness(t *testing.T) {
	outcome := StartupOutcome{Owner: StartupOwnerLaunch, Service: "fixture", Attempt: 9,
		Phase: StartupPhaseSpawn, Kind: StartupKindDaemonStartFailed, Spawned: false,
		Readiness: StartupReadinessUnknown}
	if outcome.Kind == StartupKindReady || outcome.Readiness == StartupReadinessAccepted {
		t.Fatalf("failed spawn was interpreted as ready: %+v", outcome)
	}
	outcome = StartupOutcome{Owner: StartupOwnerLaunch, Service: "fixture", Attempt: 10,
		Phase: StartupPhaseReadiness, Kind: StartupKindReady, Spawned: true,
		Readiness: StartupReadinessAccepted}
	if !outcome.Known() || !outcome.Spawned || outcome.Attempt != 10 {
		t.Fatalf("accepted current-attempt outcome lost owner evidence: %+v", outcome)
	}
}

func TestStartupOutcomeRetainsClosedReasonClassWithoutWireAttribution(t *testing.T) {
	for _, class := range []string{"configuration", "dependency", "permission", "internal"} {
		if !HasCooperativeClass(class) {
			t.Errorf("valid cooperative class %q rejected", class)
		}
	}
	if HasCooperativeClass("secret-profile-name") {
		t.Fatal("open-ended reason class accepted")
	}
	outcome := StartupOutcome{Owner: StartupOwnerSingleton, Service: "fixture", Attempt: 3,
		Phase: StartupPhaseReadiness, Kind: StartupKindCooperativeRefusal,
		ReasonRead: StartupReasonReadOutcome{Kind: StartupReasonReadRecord, ReasonClass: "dependency",
			Reason: "safe cause", Remedy: "safe remedy"},
		ReasonClass: "dependency", Reason: "safe cause", Remedy: "safe remedy"}
	if outcome.ReasonRead.Kind != StartupReasonReadRecord || outcome.ReasonClass != "dependency" || outcome.Attempt != 3 {
		t.Fatalf("typed evidence not retained: %+v", outcome)
	}
	if outcome.Attempt == 0 {
		t.Fatal("local attempt identity was lost")
	}
}
