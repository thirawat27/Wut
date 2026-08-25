//go:build !windows

package tty

import "os"

// openPlatform opens /dev/tty, which is the controlling terminal regardless of
// how stdin and stdout have been redirected. Opening it read-write gives one
// handle for both directions.
func openPlatform() (in, out *os.File, same bool, err error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, false, err
	}
	return f, f, true, nil
}
