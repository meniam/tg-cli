package rpc

import (
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFrameBoundariesAndBuffer(t *testing.T) {
	for _, n := range []int{MaxFrameSize, MaxFrameSize + 1} {
		r := newFrameReader(strings.NewReader(strings.Repeat("a", n) + "\n"))
		line, err := r.next()
		if n == MaxFrameSize && (err != nil || len(line) != n) {
			t.Fatalf("size %d: len=%d err=%v", n, len(line), err)
		}
		if n > MaxFrameSize && !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("size %d: %v", n, err)
		}
		if r.r.Size() != 4096 {
			t.Fatalf("retained reader buffer: %d", r.r.Size())
		}
	}
	r := newFrameReader(strings.NewReader("\r\nhello\r\nlast"))
	for _, want := range []string{"hello", "last"} {
		line, err := r.next()
		if err != nil || string(line) != want {
			t.Fatalf("got %q %v, want %q", line, err, want)
		}
	}
	if _, err := r.next(); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestFrameMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("heap measurement")
	}
	const n = 200
	s := NewServer(nil)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	conns := make([]net.Conn, 0, n)
	for range n {
		a, b := net.Pipe()
		conns = append(conns, a)
		go s.ServeConn(b)
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
		_ = s.Close()
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		count := len(s.conns)
		s.mu.Unlock()
		if count == n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connections not started")
		}
		runtime.Gosched()
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc && (after.HeapAlloc-before.HeapAlloc)/n > 32*1024 {
		t.Fatalf("heap per connection: %d", (after.HeapAlloc-before.HeapAlloc)/n)
	}
}
