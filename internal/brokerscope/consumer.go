package brokerscope

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const SnapshotConsumerVersion = "yolo.snapshot-consumer/1"

// SnapshotConsumer is the daemon-authored liveness and reader-capability proof
// stored under the source's protected launch state. Only real daemon Main
// writes it after binding its live lease and validating its initial snapshot
// reader. Apply validates this proof and daemon liveness before treating a
// running broker as snapshot-consuming.
type SnapshotConsumer struct {
	Version               string        `json:"version"`
	Binding               PolicyBinding `json:"binding"`
	Start                 string        `json:"start"`
	BrokerPID             int           `json:"broker_pid"`
	SupportedReaderPolicy string        `json:"supported_reader_policy"`
}

// SnapshotConsumerPath returns the private host launch-state path for a
// launch's snapshot-consumer proof.
func SnapshotConsumerPath(f File) string {
	return filepath.Join(filepath.Dir(paths.BrokerScopeFile(f.Source, f.LaunchID)), ".snapshot-consumer-"+f.LaunchID+".json")
}

// InputDigest returns the canonical sha256 hex digest of an immutable private
// launch input payload.
func InputDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validateSnapshotConsumer(f File, c SnapshotConsumer, declarationHash, inputDigest string) error {
	if !loopholedecl.ValidBrokerSourceName(f.Source) || f.LaunchID == "" || strings.ContainsAny(f.LaunchID, "/\\.") || f.Workspace == "" {
		return policyFailure("consumer proof launch identity is invalid")
	}
	if c.Version != SnapshotConsumerVersion {
		return policyFailure("consumer proof version is unsupported")
	}
	if c.Binding != (PolicyBinding{
		Source:          f.Source,
		Workspace:       f.Workspace,
		LaunchID:        f.LaunchID,
		DeclarationHash: declarationHash,
		InputDigest:     inputDigest,
	}) || !validPolicyDigest(declarationHash) || !validPolicyDigest(inputDigest) {
		return policyFailure("consumer proof binding disagrees with launch")
	}
	if len(c.Start) != 32 || !validLowerHex(c.Start) {
		return policyFailure("consumer proof lease token is invalid")
	}
	if f.BrokerStart == "" || c.Start != f.BrokerStart || f.BrokerPID <= 0 || c.BrokerPID != f.BrokerPID || !processAlive(c.BrokerPID) {
		return policyFailure("consumer proof does not match live broker lease")
	}
	if c.SupportedReaderPolicy != PolicySnapshotVersion {
		return policyFailure("consumer proof reader policy version is unsupported")
	}
	return policyDirectories(f)
}

func validLowerHex(s string) bool {
	if s == "" || s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// PublishSnapshotConsumer writes the private regular 0600 snapshot-consumer
// proof under the source transaction. Only real daemon Main calls this after
// binding its live lease and successfully validating its initial snapshot reader.
func PublishSnapshotConsumer(f File, c SnapshotConsumer) error {
	if !loopholedecl.ValidBrokerSourceName(f.Source) || f.LaunchID == "" || strings.ContainsAny(f.LaunchID, "/\\.") {
		return policyFailure("consumer proof launch identity is invalid")
	}
	return brokerlock.WithTransaction(paths.BrokerSourceDir(f.Source), func() error {
		return PublishSnapshotConsumerLocked(f, c)
	})
}

// PublishSnapshotConsumerLocked writes the private regular 0600
// snapshot-consumer proof while the caller already holds the source transaction.
func PublishSnapshotConsumerLocked(f File, c SnapshotConsumer) error {
	c.Version = SnapshotConsumerVersion
	if err := validateSnapshotConsumer(f, c, c.Binding.DeclarationHash, c.Binding.InputDigest); err != nil {
		return err
	}
	path := SnapshotConsumerPath(f)
	data, err := json.Marshal(c)
	if err != nil || len(data) > 1<<20 {
		return policyFailure("cannot encode consumer proof")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".snapshot-consumer-prepare-*")
	if err != nil {
		return policyFailure("cannot prepare consumer proof")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return policyFailure("consumer proof write failed")
	}
	if err := file.Chmod(0o600); err != nil {
		return policyFailure("consumer proof mode failed")
	}
	if err := file.Close(); err != nil {
		return policyFailure("consumer proof close failed")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return policyFailure("consumer proof rename failed")
	}
	_, err = ReadSnapshotConsumerLocked(f, c.Binding.DeclarationHash, c.Binding.InputDigest)
	return err
}

// ReadSnapshotConsumerLocked reads and validates the private 0600
// snapshot-consumer proof under the source transaction. Unknown fields,
// symlinks, non-0600 files, dead PIDs, or mismatched bindings are rejected.
func ReadSnapshotConsumerLocked(f File, declarationHash, inputDigest string) (SnapshotConsumer, error) {
	var c SnapshotConsumer
	if !loopholedecl.ValidBrokerSourceName(f.Source) || f.LaunchID == "" || strings.ContainsAny(f.LaunchID, "/\\.") {
		return c, policyFailure("consumer proof launch identity is invalid")
	}
	if err := policyDirectories(f); err != nil {
		return c, err
	}
	path := SnapshotConsumerPath(f)
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return c, policyFailure("consumer proof is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return c, policyFailure("consumer proof is not a private regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || strictPolicyJSON(data, &c) != nil {
		return SnapshotConsumer{}, policyFailure("consumer proof readback/format is invalid")
	}
	if err := validateSnapshotConsumer(f, c, declarationHash, inputDigest); err != nil {
		return SnapshotConsumer{}, err
	}
	return c, nil
}
