package gobridge

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type metadataItem struct {
	Name string `json:"name" doc:"An item name." validate:"minlen=1,maxlen=4"`
}

type metadataRequest struct {
	Age      int64                   `json:"age" doc:"Age in years." validate:"min=0,max=120"`
	Ratio    float64                 `json:"ratio" validate:"min=-1.5,max=1.5"`
	Name     string                  `json:"name" doc:"A short name." validate:"minlen=1,maxlen=3"`
	Items    []metadataItem          `json:"items" validate:"minlen=1,maxlen=2"`
	Labels   map[string]metadataItem `json:"labels" validate:"minlen=1,maxlen=2"`
	Optional *int                    `json:"optional,omitempty" validate:"min=0,max=120"`
}

func validMetadataRequest() map[string]any {
	return map[string]any{"age": 120, "ratio": 1.5, "name": "é😊", "items": []any{map[string]any{"name": "one"}}, "labels": map[string]any{"primary": map[string]any{"name": "two"}}, "optional": nil}
}

func TestFieldConstraintsApplyToRegisterAndBind(t *testing.T) {
	for _, adapter := range []string{"register", "bind"} {
		t.Run(adapter, func(t *testing.T) {
			r := New()
			var calls int
			var err error
			if adapter == "register" {
				err = Register(r, "check", "", func(_ context.Context, request metadataRequest) (testOutput, error) {
					calls++
					return testOutput{request.Name}, nil
				})
			} else {
				err = Bind(r, "check", func(request metadataRequest) testOutput { calls++; return testOutput{request.Name} }, "request")
			}
			if err != nil {
				t.Fatal(err)
			}
			encode := func(request map[string]any) []byte {
				var value any = request
				if adapter == "bind" {
					value = map[string]any{"request": request}
				}
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return data
			}
			if _, err := r.Call(context.Background(), "check", encode(validMetadataRequest())); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("valid call did not run")
			}
			for _, tc := range []struct {
				name          string
				change        func(map[string]any)
				path, message string
			}{
				{"minimum", func(v map[string]any) { v["age"] = -1 }, "age", "must be at least 0"},
				{"maximum", func(v map[string]any) { v["age"] = 121 }, "age", "must be at most 120"},
				{"float_minimum", func(v map[string]any) { v["ratio"] = -1.5001 }, "ratio", "must be at least -1.5"},
				{"float_maximum", func(v map[string]any) { v["ratio"] = 1.5001 }, "ratio", "must be at most 1.5"},
				{"short_string", func(v map[string]any) { v["name"] = "" }, "name", "length must be at least 1"},
				{"long_unicode", func(v map[string]any) { v["name"] = "é😊x" }, "name", "length must be at most 3"},
				{"short_slice", func(v map[string]any) { v["items"] = []any{} }, "items", "length must be at least 1"},
				{"long_slice", func(v map[string]any) { v["items"] = []any{metadataItem{"a"}, metadataItem{"b"}, metadataItem{"c"}} }, "items", "length must be at most 2"},
				{"nested_slice", func(v map[string]any) { v["items"] = []any{metadataItem{""}} }, "items[0].name", "length must be at least 1"},
				{"short_map", func(v map[string]any) { v["labels"] = map[string]any{} }, "labels", "length must be at least 1"},
				{"long_map", func(v map[string]any) {
					v["labels"] = map[string]any{"a": metadataItem{"a"}, "b": metadataItem{"b"}, "c": metadataItem{"c"}}
				}, "labels", "length must be at most 2"},
				{"nested_map", func(v map[string]any) { v["labels"] = map[string]any{"primary": metadataItem{""}} }, `labels["primary"].name`, "length must be at least 1"},
				{"optional_bound", func(v map[string]any) { v["optional"] = -1 }, "optional", "must be at least 0"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					request := validMetadataRequest()
					tc.change(request)
					_, err := r.Call(context.Background(), "check", encode(request))
					if err == nil || wireError(err).Code != "invalid_argument" {
						t.Fatalf("accepted invalid request: %v", err)
					}
					path := tc.path
					if adapter == "bind" {
						path = "request." + path
					}
					if !strings.Contains(err.Error(), path+": "+tc.message) {
						t.Fatalf("missing useful field path/bound: %v", err)
					}
				})
			}
			if calls != 1 {
				t.Fatal("invalid requests invoked handler")
			}
			for _, mode := range []string{"nullable", "lower_bound", "three_codepoints"} {
				request := validMetadataRequest()
				switch mode {
				case "nullable":
					request["items"], request["labels"] = nil, nil
					delete(request, "optional")
				case "lower_bound":
					request["age"], request["ratio"], request["optional"] = 0, -1.5, 0
				case "three_codepoints":
					request["name"] = "é😊"
				}
				if _, err := r.Call(context.Background(), "check", encode(request)); err != nil {
					t.Fatalf("%s rejected: %v", mode, err)
				}
			}
		})
	}
}

