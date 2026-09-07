package gobridge

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"
)

// RegisterStream registers a typed, pull-driven producer. yield blocks until
// the consumer asks for an item; producers must honor ctx and yield errors.
func RegisterStream[I, O any](r *Registry, name, description string, fn func(context.Context, I, func(O) error) error) error {
	if err := r.checkName(name); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("stream function must not be nil")
	}
	in, out := reflect.TypeOf((*I)(nil)).Elem(), reflect.TypeOf((*O)(nil)).Elem()
	if in.Kind() != reflect.Struct || in.Name() == "" {
		return fmt.Errorf("stream input must be a named struct")
	}
	for _, t := range []reflect.Type{in, out} {
		if err := validateType(t, map[reflect.Type]bool{}); err != nil {
			return err
		}
	}
	return r.add(operation{name: name, description: description, in: in, out: out, stream: func(ctx context.Context, raw json.RawMessage, yield func(any) error) error {
		var input I
		if len(raw) == 0 {
			raw = []byte("{}")
		}
		if err := validateValue(raw, in); err != nil {
			return Failure("invalid_argument", err.Error())
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return Failure("invalid_argument", err.Error())
		}
		return fn(ctx, input, func(value O) error { return yield(value) })
	}})
}

type streamItem struct {
	value any
	err   error
	done  bool
}
type streamCursor struct {
	done   <-chan struct{}
	items  chan streamItem
	demand chan struct{}
	cancel context.CancelFunc
	timer  *time.Timer
	next   sync.Mutex
}
type streamSession struct {
	mu       sync.Mutex
	next     uint64
	cursors  map[string]*streamCursor
	ctx      context.Context
	registry *Registry
	limit    int
}

func (s *streamSession) close(id string) {
	s.mu.Lock()
	c := s.cursors[id]
	delete(s.cursors, id)
	s.mu.Unlock()
	if c != nil {
		c.cancel()
		c.timer.Stop()
	}
}
func (s *streamSession) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.cursors {
		c.cancel()
		c.timer.Stop()
		delete(s.cursors, id)
	}
}
func (s *streamSession) invoke(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var params struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Cursor string          `json:"cursor"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, Failure("invalid_argument", "invalid stream request")
	}
	if method == "$stream_open" {
		op, ok := s.registry.ops[params.Method]
		if !ok || op.stream == nil {
			return nil, Failure("not_found", "unknown streaming operation")
		}
		if s.registry.NeedsInit() {
			return nil, Failure("failed_precondition", "initialize the service before streaming")
		}
		s.mu.Lock()
		if len(s.cursors) >= s.limit {
			s.mu.Unlock()
			return nil, Failure("busy", "too many open streams")
		}
		s.next++
		id := strconv.FormatUint(s.next, 10)
		work, cancel := context.WithCancel(context.WithValue(s.ctx, requestIDKey{}, RequestID(ctx)))
		c := &streamCursor{done: work.Done(), items: make(chan streamItem), demand: make(chan struct{}), cancel: cancel}
		c.timer = time.AfterFunc(30*time.Second, func() { s.close(id) })
		s.cursors[id] = c
		s.mu.Unlock()
		go func() {
			started := time.Now()
			var err error
			defer func() { s.registry.observe(work, params.Method, started, err) }()
			func() {
				defer func() {
					if recover() != nil {
						err = Failure("internal", "stream producer panicked")
					}
				}()
				err = op.stream(work, params.Params, func(value any) error {
					select {
					case <-work.Done():
						return work.Err()
					case <-c.demand:
					}
					select {
					case <-work.Done():
						return work.Err()
					case c.items <- streamItem{value: value}:
						return nil
					}
				})
			}()
			select {
			case c.items <- streamItem{done: true, err: err}:
			case <-work.Done():
			}
		}()
		return map[string]string{"cursor": id}, nil
	}
	s.mu.Lock()
	c := s.cursors[params.Cursor]
	s.mu.Unlock()
	if method == "$stream_close" {
		s.close(params.Cursor)
		return nil, nil
	}
	if c == nil {
		return nil, Failure("not_found", "stream closed or idle timeout exceeded")
	}
	if !c.next.TryLock() {
		return nil, Failure("busy", "a stream read is already pending")
	}
	defer c.next.Unlock()
	s.mu.Lock()
	if s.cursors[params.Cursor] == c {
		c.timer.Reset(30 * time.Second)
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.cursors[params.Cursor] == c {
			c.timer.Reset(30 * time.Second)
		}
	}()
	var item streamItem
	select {
	case <-c.done:
		return nil, Failure("cancelled", "stream closed")
	case <-ctx.Done():
		s.close(params.Cursor)
		return nil, ctx.Err()
	case item = <-c.items:
	case c.demand <- struct{}{}:
		select {
		case <-c.done:
			return nil, Failure("cancelled", "stream closed")
		case <-ctx.Done():
			s.close(params.Cursor)
			return nil, ctx.Err()
		case item = <-c.items:
		}
	}
	if item.done {
		s.close(params.Cursor)
	}
	if item.err != nil {
		return nil, item.err
	}
	return struct {
		Done bool `json:"done"`
		Item any  `json:"item"`
	}{item.done, item.value}, nil
}

// BatchCall describes a unary call using its stable wire name and JSON inputs.
type BatchCall struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// BatchResult preserves input order; each entry contains either a result or error.
type BatchResult struct {
	Result any    `json:"result"`
	Error  *Error `json:"error,omitempty"`
}

// Batch executes up to 128 unary calls sequentially. It is not a transaction:
// failures do not roll back earlier effects. Cancellation stops remaining work.
func (r *Registry) Batch(ctx context.Context, calls []BatchCall) ([]BatchResult, error) {
	if len(calls) > 128 {
		return nil, Failure("resource_exhausted", "batch limit is 128 calls")
	}
	results := make([]BatchResult, len(calls))
	remaining := MaxFrame - 32768
	for i, call := range calls {
		if remaining <= 0 {
			results[i].Error = wireError(Failure("resource_exhausted", "batch response limit reached; call not executed"))
			continue
		}
		value, err := r.Call(ctx, call.Method, call.Params)
		encoded, encodeErr := json.Marshal(value)
		if err == nil && (encodeErr != nil || len(encoded) > remaining) {
			err = Failure("resource_exhausted", "batch result exceeds response budget")
			remaining = 0
		}
		if err == nil {
			remaining -= len(encoded)
			results[i].Result = json.RawMessage(encoded)
		}
		if err != nil {
			results[i].Result = nil
			results[i].Error = wireError(err)
		}
	}
	return results, nil
}
