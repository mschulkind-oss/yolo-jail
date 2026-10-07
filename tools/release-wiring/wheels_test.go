package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateWheelDirectoryChecksExactReleasePlatformAndMetadata(t *testing.T) {
	dir := t.TempDir()
	for _, platform := range publishWheelPlatforms {
		name := "yolo_jail-0.12.1-py3-none-" + platform + ".whl"
		writeValidTestWheel(t, filepath.Join(dir, name), "0.12.1")
	}
	got, err := validateWheelDir(dir, "0.12.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(publishWheelPlatforms) {
		t.Fatalf("got %d wheels, want %d", len(got), len(publishWheelPlatforms))
	}
}

func TestWheelArtifactsRejectUnknownMissingSymlinkAndWrongVersion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, dir string)
		want   string
	}{
		{name: "unknown filename", mutate: func(t *testing.T, dir string) { writeValidTestWheel(t, filepath.Join(dir, "attacker.whl"), "0.12.1") }, want: "unexpected wheel artifact"},
		{name: "missing platform", mutate: func(t *testing.T, dir string) {
			_ = os.Remove(filepath.Join(dir, "yolo_jail-0.12.1-py3-none-macosx_11_0_arm64.whl"))
		}, want: "found 5 wheel artifacts"},
		{name: "symlink", mutate: func(t *testing.T, dir string) {
			_ = os.Remove(filepath.Join(dir, "yolo_jail-0.12.1-py3-none-macosx_11_0_arm64.whl"))
			if err := os.Symlink("yolo_jail-0.12.1-py3-none-manylinux_2_17_x86_64.whl", filepath.Join(dir, "yolo_jail-0.12.1-py3-none-macosx_11_0_arm64.whl")); err != nil {
				t.Fatal(err)
			}
		}, want: "regular file"},
		{name: "wrong internal version", mutate: func(t *testing.T, dir string) {
			writeValidTestWheel(t, filepath.Join(dir, "yolo_jail-0.12.1-py3-none-manylinux_2_17_x86_64.whl"), "9.9.9")
		}, want: "does not match the dispatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, platform := range publishWheelPlatforms {
				writeValidTestWheel(t, filepath.Join(dir, "yolo_jail-0.12.1-py3-none-"+platform+".whl"), "0.12.1")
			}
			tc.mutate(t, dir)
			if _, err := validateWheelDir(dir, "0.12.1"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wheel validation = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWheelArchiveRejectsDuplicateMembers(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "yolo_jail-0.12.1-py3-none-manylinux_2_17_x86_64.whl")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for i := 0; i < 2; i++ {
		header := &zip.FileHeader{Name: "yolo_jail-0.12.1.dist-info/METADATA"}
		header.SetMode(0o644)
		member, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write([]byte("Metadata-Version: 2.1\nName: yolo-jail\nVersion: 0.12.1\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := validateWheel(filename, "0.12.1"); err == nil || !strings.Contains(err.Error(), "duplicate wheel member") {
		t.Fatalf("duplicate wheel member validation = %v, want refusal", err)
	}
}

func writeValidTestWheel(t *testing.T, filename, metadataVersion string) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	root := "yolo_jail-0.12.1.dist-info/"
	members := map[string]string{
		root + "METADATA": "Metadata-Version: 2.1\nName: yolo-jail\nVersion: " + metadataVersion + "\n",
		root + "WHEEL":    "Wheel-Version: 1.0\n",
		root + "RECORD":   "",
	}
	for name, body := range members {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o644)
		member, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
