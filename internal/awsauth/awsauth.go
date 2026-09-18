// Package awsauth turns a live host `aws sso login` into a short-lived, narrowed
// credential and answers for it. It is the HOST half of docs/design/sso-backed-bedrock.md
// — the state, the lock, the mint and the narrowing — with no transport in it: the
// daemon wrapping it is internal/awsauthdaemon and the jail-side adapter that speaks
// the container-credentials protocol is internal/awscredadapter.
//
// # It shells out to the `aws` CLI, and that is a decision rather than an expedient
//
// Nothing AWS is vendored: vendor/ holds no AWS code, the nix build is hermetic, and
// aws-sdk-go-v2 is a large committed tree. More importantly the refresh is INHERITED —
// `aws configure export-credentials` refreshes the SSO access token itself wherever a
// refresh token exists, which is the design's P2 by construction rather than by code
// here. The whole AWS surface is therefore one seam (Runner, mint.go), and no test in
// this package may execute the real binary or touch the network.
//
// # The rename that the SDK will not name for you
//
// `aws … --format process` spells the session token `SessionToken`. The
// container-credentials protocol every AWS SDK already knows how to ask spells it
// `Token`, and rejects a body missing that field WITHOUT naming it. The rename happens
// in exactly one place, Credential.ContainerCredentials, and is pinned by a test.
//
// # What it never does
//
// It never runs a login. Refreshing an access token inside a live session is expected
// (P2); starting a new session is a browser flow and the human's. A lapsed session is a
// MESSAGE — the 4xx body below, carrying `aws sso login --profile X` verbatim — never a
// request this package files (OQ-SSO6). It also never holds an access token across
// mints: every mint re-reads the host's SSO cache by shelling out afresh, which is what
// makes "log in again and carry on" work with no restart.
package awsauth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// RemintLead is how much remaining life makes a cached credential due for
// re-minting: twice the SDK's own 5-minute refresh window, so the cache is
// always warm by the time an agent asks (design §8, "Defaults, with units").
const RemintLead = 10 * time.Minute

// MintDuration is the requested session duration for the AssumeRole arms. It is
// the role-chaining ceiling and is not raisable: IAM Identity Center has already
// handed out a role session, so this AssumeRole is chaining and STS caps the
// result at an hour whatever the role says. A shorter value would only add mints.
const MintDuration = 3600 * time.Second

// SessionName is the RoleSessionName every AssumeRole here uses. Fixed rather
// than configurable so it is one greppable string in CloudTrail; it satisfies
// STS's [\w+=,.@-]{2,64} constraint.
const SessionName = "yolo-jail"

// StateFileName is the basename of the minted-credential cache.
const StateFileName = "credentials.json"

// LockFileName is the basename of the host-wide mint lock, kept BESIDE the state
// file so every process that can write the cache contends for the same inode.
const LockFileName = "mint.lock"

// Credential is one minted, narrowed AWS credential — the four fields the
// container-credentials protocol carries, plus the bookkeeping the cache needs.
//
// Times are Unix milliseconds in the persisted form so the file does not depend
// on a locale or time-zone parser; the WIRE form is RFC3339, which is what
// ContainerCredentials renders.
type Credential struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	// SessionToken is what `aws` calls it. ContainerCredentials publishes it as
	// "Token"; see the package comment.
	SessionToken string `json:"session_token"`
	ExpiresAtMS  int64  `json:"expires_at"`
	MintedAtMS   int64  `json:"minted_at"`
	// Narrowing records WHICH arm minted this credential, so a cache entry left
	// by a differently-configured daemon is a miss rather than a silent serve of
	// a wider credential than the current configuration asked for.
	Narrowing string `json:"narrowing"`
	// NarrowingDigest is the fingerprint of the resolved narrowing (role ARN and
	// session policy). Same reason as Narrowing, one level finer.
	NarrowingDigest string `json:"narrowing_digest"`
}

// ExpiresAt is the credential's expiry as a time.
func (c Credential) ExpiresAt() time.Time { return time.UnixMilli(c.ExpiresAtMS) }

