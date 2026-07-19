package main

import (
	"fmt"
	"os"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/cmd"
)

func init() {
	trace.OpenTelemetryTracerName = "github.com/nonchan7720/manifold"
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	return cmd.Execute()
}
