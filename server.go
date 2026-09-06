package gobridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

const MaxFrame = 1024 * 1024

type request struct {
	ID        string          `json:"id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	TimeoutMS int64           `json:"timeout_ms,omitempty"`
}
type response struct {
	ID     string `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// A successful void operation must include result:null. Omitting it would make
// the response indistinguishable from an invalid envelope to strict clients.
func (r response) MarshalJSON() ([]byte, error) {
	if r.Error != nil {
		return json.Marshal(struct {
			ID    string `json:"id"`
			Error *Error `json:"error"`
		}{r.ID, r.Error})
	}
	return json.Marshal(struct {
		ID     string `json:"id"`
		Result any    `json:"result"`
	}{r.ID, r.Result})
}

// Serve owns one session. EOF cancels all calls and returns immediately;
// cooperative handlers stop via their context. The hosting main must exit on
// return. stdout is exclusively protocol data; send logging to stderr.
func (r *Registry) Serve(parent context.Context, in io.Reader, out io.Writer, maxConcurrent int) error {
	if maxConcurrent < 1 {
		return fmt.Errorf("max concurrency must be positive")
	}
	ctx, cancelAll := context.WithCancel(parent)
	defer cancelAll()
	streams := &streamSession{ctx: ctx, registry: r, limit: maxConcurrent, cursors: map[string]*streamCursor{}}
	defer streams.closeAll()
	var mu, writeMu sync.Mutex
	active := map[string]context.CancelFunc{}
	write := func(resp response) {
		// MarshalJSON already produces validated JSON. Calling json.Marshal on
		// response would scan and copy that entire encoded payload a second time.
		data, err := resp.MarshalJSON()
		if err != nil || len(data) > MaxFrame {
			data, _ = (response{ID: resp.ID, Error: &Error{Code: "resource_exhausted", Message: "response cannot be encoded within frame limit"}}).MarshalJSON()
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if _, err := out.Write(append(data, '\n')); err != nil {
			cancelAll()
		}
	}
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), MaxFrame+1)
	for scan.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var req request
		if err := json.Unmarshal(scan.Bytes(), &req); err != nil {
			return fmt.Errorf("invalid protocol frame: %w", err)
		}
		if req.Method == "$cancel" {
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil {
				return err
			}
			mu.Lock()
			if cancel := active[p.ID]; cancel != nil {
				cancel()
			}
			mu.Unlock()
			continue
		}
		if req.ID == "" || len(req.ID) > 128 {
			return fmt.Errorf("request ID must contain 1..128 bytes")
		}
		mu.Lock()
		if _, ok := active[req.ID]; ok {
			mu.Unlock()
			return fmt.Errorf("duplicate active request ID")
		}
		if len(active) >= maxConcurrent {
			mu.Unlock()
			write(response{ID: req.ID, Error: &Error{Code: "busy", Message: "too many concurrent requests"}})
			continue
		}
		if req.TimeoutMS < 0 || req.TimeoutMS > 86400000 {
			mu.Unlock()
			write(response{ID: req.ID, Error: &Error{Code: "invalid_argument", Message: "timeout must be 0..86400000 ms"}})
			continue
		}
		var callCtx context.Context
		var callCancel context.CancelFunc
		if req.TimeoutMS > 0 {
			callCtx, callCancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
		} else {
			callCtx, callCancel = context.WithCancel(ctx)
		}
		callCtx = context.WithValue(callCtx, requestIDKey{}, req.ID)
		active[req.ID] = callCancel
		mu.Unlock()
		go func(req request, c context.Context, stop context.CancelFunc) {
			defer stop()
			var result any
			var err error
			if req.Method == "$batch" {
				var batch struct {
					Calls []BatchCall `json:"calls"`
				}
				if decodeErr := json.Unmarshal(req.Params, &batch); decodeErr != nil {
					err = Failure("invalid_argument", "invalid batch request")
				} else {
					result, err = r.Batch(c, batch.Calls)
				}
			} else if req.Method == "$stream_open" || req.Method == "$stream_next" || req.Method == "$stream_close" {
				result, err = streams.invoke(c, req.Method, req.Params)
			} else if req.Method == "$hello" {
				result, err = r.hello(req.Params)
			} else if req.Method == "$init" {
				err = r.Initialize(c, req.Params)
			} else {
				result, err = r.Call(c, req.Method, req.Params)
			}
			resp := response{ID: req.ID, Result: result}
			if err != nil {
				resp.Result = nil
				resp.Error = wireError(err)
			}
			// Release capacity before replying, so a caller can immediately issue
			// the next operation upon receiving a result.
			mu.Lock()
			delete(active, req.ID)
			mu.Unlock()
			write(resp)
		}(req, callCtx, callCancel)
	}
	return scan.Err()
}

// Compact hello is opt-in: older clients still receive the complete schema,
// and newer clients can accept full hello responses from older daemons.
func (r *Registry) hello(raw json.RawMessage) (any, error) {
	var options struct {
		Compact bool `json:"compact"`
	}
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &options); err != nil {
			return nil, Failure("invalid_argument", "invalid hello options")
		}
	}
	schema := r.Schema()
	if !options.Compact {
		return schema, nil
	}
	var constructor *struct{}
	if schema.Constructor != nil {
		constructor = &struct{}{}
	}
	return struct {
		Protocol    int       `json:"protocol"`
		Hash        string    `json:"schema_hash"`
		Constructor *struct{} `json:"constructor,omitempty"`
	}{schema.Protocol, schema.Hash, constructor}, nil
}
