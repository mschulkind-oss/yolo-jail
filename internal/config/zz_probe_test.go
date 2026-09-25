package config

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestZZProbeReserved(t *testing.T) {
	shipped := packload.Embedded()
	segs := reservedHomeSegments(shipped)
	keys := []string{}
	for k := range segs {
		keys = append(keys, k)
	}
	t.Logf("reservedHomeSegments count=%d: %v", len(segs), keys)
	roots := []string{}
	for k := range hostFileWritableRoots(shipped) {
		roots = append(roots, k)
	}
	t.Logf("hostFileWritableRoots count=%d: %v", len(hostFileWritableRoots(shipped)), roots)
	e := HostFileEntry{Path: ".claude/mytool.json"}
	t.Logf("StagingFor(.claude/mytool.json) = %v", e.StagingFor(shipped))
	dirs := reservedHomeDirs(shipped)
	t.Logf("reservedHomeDirs count=%d", len(dirs))
}
