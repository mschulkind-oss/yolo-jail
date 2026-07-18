package tomlx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeBasic(t *testing.T) {
	m, err := Decode([]byte("[a]\nb = 1\nc = \"x\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	a, ok := m["a"].(map[string]any)
	if !ok {
		t.Fatalf("a is %T, want table", m["a"])
	}
	if a["c"] != "x" {
		t.Errorf("a.c = %v, want x", a["c"])
	}
}

func TestVenvValueStrForm(t *testing.T) {
	m, _ := Decode([]byte("[env._.python]\nvenv = \".venv\"\n"))
	v, ok := VenvValue(m)
	if !ok || v != ".venv" {
		t.Errorf("VenvValue = %v, %v; want .venv, true", v, ok)
	}
}

func TestVenvValueTableForm(t *testing.T) {
	m, _ := Decode([]byte("[env._.python.venv]\ncreate = true\npath = \"myenv\"\n"))
	v, ok := VenvValue(m)
	if !ok {
		t.Fatal("VenvValue not found")
	}
	tbl, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("venv is %T, want table", v)
	}
	if tbl["create"] != true || tbl["path"] != "myenv" {
		t.Errorf("venv table = %v", tbl)
	}
}

func TestVenvValueAbsent(t *testing.T) {
	m, _ := Decode([]byte("[tools]\nnode = \"22\"\n"))
	if _, ok := VenvValue(m); ok {
		t.Error("VenvValue should be absent")
	}
}

// TestMiseVenvPath exercises the full priority-ordered discovery + the
// str/table/create/path resolution, matching entrypoint/shell.py.
func TestMiseVenvPath(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		return p
	}

	// String form -> path directly.
	strFile := write("str.toml", "[env._.python]\nvenv = \".venv\"\n")
	if path, ok := MiseVenvPath([]string{strFile}); !ok || path != ".venv" {
		t.Errorf("str MiseVenvPath = %q, %v; want .venv, true", path, ok)
	}

	// Table with create=true -> path.
	tblFile := write("tbl.toml", "[env._.python.venv]\ncreate = true\npath = \"myenv\"\n")
	if path, ok := MiseVenvPath([]string{tblFile}); !ok || path != "myenv" {
		t.Errorf("table MiseVenvPath = %q, %v; want myenv, true", path, ok)
	}

	// Table with create=false -> no venv.
	noCreate := write("nc.toml", "[env._.python.venv]\ncreate = false\n")
	if _, ok := MiseVenvPath([]string{noCreate}); ok {
		t.Error("create=false should yield no venv")
	}

	// Table with create=true, no path -> default .venv.
	defPath := write("def.toml", "[env._.python.venv]\ncreate = true\n")
	if path, ok := MiseVenvPath([]string{defPath}); !ok || path != ".venv" {
		t.Errorf("default path = %q, %v; want .venv, true", path, ok)
	}

	// Priority: first file WITH a value wins. A file without a venv is skipped.
	noVenv := write("nov.toml", "[tools]\nnode = \"22\"\n")
	if path, ok := MiseVenvPath([]string{noVenv, strFile}); !ok || path != ".venv" {
		t.Errorf("priority skip: %q, %v; want .venv from 2nd file", path, ok)
	}

	// No files have a venv -> not found.
	if _, ok := MiseVenvPath([]string{noVenv}); ok {
		t.Error("no venv anywhere should be not-found")
	}
}
