package ghbroker

import "bytes"

// redact.go is the backstop BB-D16 describes: the broker reads the host token once, at
// start, and replaces any occurrence of it in a command's output with a marker, counting
// each. The count should always be zero — every command that prints a token is refused
// before it runs (§4.2) — so a non-zero count in the audit line is a classifier bug, and
// the audit is where it is seen.

// RedactionMarker replaces the token wherever it appears in output.
const RedactionMarker = "[redacted by yolo]"

// redactor is a streaming replacer: output arrives in arbitrary chunks, so it holds back
// the last len(token)-1 bytes of each write in case a token straddles two chunks.
type redactor struct {
	token []byte
	held  []byte
	count int
	emit  func([]byte)
}

func newRedactor(token string, emit func([]byte)) *redactor {
	return &redactor{token: []byte(token), emit: emit}
}

// write takes the next chunk of one stream.
func (r *redactor) write(p []byte) {
	if len(r.token) == 0 {
		r.emit(p)
		return
	}
	r.held = append(r.held, p...)
	for {
		i := bytes.Index(r.held, r.token)
		if i < 0 {
			break
		}
		if i > 0 {
			r.emit(append([]byte(nil), r.held[:i]...))
		}
		r.emit([]byte(RedactionMarker))
		r.count++
		r.held = r.held[i+len(r.token):]
	}
	if keep := len(r.token) - 1; len(r.held) > keep {
		cut := len(r.held) - keep
		r.emit(append([]byte(nil), r.held[:cut]...))
		r.held = append([]byte(nil), r.held[cut:]...)
	}
}

// flush emits what is held once the stream has ended.
func (r *redactor) flush() {
	if len(r.held) > 0 {
		r.emit(r.held)
		r.held = nil
	}
}