func TestFieldConstraintsPreserveExactInt64Bounds(t *testing.T) {
	type precise struct {
		Value int64 `json:"value" validate:"min=9007199254740993,max=9007199254740995"`
	}
	r := New()
	if err := Register(r, "check", "", func(_ context.Context, value precise) (precise, error) { return value, nil }); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"9007199254740993", "9007199254740994", "9007199254740995"} {
		if _, err := r.Call(context.Background(), "check", []byte(`{"value":`+value+`}`)); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"9007199254740992", "9007199254740996"} {
		if _, err := r.Call(context.Background(), "check", []byte(`{"value":`+value+`}`)); err == nil {
			t.Fatalf("accepted out-of-range precise integer %s", value)
		}
	}
	field := r.Schema().Operations[0].Input.Fields[0]
	if field.Constraints.Minimum.String() != "9007199254740993" || field.Constraints.Maximum.String() != "9007199254740995" {
		t.Fatal("schema rounded int64 bounds")
	}
	data, err := json.Marshal(field)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"minimum":9007199254740993`) {
		t.Fatalf("bound is not an exact JSON number: %s", data)
	}
	type extremes struct {
		Low  int64 `json:"low" validate:"min=-9223372036854775808,max=-9223372036854775807"`
		High int64 `json:"high" validate:"min=9223372036854775806,max=9223372036854775807"`
	}
	r = New()
	if err := Register(r, "check", "", func(_ context.Context, v extremes) (extremes, error) { return v, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Call(context.Background(), "check", []byte(`{"low":-9223372036854775808,"high":9223372036854775807}`)); err != nil {
		t.Fatal(err)
	}
}

func TestFieldConstraintDeclarationsFailDuringRegistration(t *testing.T) {
	type unknown struct {
		Value int `json:"value" validate:"unknown=1"`
	}
	type missing struct {
		Value int `json:"value" validate:"min"`
	}
	type empty struct {
		Value int `json:"value" validate:"min="`
	}
	type trailing struct {
		Value int `json:"value" validate:"min=0,"`
	}
	type duplicate struct {
		Value int `json:"value" validate:"min=0,min=0"`
	}
	type noninteger struct {
		Value int `json:"value" validate:"min=1.5"`
	}
	type tooLarge struct {
		Value int8 `json:"value" validate:"max=128"`
	}
	type reversed struct {
		Value int `json:"value" validate:"min=2,max=1"`
	}
	type negativeLength struct {
		Value string `json:"value" validate:"minlen=-1"`
	}
	type longLength struct {
		Value string `json:"value" validate:"maxlen=2147483648"`
	}
	type floatLength struct {
		Value string `json:"value" validate:"minlen=1.5"`
	}
	type reversedLength struct {
		Value string `json:"value" validate:"minlen=2,maxlen=1"`
	}
	type wrongNumeric struct {
		Value string `json:"value" validate:"min=1"`
	}
	type wrongLength struct {
		Value int `json:"value" validate:"minlen=1"`
	}
	type boolean struct {
		Value bool `json:"value" validate:"max=1"`
	}
	type nan struct {
		Value float64 `json:"value" validate:"min=NaN"`
	}
	type infinity struct {
		Value float64 `json:"value" validate:"max=Inf"`
	}
	type floatRange struct {
		Value float32 `json:"value" validate:"max=1e100"`
	}
	type hexFloat struct {
		Value float64 `json:"value" validate:"max=0x1p2"`
	}
	type underscoreFloat struct {
		Value float64 `json:"value" validate:"max=1_000"`
	}
	for _, value := range []any{unknown{}, missing{}, empty{}, trailing{}, duplicate{}, noninteger{}, tooLarge{}, reversed{}, negativeLength{}, longLength{}, floatLength{}, reversedLength{}, wrongNumeric{}, wrongLength{}, boolean{}, nan{}, infinity{}, floatRange{}, hexFloat{}, underscoreFloat{}} {
		typ := reflect.TypeOf(value)
		t.Run(typ.Name(), func(t *testing.T) {
			function := reflect.MakeFunc(reflect.FuncOf([]reflect.Type{typ}, nil, false), func([]reflect.Value) []reflect.Value { return nil }).Interface()
			r := New()
			if err := Bind(r, "check", function, "request"); err == nil {
				t.Fatal("invalid tag accepted by Bind")
			}
			if len(r.Schema().Operations) != 0 {
				t.Fatal("invalid registration was published")
			}
		})
	}
	if err := Register(New(), "check", "", func(context.Context, unknown) (testOutput, error) { return testOutput{}, nil }); err == nil {
		t.Fatal("invalid tag accepted by Register")
	}
	if _, err := NewObject(New(), func(unknown) *boundCounter { return &boundCounter{} }); err == nil {
		t.Fatal("invalid tag accepted by constructor")
	}
}

func TestFieldMetadataSchemaAndIndependentSnapshots(t *testing.T) {
	r := New()
	if err := Register(r, "check", "", func(_ context.Context, request metadataRequest) (testOutput, error) { return testOutput{}, nil }); err != nil {
		t.Fatal(err)
	}
	schema := r.Schema()
	age := schema.Operations[0].Input.Fields[0]
	if age.Description != "Age in years." || age.Constraints == nil || age.Constraints.Minimum != "0" || age.Constraints.Maximum != "120" {
		t.Fatalf("missing metadata: %#v", age)
	}
	name := schema.Operations[0].Input.Fields[2]
	if name.Constraints == nil || *name.Constraints.MinLength != 1 || *name.Constraints.MaxLength != 3 {
		t.Fatalf("missing length metadata: %#v", name)
	}
	*name.Constraints.MinLength = 100
	age.Constraints.Minimum = "999"
	if r.Schema().Hash != schema.Hash {
		t.Fatal("schema snapshot mutated the registered declaration")
	}
	plain, err := json.Marshal(testRegistry(t).Schema().Operations[0].Input.Fields[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != `{"name":"text","type":{"kind":"string"}}` {
		t.Fatalf("untagged schema shape changed: %s", plain)
	}
}

func TestConstructorFieldConstraintsBeforeSideEffects(t *testing.T) {
	type options struct {
		Initial int64 `json:"initial" doc:"Starting count." validate:"min=0,max=10"`
	}
	for _, initial := range []string{"-1", "11"} {
		r := New()
		var calls int
		if _, err := NewObject(r, func(opts options) *boundCounter { calls++; return &boundCounter{} }); err != nil {
			t.Fatal(err)
		}
		err := r.Initialize(context.Background(), []byte(`{"initial":`+initial+`}`))
		if err == nil || wireError(err).Code != "invalid_argument" || calls != 0 {
			t.Fatalf("invalid constructor config invoked user code: %v, calls=%d", err, calls)
		}
		field := r.Schema().Constructor.Fields[0]
		if field.Description != "Starting count." || field.Constraints == nil {
			t.Fatal("constructor schema lost metadata")
		}
	}
}

func TestConstraintWhitespaceAndPointerContainers(t *testing.T) {
	type request struct {
		Count int       `json:"count" validate:" min = +0 , max = 01 "`
		Ratio float32   `json:"ratio" validate:"min=.1,max=2e-1"`
		Names *[]string `json:"names,omitempty" validate:"minlen=1,maxlen=1"`
	}
	r := New()
	if err := Register(r, "check", "", func(_ context.Context, v request) (request, error) { return v, nil }); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"count":0,"ratio":0.1}`, `{"count":1,"ratio":0.2,"names":null}`, `{"count":1,"ratio":0.2,"names":["é😊"]}`} {
		if _, err := r.Call(context.Background(), "check", []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Call(context.Background(), "check", []byte(`{"count":0,"ratio":0.1,"names":[]}`)); err == nil {
		t.Fatal("pointer container ignored minlen")
	}
}

type observedErrContext struct {
	context.Context
	first chan struct{}
	once  sync.Once
}

func (c *observedErrContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.first) })
	return err
}

func TestCanceledCallWaitingForInitializationDoesNotInvoke(t *testing.T) {
	r := New()
	started, release := make(chan struct{}), make(chan struct{})
	_, err := NewObject(r, func(ctx context.Context, _ counterOptions) (*boundCounter, error) {
		close(started)
		select {
		case <-release:
			return &boundCounter{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	if err := Bind(r, "effect", func() { calls.Add(1) }); err != nil {
		t.Fatal(err)
	}
	initCtx, stopInit := context.WithCancel(context.Background())
	defer stopInit()
	initialized := make(chan error, 1)
	go func() { initialized <- r.Initialize(initCtx, []byte(`{"initial":0}`)) }()
	<-started
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &observedErrContext{Context: base, first: make(chan struct{})}
	finished := make(chan error, 1)
	go func() { _, err := r.Call(ctx, "effect", nil); finished <- err }()
	// Synchronize on the request's first Err call rather than scheduler timing.
	// A context wrapper makes cancellation happen only after that initial check.
	<-ctx.first
	cancel()
	close(release)
	if err := <-initialized; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting call did not finish")
	}
	if calls.Load() != 0 {
		t.Fatal("cancelled call invoked context-free handler")
	}
}
