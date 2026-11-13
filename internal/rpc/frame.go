package rpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// MaxFrameSize is the largest accepted message, matching the installed tg.
const MaxFrameSize = 1 << 20

// ErrFrameTooLarge is returned when a line exceeds MaxFrameSize.
var ErrFrameTooLarge = errors.New("rpc: frame exceeds 1 MiB")

type frameReader struct {
	sc *bufio.Scanner
}

func newFrameReader(r io.Reader) *frameReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), MaxFrameSize)
	return &frameReader{sc: sc}
}

// next returns the next non-empty line. The slice is valid until the next call.
func (f *frameReader) next() ([]byte, error) {
	for f.sc.Scan() {
		if line := f.sc.Bytes(); len(line) > 0 {
			return line, nil
		}
	}
	if err := f.sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, ErrFrameTooLarge
		}
		return nil, err
	}
	return nil, io.EOF
}

type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (f *frameWriter) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err = f.w.Write(data)
	return err
}
