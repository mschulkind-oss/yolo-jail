//go:build !linux

package lingerprobe

import "time"

func syscallName(int) string { return "" }

func clockNow(int) (time.Duration, bool) { return 0, false }
