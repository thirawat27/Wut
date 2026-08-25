package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/thirawat27/wut/internal/app"
)

// Server hosts the use cases behind a local socket.
type Server struct {
	app     *app.App
	version string
	dir     string

	token   string
	started time.Time

	mu       sync.Mutex
	requests uint64
	failures uint64
	samples  []float64 // request durations in ms, bounded
	lastSeen time.Time

	idleTimeout time.Duration
	listener    net.Listener
	stop        chan struct{}
	stopOnce    sync.Once
}

// maxSamples bounds the latency window. A few hundred is enough for a p95 and
// keeps the daemon's own memory flat.
const maxSamples = 512

// New builds a server. dir is the state directory.
func New(a *app.App, version, dir string, idleTimeout time.Duration) *Server {
	if idleTimeout <= 0 {
		idleTimeout = 30 * time.Minute
	}
	return &Server{
		app:         a,
		version:     version,
		dir:         dir,
		started:     time.Now(),
		lastSeen:    time.Now(),
		idleTimeout: idleTimeout,
		stop:        make(chan struct{}),
	}
}

// handshakeFile records where the daemon is listening and the token a client
// must present.
type handshakeFile struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
	PID      int    `json:"pid"`
	Socket   string `json:"socket"`
	Token    string `json:"token"`
	Started  int64  `json:"started_unix"`
}

func handshakePath(dir string) string { return filepath.Join(dir, "daemon.json") }

// Serve binds the socket and handles requests until the context is cancelled,
// Stop is called, or the idle timeout expires.
func (s *Server) Serve(ctx context.Context) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}

	token, err := newToken()
	if err != nil {
		return err
	}
	s.token = token

	ln, addr, err := listen(s.dir)
	if err != nil {
		return err
	}
	s.listener = ln
	defer ln.Close()

	// The handshake file is the discovery mechanism *and* the second half of
	// authentication: it is 0600, so another user on a shared machine cannot
	// read the token even if they can reach the socket.
	hs := handshakeFile{
		Protocol: ProtocolVersion,
		Version:  s.version,
		PID:      os.Getpid(),
		Socket:   addr,
		Token:    token,
		Started:  s.started.Unix(),
	}
	if err := writeHandshake(s.dir, hs); err != nil {
		return err
	}
	defer os.Remove(handshakePath(s.dir))

	go s.watchIdle(ctx)

	// Unblock Accept when the context ends.
	go func() {
		select {
		case <-ctx.Done():
		case <-s.stop:
		}
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			case <-s.stop:
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(ctx, conn)
	}
}

// Stop ends the server.
func (s *Server) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// watchIdle shuts the daemon down when nothing has used it for a while.
//
// A background process that outlives its usefulness is a background process
// people uninstall the whole tool over.
func (s *Server) watchIdle(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
			s.mu.Lock()
			idle := time.Since(s.lastSeen)
			s.mu.Unlock()
			if idle > s.idleTimeout {
				s.Stop()
				return
			}
		}
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	var req Request
	if err := readFrame(conn, &req); err != nil {
		return
	}
	started := time.Now()

	resp := s.dispatch(ctx, req)
	resp.Protocol = ProtocolVersion

	s.record(time.Since(started), resp.Error != "")
	_ = writeFrame(conn, resp)

	if req.Method == MethodStop && resp.Error == "" {
		s.Stop()
	}
}

func (s *Server) dispatch(ctx context.Context, req Request) Response {
	if req.Protocol != ProtocolVersion {
		return Response{Error: fmt.Sprintf("protocol %d, this daemon speaks %d", req.Protocol, ProtocolVersion)}
	}
	// Constant-time comparison is not warranted here: the token is only a
	// guard against another local user, the socket is already 0600, and an
	// attacker who can time this can read the handshake file anyway.
	// A server with no token yet refuses everything. Today the token is
	// generated before the socket is bound, so this cannot happen — but the
	// comparison below succeeds when both sides are empty, and that turns any
	// future reordering of Serve into an unauthenticated daemon. Failing
	// closed costs one line.
	if s.token == "" || req.Token != s.token {
		return Response{Error: "bad token"}
	}

	switch req.Method {
	case MethodPing:
		return Response{}

	case MethodStop:
		return Response{}

	case MethodStatus:
		st := s.status()
		return Response{Status: &st}

	case MethodAsk:
		if req.Ask == nil {
			return Response{Error: "ask request is empty"}
		}
		return s.result(s.app.Ask(ctx, *req.Ask))

	case MethodFix:
		if req.Fix == nil {
			return Response{Error: "fix request is empty"}
		}
		return s.result(s.app.Fix(ctx, *req.Fix))

	case MethodExplain:
		if req.Explain == nil {
			return Response{Error: "explain request is empty"}
		}
		return s.result(s.app.Explain(ctx, *req.Explain))
	}
	return Response{Error: fmt.Sprintf("unknown method %q", req.Method)}
}

func (s *Server) result(res app.Result, err error) Response {
	if err != nil {
		return Response{Error: err.Error()}
	}
	encoded, err := json.Marshal(res.Candidates)
	if err != nil {
		return Response{Error: err.Error()}
	}
	return Response{Result: &Result{
		Kind:       string(res.Kind),
		Query:      res.Query,
		Candidates: encoded,
		Notes:      res.Notes,
	}}
}

func (s *Server) record(d time.Duration, failed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	if failed {
		s.failures++
	}
	s.lastSeen = time.Now()
	if len(s.samples) >= maxSamples {
		copy(s.samples, s.samples[1:])
		s.samples = s.samples[:maxSamples-1]
	}
	s.samples = append(s.samples, float64(d.Microseconds())/1000)
}

func (s *Server) status() Status {
	s.mu.Lock()
	samples := append([]float64(nil), s.samples...)
	st := Status{
		Protocol:    ProtocolVersion,
		Version:     s.version,
		PID:         os.Getpid(),
		UptimeSec:   time.Since(s.started).Seconds(),
		Requests:    s.requests,
		Errors:      s.failures,
		IdleTimeout: s.idleTimeout.String(),
	}
	s.mu.Unlock()

	if s.listener != nil {
		st.SocketPath = s.listener.Addr().String()
	}
	ks := s.app.Deps().Knowledge.Stats()
	st.IndexReady, st.IndexPages = ks.Ready, ks.Pages
	if g := s.app.Deps().Generator; g != nil && g.Available() {
		st.ModelName = g.Name()
	}
	st.P50Millis, st.P95Millis = percentiles(samples)
	return st
}

func percentiles(samples []float64) (p50, p95 float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	sort.Float64s(samples)
	at := func(q float64) float64 {
		i := int(q * float64(len(samples)-1))
		return samples[i]
	}
	return at(0.50), at(0.95)
}

func newToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func writeHandshake(dir string, hs handshakeFile) error {
	data, err := json.MarshalIndent(hs, "", "  ")
	if err != nil {
		return err
	}
	path := handshakePath(dir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Rename(tmp, path)
}
