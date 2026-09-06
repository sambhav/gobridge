package main

import "github.com/sambhav/gobridge/examples/catalog"

func main() {
	r, err := catalog.NewGobridge()
	if err != nil {
		panic(err)
	}
	r.Main()
}
