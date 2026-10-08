package hostservice

// StartupOwner identifies the process-lifetime owner of one startup attempt.
type StartupOwner string

const (
	StartupOwnerUnknown   StartupOwner = ""
	StartupOwnerSingleton StartupOwner = "shared-singleton"
	StartupOwnerLaunch    StartupOwner = "launch-owned-child"
)

// StartupPhase identifies the last startup boundary reached without encoding its owner policy.
type StartupPhase string

const (
	StartupPhaseUnknown     StartupPhase = ""
	StartupPhasePreflight   StartupPhase = "preflight"
	StartupPhaseLock        StartupPhase = "lock"
	StartupPhasePublication StartupPhase = "publication"
	StartupPhasePreparation StartupPhase = "preparation"
	StartupPhaseMigration   StartupPhase = "migration"
	StartupPhaseSpawn       StartupPhase = "spawn"
	StartupPhaseChannel     StartupPhase = "reason-channel"
	StartupPhaseReadiness   StartupPhase = "readiness"
	StartupPhaseFront       StartupPhase = "front"
	StartupPhaseEndpoint    StartupPhase = "endpoint"
)

// StartupKind is the primary observation from one owner-local start attempt.
type StartupKind string

const (
	StartupKindUnknown              StartupKind = ""
	StartupKindReady                StartupKind = "ready"
	StartupKindReused               StartupKind = "reused"
	StartupKindSocketObserved       StartupKind = "socket-observed"
	StartupKindPreflightRefused     StartupKind = "preflight-refused"
	StartupKindValidatorTimedOut    StartupKind = "validator-timed-out"
	StartupKindValidatorStartFailed StartupKind = "validator-start-failed"
	StartupKindPreparationFailed    StartupKind = "preparation-failed"
	StartupKindMigrationFailed      StartupKind = "migration-failed"
	StartupKindLockFailed           StartupKind = "lock-failed"
	StartupKindPublicationFailed    StartupKind = "publication-failed"
	StartupKindChannelSetupFailed   StartupKind = "channel-setup-failed"
	StartupKindDaemonStartFailed    StartupKind = "daemon-start-failed"
	StartupKindCooperativeRefusal   StartupKind = "cooperative-refusal"
	StartupKindProcessExited        StartupKind = "process-exited"
	StartupKindReadinessTimedOut    StartupKind = "readiness-timed-out"
	StartupKindChannelFault         StartupKind = "channel-fault"
	StartupKindTransportFailed      StartupKind = "transport-failed"
)

// StartupReadiness says what the owner actually established; process creation alone is not ready.
type StartupReadiness string

const (
	StartupReadinessUnknown  StartupReadiness = ""
	StartupReadinessNotReady StartupReadiness = "not-ready"
	StartupReadinessObserved StartupReadiness = "observed"
	StartupReadinessAccepted StartupReadiness = "accepted"
)

// StartupProcessState is limited to what the owning path observed.
type StartupProcessState string

const (
	StartupProcessUnknown StartupProcessState = ""
	StartupProcessAlive   StartupProcessState = "alive"
	StartupProcessExited  StartupProcessState = "exited"
)

// StartupReasonReadKind keeps absence and owner cancellation distinct from channel faults.
type StartupReasonReadKind string

const (
	StartupReasonReadUnknown      StartupReasonReadKind = ""
	StartupReasonReadNotEnabled   StartupReasonReadKind = "not-enabled"
	StartupReasonReadRecord       StartupReasonReadKind = "record"
	StartupReasonReadNoRecord     StartupReasonReadKind = "no-record"
	StartupReasonReadCancelled    StartupReasonReadKind = "owner-cancelled"
	StartupReasonReadChannelFault StartupReasonReadKind = "fault"
)

// StartupReasonReadPhase records how far a bounded reason read progressed.
type StartupReasonReadPhase string

const (
	StartupReasonReadPhaseUnknown StartupReasonReadPhase = ""
	StartupReasonReadPhasePrefix  StartupReasonReadPhase = "prefix"
	StartupReasonReadPhaseBody    StartupReasonReadPhase = "body"
	StartupReasonReadPhaseDecode  StartupReasonReadPhase = "decode"
)

// StartupReasonReadFault is a closed, non-user-rendered code; raw channel errors are not retained.
type StartupReasonReadFault string

const (
	StartupReasonFaultNone            StartupReasonReadFault = ""
	StartupReasonFaultDeadlineInstall StartupReasonReadFault = "deadline-install"
	StartupReasonFaultPartialPrefix   StartupReasonReadFault = "partial-prefix"
	StartupReasonFaultPartialBody     StartupReasonReadFault = "partial-body"
	StartupReasonFaultFrameLength     StartupReasonReadFault = "frame-length"
	StartupReasonFaultMalformedRecord StartupReasonReadFault = "malformed-record"
	StartupReasonFaultAttribution     StartupReasonReadFault = "attribution"
	StartupReasonFaultEmptyReason     StartupReasonReadFault = "empty-reason"
	StartupReasonFaultUnavailable     StartupReasonReadFault = "unavailable"
	StartupReasonFaultRead            StartupReasonReadFault = "read"
)

// StartupReasonReadCause distinguishes an empty peer close from a bounded deadline.
type StartupReasonReadCause string

const (
	StartupReasonReadCauseNone     StartupReasonReadCause = ""
	StartupReasonReadCauseEOF      StartupReasonReadCause = "eof"
	StartupReasonReadCauseDeadline StartupReasonReadCause = "deadline"
)

// StartupReasonReadOutcome is the typed result of one attempt-channel read. It deliberately
// carries no raw error, frame bytes, token or environment value.
type StartupReasonReadOutcome struct {
	Kind        StartupReasonReadKind
	Phase       StartupReasonReadPhase
	Fault       StartupReasonReadFault
	Cause       StartupReasonReadCause
	Bytes       int
	ReasonClass string
	Reason      string
	Remedy      string
}

// StartupOutcome is value-based, launch-local evidence. It is not serialized and makes no
// disposition decision; an absent/cancelled channel never means ready, and Started is separate.
type StartupOutcome struct {
	Owner                  StartupOwner
	Service                string
	Attempt                uint64
	Phase                  StartupPhase
	Kind                   StartupKind
	Readiness              StartupReadiness
	Reused                 bool
	Spawned                bool
	Process                StartupProcessState
	ProcessExitStatusKnown bool
	ProcessExitStatus      int
	PreviousProcess        StartupProcessState
	PreviousStopRequested  bool
	ReasonRead             StartupReasonReadOutcome
	ReasonClass            string
	Reason                 string
	Remedy                 string
}

// Known reports whether this value contains a primary outcome rather than its safe zero value.
func (o StartupOutcome) Known() bool { return o.Kind != StartupKindUnknown }

// HasCooperativeClass accepts only the protocol's closed reason vocabulary.
func HasCooperativeClass(class string) bool { return validStartupReasonClass(class) }
