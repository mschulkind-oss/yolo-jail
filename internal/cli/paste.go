package cli

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/clipimage"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// paste.go is `yolo paste` (docs/design/image-paste.md §6.4, PA-D9): a host command that saves
// the host clipboard's image into the workspace's paste store and puts the file's in-jail path
// on the clipboard, so the user's next ordinary paste hands the agent an image path it attaches.
// It needs no terminal proxy, only the workspace bind, so it works on every backend. The reading,
// checks, re-encode and store are internal/clipimage's; this file is the verb.

const pasteUsage = `Usage: yolo paste [--setup NAME]

On the host, in a terminal inside a yolo workspace: save the image on your
clipboard into the workspace's .yolo/paste/ as a new PNG, print the path the
agent sees it at, and put that path on the clipboard. Then paste into the agent
as usual (Cmd+V, Ctrl+Shift+V): every agent yolo ships attaches a pasted image
path. The clipboard's image is replaced by the path; the saved PNG keeps it.

Only PNG, JPEG and GIF are read, up to 32 MiB and 40 megapixels. The image is
decoded and written out again as a fresh PNG, so no metadata (camera, GPS) and
no other clipboard content reaches the jail. Pastes older than 7 days, and any
beyond the newest 200, are removed.

The clipboard is read with wl-paste or xclip on Linux, and osascript on macOS;
it is written with wl-copy, xclip or pbcopy. A jail has no clipboard, so in a
jail this command does nothing and says where to run it.

Flags:
  --setup NAME  The setup the agent runs in, to print the path it sees: a
                container setup (podman-self, apple-container-self) sees the
                workspace at /workspace; a seatbelt setup and yolo host see the
                host path. Default: the setup a launch here would use.
  --help, -h    Show this help.

Examples:
  yolo paste                         # from a terminal inside the workspace
  yolo paste --setup seatbelt-self   # the agent runs on the Mac sandbox`

// clipboard is what the verb needs of the host clipboard: clipimage.Reader in production.
type clipboard interface {
	Read(ctx context.Context) (clipimage.Clip, error)
	CopyText(ctx context.Context, text string) error
}

// pasteEnv is the verb's host. hostPasteEnv fills it with the process's own.
type pasteEnv struct {
	clip   clipboard
	inJail func() bool
	// workspace resolves the workspace from the working directory.
	workspace func() (string, bool)
	// jailWorkspace is where the agent sees ws, and the setup it judged that for.
	jailWorkspace func(flag, ws string) (dest, setupName string, warnings []string, err error)
	now           func() time.Time
	rand          io.Reader
	stdout        io.Writer
	stderr        io.Writer
}

func hostPasteEnv() pasteEnv {
	return pasteEnv{
		clip:          clipimage.HostReader(runtime.GOOS),
		inJail:        config.InJail,
		workspace:     resolveWorkspaceRoot,
		jailWorkspace: pasteJailWorkspace,
		now:           time.Now,
		rand:          rand.Reader,
		stdout:        os.Stdout,
		stderr:        os.Stderr,
	}
}

func runPaste(args []string) int { return pasteMain(args[1:], hostPasteEnv()) }

// pasteJailWorkspace is the workspace's mount destination in the jail the selected setup
// runs: a container binds it at /workspace; macos-user and the host see the host path.
func pasteJailWorkspace(flag, ws string) (string, string, []string, error) {
	choice, err := config.SelectSetup(config.SetupInputs{Verb: config.SetupForLaunch, Flag: flag, GOOS: runtime.GOOS})
	if err != nil {
		return "", "", nil, err
	}
	// A host setup (host-self, named by --setup) is not KindJail: the host sees the host path.
	if choice.Setup.Kind() == setup.KindJail {
		return "/workspace", choice.Setup.Name, choice.Warnings, nil
	}
	return ws, choice.Setup.Name, choice.Warnings, nil
}

func pasteMain(args []string, e pasteEnv) int {
	if answerHelp("paste", args, e.stdout) {
		return 0
	}
	say := func(format string, a ...any) { fmt.Fprintf(e.stderr, "yolo paste: "+format+"\n", a...) }
	flag := ""
	for i := 0; i < len(args); i++ {
		name, val, glued := strings.Cut(args[i], "=")
		if name != "--setup" {
			say("unknown argument %s; see `yolo paste --help`.", shquote.QuoteDisplay(args[i]))
			return 2
		}
		if !glued {
			if i+1 >= len(args) {
				say("a setup name must follow --setup; see `yolo setup list`.")
				return 2
			}
			i++
			val = args[i]
		}
		flag = val
	}
	if e.inJail() {
		say("this reads your HOST clipboard, which a jail cannot reach, so nothing happened. Run it in a terminal on the host, inside this workspace (in the jail, `echo $YOLO_HOST_DIR` prints its host path):\n\n    yolo paste")
		return 1
	}
	ws, ok := e.workspace()
	if !ok {
		say("no yolo workspace at or above this directory, so nothing was read. Run it from inside the workspace the jail has open:\n\n    cd <workspace> && yolo paste")
		return 1
	}
	dest, setupName, warnings, err := e.jailWorkspace(flag, ws)
	for _, w := range warnings {
		fmt.Fprintln(e.stderr, w)
	}
	if err != nil {
		say("%v", err)
		return 1
	}

	ctx := context.Background()
	clip, err := e.clip.Read(ctx)
	if err != nil {
		say("%v", err)
		return 1
	}
	img, err := clipimage.Normalize(clip.Data, clip.Type)
	if err != nil {
		say("%v", err)
		return 1
	}
	name, err := clipimage.Save(ws, img.PNG, e.now(), e.rand)
	if err != nil {
		say("cannot write into %s: %v. Nothing was saved; fix the directory (a link there is refused; remove it and yolo recreates it), then run `yolo paste` again.",
			termsafe.Visible(clipimage.StoreDir(ws)), err)
		return 1
	}
	jailPath := clipimage.InJailPath(dest, name)
	say("saved the clipboard's image (%s via %s, %dx%d, %s) to %s",
		img.SourceType, clip.Tool, img.Width, img.Height, sizeText(len(img.PNG)),
		termsafe.Visible(filepath.Join(clipimage.StoreDir(ws), name)))
	fmt.Fprintln(e.stdout, jailPath)
	if err := e.clip.CopyText(ctx, jailPath); err != nil {
		say("could not put the path on the clipboard (%v); copy the path printed above and paste it into the agent.", err)
	} else {
		say("the clipboard now holds the path (as the %s setup sees it); paste it into the agent.", setupName)
	}
	if n, err := clipimage.Sweep(ws, e.now()); err != nil {
		say("could not clear old pastes from %s (%v); `yolo paste` tries again next time.", termsafe.Visible(clipimage.StoreDir(ws)), err)
	} else if n > 0 {
		say("removed %d older paste(s): the store keeps 7 days, and at most %d files.", n, clipimage.MaxFiles)
	}
	return 0
}

func sizeText(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
