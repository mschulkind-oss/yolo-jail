package brokerscope

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func consumerFixture(t *testing.T) (File, SnapshotConsumer, string, string) {
	t.Helper()
	f, p, scopePath := snapshotFixture(t)
	f.BrokerStart = strings.Repeat("d", 32)
	f.BrokerPID = os.Getpid()
	f.PolicySnapshot = prepareSnapshot(t, f, p)
	if _, err := Write(f); err != nil {
		t.Fatal(err)
	}
	c := SnapshotConsumer{
		Version: SnapshotConsumerVersion,
		Binding: PolicyBinding{
			Source:          f.Source,
			Workspace:       f.Workspace,
			LaunchID:        f.LaunchID,
			DeclarationHash: p.Binding.DeclarationHash,
			InputDigest:     p.Binding.InputDigest,
		},
		Start:                 f.BrokerStart,
		BrokerPID:             f.BrokerPID,
		SupportedReaderPolicy: PolicySnapshotVersion,
	}
	return f, c, scopePath, p.Binding.DeclarationHash
}

func TestSnapshotConsumerPublishAndReadRoundTrip(t *testing.T) {
	f, c, _, declHash := consumerFixture(t)
	if err := PublishSnapshotConsumer(f, c); err != nil {
		t.Fatal(err)
	}
	proofPath := SnapshotConsumerPath(f)
	info, err := os.Lstat(proofPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected mode %v", info.Mode())
	}
	var got SnapshotConsumer
	err = brokerlock.WithTransaction(paths.BrokerSourceDir(f.Source), func() error {
		var rerr error
		got, rerr = ReadSnapshotConsumerLocked(f, declHash, c.Binding.InputDigest)
		return rerr
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != c {
		t.Fatalf("got %+v, want %+v", got, c)
	}
}

func TestSnapshotConsumerRejectsTamperingAndDeadPID(t *testing.T) {
	cases := []string{
		"missing",
		"unknown-field",
		"symlink",
		"bad-mode",
		"wrong-start",
		"wrong-decl",
		"wrong-input",
		"dead-pid",
		"wrong-policy-version",
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			f, c, scopePath, declHash := consumerFixture(t)
			if tc != "missing" {
				if err := PublishSnapshotConsumer(f, c); err != nil {
					t.Fatal(err)
				}
			}
			proofPath := SnapshotConsumerPath(f)
			switch tc {
			case "unknown-field":
				raw, err := os.ReadFile(proofPath)
				if err != nil {
					t.Fatal(err)
				}
				var m map[string]json.RawMessage
				if err := json.Unmarshal(raw, &m); err != nil {
					t.Fatal(err)
				}
				m["future"] = json.RawMessage(`true`)
				data, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(proofPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(filepath.Dir(scopePath), ".foreign-proof.json")
				if err := os.Rename(proofPath, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, proofPath); err != nil {
					t.Fatal(err)
				}
			case "bad-mode":
				if err := os.Chmod(proofPath, 0o644); err != nil {
					t.Fatal(err)
				}
			case "wrong-start":
				f.BrokerStart = strings.Repeat("e", 32)
			case "wrong-decl":
				declHash = strings.Repeat("f", 64)
			case "wrong-input":
				c.Binding.InputDigest = strings.Repeat("f", 64)
			case "dead-pid":
				c.BrokerPID = 999999999
				f.BrokerPID = 999999999
				data, err := json.Marshal(c)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(proofPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
			case "wrong-policy-version":
				c.SupportedReaderPolicy = "yolo.broker-policy/999"
				data, err := json.Marshal(c)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(proofPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := brokerlock.WithTransaction(paths.BrokerSourceDir(f.Source), func() error {
				_, rerr := ReadSnapshotConsumerLocked(f, declHash, c.Binding.InputDigest)
				return rerr
			})
			if err == nil {
				t.Fatalf("case %s unexpectedly succeeded", tc)
			}
		})
	}
}
