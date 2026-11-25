//go:build !linux && !darwin

package unifiedhome

import "syscall"

// defaultRename has no refuse-to-replace rename to use here, so a merge moves nothing.
func defaultRename(int, string, int, string) error { return syscall.ENOSYS }
