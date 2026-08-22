package hostservice

import (
	"net"
	"sync"
	"testing"
)

// TestSessionExitDoesNotRaceFrames pins the mutex discipline on Session.exited.
//
// It exists because the fix it guards is otherwise UNPINNED: sendFrame and Exit are never
// provably concurrent in production today (ExecAllowlisted joins its pumps before exiting),
// so reverting the fix leaves every other test green and `-race` silent. A fix nothing can
// fail for is indistinguishable from no fix, so this test opens the window deliberately.
//
// It asserts nothing about output — the race detector is the assertion. Under `-race` this
// FAILS if the exited check moves back outside s.mu, and it is a no-op otherwise.
func TestSessionExitDoesNotRaceFrames(t *testing.T) {
	client, server := net.Pipe()
	// Drain, so a frame write never blocks on the pipe and the goroutines stay interleaved.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := client.Read(buf); err != nil {
				return
			}
		}
	}()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	s := &Session{conn: server}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Stdout("frame\n")
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Exit(0)
	}()
	wg.Wait()
}
