package mcpauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const outcomeVersion = 1

type OutcomeCode string

const (
	OutcomeAdopted         OutcomeCode = "adopted"
	OutcomeIssuerMismatch  OutcomeCode = "refused-issuer-mismatch"
	OutcomeClientMismatch  OutcomeCode = "refused-client-mismatch"
	OutcomeRefused         OutcomeCode = "refused-by-server"
	OutcomeTokenType       OutcomeCode = "unsupported-token-type"
	OutcomeRotatedNotSaved OutcomeCode = "rotated-not-saved"
	OutcomeDeferredNetwork OutcomeCode = "deferred-network"
	OutcomeSourceMalformed OutcomeCode = "source-malformed"
	OutcomeSourceUnsafe    OutcomeCode = "source-unsafe"
)

// Outcome is the last observed capture/refresh result, not reconstructed record
// history. Its closed fields cannot carry provider text, tokens or token hashes.
type Outcome struct {
	Version    int         `json:"version"`
	Server     string      `json:"server"`
	Identity   string      `json:"identity"`
	NativeName string      `json:"native_name,omitempty"`
	Code       OutcomeCode `json:"code"`
	At         time.Time   `json:"at"`
	Generation uint64      `json:"generation"`
}

func (o Outcome) validate() error {
	if o.Version != outcomeVersion || o.Server == "" || !validDigest(o.Identity) || o.At.IsZero() {
		return errors.New("mcp-auth: capture observation is unreadable; relaunch with a current yolo build")
	}
	switch o.Code {
	case OutcomeAdopted, OutcomeIssuerMismatch, OutcomeClientMismatch, OutcomeRefused, OutcomeTokenType, OutcomeRotatedNotSaved, OutcomeDeferredNetwork, OutcomeSourceMalformed, OutcomeSourceUnsafe:
		return nil
	}
	return errors.New("mcp-auth: unknown capture observation code; relaunch with a current yolo build")
}

func outcomeFilename(server, identity string) string {
	sum := sha256.Sum256([]byte(keyFor(server, identity)))
	return "outcome." + hex.EncodeToString(sum[:]) + ".json"
}

// ReadOutcome is an unlocked atomic-rename snapshot of an existing token-free
// sidecar. It creates/repairs nothing; unknown schemas are unavailable.
func (s *CanonicalStore) ReadOutcome(server, identity string) (Outcome, bool, error) {
	if server == "" || !validDigest(identity) {
		return Outcome{}, false, fs.ErrInvalid
	}
	root, err := paths.OpenStateDirRoot(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return Outcome{}, false, nil
	}
	if err != nil {
		return Outcome{}, false, err
	}
	defer root.Close()
	file, err := paths.OpenRegularFileBeneath(root, outcomeFilename(server, identity))
	if errors.Is(err, fs.ErrNotExist) {
		return Outcome{}, false, nil
	}
	if err != nil {
		return Outcome{}, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Mode().Perm() != 0600 {
		return Outcome{}, false, errors.New("mcp-auth: capture observation is unsafe; repair host state permissions and retry")
	}
	var outcome Outcome
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&outcome); err != nil {
		return Outcome{}, false, fmt.Errorf("mcp-auth: capture observation is unreadable; inspect host state and relaunch")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Outcome{}, false, errors.New("mcp-auth: capture observation has trailing data; inspect host state and relaunch")
	}
	if err := outcome.validate(); err != nil {
		return Outcome{}, false, err
	}
	if outcome.Server != server || outcome.Identity != identity {
		return Outcome{}, false, errors.New("mcp-auth: capture observation identity differs; relaunch")
	}
	return outcome, true, nil
}

var persistOutcome = renameInto

// writeOutcomeLocked is called only by the pass that owns this record's refresh
// lock. Failure to record an observation never invalidates a committed rotation.
func (b *OnceBroker) writeOutcomeLocked(server OnceServer, native string, code OutcomeCode, generation uint64) {
	outcome := Outcome{Version: outcomeVersion, Server: server.Name, Identity: server.Identity, NativeName: native, Code: code, At: b.now().UTC(), Generation: generation}
	if err := outcome.validate(); err != nil {
		b.warn("%s: the last capture observation could not be recorded; inspect host state permissions and relaunch", server.Name)
		return
	}
	body, err := json.Marshal(outcome)
	if err == nil {
		root, openErr := paths.OpenStateDirRoot(b.Canonical.Dir)
		if openErr != nil {
			err = openErr
		} else {
			err = persistOutcome(root, outcomeFilename(server.Name, server.Identity), body)
			root.Close()
		}
	}
	if err != nil {
		b.warn("%s: the last capture observation could not be recorded; inspect host state permissions and relaunch", server.Name)
	}
}

func (src OnceSource) nativeName(key string, raw json.RawMessage) string {
	if src.NativeName != nil {
		return src.NativeName(key, raw)
	}
	return ""
}

func refreshOutcome(err error) OutcomeCode {
	var failure *RefreshError
	if errors.As(err, &failure) && failure.Definitive {
		if failure.UnsupportedTokenType {
			return OutcomeTokenType
		}
		return OutcomeRefused
	}
	return OutcomeDeferredNetwork
}
