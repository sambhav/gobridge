package main

import (
	"encoding/json"
	"flag"
	"fmt"
	bridge "github.com/sambhav/gobridge"
	"io"
	"os"
)

func runAPIDiff(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("api-diff", flag.ContinueOnError)
	flags.SetOutput(out)
	check := flags.Bool("check", false, "fail if potentially breaking API changes are present")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 2 {
		return fmt.Errorf("usage: gobridge api-diff [--check] before.json after.json")
	}
	read := func(path string) (bridge.APISnapshot, error) {
		var value bridge.APISnapshot
		f, err := os.Open(path)
		if err != nil {
			return value, err
		}
		defer f.Close()
		dec := json.NewDecoder(f)
		dec.DisallowUnknownFields()
		err = dec.Decode(&value)
		if err != nil {
			return value, err
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			return value, fmt.Errorf("snapshot must contain one JSON object")
		}
		return value, nil
	}
	before, err := read(flags.Arg(0))
	if err != nil {
		return err
	}
	after, err := read(flags.Arg(1))
	if err != nil {
		return err
	}
	changes, err := bridge.DiffAPI(before, after)
	if err != nil {
		return err
	}
	if err = json.NewEncoder(out).Encode(changes); err != nil {
		return err
	}
	if *check {
		for _, change := range changes {
			if change.Breaking {
				return fmt.Errorf("potentially breaking API changes detected")
			}
		}
	}
	return nil
}
