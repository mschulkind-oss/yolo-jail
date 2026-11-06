# Paste an Image into the Agent

Give the agent a screenshot or any other image on your clipboard. Run `yolo paste` on your host,
then paste into the agent as usual: the agent gets the image as an attachment. It works with every
agent yolo ships and on every setup.

## How to use it

1. Copy an image: take a screenshot to the clipboard, or copy an image in your browser.
2. In a terminal on your host, inside the project, run:

   ```bash
   yolo paste
   ```

3. Paste into the agent as you would paste text (Cmd+V on a Mac, Ctrl+Shift+V in most Linux
   terminals).

`yolo paste` saves the image into the project as a new PNG file, prints the path the agent sees it
at, and puts that path on your clipboard. Agents attach an image whose path you paste to them, so
step 3 hands over the image:

```text
$ yolo paste
yolo paste: saved the clipboard's image (image/png via wl-paste, 1920x1080, 412 KiB) to /home/you/code/widgets/.yolo/paste/20261106T143307Z-4f2a9c1e.png
/workspace/.yolo/paste/20261106T143307Z-4f2a9c1e.png
yolo paste: the clipboard now holds the path (as the podman-self setup sees it); paste it into the agent.
```

The clipboard's image is replaced by the path. The saved file keeps the image, so copy it again if
you need it elsewhere.

## Where the image goes

Images are saved in the project's `.yolo/paste/` folder. git ignores `.yolo`, so a pasted image is
never committed. yolo keeps a week of pastes, and at most the newest 200; older ones are removed
the next time you run `yolo paste`.

The path printed is the one the agent sees:

| Setup | Path the agent gets |
| :--- | :--- |
| A jail (`podman-self`, `apple-container-self`) | `/workspace/.yolo/paste/<file>` |
| The Mac sandbox (`seatbelt-self`, `seatbelt-yolo-account`) | your project's own path |
| `yolo host` | your project's own path |

`yolo paste` uses the setup a launch in this project would use. If you started the agent with
`--setup`, pass the same name: `yolo paste --setup seatbelt-self`.

## What it reads, and what it doesn't

- **Images only.** PNG, JPEG and GIF, up to 32 MiB and 40 megapixels. A WebP, BMP or TIFF is refused;
  copy it as PNG instead (most screenshot tools and browsers offer that). On a Mac, a screenshot on
  the clipboard is read as PNG.
- **Only pixels reach the jail.** yolo decodes the image and writes a fresh PNG, so camera details,
  GPS location and anything else stored in the original file are left behind.
- **Only when you ask.** The agent cannot run `yolo paste` or read your clipboard. In a jail the
  command does nothing and tells you to run it on the host.

## What it needs

- **Linux:** a desktop session, and `wl-clipboard` (`wl-paste`, `wl-copy`) on Wayland or `xclip`
  on X11. Over SSH without X forwarding there is no clipboard, and `yolo paste` says so.
- **macOS:** nothing to install; it uses `osascript` and `pbcopy`.
- **A project a jail has run in**, or one with a `yolo-jail.toml`. Run `yolo paste` from inside it.

If something stops it, the message says what to do next: copy an image first, install a clipboard
tool, crop an image that is too large, or run it from inside the project.

## Planned

**Ctrl+V straight into the agent.** Pressing Ctrl+V in the agent with an image on your host
clipboard will save the image and paste its path for you, with no separate command. It is designed
and not yet built; first on Linux, then on a Mac. Until then, `yolo paste` is the way to paste an
image.

See also: [CLI Reference](../reference/cli-reference.md), [Storage](storage.md).
