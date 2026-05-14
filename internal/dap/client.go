package dap

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client is the editor / front-end side of a DAP wire. It speaks the
// same Content-Length-framed JSON protocol as Server but in the opposite
// direction: it issues requests and consumes events.
//
// Lifecycle:
//
//	c, err := dap.Dial(ctx, "tcp:host:port")    // open transport
//	caps, err := c.Initialize(args)             // server capabilities
//	_, err = c.Attach()                         // resume / inspect
//	for ev := range c.Events() { ... }          // listen for stopped / output / terminated
//	c.Disconnect()                              // graceful shutdown
//	c.Close()                                   // tear down transport
//
// Concurrency: one internal read goroutine demuxes wire bytes into
// per-request response channels and a single events fanout channel.
// Callers may invoke Request from multiple goroutines; each request
// gets its own response channel keyed on the auto-assigned seq number.
type Client struct {
	conn    io.Closer
	in      *bufio.Reader
	out     io.Writer
	writeMu sync.Mutex

	seq atomic.Int64

	pendMu  sync.Mutex
	pending map[int]chan Response

	events    chan Event
	closeOnce sync.Once
	closed    atomic.Bool

	// readErr captures the first wire error so callers see an actionable
	// message instead of a generic timeout.
	readErrMu sync.Mutex
	readErr   error
}

// Dial opens a DAP connection. address forms:
//
//	tcp:HOST:PORT
//	HOST:PORT       (tcp is the default)
//
// Stdio attach (piping into a child DAP process) lives behind NewClient
// directly — Dial is for network transports only.
func Dial(ctx context.Context, address string) (*Client, error) {
	host, port, err := parseDialAddr(address)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("dap: dial %s:%s: %w", host, port, err)
	}
	c := NewClient(conn, conn)
	c.conn = conn
	return c, nil
}

// NewClient wraps an existing transport. Used by tests (net.Pipe) and
// for stdio attach (NewClient(os.Stdin, os.Stdout)).
func NewClient(r io.Reader, w io.Writer) *Client {
	c := &Client{
		in:      bufio.NewReader(r),
		out:     w,
		pending: map[int]chan Response{},
		events:  make(chan Event, 64),
	}
	go c.readLoop()
	return c
}

// parseDialAddr accepts the tcp:HOST:PORT or HOST:PORT forms.
func parseDialAddr(addr string) (host, port string, err error) {
	if rest, ok := strings.CutPrefix(addr, "tcp:"); ok {
		addr = rest
	}
	h, p, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		return "", "", fmt.Errorf("dap: parse address %q: %w (want tcp:HOST:PORT)", addr, splitErr)
	}
	return h, p, nil
}

// Initialize sends the `initialize` request and returns the server's
// advertised capabilities. Args may be a zero value; ClientID and
// AdapterID default to "chippy" if unset.
func (c *Client) Initialize(args InitializeArguments) (Capabilities, error) {
	if args.ClientID == "" {
		args.ClientID = "chippy"
	}
	if args.AdapterID == "" {
		args.AdapterID = "chippy"
	}
	resp, err := c.Request("initialize", args)
	if err != nil {
		return Capabilities{}, err
	}
	if !resp.Success {
		return Capabilities{}, fmt.Errorf("dap: initialize failed: %s", resp.Message)
	}
	// resp.Body is a generic interface{} (json.Unmarshal landed it as
	// map[string]any). Round-trip through JSON to project onto
	// Capabilities — cheap, and avoids hand-rolled field copies that
	// would lag as the struct grows.
	raw, err := json.Marshal(resp.Body)
	if err != nil {
		return Capabilities{}, fmt.Errorf("dap: re-marshal capabilities: %w", err)
	}
	var caps Capabilities
	if err := json.Unmarshal(raw, &caps); err != nil {
		return Capabilities{}, fmt.Errorf("dap: unmarshal capabilities: %w", err)
	}
	return caps, nil
}

