package main

import (
	"context"
	"fmt"
	"github.com/sxxpqp/opskit/internal/app"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code, err := app.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(code)
}
