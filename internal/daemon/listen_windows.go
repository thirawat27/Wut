//go:build windows

package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

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
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return "", false
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		_ = os.Remove(socketPath(dir)) // stale
		return "", false
	}
	_ = conn.Close()
	return addr, true
}

func dial(addr string) (net.Conn, error) {
	if _, port, err := net.SplitHostPort(addr); err != nil || port == "" {
		return nil, fmt.Errorf("not a loopback address: %q", addr)
	} else if _, err := strconv.Atoi(port); err != nil {
		return nil, fmt.Errorf("not a loopback address: %q", addr)
	}
	return net.Dial("tcp", addr)
}
