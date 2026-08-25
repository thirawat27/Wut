// Package daemon is a transport, not a tier.
//
// It serves exactly the use cases in internal/app, with the same ports behind
// them, so the daemon path and the in-process path cannot drift: there is only
// one implementation, called two ways. Nothing in the product may require the
// daemon to be running, and the whole test suite is expected to pass with
// WUT_NO_DAEMON=1.
//
// What it buys is latency. A cold CLI invocation reads a twenty-megabyte index
// and, once a Tier 2 model is installed, would pay seconds loading it. The
// daemon holds both, so the same answer arrives in milliseconds.
package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/thirawat27/wut/internal/app"
)

// ProtocolVersion is exchanged on every request.
//
// A mismatch is not an error: the client falls back to running in-process. An
// upgraded binary talking to a daemon from the previous version must degrade
// to "slightly slower", never to "broken".
const ProtocolVersion = 1

// Method names the operation.
type Method string

const (
	MethodPing    Method = "ping"
	MethodAsk     Method = "ask"
	MethodFix     Method = "fix"
	MethodExplain Method = "explain"
	MethodStatus  Method = "status"
	MethodStop    Method = "stop"
)

// Request is one call.
type Request struct {
	Protocol int    `json:"protocol"`
	Token    string `json:"token"`
	Method   Method `json:"method"`

	// Exactly one of these is set, matching Method.
	Ask     *app.AskRequest     `json:"ask,omitempty"`
	Fix     *app.FixRequest     `json:"fix,omitempty"`
	Explain *app.ExplainRequest `json:"explain,omitempty"`
}

// Response is one reply.
type Response struct {
	Protocol int    `json:"protocol"`
	Error    string `json:"error,omitempty"`

	Result *Result `json:"result,omitempty"`
	Status *Status `json:"status,omitempty"`
}

// Result mirrors app.Result over the wire.
//
// It is a separate type rather than app.Result reused directly because the
// wire format is a compatibility surface: changing a field in the use-case
// layer must not silently change what an older client can parse.
type Result struct {
	Kind       string   `json:"kind"`
	Query      string   `json:"query,omitempty"`
	Candidates []byte   `json:"candidates"` // JSON-encoded []candidate.Candidate
	Notes      []string `json:"notes,omitempty"`
}

// Status describes the running daemon.
type Status struct {
	Protocol    int     `json:"protocol"`
	Version     string  `json:"version"`
	PID         int     `json:"pid"`
	UptimeSec   float64 `json:"uptime_seconds"`
	Requests    uint64  `json:"requests"`
	Errors      uint64  `json:"errors"`
	IndexReady  bool    `json:"index_ready"`
	IndexPages  int     `json:"index_pages"`
	ModelName   string  `json:"model,omitempty"`
	RSSBytes    uint64  `json:"rss_bytes,omitempty"`
	IdleTimeout string  `json:"idle_timeout"`
	SocketPath  string  `json:"socket"`
	P50Millis   float64 `json:"p50_ms"`
	P95Millis   float64 `json:"p95_ms"`
}

// Errors the client distinguishes.
var (
	// ErrUnavailable means no healthy daemon answered. Every caller responds
	// the same way: run the use case in-process and say nothing.
	ErrUnavailable = errors.New("daemon unavailable")
	// ErrVersionMismatch means a daemon answered but speaks another protocol.
	ErrVersionMismatch = errors.New("daemon speaks a different protocol version")
)

// maxFrame bounds one message. The socket is local and the peer is our own
// binary, but a bound costs nothing and turns a corrupt length prefix into an
// error instead of an allocation the size of the length field.
const maxFrame = 8 << 20

// writeFrame writes a length-prefixed JSON message.
func writeFrame(w io.Writer, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(payload) > maxFrame {
		return fmt.Errorf("message is %d bytes, over the %d limit", len(payload), maxFrame)
	}
	var header [4]byte
	putUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

// readFrame reads a length-prefixed JSON message.
func readFrame(r io.Reader, v any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	n := getUint32(header[:])
	if n > maxFrame {
		return fmt.Errorf("frame claims %d bytes, over the %d limit", n, maxFrame)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}
	return json.Unmarshal(payload, v)
}

func putUint32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func getUint32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
