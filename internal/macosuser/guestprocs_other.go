//go:build !darwin

package macosuser

import "errors"

// guestProcessesReal off macOS: there is no sandbox account to look for, so the answer is
// unknown, and a guest pack tree is kept. The backend only launches on macOS; this half keeps the
// package whole where its unit tests run, and those inject the table.
func guestProcessesReal(Mode) ([]GuestProc, error) {
	return nil, errors.New("the sandbox account's processes are read on macOS only")
}

// SandboxUID off macOS: there is no yolo account.
func SandboxUID() (uint32, error) {
	return 0, errors.New("the yolo account exists on macOS only")
}
