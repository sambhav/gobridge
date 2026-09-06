package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

//gobridge:enum
type Mode string

const (
	Fast    Mode = "fast"
	Careful Mode = "careful"
)

//gobridge:enum
type Level uint64

const (
	Small Level = 1
	Huge  Level = 18446744073709551615
)

type Identifier string

func (Identifier) GobridgeWireType() reflect.Type { return reflect.TypeOf("") }
func (v Identifier) MarshalText() ([]byte, error) { return []byte(v), nil }
func (v *Identifier) UnmarshalText(data []byte) error {
	if !strings.HasPrefix(string(data), "id-") {
		return fmt.Errorf("invalid identifier")
	}
	*v = Identifier(data)
	return nil
}

type Payload struct {
	ID     uint64     `json:"id" validate:"min=1,max=18446744073709551615"`
	Pair   [2]uint16  `json:"pair"`
	Mode   Mode       `json:"mode"`
	Level  Level      `json:"level"`
	Key    Identifier `json:"key"`
	Region *string    `json:"region" required:"true"`
	Label  *string    `json:"label,omitempty" required:"false" nullable:"false"`
}

//gobridge:export
//gobridge:python round_trip
//gobridge:ts roundTrip
func Echo(value Payload) Payload { return value }

//gobridge:export
func Empty(value [0]string) [0]string { return value }

//gobridge:export
func Signed(value int64) int64 { return value }

//gobridge:export
func Show(value Payload) string { data, _ := json.Marshal(value); return string(data) }
func main() {
	r, err := NewGobridge()
	if err != nil {
		panic(err)
	}
	r.Main()
}
