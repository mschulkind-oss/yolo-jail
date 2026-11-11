package approvalnotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// Main is the hidden, host-only optional on_request entry point.
func Main(args []string) int {
	return entry(args, os.Stdin, os.Stderr, os.Getenv, runtime.GOOS, config.InJail(), connectBus)
}
func entry(args []string, in io.Reader, errout io.Writer, getenv func(string) string, goos string, inJail bool, dial func(context.Context, string) (Bus, error)) int {
	fail := func(err error) int { fmt.Fprintln(errout, failure(err)); return 1 }
	if len(args) != 0 {
		return fail(fmt.Errorf("approval-notify takes no arguments"))
	}
	if goos != "linux" || inJail {
		return fail(fmt.Errorf("approval-notify requires the Linux host, not a jail"))
	}
	data, err := io.ReadAll(io.LimitReader(in, 128*1024+1))
	if err != nil {
		return fail(err)
	}
	if len(data) > 128*1024 {
		return fail(fmt.Errorf("request input exceeds 128 KiB"))
	}
	var event brokerstore.EventView
	if err = json.Unmarshal(data, &event); err != nil {
		return fail(fmt.Errorf("invalid request JSON: %w", err))
	}
	bin := getenv("YOLO_BIN")
	if !filepath.IsAbs(bin) {
		return fail(fmt.Errorf("YOLO_BIN must name an absolute executable"))
	}
	info, err := os.Stat(bin)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fail(fmt.Errorf("YOLO_BIN is not an executable file"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = Run(ctx, event, Dependencies{Address: getenv("DBUS_SESSION_BUS_ADDRESS"), Bin: bin, Dial: dial, Decide: Decision})
	if err != nil {
		fmt.Fprintln(errout, err)
		return 1
	}
	return 0
}

// Decision executes exactly the supported host CLI argv without a shell, retaining
// only bounded diagnostics while draining both streams to avoid subprocess deadlock.
func Decision(ctx context.Context, bin string, args []string) (int, string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	out := &boundedOutput{}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	if err == nil {
		return 0, string(out.data), nil
	}
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode(), string(out.data), nil
	}
	return 0, string(out.data), err
}

type boundedOutput struct{ data []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := textLimit - len(b.data)
	if left > len(p) {
		left = len(p)
	}
	b.data = append(b.data, p[:left]...)
	return n, nil
}
