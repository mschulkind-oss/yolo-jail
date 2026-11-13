package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

type preparationWorkerControl struct {
	Wake   func()
	Drain  func()
	Finish func(*preparationState, error)
}

type preparationControlCall struct {
	command preparationCommand
	reply   chan preparationResponse
}

// preparationBinaryDigest is a var only so a test can count the hashes; nothing else reassigns it.
var preparationBinaryDigest = func(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// runPreparationWorker owns only lifecycle. dispatch must reauthorize inputs and
// guard admission; it receives the cancellation context used by existing owned
// build-child/jail cleanup. No autonomous adapter may bypass that prerequisite.
// Replace drains; stop cancels. The lease remains held through either teardown.
func runPreparationWorker(ctx context.Context, root, selectedExecutable string, ticks <-chan time.Time, dispatch func(context.Context) error, controls ...preparationWorkerControl) error {
	return runPreparationWorkerReady(ctx, root, selectedExecutable, ticks, dispatch, nil, controls...)
}

// runPreparationWorkerReady is runPreparationWorker with ready, when non-nil, called once
// with this incarnation's record after the record is written and its control endpoint is
// served: the event a test waits on instead of polling the probe against a clock.
func runPreparationWorkerReady(ctx context.Context, root, selectedExecutable string, ticks <-chan time.Time, dispatch func(context.Context) error, ready func(preparationWorker), controls ...preparationWorkerControl) (resultErr error) {
	if dispatch == nil {
		return errors.New("preparation dispatcher missing; wire generation-guarded admission before starting")
	}
	if err := ensurePreparationRoot(root); err != nil {
		return err
	}
	lock, err := pidlock.Acquire(filepath.Join(root, "worker.lock"), pidlock.NoWait, nil)
	if err != nil {
		return err
	}
	defer lock.Release()
	digest, err := preparationBinaryDigest(selectedExecutable)
	if err != nil {
		return err
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return err
	}
	record := preparationWorker{Identity: hex.EncodeToString(token), Executable: selectedExecutable, BinaryDigest: digest, PID: os.Getpid()}
	listener, err := svcendpoint.Listen(filepath.Join(root, "control.endpoint"), "127.0.0.1")
	if err != nil {
		return err
	}
	defer listener.Close()
	if err = withPreparationStateAt(root, func(state *preparationState) error { state.Worker = record; return nil }); err != nil {
		return err
	}
	// Final state belongs to this incarnation while its lease is still held.
	// A successor cannot be relabelled by a late post-release cleanup.
	defer func() {
		err := withPreparationStateAt(root, func(state *preparationState) error {
			if state.Worker.Identity != record.Identity {
				return nil
			}
			state.Worker.StoppedAt = time.Now()
			if len(controls) > 0 && controls[0].Finish != nil {
				controls[0].Finish(state, resultErr)
			}
			return nil
		})
		if err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	// Do not delete stale records: a crash/replacement retains diagnostic evidence.
	ownerCtx, ownerCancel := context.WithCancel(ctx)
	defer ownerCancel()
	calls := make(chan preparationControlCall)
	go servePreparationControl(ownerCtx, listener, calls)
	if ready != nil {
		ready(record)
	}
	phase := "idle"
	var active <-chan error
	var result error
	pending := false
	ctxDone := ctx.Done()
	begin := func() {
		current, e := preparationBinaryDigest(selectedExecutable)
		if e != nil || current != digest {
			phase = "draining"
			result = fmt.Errorf("preparation executable identity changed or unreadable; start again with the selected binary: %v", e)
			return
		}
		done := make(chan error, 1)
		active = done
		phase = "running"
		go func() { done <- dispatch(ownerCtx) }()
	}
	for {
		if (phase == "stopping" || phase == "draining") && active == nil {
			return result
		}
		select {
		case <-ctxDone:
			phase = "stopping"
			ownerCancel()
			ctxDone = nil
		case call := <-calls:
			response := preparationResponse{Identity: record.Identity, Phase: phase}
			if call.command.Identity != record.Identity {
				response.Error = "preparation incarnation changed; reread status before control"
			} else {
				switch call.command.Operation {
				case "status":
				case "wake":
					if phase == "stopping" || phase == "draining" {
						response.Error = "preparation is draining/stopping; wait for lease release before starting"
					} else if active == nil {
						begin()
					} else if len(controls) > 0 {
						controls[0].Wake()
					} else {
						pending = true
					}
				case "stop":
					if err := withPreparationStateAt(root, func(state *preparationState) error {
						if state.Worker.Identity != record.Identity {
							return errors.New("preparation incarnation changed; reread status")
						}
						state.Worker.StopRequested = true
						return nil
					}); err != nil {
						response.Error = err.Error()
						break
					}
					phase = "stopping"
					pending = false
					ownerCancel()
				case "replace":
					if phase != "stopping" {
						phase = "draining"
						if len(controls) > 0 {
							controls[0].Drain()
						}
					}
					pending = false
				default:
					response.Error = "unknown preparation control; use wake, status, stop or replace"
				}
			}
			response.Phase = phase
			call.reply <- response
		case _, ok := <-ticks:
			if !ok {
				ticks = nil
				continue
			}
			if phase == "stopping" || phase == "draining" {
				continue
			}
			// Check identity even while busy; replacement cannot start another act.
			current, e := preparationBinaryDigest(selectedExecutable)
			if e != nil || current != digest {
				phase = "draining"
				pending = false
				result = fmt.Errorf("preparation executable identity changed or unreadable; start again with the selected binary: %v", e)
				continue
			}
			if active == nil {
				begin()
			} else if len(controls) > 0 {
				controls[0].Wake()
			} else {
				pending = true
			}
		case dispatchErr := <-active:
			// Adapter owns outcome persistence. A failed key never becomes scheduler success.
			active = nil
			if len(controls) > 0 {
				return dispatchErr
			}
			if phase == "running" {
				phase = "idle"
				if pending {
					pending = false
					begin()
				}
			}
		}
	}
}

func servePreparationControl(ctx context.Context, listener *svcendpoint.Listener, calls chan<- preparationControlCall) {
	// A slow client consumes one slot, not the control accept loop. Excess clients
	// are closed rather than spawning unbounded authenticated/handshake waiters.
	slots := make(chan struct{}, 8)
	var handlers sync.WaitGroup
	defer handlers.Wait()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			defer func() { <-slots }()
			defer conn.Close()
			deadline := time.Now().Add(preparationControlTimeout)
			conn.SetDeadline(deadline)
			var cmd preparationCommand
			_, err := svcendpoint.ReadPreamble(conn)
			conn.SetDeadline(deadline)
			if err == nil {
				err = json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&cmd)
			}
			if err != nil {
				return
			}
			call := preparationControlCall{cmd, make(chan preparationResponse, 1)}
			timer := time.NewTimer(time.Until(deadline))
			defer timer.Stop()
			select {
			case calls <- call:
			case <-ctx.Done():
				return
			case <-timer.C:
				return
			}
			select {
			case response := <-call.reply:
				_ = json.NewEncoder(conn).Encode(response)
			case <-timer.C:
			}
		}()
	}
}

