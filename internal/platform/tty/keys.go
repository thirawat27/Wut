package tty

import (
	"errors"
	"io"
)

// Key is one decoded keypress.
type Key int

const (
	KeyUnknown Key = iota
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyEnter
	KeyEscape
	KeyCtrlC
	KeyCtrlD
	KeyBackspace
	KeyTab
	KeyRune
)

// Press pairs a key with the rune it carried, when it carried one.
type Press struct {
	Key  Key
	Rune rune
}

// ReadKey decodes one keypress from a raw-mode terminal.
//
// Escape sequences are the awkward part: a bare Esc and the start of an arrow
// key are the same first byte. This reads the following bytes only when they
// are already available, so pressing Esc alone is not swallowed while the
// reader waits for a sequence that will never arrive.
func ReadKey(r io.Reader) (Press, error) {
	var buf [1]byte
	n, err := r.Read(buf[:])
	if err != nil {
		return Press{}, err
	}
	if n == 0 {
		return Press{}, errors.New("no input")
	}

	switch b := buf[0]; b {
	case 0x03:
		return Press{Key: KeyCtrlC}, nil
	case 0x04:
		return Press{Key: KeyCtrlD}, nil
	case '\r', '\n':
		return Press{Key: KeyEnter}, nil
	case '\t':
		return Press{Key: KeyTab}, nil
	case 0x7f, 0x08:
		return Press{Key: KeyBackspace}, nil
	case 0x1b:
		return readEscapeSequence(r)
	default:
		if b < 0x20 {
			return Press{Key: KeyUnknown}, nil
		}
		return Press{Key: KeyRune, Rune: rune(b)}, nil
	}
}

func readEscapeSequence(r io.Reader) (Press, error) {
	var buf [2]byte
	n, err := r.Read(buf[:1])
	if err != nil || n == 0 {
		// Nothing followed the ESC byte, so the user pressed Escape. This is
		// not an error path: a bare Escape and a truncated escape sequence are
		// indistinguishable at this layer, and treating both as Escape is what
		// makes the key work at all.
		return Press{Key: KeyEscape}, nil //nolint:nilerr // a read that ends here means Escape
	}
	if buf[0] != '[' && buf[0] != 'O' {
		return Press{Key: KeyEscape}, nil
	}
	n, err = r.Read(buf[1:2])
	if err != nil || n == 0 {
		return Press{Key: KeyEscape}, nil //nolint:nilerr // same: an incomplete sequence is Escape
	}
	switch buf[1] {
	case 'A':
		return Press{Key: KeyUp}, nil
	case 'B':
		return Press{Key: KeyDown}, nil
	case 'C':
		return Press{Key: KeyRight}, nil
	case 'D':
		return Press{Key: KeyLeft}, nil
	}
	return Press{Key: KeyUnknown}, nil
}
