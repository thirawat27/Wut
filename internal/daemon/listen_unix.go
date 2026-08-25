//go:build !windows

package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// socketPath is the unix socket the daemon binds.
func socketPath(dir string) string { return filepath.Join(dir, "daemon.sock") }

// listen binds the unix socket.
//
// A stale socket file is removed first, but only after a connect attempt fails
// — deleting a socket another live daemon is using would take that daemon's
// clients down with it.
func listen(dir string) (net.Listener, string, error) {
	path := socketPath(dir)

	if _, err := os.Stat(path); err == nil {
		if conn, err := net.Dial("unix", path); err == nil {
			_ = conn.Close()
			return nil, "", fmt.Errorf("a daemon is already listening on %s", path)
		}
		// Nothing answered: the file is left over from a crash.
		_ = os.Remove(path)
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", err
	}
	// Only this user may connect. The token in the handshake file is the
	// second layer; this is the first.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, "", err
	}
	return ln, path, nil
}

// dial connects to a daemon socket.
func dial(addr string) (net.Conn, error) {
	return net.Dial("unix", addr)
}
