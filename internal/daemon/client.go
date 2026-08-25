package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/thirawat27/wut/internal/app"
	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/pkg/wutjson"
)

// Budgets. These are the whole reason the fallback is silent: if the daemon
// cannot answer within them, going in-process is faster than waiting.
const (
	connectTimeout = 200 * time.Millisecond
	requestTimeout = 5 * time.Second
	// generateTimeout applies once a Tier 2 model is in play, where a few
	// seconds is normal rather than a symptom.
	generateTimeout = 30 * time.Second
)

// Client talks to a running daemon.
type Client struct {
	dir string
}

// NewClient returns a client for the state directory.
func NewClient(dir string) *Client { return &Client{dir: dir} }

// Available reports a daemon this client can talk to.
func (c *Client) Available() bool {
	return c.Ping() == nil
}

// Ping checks liveness and protocol compatibility.
func (c *Client) Ping() error {
	_, err := c.call(context.Background(), Request{Method: MethodPing}, requestTimeout)
	return err
}

// Status asks the daemon to describe itself.
func (c *Client) Status(ctx context.Context) (Status, error) {
	resp, err := c.call(ctx, Request{Method: MethodStatus}, requestTimeout)
	if err != nil {
		return Status{}, err
	}
	if resp.Status == nil {
		return Status{}, errors.New("daemon returned no status")
	}
	return *resp.Status, nil
}

// Stop asks the daemon to exit.
func (c *Client) Stop(ctx context.Context) error {
	_, err := c.call(ctx, Request{Method: MethodStop}, requestTimeout)
	return err
}

// Ask, Fix, and Explain mirror the use cases.
//
// Each returns ErrUnavailable when there is no healthy daemon, and the caller
// is expected to run the same use case in-process without telling the user
// anything. A daemon that is missing, wedged, or a version behind must never
// be worse than not having one.
func (c *Client) Ask(ctx context.Context, req app.AskRequest) (app.Result, error) {
	return c.useCase(ctx, Request{Method: MethodAsk, Ask: &req}, requestTimeout)
}

func (c *Client) Fix(ctx context.Context, req app.FixRequest) (app.Result, error) {
	return c.useCase(ctx, Request{Method: MethodFix, Fix: &req}, requestTimeout)
}

func (c *Client) Explain(ctx context.Context, req app.ExplainRequest) (app.Result, error) {
	// Explain may reach a generative model, so it gets the longer budget.
	return c.useCase(ctx, Request{Method: MethodExplain, Explain: &req}, generateTimeout)
}

func (c *Client) useCase(ctx context.Context, req Request, timeout time.Duration) (app.Result, error) {
	resp, err := c.call(ctx, req, timeout)
	if err != nil {
		return app.Result{}, err
	}
	if resp.Result == nil {
		return app.Result{}, errors.New("daemon returned no result")
	}
	var cands []candidate.Candidate
	if len(resp.Result.Candidates) > 0 {
		if err := json.Unmarshal(resp.Result.Candidates, &cands); err != nil {
			return app.Result{}, err
		}
	}
	return app.Result{
		Kind:       wutjson.Kind(resp.Result.Kind),
		Query:      resp.Result.Query,
		Candidates: cands,
		Notes:      resp.Result.Notes,
	}, nil
}

func (c *Client) call(ctx context.Context, req Request, timeout time.Duration) (Response, error) {
	hs, err := readHandshake(c.dir)
	if err != nil {
		return Response{}, ErrUnavailable
	}
	if hs.Protocol != ProtocolVersion {
		return Response{}, ErrVersionMismatch
	}

	conn, err := dialWithTimeout(hs.Socket, connectTimeout)
	if err != nil {
		return Response{}, ErrUnavailable
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	req.Protocol = ProtocolVersion
	req.Token = hs.Token
	if err := writeFrame(conn, req); err != nil {
		return Response{}, ErrUnavailable
	}
	var resp Response
	if err := readFrame(conn, &resp); err != nil {
		return Response{}, ErrUnavailable
	}
	if resp.Error != "" {
		return resp, fmt.Errorf("daemon: %s", resp.Error)
	}
	return resp, nil
}

func dialWithTimeout(addr string, timeout time.Duration) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := dial(addr)
		ch <- result{conn, err}
	}()
	select {
	case r := <-ch:
		return r.conn, r.err
	case <-time.After(timeout):
		return nil, errors.New("connect timed out")
	}
}

func readHandshake(dir string) (handshakeFile, error) {
	data, err := os.ReadFile(handshakePath(dir))
	if err != nil {
		return handshakeFile{}, err
	}
	var hs handshakeFile
	if err := json.Unmarshal(data, &hs); err != nil {
		return handshakeFile{}, err
	}
	if hs.Socket == "" || hs.Token == "" {
		return handshakeFile{}, errors.New("handshake file is incomplete")
	}
	return hs, nil
}
