//go:build linux

package notty

import (
	"os"
	"strings"
)

// procGone says whether pid has exited: no /proc entry, or a zombie its new parent has not reaped.
func procGone(pid string) bool {
	b, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return true
	}
	// The state is the field after the parenthesized command name.
	if i := strings.LastIndexByte(string(b), ')'); i >= 0 && i+2 < len(b) {
		return b[i+2] == 'Z'
	}
	return false
}
