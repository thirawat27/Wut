package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/thirawat27/wut/internal/adapter/nullport"
	"github.com/thirawat27/wut/internal/app"
	"github.com/thirawat27/wut/internal/core/facts"
	"github.com/thirawat27/wut/internal/port"
)

// The daemon is a transport, not a tier. Its whole contract is that a client
// talking to a daemon and a client running in-process get the same answers,
// and that anything going wrong degrades to "slightly slower" rather than to
// "broken".

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	sent := Request{
		Protocol: ProtocolVersion,
		Token:    "t0ken",
		Method:   MethodAsk,
		Ask:      &app.AskRequest{Question: "compress a folder", Cwd: "/src", Limit: 5},
	}
	if err := writeFrame(&buf, sent); err != nil {
		t.Fatal(err)
	}

	var got Request
	if err := readFrame(&buf, &got); err != nil {
		t.Fatal(err)
	}
	if got.Method != sent.Method || got.Token != sent.Token || got.Ask == nil {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.Ask.Question != sent.Ask.Question || got.Ask.Limit != sent.Ask.Limit {
		t.Errorf("ask request = %+v", got.Ask)
	}
}

func TestFramesAreReadBackInOrder(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 3; i++ {
		if err := writeFrame(&buf, Request{Protocol: ProtocolVersion, Method: MethodPing, Token: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		var got Request
		if err := readFrame(&buf, &got); err != nil {
			t.Fatal(err)
		}
		if want := string(rune('a' + i)); got.Token != want {
			t.Errorf("frame %d carried %q, want %q", i, got.Token, want)
		}
	}
	if err := readFrame(&buf, &Request{}); err == nil {
		t.Error("reading past the last frame reported success")
	}
}

// A corrupt length prefix must produce an error, not an allocation the size of
// whatever the prefix claimed.
func TestAnAbsurdLengthIsRefused(t *testing.T) {
	var header [4]byte
	putUint32(header[:], 0xFFFFFFFF)
	err := readFrame(bytes.NewReader(append(header[:], "junk"...)), &Request{})
	if err == nil {
		t.Fatal("a 4 GB frame was accepted")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("the error does not mention the limit: %v", err)
	}
}

func TestATruncatedFrameIsAnError(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, Request{Protocol: ProtocolVersion, Method: MethodPing}); err != nil {
		t.Fatal(err)
	}
	truncated := buf.Bytes()[:buf.Len()-3]
	if err := readFrame(bytes.NewReader(truncated), &Request{}); err == nil {
		t.Error("a truncated frame was accepted")
	}
	if err := readFrame(bytes.NewReader([]byte{1, 2}), &Request{}); err == nil {
		t.Error("a frame shorter than its header was accepted")
	} else if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("short header gave %v, want an unexpected EOF", err)
	}
}

func TestUint32IsLittleEndianBothWays(t *testing.T) {
	for _, v := range []uint32{0, 1, 255, 256, 65535, 1 << 20, 0xDEADBEEF} {
		var b [4]byte
		putUint32(b[:], v)
		if got := getUint32(b[:]); got != v {
			t.Errorf("round trip of %d gave %d", v, got)
		}
	}
}

func testServer(t *testing.T) *Server {
	t.Helper()
	a := app.New(app.Deps{
		Knowledge: nullport.Knowledge{Reason: "test"},
		Events:    nullport.Events{},
		Generator: nullport.Generator{},
		Embedder:  nullport.Embedder{},
		Facts:     nullFacts{},
		Clock:     port.SystemClock{},
	})
	s := New(a, "1.0.0", t.TempDir(), time.Minute)
	// Serve generates the token; these tests dispatch directly, so they set
	// one here. Leaving it empty is itself a case, tested separately.
	s.token = "test-token"
	return s
}

// A request with the wrong token is refused. The token is the only thing
// stopping another local user from asking this daemon to do work on the
// user's behalf.
func TestABadTokenIsRefused(t *testing.T) {
	s := testServer(t)
	resp := s.dispatch(context.Background(), Request{
		Protocol: ProtocolVersion, Token: "not the token", Method: MethodPing,
	})
	if resp.Error == "" {
		t.Fatal("a request with the wrong token was served")
	}
	if !strings.Contains(resp.Error, "token") {
		t.Errorf("the refusal does not mention the token: %q", resp.Error)
	}
}

