// perf supplies identical work to native Go and generated bridge clients.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	bridge "github.com/sambhav/gobridge"
)

type Node struct {
	Name   string  `json:"name"`
	Values []int64 `json:"values"`
}

type Input struct {
	Data   []byte `json:"data"`
	Rounds int    `json:"rounds"`
	Nodes  []Node `json:"nodes"`
}

type Result struct {
	Data   []byte `json:"data"`
	Nodes  []Node `json:"nodes"`
	Digest string `json:"digest"`
}

func work(ctx context.Context, in Input) (Result, error) {
	data := in.Data
	var digest [32]byte
	for i := 0; i < in.Rounds; i++ {
		if i%256 == 0 && ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		digest = sha256.Sum256(data)
		data = digest[:]
	}
	return Result{in.Data, in.Nodes, hex.EncodeToString(digest[:])}, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "native" {
		native()
		return
	}
	r := bridge.New()
	if err := bridge.Register(r, "work", "Echo data and optionally perform sequential SHA-256 rounds.", work); err != nil {
		panic(err)
	}
	r.Main()
}

func native() {
	flags := flag.NewFlagSet("native", flag.ExitOnError)
	calls := flags.Int("calls", 1000, "calls")
	concurrency := flags.Int("concurrency", 1, "workers")
	size := flags.Int("size", 0, "bytes")
	rounds := flags.Int("rounds", 0, "hash rounds")
	nested := flags.Bool("nested", false, "nested model")
	_ = flags.Parse(os.Args[2:])
	if *calls < 1 || *concurrency < 1 || *size < 0 || *rounds < 0 {
		panic("invalid benchmark parameters")
	}
	in := Input{Data: make([]byte, *size), Rounds: *rounds, Nodes: []Node{}}
	if *nested {
		for i := 0; i < 16; i++ {
			in.Nodes = append(in.Nodes, Node{"entry", []int64{1, 2, 3, 4}})
		}
	}
	samples := make([]float64, *calls)
	var wg sync.WaitGroup
	started := time.Now()
	for worker := 0; worker < *concurrency; worker++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := offset; i < *calls; i += *concurrency {
				start := time.Now()
				result, err := work(context.Background(), in)
				samples[i] = float64(time.Since(start).Nanoseconds()) / 1000
				if err != nil || len(result.Data) != *size || len(result.Digest) != 64 {
					panic("incorrect native result")
				}
			}
		}(worker)
	}
	wg.Wait()
	elapsed := time.Since(started).Seconds()
	sort.Float64s(samples)
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"calls_per_second": float64(*calls) / elapsed, "p50_us": samples[(*calls-1)/2], "p95_us": samples[int(float64(*calls-1)*.95)], "p99_us": samples[int(float64(*calls-1)*.99)], "samples_us": samples}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
