package brokerscope

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// DeliveredAsks is launch provenance only: neither current workspace authority,
// executable permission nor evidence that a daemon consumes policy snapshots.
// Raw JSON preserves false/null/absent versus mappings and explicit cell presence.
type DeliveredAsks struct {
	Requests json.RawMessage `json:"requests"`
	Scopes   json.RawMessage `json:"scopes"`
	PackRead json.RawMessage `json:"pack_read"`
	Model    string          `json:"model"`
}

type DeliveredBaseline struct {
	Version           string        `json:"version"`
	Binding           PolicyBinding `json:"binding"`
	Start             string        `json:"start"`
	Asks              DeliveredAsks `json:"asks"`
	ReadStanding      bool          `json:"read_standing"`
	ReadGrantStanding string        `json:"read_grant_standing"`
	ReadWithheld      bool          `json:"read_withheld"`
}

func DeliveredBaselinePath(f File) string {
	return filepath.Join(filepath.Dir(paths.BrokerScopeFile(f.Source, f.LaunchID)), ".delivered-"+f.LaunchID+".json")
}

func validateDelivered(f File, b DeliveredBaseline, declaration, input string) error {
	if !loopholedecl.ValidBrokerSourceName(f.Source) || f.LaunchID == "" || strings.ContainsAny(f.LaunchID, "/\\.") || b.Version != "yolo.delivered-asks/1" || b.Binding != (PolicyBinding{Source: f.Source, Workspace: f.Workspace, LaunchID: f.LaunchID, DeclarationHash: declaration, InputDigest: input}) || !validPolicyDigest(declaration) || !validPolicyDigest(input) || len(b.Start) != 32 || (f.BrokerStart != "" && b.Start != f.BrokerStart) {
		return errors.New("delivered asks have no matching input/declaration/launch/lease")
	}
	if _, err := hex.DecodeString(b.Start); err != nil {
		return err
	}
	for _, data := range []json.RawMessage{b.Asks.Requests, b.Asks.Scopes, b.Asks.PackRead} {
		if len(data) == 0 || !json.Valid(data) {
			return errors.New("delivered complete asks are missing")
		}
	}
	if !requestObject(b.Asks.Requests) || !requestObject(b.Asks.Scopes) {
		return errors.New("delivered request/scoped ask shape unknown")
	}
	if b.Asks.Model != "legacy/1" && b.Asks.Model != "scoped/1" {
		return errors.New("delivered model unknown")
	}
	return policyDirectories(f)
}

// PublishDeliveredLocked is the initial gate/launch publisher. Caller holds the
// source lock. It never overwrites another delivered view or changes scope/asks.
func PublishDeliveredLocked(f File, b DeliveredBaseline) (DeliveredBaseline, error) {
	b.Version = "yolo.delivered-asks/1"
	if b.Start == "" {
		if f.BrokerStart != "" {
			b.Start = f.BrokerStart
		} else {
			var token [16]byte
			if _, err := rand.Read(token[:]); err != nil {
				return b, err
			}
			b.Start = hex.EncodeToString(token[:])
		}
	}
	if err := validateDelivered(f, b, b.Binding.DeclarationHash, b.Binding.InputDigest); err != nil {
		return b, err
	}
	path := DeliveredBaselinePath(f)
	if _, err := os.Lstat(path); err == nil {
		return b, errors.New("delivered launch view already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return b, err
	}
	data, err := json.Marshal(b)
	if err != nil || len(data) > 1<<20 {
		return b, errors.New("delivered view is oversized or unencodable")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".delivered-prepare-*")
	if err != nil {
		return b, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return b, err
	}
	if err := file.Close(); err != nil {
		return b, err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return b, err
	}
	return ReadDelivered(f, b.Binding.DeclarationHash, b.Binding.InputDigest)
}

// ReadDelivered uses deterministic private launch state, not approval/boot files.
// An initial Main reads before lease binding; later readers require its exact start.
func ReadDelivered(f File, declaration, input string) (DeliveredBaseline, error) {
	var b DeliveredBaseline
	if !loopholedecl.ValidBrokerSourceName(f.Source) || f.LaunchID == "" || strings.ContainsAny(f.LaunchID, "/\\.") {
		return b, errors.New("delivered launch path invalid")
	}
	if err := policyDirectories(f); err != nil {
		return b, err
	}
	file, err := os.OpenFile(DeliveredBaselinePath(f), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return b, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return b, errors.New("delivered view is not a private regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || strictPolicyJSON(data, &b) != nil {
		return b, errors.New("delivered view unreadable or malformed")
	}
	if err := validateDelivered(f, b, declaration, input); err != nil {
		return b, err
	}
	return b, nil
}
