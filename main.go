package main

import (
	"context"
	"fmt"
	"os"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/cmd"
	"github.com/nonchan7720/manifold/pkg/config"
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
	_, err := config.Load(context.Background())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	return cmd.Execute()
}
