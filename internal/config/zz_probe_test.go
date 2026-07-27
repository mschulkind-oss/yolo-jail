package config

import "testing"

func TestZZProbeReserved(t *testing.T) {
	segs := reservedHomeSegments()
	keys := []string{}
	for k := range segs {
		keys = append(keys, k)
	}
	t.Logf("reservedHomeSegments count=%d: %v", len(segs), keys)
	roots := []string{}
	for k := range hostFileWritableRoots {
		roots = append(roots, k)
	}
	t.Logf("hostFileWritableRoots count=%d: %v", len(hostFileWritableRoots), roots)
	e := HostFileEntry{Path: ".claude/mytool.json"}
	t.Logf("StagingFor(.claude/mytool.json) = %v", e.StagingFor())
	dirs := reservedHomeDirs()
	t.Logf("reservedHomeDirs count=%d", len(dirs))
}
