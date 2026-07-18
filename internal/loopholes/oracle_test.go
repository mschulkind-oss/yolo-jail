package loopholes

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// pythonRunner returns a factory for `uv run python` (preferred) or `python3`
// commands rooted at the repo, or nil when neither is available. Mirrors the
// house parity-test pattern (agentsmd, config).
func pythonRunner(t *testing.T) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRootDir(t)
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "python"}, args...)...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("python3", args...)
			c.Dir = root
			return c
		}
	}
	return nil
}

func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// oracleScript is the embedded Python differential oracle. It reads one JSON
// request from stdin and prints one JSON response to stdout, driving the LIVE
// src/loopholes.py. Everything the Go package exposes has a matching action.
const oracleScript = `
import json, sys
sys.path.insert(0, 'src')
from pathlib import Path
from src import loopholes as L

req = json.load(sys.stdin)
action = req["action"]

def loophole_view(m):
    return {
        "name": m.name,
        "active": m.active,
        "inactive_reason": m.inactive_reason,
        "from_config": m.from_config,
        "source": m.source,
        "enabled": m.enabled,
        "transport": m.transport,
        "lifecycle": m.lifecycle,
    }

def discover(req):
    kwargs = dict(include_bundled=req.get("include_bundled", False))
    if req.get("include_disabled"):
        kwargs["include_disabled"] = True
    if req.get("loopholes_config") is not None:
        kwargs["loopholes_config"] = req["loopholes_config"]
    root = req.get("root")
    if root is not None:
        return L.discover_loopholes(Path(root), **kwargs)
    return L.discover_loopholes(**kwargs)

if action == "discover_and_args":
    loaded = discover(req)
    out = {
        "loopholes": [loophole_view(m) for m in loaded],
        "args": L.runtime_args_for(loaded, runtime=req.get("runtime")),
        "specs": L.manifest_host_daemon_specs(loaded),
    }
elif action == "load_and_args":
    m = L.load_loophole(Path(req["module_path"]))
    out = {
        "loophole": loophole_view(m),
        "args": L.runtime_args_for([m], runtime=req.get("runtime")),
        "host_devices": list(m.host_devices),
        "bind_mounts": [
            {"host": str(bm.host), "container": bm.container, "readonly": bm.readonly}
            for bm in m.host_bind_mounts
        ],
    }
elif action == "validate":
    kwargs = dict(include_bundled=req.get("include_bundled", False))
    root = req.get("root")
    entries = L.validate_loopholes(Path(root) if root is not None else None, **kwargs)
    out = {
        "entries": [
            [str(p), (m.name if m is not None else None), err]
            for (p, m, err) in entries
        ]
    }
elif action == "set_enabled":
    L.set_enabled(Path(req["module_path"]), req["enabled"])
    out = {"content": (Path(req["module_path"]) / "manifest.jsonc").read_text()}
else:
    raise SystemExit("unknown action: " + action)

sys.stdout.write(json.dumps(out))
`

// runOracle invokes the embedded Python oracle with req on stdin and the current
// process environment (so YOLO_VERSION / XDG_RUNTIME_DIR / HOME set via
// t.Setenv reach Python identically), returning the decoded response. Skips the
// test when Python can't run.
func runOracle(t *testing.T, py func(...string) *exec.Cmd, req map[string]any) map[string]any {
	t.Helper()
	reqBytes, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal oracle request: %v", err)
	}
	cmd := py("-c", oracleScript)
	cmd.Stdin = bytes.NewReader(reqBytes)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Skipf("python oracle failed (%v): %s", err, stderr.String())
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode oracle output: %v\nstdout=%q\nstderr=%q", err, stdout.String(), stderr.String())
	}
	return out
}

// toStringList coerces a decoded JSON array of strings.
func toStringList(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}
