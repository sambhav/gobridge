// Command annotated exposes the greeter library as a CLI and stdio daemon.
package main

import (
	"fmt"
	"os"

	greeter "github.com/sambhav/gobridge/examples/annotated"
)

func main() {
	r, err := greeter.NewGobridge()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	r.Main()
}
