package main

import (
	"context"
	_ "embed"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"webdock/internal/app"
)

//go:embed VERSION
var version string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(app.New(strings.TrimSpace(version)).Run(ctx, os.Args[1:]))
}
