package dap

import (
	"encoding/json"
	"fmt"
)

// In-process DAP transport (issue #393).
//
// The wire transports (stdio / tcp / unix) frame each message with a
// Content-Length header and marshal it to JSON. For a same-process client —
// the long-term TUI-via-DAP architecture (v2.0) — that overhead is pure waste:
// the client and server share an address space, so Request/Response/Event
// structs can pass directly.
//
// InprocClient.Request submits a Request straight to the server's dispatcher
// and reads back the Response struct the server's `sink` captures before it
// would have been marshalled. Requests with nil args and all responses make
// the round-trip with zero serialization; only typed args incur a single
// marshal (handlers still parse Request.Arguments as json.RawMessage).
//
// A single InprocClient is driven from one goroutine (request/response is
// synchronous); asynchronous events — e.g. the `stopped` a backgrounded
// `continue` emits — arrive on Events().

// InprocClient is a same-process DAP client bound directly to a Server.
type InprocClient struct {
	srv    *Server
	seq    int
	respCh chan Response
	events chan Event
}

// NewInprocServer returns a Server wired to an InprocClient over in-memory
// channels — no sockets, no JSON framing. The caller attaches a debuggee via
// the Server (AttachExisting) exactly as in the wire transports, then drives
// it through the client.
func NewInprocServer() (*Server, *InprocClient) {
	s := newServer()
	c := &InprocClient{
		srv:    s,
		respCh: make(chan Response, 1),
		events: make(chan Event, 256),
	}
	s.sink = func(v any) {
		switch m := v.(type) {
		case Response:
			// Buffered cap 1; a command sends exactly one response, drained
			// by Request before the next dispatch.
			select {
			case c.respCh <- m:
			default:
			}
		case Event:
			select {
			case c.events <- m:
			default:
			}
		}
	}
	return s, c
}

// Request dispatches a DAP command and returns the server's response. args may
// be nil (no serialization) or any JSON-marshalable struct.
func (c *InprocClient) Request(command string, args any) (Response, error) {
	c.seq++
	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: c.seq, Type: "request"},
		Command:         command,
	}
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			return Response{}, err
		}
		req.Arguments = raw
	}
	// Drain any stale response (defensive; the channel should be empty).
	select {
	case <-c.respCh:
	default:
	}
	c.srv.dispatch(req)
	select {
	case resp := <-c.respCh:
		return resp, nil
	default:
		return Response{}, fmt.Errorf("inproc: %q produced no response", command)
	}
}

// Initialize / Attach mirror the wire Client helpers so callers can share the
// same session shape across transports.
func (c *InprocClient) Initialize() (Response, error) {
	return c.Request("initialize", InitializeArguments{AdapterID: "chippy"})
}

func (c *InprocClient) Attach() (Response, error) {
	return c.Request("attach", nil)
}

func (c *InprocClient) Disconnect() (Response, error) {
	return c.Request("disconnect", nil)
}

// Events streams asynchronous events (stopped, output, terminated, …).
func (c *InprocClient) Events() <-chan Event { return c.events }
