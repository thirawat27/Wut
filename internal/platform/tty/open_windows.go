//go:build windows

package tty

import "os"

// openPlatform opens the console device pair. Windows has no /dev/tty: CONIN$
// and CONOUT$ are the equivalent, and they keep working when stdin or stdout
// have been redirected, which is exactly the case this package exists for.
func openPlatform() (in, out *os.File, same bool, err error) {
	inFile, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, false, err
	}
	outFile, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		_ = inFile.Close()
		return nil, nil, false, err
	}
	return inFile, outFile, false, nil
}
