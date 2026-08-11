package runner

import (
	"bytes"
	"io"
	"sync"
)

const (
	ansiReset = "\x1b[0m"
	ansiPass  = "\x1b[1;32m"
	ansiFail  = "\x1b[1;31m"
)

// statusColorWriter decorates complete PASS/FAIL lines while preserving partial
// writes. Feature output often arrives in arbitrary chunks when it is captured
// by the TUI, so matching each Write call independently is not reliable.
type statusColorWriter struct {
	mu      sync.Mutex
	target  io.Writer
	enabled bool
	pending []byte
}

func newStatusColorWriter(target io.Writer, enabled bool) *statusColorWriter {
	return &statusColorWriter{target: target, enabled: enabled}
}

func (w *statusColorWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.pending = append(w.pending, data...)
	for {
		newline := bytes.IndexByte(w.pending, '\n')
		if newline < 0 {
			return len(data), nil
		}

		line := append([]byte(nil), w.pending[:newline+1]...)
		w.pending = w.pending[newline+1:]
		if err := w.writeLine(line); err != nil {
			return len(data), err
		}
	}
}

func (w *statusColorWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.pending) == 0 {
		return nil
	}
	line := append([]byte(nil), w.pending...)
	w.pending = nil
	return w.writeLine(line)
}

func (w *statusColorWriter) writeLine(line []byte) error {
	color := ""
	if w.enabled {
		switch {
		case bytes.Contains(line, []byte("[FAIL]")):
			color = ansiFail
		case bytes.Contains(line, []byte("[PASS]")):
			color = ansiPass
		}
	}

	if color == "" {
		_, err := w.target.Write(line)
		return err
	}

	endingAt := len(line)
	if endingAt > 0 && line[endingAt-1] == '\n' {
		endingAt--
		if endingAt > 0 && line[endingAt-1] == '\r' {
			endingAt--
		}
	}
	decorated := make([]byte, 0, len(color)+len(line)+len(ansiReset))
	decorated = append(decorated, color...)
	decorated = append(decorated, line[:endingAt]...)
	decorated = append(decorated, ansiReset...)
	decorated = append(decorated, line[endingAt:]...)
	_, err := w.target.Write(decorated)
	return err
}
