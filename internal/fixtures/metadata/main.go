package main

import (
	"context"

	bridge "github.com/sambhav/gobridge"
)

type Config struct {
	Capacity int `json:"capacity" doc:"Maximum stored items." validate:"min=1,max=5"`
}
type Request struct {
	Name     string            `json:"name" doc:"One to four Unicode code points." validate:"minlen=1,maxlen=4"`
	Age      *int              `json:"age,omitempty" doc:"Optional age." validate:"min=0,max=120"`
	Big      int64             `json:"big" validate:"min=9007199254740993,max=9007199254740995"`
	Tags     []string          `json:"tags" validate:"maxlen=2"`
	Labels   map[string]string `json:"labels" validate:"maxlen=1"`
	Fraction float64           `json:"fraction" validate:"min=0.5,max=1.5"`
}
type record struct {
	Name string `json:"name"`
}
type Holder struct {
	Record *record `json:"record,omitempty"`
}

type Store struct{}

func NewStore(config Config) *Store           { return &Store{} }
func (s *Store) Echo(request Request) Request { return request }
func main() {
	r := bridge.New()
	object, err := bridge.NewObject(r, NewStore)
	if err != nil {
		panic(err)
	}
	if err := object.Bind("echo", (*Store).Echo, "request"); err != nil {
		panic(err)
	}
	if err := bridge.Register(r, "flattened", "Echo flattened request fields.", func(ctx context.Context, request Request) (Request, error) { return request, nil }); err != nil {
		panic(err)
	}
	if err := bridge.Bind(r, "lowercase", func(record Holder) Holder { return record }, "record"); err != nil {
		panic(err)
	}
	r.Main()
}
