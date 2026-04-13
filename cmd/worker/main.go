package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Worker entrypoint will be wired when Temporal workflow is refactored
	// to use the new port-based architecture. The workflow and activity
	// packages depend on the application/port layer, not concrete adapters.
	fmt.Println("worker: not yet wired to new hexagonal structure")
	return nil
}