// Complete reports whether this credential can be published over the
// container-credentials protocol at all.
//
// ALL FOUR FIELDS ARE REQUIRED, and the missing one is usually SessionToken: a
// profile holding long-term static keys exports an AccessKeyId and a
// SecretAccessKey and nothing else. Design §8 says such a profile is "served the
// same way" and it is — no SSO-only check exists anywhere in this package, and
// the un-narrowed arm resolves any profile the `aws` CLI can resolve. What the
// protocol cannot carry is a credential with no session token and no expiry, so
// that case becomes the named 4xx below rather than a body the SDK rejects
// without saying which field it wanted.
func (c Credential) Complete() bool {
	return c.AccessKeyID != "" && c.SecretAccessKey != "" && c.SessionToken != "" && c.ExpiresAtMS > 0
}

// ContainerCredentials renders the 200 body of the container-credentials
// protocol: exactly four strings, nothing else read by any SDK.
//
// THIS IS THE ONLY PLACE THE RENAME HAPPENS. The adapter forwards this map
// verbatim, so there is one spelling of `Token` in the tree.
func (c Credential) ContainerCredentials() map[string]any {
	return map[string]any{
		"AccessKeyId":     c.AccessKeyID,
		"SecretAccessKey": c.SecretAccessKey,
		"Token":           c.SessionToken,
		"Expiration":      c.ExpiresAt().UTC().Format(time.RFC3339),
	}
}

// Fingerprint is the non-reversible, stable credential identifier used in
// diagnostics and in the status view. A raw credential must never be logged.
func (c Credential) Fingerprint() string { return Fingerprint(c.AccessKeyID) }

// Fingerprint is the non-reversible identifier for any secret-ish string.
func Fingerprint(s string) string {
	if s == "" {
		return "(none)"
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// Elided renders a credential for a human: the key id's fingerprint, the expiry,
// and the SECRET REPLACED. `--self-check` prints this, and the four field names
// are spelled out so a reader can see the protocol shape without seeing a secret.
func (c Credential) Elided() map[string]any {
	return map[string]any{
		"AccessKeyId":     c.Fingerprint(),
		"SecretAccessKey": "(elided)",
		"Token":           "(elided)",
		"Expiration":      c.ExpiresAt().UTC().Format(time.RFC3339),
	}
}

// FailureKind classifies a mint failure by what a human would have to do about
// it. Every one of these is observable and none is silent (design §8).
type FailureKind string

const (
	// FailureLoginRequired: the SSO session lapsed or was never established. The
	// message carries the login command verbatim and nothing here runs it.
	FailureLoginRequired FailureKind = "login_required"
	// FailureCLIMissing: the `aws` CLI is not on the host. This is a LOUD SPAWN
	// FAILURE, not a per-request condition — and deliberately not a
	// requires.command_on_path probe, which is the shape that removed the Claude
	// broker for exactly the user it existed for.
	FailureCLIMissing FailureKind = "cli_missing"
	// FailureProfileMissing: the configured profile is not in ~/.aws/config.
	// Distinguished from FailureLoginRequired because advising `aws sso login
	// --profile X` for a profile that does not exist is actively wrong advice.
	FailureProfileMissing FailureKind = "profile_missing"
	// FailureRejected: AWS answered and said no (STS refused the role or the
	// session policy). STS's own message is forwarded.
	FailureRejected FailureKind = "rejected"
	// FailureUnavailable: anything else — a network fault, an unparseable
	// response, a timeout.
	FailureUnavailable FailureKind = "unavailable"
)

// MintError is a mint failure in the shape the container-credentials protocol
// carries it: a Code and a Message, both surfaced on the error the SDK raises.
//
// Message IS jail-visible and IS agent-visible, so it holds only what a human
// needs: a command to run, or AWS's own refusal text. Never a credential.
type MintError struct {
	Kind    FailureKind
	Code    string
	Message string
}

func (e *MintError) Error() string { return e.Code + ": " + e.Message }

// ContainerError renders the 4xx body: `Code` and `Message`, the two fields the
// SDK puts on the error it raises.
func (e *MintError) ContainerError() map[string]any {
	return map[string]any{"Code": e.Code, "Message": e.Message}
}

// loginRequired builds the 4xx a lapsed session gets. The command is spelled in
// full because the agent's error text is where a human reads it (OQ-SSO6: a
// lapsed session is a message, not a request).
func loginRequired(profile, detail string) *MintError {
	msg := fmt.Sprintf("the AWS SSO session for profile %q has expired or was never "+
		"established — on the HOST run: aws sso login --profile %s", profile, profile)
	if detail != "" {
		msg += " (aws said: " + detail + ")"
	}
	return &MintError{Kind: FailureLoginRequired, Code: "ExpiredToken", Message: msg}
}
