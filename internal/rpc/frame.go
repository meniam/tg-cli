package rpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// MaxFrameSize is the largest accepted message, matching the installed tg.
const MaxFrameSize = 1 << 20

// ErrFrameTooLarge is returned when a line exceeds MaxFrameSize.
var ErrFrameTooLarge = errors.New("rpc: frame exceeds 1 MiB")

type frameReader struct {
	r *bufio.Reader
}

func newFrameReader(r io.Reader) *frameReader {
	return &frameReader{r: bufio.NewReaderSize(r, 4*1024)}
}

// next returns the next non-empty line. The slice is valid until the next call.
func (f *frameReader) next() ([]byte, error) {
	for {
		var line []byte
		for {
			part, err := f.r.ReadSlice('\n')
			if len(line)+len(part) > MaxFrameSize+1 {
				return nil, ErrFrameTooLarge
			}
			line = append(line, part...)
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			if len(line) > 0 && line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			if len(line) > MaxFrameSize {
				return nil, ErrFrameTooLarge
			}
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			if len(line) > 0 {
				return line, nil
			}
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			break
		}
	}
}

type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (f *frameWriter) write(v any) error {
	return f.writeWithTimeout(v, 0)
}

// writeWithTimeout serializes both the frame and the connection write deadline.
// Only notifications use a timeout; ordinary response behavior is unchanged.
func (f *frameWriter) writeWithTimeout(v any, timeout time.Duration) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f.mu.Lock()
	defer f.mu.Unlock()
	if timeout > 0 {
		if conn, ok := f.w.(net.Conn); ok {
			if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
				return err
			}
			defer conn.SetWriteDeadline(time.Time{})
		}
	}
	_, err = f.w.Write(data)
	return err
}