// Attach sends an `attach` request. chippy's server ignores arguments
// on attach (it shares the host TUI's CPU/RAM directly), so callers
// typically pass nil.
func (c *Client) Attach() (Response, error) {
	return c.Request("attach", map[string]any{})
}

// Disconnect sends a `disconnect` request. The server replies, then
// closes its side of the wire — the read loop sees EOF and exits.
// Callers should still Close() to free the transport.
func (c *Client) Disconnect() error {
	if c.closed.Load() {
		return nil
	}
	_, err := c.Request("disconnect", map[string]any{})
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Request issues an arbitrary DAP request and blocks until the matching
// response arrives. Concurrent callers are safe; each request is keyed
// on an auto-assigned seq number.
func (c *Client) Request(command string, args any) (Response, error) {
	if c.closed.Load() {
		return Response{}, errors.New("dap: client closed")
	}
	seq := int(c.seq.Add(1))
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return Response{}, fmt.Errorf("dap: marshal %s args: %w", command, err)
	}
	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: seq, Type: "request"},
		Command:         command,
		Arguments:       argsJSON,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("dap: marshal %s request: %w", command, err)
	}

	ch := make(chan Response, 1)
	c.pendMu.Lock()
	c.pending[seq] = ch
	c.pendMu.Unlock()

	c.writeMu.Lock()
	werr := WriteMessage(c.out, body)
	c.writeMu.Unlock()
	if werr != nil {
		c.pendMu.Lock()
		delete(c.pending, seq)
		c.pendMu.Unlock()
		return Response{}, fmt.Errorf("dap: send %s: %w", command, werr)
	}

	// Default wait is 5s — long enough that a real CPU-pausing request
	// has time, short enough that a wedged server doesn't hang the TUI
	// forever. Caller can wrap with their own ctx if they need more.
	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(5 * time.Second):
		c.pendMu.Lock()
		delete(c.pending, seq)
		c.pendMu.Unlock()
		if rerr := c.lastReadErr(); rerr != nil {
			return Response{}, fmt.Errorf("dap: wait %s response: %w", command, rerr)
		}
		return Response{}, fmt.Errorf("dap: timeout waiting for %s response", command)
	}
}

// Events returns the channel of asynchronous server events
// (`stopped`, `continued`, `output`, `terminated`, etc.). The channel
// is closed when the read loop exits.
func (c *Client) Events() <-chan Event { return c.events }

// Close tears down the transport. Idempotent.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
	return nil
}

func (c *Client) readLoop() {
	defer close(c.events)
	for {
		body, err := ReadMessage(c.in)
		if err != nil {
			c.readErrMu.Lock()
			c.readErr = err
			c.readErrMu.Unlock()
			// Wake all pending requesters so they don't hang on the
			// 5-second timeout.
			c.pendMu.Lock()
			for seq, ch := range c.pending {
				close(ch)
				delete(c.pending, seq)
			}
			c.pendMu.Unlock()
			return
		}
		var pm ProtocolMessage
		if err := json.Unmarshal(body, &pm); err != nil {
			continue
		}
		switch pm.Type {
		case "response":
			var resp Response
			if err := json.Unmarshal(body, &resp); err != nil {
				continue
			}
			c.pendMu.Lock()
			ch, ok := c.pending[resp.RequestSeq]
			if ok {
				delete(c.pending, resp.RequestSeq)
			}
			c.pendMu.Unlock()
			if ok {
				ch <- resp
			}
		case "event":
			var ev Event
			if err := json.Unmarshal(body, &ev); err != nil {
				continue
			}
			// Non-blocking: a hung consumer must not jam the read loop.
			// Drop events when the buffer is full — callers that need
			// every event should drain promptly.
			select {
			case c.events <- ev:
			default:
			}
		}
	}
}

func (c *Client) lastReadErr() error {
	c.readErrMu.Lock()
	defer c.readErrMu.Unlock()
	return c.readErr
}
