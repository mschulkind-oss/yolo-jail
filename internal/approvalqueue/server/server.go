package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/hub"
)

// CSP is the Content-Security-Policy every response carries (trust-boundary doc §5).
const CSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Config is what a Server needs.
type Config struct {
	Hub            *hub.Hub
	Auth           *Auth
	Assets         fs.FS         // index.html, app.js, app.css
	Logger         *log.Logger   // counts, errors and peers only; never a code, token or header
	StreamInterval time.Duration // the stream sends at least this often; default 5 s
}

// Server is the read-only HTTP server.
type Server struct {
	cfg    Config
	base   context.Context
	cancel context.CancelFunc

	mu     sync.Mutex // guards http and closed (Serve races Close)
	http   *http.Server
	closed bool
}

// New makes a Server.
func New(cfg Config) *Server {
	if cfg.StreamInterval <= 0 {
		cfg.StreamInterval = 5 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(discard{}, "", 0)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{cfg: cfg, base: ctx, cancel: cancel}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// The connection limits. Anything that can reach the port can open a connection, so a client
// that stalls mid-request or sits idle is dropped. There is no write timeout: the live stream
// is a long response by design.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	idleTimeout       = 60 * time.Second
	maxHeaderBytes    = 16 << 10
)

func (s *Server) httpServer(port int) *http.Server {
	return &http.Server{
		Handler:           s.Handler(port),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return s.base },
	}
}

// Serve serves on l until Close; the Host and Origin checks use l's port.
func (s *Server) Serve(l net.Listener) error {
	srv := s.httpServer(l.Addr().(*net.TCPAddr).Port)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		l.Close()
		return nil
	}
	s.http = srv
	s.mu.Unlock()
	err := srv.Serve(l)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Close ends every stream and stops serving.
func (s *Server) Close() {
	s.cancel()
	s.mu.Lock()
	s.closed = true
	srv := s.http
	s.mu.Unlock()
	if srv != nil {
		srv.Close()
	}
}

// Handler is the full handler for a listener on 127.0.0.1:port.
func (s *Server) Handler(port int) http.Handler {
	host := fmt.Sprintf("127.0.0.1:%d", port)
	origin := "http://" + host
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w := &statusWriter{ResponseWriter: rw, status: 200}
		defer func() {
			s.cfg.Logger.Printf("%s %s -> %d peer=%s", r.Method, r.URL.Path, w.status, r.RemoteAddr)
		}()
		h := w.Header()
		h.Set("Content-Security-Policy", CSP)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}

		if r.Host != host {
			w.WriteHeader(http.StatusMisdirectedRequest)
			return
		}
		if r.Method == http.MethodOptions {
			h.Set("Allow", "GET, HEAD, POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if o := r.Header.Get("Origin"); (o != "" && o != origin) || (o == "" && r.Method == http.MethodPost) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && strings.HasPrefix(r.URL.Path, "/api/") {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		get := r.Method == http.MethodGet || r.Method == http.MethodHead
		if static, ok := staticFiles[r.URL.Path]; ok && get {
			s.static(w, r, static)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/session" {
			s.session(w, r)
			return
		}

		// Everything else needs a live token, even a path that does not exist.
		tok, ok := s.bearer(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/state":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.state(w)
		case "/api/stream":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.stream(w, r, tok)
		case "/", "/app.js", "/app.css", "/api/session":
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

type staticFile struct{ name, ctype string }

var staticFiles = map[string]staticFile{
	"/":        {"index.html", "text/html; charset=utf-8"},
	"/app.js":  {"app.js", "text/javascript; charset=utf-8"},
	"/app.css": {"app.css", "text/css; charset=utf-8"},
}

// bearer returns the request's token when it is a live one.
func (s *Server) bearer(r *http.Request) (string, bool) {
	const p = "Bearer "
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, p) {
		return "", false
	}
	return v[len(p):], s.cfg.Auth.Valid(v[len(p):])
}

func (s *Server) static(w http.ResponseWriter, r *http.Request, f staticFile) {
	b, err := fs.ReadFile(s.cfg.Assets, f.name)
	if err != nil {
		s.cfg.Logger.Printf("asset %s: %v", f.name, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", f.ctype)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(b)
	}
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	tok, res := s.cfg.Auth.ExchangeResult(body.Code)
	switch res {
	case Reused:
		// The race alarm: the person's tab arrived second. The page says so.
		w.WriteHeader(http.StatusConflict)
		return
	case Failed:
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(EncodeJSON(map[string]string{"token": tok}))
}

func (s *Server) state(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(EncodeJSON(s.cfg.Hub.Published()))
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request, tok string) {
	fl, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	send := func() bool {
		// A token evicted or otherwise gone ends its stream; the page's reconnect then meets a
		// 401 and shows the "get a new link" message.
		if !s.cfg.Auth.Valid(tok) {
			return false
		}
		if _, err := w.Write(append(append([]byte("data: "), EncodeJSON(s.cfg.Hub.Published())...), '\n', '\n')); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	tick := time.NewTicker(s.cfg.StreamInterval)
	defer tick.Stop()
	for {
		changed := s.cfg.Hub.Changed()
		if !send() {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-s.base.Done():
			return
		case <-changed:
		case <-tick.C:
		}
	}
}

// EncodeJSON marshals v and escapes every non-ASCII character as \uXXXX, so what the server
// emits is pure ASCII: control, bidirectional and zero-width characters can never reach the
// wire raw, and the text is still ordinary valid JSON. encoding/json already escapes <, >, &,
// controls and the line separators and replaces invalid UTF-8.
func EncodeJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(`{"error":"encoding failed"}`)
	}
	var out bytes.Buffer
	out.Grow(len(b))
	for _, r := range string(b) {
		switch {
		case r < 0x7f:
			out.WriteByte(byte(r))
		case r < 0x10000:
			fmt.Fprintf(&out, `\u%04x`, r)
		default:
			r -= 0x10000
			fmt.Fprintf(&out, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		}
	}
	return out.Bytes()
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
