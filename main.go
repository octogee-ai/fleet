package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"time"

	"github.com/octogee-ai/octogee-fleet/internal/cli"
)

var version string

func versionOf(stamped string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if stamped != "" {
		return stamped
	}
	if info, ok := readBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return cli.Run(ctx, os.Args[1:], cli.Runtime{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Getenv:  os.Getenv,
		Sleep:   sleep,
		Version: versionOf(version, debug.ReadBuildInfo),
	})
}

func main() {
	os.Exit(run())
}
