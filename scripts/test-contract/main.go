package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	var files []sourceFile
	if err := json.NewDecoder(os.Stdin).Decode(&files); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	failed := false
	for _, f := range files {
		for _, err := range checkSource(f) {
			fmt.Fprintln(os.Stderr, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Printf("PASS: %d changed Go test files checked\n", len(files))
}
