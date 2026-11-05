//go:build !linux

package resourcegroups

import (
	"context"
	"testing"
)

func TestDelegatedIOUnsupportedPlatformDoesNotCallAnyAuthority(t *testing.T) {
	read := func(string) ([]byte, error) { t.Fatal("unsupported platform read policy"); return nil, nil }
	mkdir := func(string) error { t.Fatal("unsupported platform created child"); return nil }
	write := func(string, []byte) error { t.Fatal("unsupported platform wrote policy"); return nil }
	if err := CheckDelegatedIO(IOPaths{}, read); err == nil {
		t.Fatal("accepted unsupported preflight")
	}
	if err := ReconcileDelegatedIO(context.Background(), IOPaths{}, 5, "/unused", func() (IOPaths, error) { t.Fatal("unsupported platform resolved manager"); return IOPaths{}, nil }, read, mkdir, write); err == nil {
		t.Fatal("accepted unsupported reconciliation")
	}
	if err := VerifyDelegatedMember(IOPaths{}, "/unused", 1, func(int) (string, error) { t.Fatal("unsupported platform observed membership"); return "", nil }); err == nil {
		t.Fatal("accepted unsupported membership")
	}
}
