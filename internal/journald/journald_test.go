package journald

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestFrameStreamIDsAreDistinctFromLoopholeProtocol(t *testing.T) {
	// The journal bridge uses 1/2/3, NOT frameproto v1's 0/1/2 — the whole
	// point of this test is to lock that in.
	if FrameStdout != 1 || FrameStderr != 2 || FrameExit != 3 {
		t.Fatalf("journal stream ids = %d/%d/%d, want 1/2/3", FrameStdout, FrameStderr, FrameExit)
	}
}

func TestWriteFrameHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, FrameStdout, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	// >BI header: stream=1, len=2, then "hi".
	want := []byte{0x01, 0x00, 0x00, 0x00, 0x02, 'h', 'i'}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("frame = % x, want % x", buf.Bytes(), want)
	}
}

func TestWriteExitSigned(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteExit(&buf, 127); err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()
	if got[0] != FrameExit {
		t.Errorf("exit stream = %d, want %d", got[0], FrameExit)
	}
	if binary.BigEndian.Uint32(got[1:5]) != 4 {
		t.Errorf("exit payload len = %d, want 4", binary.BigEndian.Uint32(got[1:5]))
	}
	rc := int32(binary.BigEndian.Uint32(got[5:9]))
	if rc != 127 {
		t.Errorf("exit rc = %d, want 127", rc)
	}
}

func TestParseRequestValidation(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		mode    string
		wantErr bool
		exit    int
		args    []string
	}{
		{"valid full", `{"args":["-n","20"]}`, "full", false, 0, []string{"-n", "20"}},
		{"valid user prepends --user", `{"args":["-f"]}`, "user", false, 0, []string{"--user", "-f"}},
		{"empty args full", `{}`, "full", false, 0, nil},
		{"empty args user", `{}`, "user", false, 0, []string{"--user"}},
		{"invalid json", `{not json`, "full", true, 2, nil},
		{"args not a list", `{"args":"nope"}`, "full", true, 2, nil},
		{"non-string arg", `{"args":[1]}`, "full", true, 2, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseRequest([]byte(tc.header), tc.mode)
			if tc.wantErr {
				if got.ErrText == "" {
					t.Fatalf("expected error, got args=%v", got.Args)
				}
				if got.ExitCode != tc.exit {
					t.Errorf("exit = %d, want %d", got.ExitCode, tc.exit)
				}
				return
			}
			if got.ErrText != "" {
				t.Fatalf("unexpected error: %q", got.ErrText)
			}
			if strings.Join(got.Args, " ") != strings.Join(tc.args, " ") {
				t.Errorf("args = %v, want %v", got.Args, tc.args)
			}
		})
	}
}

func TestParseRequestTooManyArgs(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"args":[`)
	for i := 0; i < MaxArgs+1; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`"x"`)
	}
	sb.WriteString(`]}`)
	got := ParseRequest([]byte(sb.String()), "full")
	if got.ErrText == "" || got.ExitCode != 2 {
		t.Errorf(">MaxArgs should be rejected with exit 2, got %+v", got)
	}
}

func TestParseRequestArgTooLong(t *testing.T) {
	long := strings.Repeat("x", MaxArgLen+1)
	got := ParseRequest([]byte(`{"args":["`+long+`"]}`), "full")
	if got.ErrText == "" || got.ExitCode != 2 {
		t.Errorf("over-long arg should be rejected with exit 2, got %+v", got)
	}
}