// A server that has not generated a token yet must refuse everything.
//
// The comparison `req.Token != s.token` succeeds when both are empty, so a
// daemon that bound its socket before generating a token would be
// unauthenticated. That ordering is correct today; this makes it stay correct.
func TestAnEmptyTokenIsRefused(t *testing.T) {
	s := testServer(t)
	s.token = ""
	resp := s.dispatch(context.Background(), Request{Protocol: ProtocolVersion, Method: MethodPing})
	if resp.Error == "" {
		t.Fatal("a request with no token at all was served")
	}

	s.token = "real-token"
	if resp := s.dispatch(context.Background(), Request{Protocol: ProtocolVersion, Method: MethodPing}); resp.Error == "" {
		t.Fatal("an empty token was accepted once the server had a real one")
	}
}

// A protocol mismatch must be an ordinary error the client can fall back on,
// not a crash and not silent success. An upgraded binary talking to an old
// daemon has to degrade to "slightly slower".
func TestAProtocolMismatchIsReportedClearly(t *testing.T) {
	s := testServer(t)
	resp := s.dispatch(context.Background(), Request{
		Protocol: ProtocolVersion + 99, Token: s.token, Method: MethodPing,
	})
	if resp.Error == "" {
		t.Fatal("a request from a different protocol version was served")
	}
	if !strings.Contains(resp.Error, "protocol") {
		t.Errorf("the refusal does not mention the protocol: %q", resp.Error)
	}
}

// The version is checked before the token, so a client from another version
// is told the truth rather than being told its token is wrong.
func TestVersionIsCheckedBeforeTheToken(t *testing.T) {
	s := testServer(t)
	resp := s.dispatch(context.Background(), Request{
		Protocol: ProtocolVersion + 1, Token: "wrong too", Method: MethodPing,
	})
	if !strings.Contains(resp.Error, "protocol") {
		t.Errorf("error = %q, want it to name the protocol mismatch", resp.Error)
	}
}

func TestPingAndStatus(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()

	if resp := s.dispatch(ctx, Request{Protocol: ProtocolVersion, Token: s.token, Method: MethodPing}); resp.Error != "" {
		t.Errorf("ping failed: %s", resp.Error)
	}

	resp := s.dispatch(ctx, Request{Protocol: ProtocolVersion, Token: s.token, Method: MethodStatus})
	if resp.Error != "" || resp.Status == nil {
		t.Fatalf("status failed: %+v", resp)
	}
	if resp.Status.Version != "1.0.0" {
		t.Errorf("status reports version %q", resp.Status.Version)
	}
}

// An empty payload must be an error rather than a nil dereference. The peer is
// our own binary, but a bug in it should not take the daemon down for every
// other shell on the machine.
func TestAnEmptyPayloadIsAnErrorNotACrash(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	for _, method := range []Method{MethodAsk, MethodFix, MethodExplain} {
		resp := s.dispatch(ctx, Request{Protocol: ProtocolVersion, Token: s.token, Method: method})
		if resp.Error == "" {
			t.Errorf("%s with no payload was accepted", method)
		}
	}
}

func TestAnUnknownMethodIsRefused(t *testing.T) {
	s := testServer(t)
	resp := s.dispatch(context.Background(), Request{
		Protocol: ProtocolVersion, Token: s.token, Method: Method("dance"),
	})
	if resp.Error == "" {
		t.Error("an unknown method was accepted")
	}
}

// The handshake file is how a client finds the socket and the token, so it has
// to be JSON the client can actually read.
func TestHandshakePathAndShape(t *testing.T) {
	dir := t.TempDir()
	if got := handshakePath(dir); !strings.HasSuffix(got, "daemon.json") {
		t.Errorf("handshake path = %q", got)
	}
	data, err := json.Marshal(handshakeFile{Protocol: ProtocolVersion, Socket: "127.0.0.1:1", Token: "t", PID: 1, Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	var back handshakeFile
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Token != "t" || back.Socket != "127.0.0.1:1" || back.Protocol != ProtocolVersion {
		t.Errorf("handshake round trip = %+v", back)
	}
}

// A client with no daemon running must report unavailable rather than fail.
// Every call site falls back to running in-process on this answer.
func TestAClientWithNoDaemonIsUnavailable(t *testing.T) {
	c := NewClient(t.TempDir())
	if c.Available() {
		t.Error("a client reported a daemon available with no handshake file")
	}
	if err := c.Ping(); err == nil {
		t.Error("pinging a daemon that is not running reported success")
	}
}

// nullFacts is the second implementation every port is required to have.
type nullFacts struct{}

func (nullFacts) For(string) facts.Facts { return facts.Empty{} }
