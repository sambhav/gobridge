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

type Request struct {
	ID        string          `json:"id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	TimeoutMS int64           `json:"timeout_ms,omitempty"`
}
type Response struct {
	ID     string `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// A successful void operation must include result:null. Omitting it would make
// the response indistinguishable from an invalid envelope to strict clients.
func (r Response) MarshalJSON() ([]byte, error) {
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
	var mu, writeMu sync.Mutex
	active := map[string]context.CancelFunc{}
	write := func(resp Response) {
		data, err := json.Marshal(resp)
		if err != nil || len(data) > MaxFrame {
			data, _ = json.Marshal(Response{ID: resp.ID, Error: &Error{"resource_exhausted", "response cannot be encoded within frame limit"}})
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
		var req Request
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
			write(Response{ID: req.ID, Error: &Error{"busy", "too many concurrent requests"}})
			continue
		}
		if req.TimeoutMS < 0 || req.TimeoutMS > 86400000 {
			mu.Unlock()
			write(Response{ID: req.ID, Error: &Error{"invalid_argument", "timeout must be 0..86400000 ms"}})
			continue
		}
		callCtx, callCancel := context.WithCancel(ctx)
		if req.TimeoutMS > 0 {
			callCancel()
			callCtx, callCancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
		}
		active[req.ID] = callCancel
		mu.Unlock()
		go func(req Request, c context.Context, stop context.CancelFunc) {
			defer stop()
			var result any
			var err error
			if req.Method == "$hello" {
				result = r.Schema()
			} else if req.Method == "$init" {
				err = r.Initialize(c, req.Params)
			} else {
				result, err = r.Call(c, req.Method, req.Params)
			}
			resp := Response{ID: req.ID, Result: result}
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
