//go:build windows

package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// probeDialTimeout bounds the "is one already running" check, so a wedged
// listener cannot hang a daemon start.
const probeDialTimeout = 2 * time.Second

// Windows has AF_UNIX in recent builds, but Go's net package does not expose
// named pipes, and unix sockets on Windows do not support the permission model
// this design relies on. A loopback listener on an ephemeral port, bound to
// 127.0.0.1 only and guarded by the token in the 0600 handshake file, is the
// portable equivalent.
//
// The port is never fixed and never advertised: it is written to the handshake
// file, which only this user can read.
func socketPath(dir string) string { return filepath.Join(dir, "daemon.port") }

func listen(dir string) (net.Listener, string, error) {
	if addr, ok := existingListener(dir); ok {
		return nil, "", fmt.Errorf("a daemon is already listening on %s", addr)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	addr := ln.Addr().String()
	if err := os.WriteFile(socketPath(dir), []byte(addr), 0o600); err != nil {
		_ = ln.Close()
		return nil, "", err
	}
	return ln, addr, nil
}

// existingListener reports a live daemon, so a second one refuses to start
// rather than silently competing for the same state.
func existingListener(dir string) (string, bool) {
	data, err := os.ReadFile(socketPath(dir))
	if err != nil {
		return "", false
	}
	addr := string(data)
	if checkLoopback(addr) != nil {
		return "", false
	}
	conn, err := net.DialTimeout("tcp", addr, probeDialTimeout)
	if err != nil {
		_ = os.Remove(socketPath(dir)) // stale
		return "", false
	}
	_ = conn.Close()
	return addr, true
}

// dial connects to a daemon, giving up after timeout.
//
// The bound belongs to the dialler rather than to a race against a timer: a
// dial abandoned by a timer still completes, and the connection it produced
// has nobody left to close it.
func dial(addr string, timeout time.Duration) (net.Conn, error) {
	if err := checkLoopback(addr); err != nil {
		return nil, err
	}
	return net.DialTimeout("tcp", addr, timeout)
}

// checkLoopback rejects anything that is not this machine.
//
// The address comes out of the handshake file, and the client presents its
// token to whatever that file names. Validating only the port — which is what
// this did — meant a tampered or stale handshake file could send the token to
// any host on the network. The error message claimed the check all along; this
// is the check.
func checkLoopback(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return fmt.Errorf("not a loopback address: %q", addr)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("not a loopback address: %q", addr)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("not a loopback address: %q", addr)
	}
	return nil
}
