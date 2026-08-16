// Command stat prints a bundle's structural facts, so a second implementation's
// reading of the same file can be compared against stage0's own.
package main

import (
	"fmt"
	"os"

	"elisacore/src/frontendir"
)

func main() {
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	types, nodes, root, source, err := frontendir.BundleStats(data)
	if err != nil {
		fmt.Printf("fail %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("ok 2 types=%d nodes=%d root=%d source=%s\n", types, nodes, root, source)
}
