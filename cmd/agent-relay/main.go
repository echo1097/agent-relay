package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"agent-relay/internal/cli"
)

var version = "0.1.0-dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, version)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-relay:", err)
		os.Exit(1)
	}
}
