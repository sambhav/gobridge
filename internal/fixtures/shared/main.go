// Shared exercises domain configuration over one module transport.
package main

import (
	"context"
	bridge "github.com/sambhav/gobridge"
	"os"
	"sync/atomic"
)

type Config struct {
	Account string   `json:"account" python:"account_name" ts:"accountName"`
	Labels  []string `json:"labels"`
}
type AuthClient struct {
	config Config
	calls  int
}
type Result struct {
	Account   string   `json:"account"`
	Labels    []string `json:"labels"`
	ProcessID int      `json:"process_id"`
	Calls     int      `json:"calls"`
}

var constructions atomic.Int64

// NewAuthClient makes cheap per-operation configuration.
//
//gobridge:constructor
//gobridge:shared
func NewAuthClient(ctx context.Context, config Config) (*AuthClient, error) {
	constructions.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.Account == "bad" {
		return nil, bridge.Failure("invalid_argument", "bad account")
	}
	if config.Account == "panic" {
		panic("constructor panic")
	}
	if config.Account == "nil" {
		return nil, nil
	}
	return &AuthClient{config: config}, nil
}

//gobridge:export
func (a *AuthClient) Identify() Result {
	a.calls++
	return Result{a.config.Account, a.config.Labels, os.Getpid(), a.calls}
}

//gobridge:export
func (a *AuthClient) Watch(ctx context.Context, count int, yield func(Result) error) error {
	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := yield(a.Identify()); err != nil {
			return err
		}
	}
	return nil
}

//gobridge:export
func ProcessID() int { return os.Getpid() }

//gobridge:export
func Constructions() int64 { return constructions.Load() }

func main() {
	r, err := NewGobridge()
	if err != nil {
		panic(err)
	}
	r.Main()
}
