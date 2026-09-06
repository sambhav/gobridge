package gobridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func BenchmarkFrameDecode(b *testing.B) {
	// Each iteration reads 100 in-memory frames. JSON parsing and OS I/O are
	// excluded; this isolates delimiter scanning versus a uint32 length prefix.
	for _, size := range []int{64, 65536} {
		payload := bytes.Repeat([]byte("x"), size)
		for _, format := range []string{"jsonl", "length"} {
			b.Run(fmt.Sprintf("%s/bytes_%d", format, size), func(b *testing.B) {
				frame := append(append([]byte(nil), payload...), '\n')
				if format == "length" {
					frame = append(binary.BigEndian.AppendUint32(nil, uint32(size)), payload...)
				}
				stream := bytes.Repeat(frame, 100)
				buffer := make([]byte, MaxFrame)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					reader := bytes.NewReader(stream)
					if format == "jsonl" {
						scan := bufio.NewScanner(reader)
						scan.Buffer(buffer, MaxFrame+1)
						count := 0
						for scan.Scan() {
							count++
						}
						if scan.Err() != nil || count != 100 {
							b.Fatal("invalid frames", scan.Err())
						}
					} else {
						var header [4]byte
						for j := 0; j < 100; j++ {
							if _, err := io.ReadFull(reader, header[:]); err != nil {
								b.Fatal(err)
							}
							length := binary.BigEndian.Uint32(header[:])
							if length > MaxFrame {
								b.Fatal("frame too large")
							}
							if _, err := io.ReadFull(reader, buffer[:length]); err != nil {
								b.Fatal(err)
							}
						}
					}
				}
			})
		}
	}

}

func BenchmarkResponseEncoding(b *testing.B) {
	for _, size := range []int{0, 1024, 65536} {
		response := response{ID: "1", Result: struct {
			Data []byte `json:"data"`
		}{make([]byte, size)}}
		for _, direct := range []bool{false, true} {
			b.Run(fmt.Sprintf("bytes_%d/direct_%t", size, direct), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					var err error
					if direct {
						_, err = response.MarshalJSON()
					} else {
						_, err = json.Marshal(response)
					}
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkHello(b *testing.B) {
	r := New()
	for i := 0; i < 200; i++ {
		if err := Register(r, fmt.Sprintf("operation_%d", i), "", func(_ context.Context, in testInput) (testOutput, error) { return testOutput{in.Text}, nil }); err != nil {
			b.Fatal(err)
		}
	}
	for _, compact := range []bool{false, true} {
		b.Run(fmt.Sprintf("compact_%t", compact), func(b *testing.B) {
			raw := json.RawMessage(fmt.Sprintf(`{"compact":%t}`, compact))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				value, err := r.hello(raw)
				if err != nil {
					b.Fatal(err)
				}
				data, err := (response{ID: "1", Result: value}).MarshalJSON()
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(len(data)), "wire_bytes")
			}
		})
	}
}

// Transport-only, same-process echo: excludes serialization, process startup,
// and Python/Node scheduling. This is not an alternative production transport.
func BenchmarkTransportRoundTrip(b *testing.B) {
	for _, kind := range []string{"pipes", "unix"} {
		b.Run(kind, func(b *testing.B) {
			var reader io.Reader
			var writer io.Writer
			if kind == "pipes" {
				input, send, err := os.Pipe()
				if err != nil {
					b.Fatal(err)
				}
				recv, output, err := os.Pipe()
				if err != nil {
					input.Close()
					send.Close()
					b.Fatal(err)
				}
				b.Cleanup(func() { input.Close(); send.Close(); recv.Close(); output.Close() })
				go func() { _, _ = io.Copy(output, input) }()
				reader, writer = recv, send
			} else {
				listener, err := net.Listen("unix", filepath.Join(b.TempDir(), "echo.sock"))
				if err != nil {
					b.Skipf("Unix socket unavailable: %v", err)
				}
				b.Cleanup(func() { listener.Close() })
				go func() {
					peer, err := listener.Accept()
					if err == nil {
						defer peer.Close()
						_, _ = io.Copy(peer, peer)
					}
				}()
				conn, err := net.Dial("unix", listener.Addr().String())
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { conn.Close() })
				reader, writer = conn, conn
			}
			payload, reply := make([]byte, 64), make([]byte, 64)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := writer.Write(payload); err != nil {
					b.Fatal(err)
				}
				if _, err := io.ReadFull(reader, reply); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Compare parsing strategies on the same nested schema and wire bytes. The
// retained legacy decoder is also the oracle for decoder fuzzing.
func BenchmarkDecodeNested(b *testing.B) {
	value := fuzzInput{Text: "hello", Count: 1, Data: make([]byte, 1024), At: time.Now().UTC()}
	for i := 0; i < 16; i++ {
		value.Items = append(value.Items, fuzzChild{Name: "entry"})
	}
	raw, err := json.Marshal(value)
	if err != nil {
		b.Fatal(err)
	}
	typ := reflect.TypeOf(value)
	for _, name := range []string{"legacy", "optimized"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var err error
				if name == "legacy" {
					_, err = legacyDecode(raw, typ)
				} else {
					_, err = decodeInput(raw, typ)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCall(b *testing.B) {
	r := New()
	_ = Register(r, "echo", "", func(_ context.Context, in testInput) (testOutput, error) { return testOutput{in.Text}, nil })
	ctx := context.Background()
	raw := []byte(`{"text":"hello","count":1}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Call(ctx, "echo", raw); err != nil {
			b.Fatal(err)
		}
	}
}

// Same wire payload and result as BenchmarkCall, with the plain-function adapter.
func BenchmarkBindCall(b *testing.B) {
	r := New()
	if err := Bind(r, "echo", func(_ context.Context, text string, count int) (testOutput, error) {
		return testOutput{text}, nil
	}, "text", "count"); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	raw := []byte(`{"text":"hello","count":1}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Call(ctx, "echo", raw); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkMemoHit(b *testing.B) {
	m := NewMemo[string, int](10, time.Hour)
	ctx := context.Background()
	load := func(context.Context) (int, error) { return 1, nil }
	_, _ = m.Get(ctx, "key", load)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := m.Get(ctx, "key", load); err != nil {
			b.Fatal(err)
		}
	}
}
