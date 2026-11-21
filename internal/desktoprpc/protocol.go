// Package desktoprpc exposes Trestle's application layer over a local stdio connection.
package desktoprpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"trestle/internal/diag"
)

const version = 1
const maxRequest = 1 << 20
const maxRequests = 16

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (err *rpcError) Error() string { return err.Message }

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  Event  `json:"params"`
}

type Event struct {
	Version   int       `json:"version"`
	BuildID   string    `json:"build_id"`
	Sequence  uint64    `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

func decodeParams(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if len(raw) == 0 || raw[0] != '{' {
		return &rpcError{Code: -32602, Message: "params must be an object"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return &rpcError{Code: -32602, Message: err.Error()}
	}
	return nil
}

func wireError(err error) *rpcError {
	var rpc *rpcError
	if errors.As(err, &rpc) {
		return rpc
	}
	var diagnostic *diag.Error
	if errors.As(err, &diagnostic) {
		return &rpcError{Code: -32000, Message: diagnostic.Error(), Data: map[string]string{
			"type": diagnostic.Code, "stage": string(diagnostic.Stage), "log_path": diagnostic.LogPath,
		}}
	}
	return &rpcError{Code: -32000, Message: err.Error()}
}

type connection struct {
	ctx      context.Context
	cancel   context.CancelFunc
	replies  chan response
	events   chan notification
	ready    atomic.Bool
	requests sync.WaitGroup
	ids      sync.Map
	jobs     *session
}

func Serve(ctx context.Context, in io.ReadCloser, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	jobs, err := newSession(ctx)
	if err != nil {
		cancel()
		return err
	}
	c := &connection{ctx: ctx, cancel: cancel, replies: make(chan response, 32), events: make(chan notification, 128), jobs: jobs}
	stop := context.AfterFunc(ctx, func() { _ = in.Close() })
	defer stop()
	defer func() {
		cancel()
		c.requests.Wait()
		jobs.close()
	}()
	go c.write(out)

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), maxRequest)
	slots := make(chan struct{}, maxRequests)
	for scanner.Scan() {
		c.accept(scanner.Bytes(), slots)
	}
	if ctx.Err() != nil {
		return nil
	}
	return scanner.Err()
}

func (c *connection) accept(frame []byte, slots chan struct{}) {
	var req request
	if !json.Valid(frame) {
		c.respond(nil, nil, &rpcError{Code: -32700, Message: "invalid JSON"})
		return
	}
	if err := json.Unmarshal(frame, &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" || !validID(req.ID) {
		c.respond(nil, nil, &rpcError{Code: -32600, Message: "expected a JSON-RPC 2.0 request with a string or integer ID"})
		return
	}
	key := string(req.ID)
	if _, exists := c.ids.LoadOrStore(key, struct{}{}); exists {
		c.respond(nil, nil, &rpcError{Code: -32600, Message: "duplicate in-flight request ID"})
		return
	}
	select {
	case slots <- struct{}{}:
		c.requests.Go(func() {
			defer func() { <-slots }()
			defer c.ids.Delete(key)
			ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
			defer cancel()
			result, err := c.dispatch(ctx, req)
			c.respond(req.ID, result, err)
		})
	default:
		c.ids.Delete(key)
		c.respond(req.ID, nil, &rpcError{Code: -32003, Message: "too many pending requests"})
	}
}

func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return false
	}
	if id[0] == '"' {
		return true
	}
	var number int64
	return (id[0] == '-' || (id[0] >= '0' && id[0] <= '9')) && json.Unmarshal(id, &number) == nil
}

func (c *connection) respond(id json.RawMessage, result any, err error) {
	if id == nil {
		id = json.RawMessage("null")
	}
	r := response{JSONRPC: "2.0", ID: id, Result: result}
	if err != nil {
		r.Error = wireError(err)
	}
	select {
	case c.replies <- r:
	case <-c.ctx.Done():
	}
}

func (c *connection) publish(method string, event Event) {
	select {
	case c.events <- notification{JSONRPC: "2.0", Method: "event.build." + method, Params: event}:
	default:
		// Build output is independently retained. Status polling recovers any
		// skipped events without blocking Ninja on the desktop window.
	}
}

func (c *connection) write(out io.Writer) {
	encoder := json.NewEncoder(out)
	for c.ctx.Err() == nil {
		var frame any
		select {
		case frame = <-c.replies:
		default:
			select {
			case frame = <-c.replies:
			case frame = <-c.events:
			case <-c.ctx.Done():
				return
			}
		}
		if encoder.Encode(frame) != nil {
			c.cancel()
			return
		}
	}
}

func (c *connection) handshake(raw json.RawMessage) (any, error) {
	var params struct {
		Versions []int `json:"versions"`
	}
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	for _, candidate := range params.Versions {
		if candidate == version {
			c.ready.Store(true)
			return map[string]any{"version": version, "capabilities": methods}, nil
		}
	}
	return nil, &rpcError{Code: -32001, Message: fmt.Sprintf("incompatible protocol; Core supports version %d", version)}
}
