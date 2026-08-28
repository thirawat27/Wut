//go:build windows

package daemon

import "testing"

// The address comes out of the handshake file and the client presents its
// token to whatever it names, so anything that is not this machine has to be
// refused before the dial rather than after it.
func TestDialRefusesAnythingButLoopback(t *testing.T) {
	for _, addr := range []string{
		"example.com:80",
		"10.0.0.5:11434",
		"0.0.0.0:9000",
		"127.0.0.1",
		"127.0.0.1:not-a-port",
		"",
	} {
		if err := checkLoopback(addr); err == nil {
			t.Errorf("checkLoopback(%q) accepted it", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:11434", "[::1]:9000", "127.0.0.5:1"} {
		if err := checkLoopback(addr); err != nil {
			t.Errorf("checkLoopback(%q): %v", addr, err)
		}
	}
}
