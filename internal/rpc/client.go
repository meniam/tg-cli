package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ErrClosed is returned by calls on a closed or broken connection.
var ErrClosed = errors.New("rpc: connection closed")

// Client is a JSON-RPC client. Calls may run in parallel.
type Client struct {
	conn      net.Conn
	w         *frameWriter
	writeSlot chan struct{}

	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan Response

	done chan struct{}
	err  error // set before done is closed
}

// Dial connects to a server and returns a ready client.
func Dial(ctx context.Context, network, address string) (*Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

// NewClient wraps an established connection.
func NewClient(conn net.Conn) *Client {
	c := &Client{
		conn:      conn,
		w:         &frameWriter{w: conn},
		writeSlot: make(chan struct{}, 1),
		pending:   map[uint64]chan Response{},
		done:      make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// IsNotRunning reports whether a Dial error means nobody listens on the socket.
func IsNotRunning(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}

func (c *Client) readLoop() {
	fr := newFrameReader(c.conn)
	var loopErr error
	for {
		line, err := fr.next()
		if err != nil {
			loopErr = err
			break
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		id, err := strconv.ParseUint(string(resp.ID), 10, 64)
		if err != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
	if loopErr == nil || errors.Is(loopErr, os.ErrClosed) {
		loopErr = ErrClosed
	}
	c.err = loopErr
	close(c.done)
}

// Call invokes a method and decodes the result into result (may be nil).
// A server-side error comes back as *Error.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan Response, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	req := Request{
		JSONRPC: Version,
		ID:      json.RawMessage(strconv.FormatUint(id, 10)),
		Method:  method,
		Params:  raw,
	}
	if err := c.write(ctx, req); err != nil {
		c.forget(id)
		return err
	}

	var resp Response
	select {
	case resp = <-ch:
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	case <-c.done:
		select {
		case resp = <-ch:
		default:
			c.forget(id)
			return c.err
		}
	}
	if resp.Error != nil {
		return resp.Error
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(resp.Result, result)
}

// Notify sends a request that expects no response.
func (c *Client) Notify(method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	return c.write(context.Background(), Request{JSONRPC: Version, Method: method, Params: raw})
}

// write bounds both waiting for the writer and a blocked socket write.
// Only one write owns the connection deadline at a time.
func (c *Client) write(ctx context.Context, req Request) error {
	select {
	case c.writeSlot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.writeSlot }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = c.conn.SetWriteDeadline(time.Now())
		close(finished)
	})
	err := c.w.write(req)
	if !stop() {
		<-finished
	}
	resetErr := c.conn.SetWriteDeadline(time.Time{})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		var netErr net.Error
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) && errors.As(err, &netErr) && netErr.Timeout() {
			return context.DeadlineExceeded
		}
		return err
	}
	return resetErr
}

// Close closes the connection. In-flight calls on other goroutines return an error.
func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) forget(id uint64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	return json.Marshal(params)
}