type preparationLiveness string

const (
	preparationGone         preparationLiveness = "gone"
	preparationAlive        preparationLiveness = "alive"
	preparationUnknown      preparationLiveness = "unknown"
	preparationControlBound                     = time.Second
)

// preparationControlTimeout bounds one control exchange, on both ends. It is
// preparationControlBound, and a var only so a test asserting what the owner ANSWERS can
// raise it to a generous failure guard rather than race a loaded machine against a second.
var preparationControlTimeout = preparationControlBound

type preparationCommand struct{ Identity, Operation string }
type preparationResponse struct{ Identity, Phase, Error string }

// probePreparationWorker never creates, rewrites or removes state. Only a free
// kernel lease proves gone; a held lease needs authenticated incarnation proof.
func probePreparationWorker(root string) (preparationWorker, preparationLiveness, error) {
	var record preparationWorker
	f, err := os.OpenFile(filepath.Join(root, "worker.lock"), os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return record, preparationGone, nil
	}
	if err != nil {
		return record, preparationUnknown, err
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return record, preparationGone, nil
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
		return record, preparationUnknown, err
	}
	state, err := loadPreparationStateAt(root)
	record = state.Worker
	if err != nil || record.Identity == "" {
		return record, preparationUnknown, fmt.Errorf("preparation owner unproven; retry status or inspect scheduler state before starting: %v", err)
	}
	err = controlPreparationWorker(context.Background(), root, record.Identity, "status")
	if err != nil {
		return record, preparationUnknown, err
	}
	return record, preparationAlive, nil
}

// The expected random lease incarnation, not a PID or build stamp, targets control.
func controlPreparationWorker(ctx context.Context, root, expectedIdentity, op string) error {
	if expectedIdentity == "" {
		return errors.New("preparation identity missing; read status before control")
	}
	deadline := time.Now().Add(preparationControlTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := svcendpoint.DialLocal(filepath.Join(root, "control.endpoint"), time.Until(deadline))
	if err != nil {
		return fmt.Errorf("preparation control unavailable; retry status, do not replace an unproven owner: %w", err)
	}
	defer conn.Close()
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	if err = json.NewEncoder(conn).Encode(preparationCommand{expectedIdentity, op}); err != nil {
		return err
	}
	var response preparationResponse
	if err = json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&response); err != nil {
		return err
	}
	if response.Identity != expectedIdentity {
		return errors.New("preparation incarnation changed; reread status before control")
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	return nil
}

var errPreparationAdmissionStale = errors.New("preparation inputs or authorization changed; retry --update with the current selection")
