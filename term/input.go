package term

import (
	"fmt"
	"os"

	goterm "golang.org/x/term"
)

// KeyReader reads single keypresses from stdin in raw terminal mode.
// It is safe to use concurrently; keypresses are delivered via a channel.
type KeyReader struct {
	ch       chan byte
	oldState *goterm.State
	done     chan struct{}
}

// NewKeyReader enters raw terminal mode on stdin and starts a goroutine
// that reads single bytes, sending them to the Keys channel.
// Returns nil if stdin is not a terminal.
func NewKeyReader() *KeyReader {
	fd := int(os.Stdin.Fd())
	if !goterm.IsTerminal(fd) {
		return nil
	}

	old, err := goterm.MakeRaw(fd)
	if err != nil {
		return nil
	}

	Default.SetRawMode(true)

	kr := &KeyReader{
		ch:       make(chan byte, 16),
		oldState: old,
		done:     make(chan struct{}),
	}

	go kr.readLoop()
	return kr
}

func (k *KeyReader) readLoop() {
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			close(k.ch)
			return
		}
		if n == 1 {
			select {
			case k.ch <- buf[0]:
			case <-k.done:
				return
			}
		}
	}
}

// Keys returns the channel of single keypresses.
func (k *KeyReader) Keys() <-chan byte {
	return k.ch
}

// ReadLine reads a line of text from the key channel with the given prompt.
// Backspace, Escape and Ctrl-C are handled. Returns the entered string on
// Enter, or ("", false) if cancelled.
func (k *KeyReader) ReadLine(prompt string) (string, bool) {
	fmt.Fprint(os.Stderr, prompt)

	var line []byte
	for b := range k.ch {
		switch {
		case b == '\r' || b == '\n':
			fmt.Fprint(os.Stderr, "\r\n")
			return string(line), true
		case b == 3: // Ctrl-C
			fmt.Fprint(os.Stderr, "\r\n")
			return "", false
		case b == 27: // Escape
			fmt.Fprint(os.Stderr, "\r\n")
			return "", false
		case b == 127 || b == 8: // Backspace / DEL
			if len(line) > 0 {
				line = line[:len(line)-1]
				fmt.Fprint(os.Stderr, "\b \b")
			}
		case b >= 32: // Printable
			line = append(line, b)
			fmt.Fprint(os.Stderr, string(b))
		}
	}
	return "", false
}

// ReadLineFiltered reads a line of text, calling onChange after each keystroke.
// onChange receives the current input and returns the number of lines it printed.
// Those lines are cleared before the next onChange call.
// Returns the entered string on Enter, or ("", false) if cancelled.
func (k *KeyReader) ReadLineFiltered(prompt string, onChange func(input string) int) (string, bool) {
	prevLines := onChange("")
	fmt.Fprint(os.Stderr, prompt)

	var line []byte
	for b := range k.ch {
		switch {
		case b == '\r' || b == '\n':
			fmt.Fprint(os.Stderr, "\r\n")
			return string(line), true
		case b == 3: // Ctrl-C
			fmt.Fprint(os.Stderr, "\r\n")
			return "", false
		case b == 27: // Escape
			fmt.Fprint(os.Stderr, "\r\n")
			return "", false
		case b == 127 || b == 8: // Backspace / DEL
			if len(line) > 0 {
				line = line[:len(line)-1]
			}
		case b >= 32: // Printable
			line = append(line, b)
		default:
			continue
		}

		// Clear previous output (rendered lines + prompt line)
		ClearLines(prevLines + 1)

		// Re-render filtered list and prompt
		prevLines = onChange(string(line))
		fmt.Fprint(os.Stderr, prompt+string(line))
	}
	return "", false
}

// Close restores the terminal to its original state and stops the reader.
func (k *KeyReader) Close() error {
	select {
	case <-k.done:
		return nil // already closed
	default:
	}
	close(k.done)
	Default.SetRawMode(false)
	return goterm.Restore(int(os.Stdin.Fd()), k.oldState)
}
