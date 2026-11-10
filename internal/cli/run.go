// Package cli implements the fleet command line. Every command is a client of
// the Admin API: it reads what it needs, then sends at most one mutation.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/octogee-ai/fleet/internal/admin"
	"github.com/octogee-ai/fleet/internal/config"
	"github.com/octogee-ai/fleet/internal/oidc"
)

// Runtime is everything the command line touches outside its own memory.
type Runtime struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Getenv  func(string) string
	HTTP    admin.Doer
	Sleep   func(context.Context, time.Duration) error
	Version string
}

// Run executes one command line and returns the process exit code.
func Run(ctx context.Context, argv []string, runtime Runtime) int {
	if runtime.HTTP == nil {
		runtime.HTTP = oidc.NewHTTPClient()
	}
	err := run(ctx, argv, runtime)
	if err == nil {
		return 0
	}
	if !errors.Is(err, errNoCommand) {
		fmt.Fprintf(runtime.Stderr, "fleet: %v\n", err)
	}
	return 1
}

func run(ctx context.Context, argv []string, runtime Runtime) error {
	parsed, err := parseArgs(argv)
	if err != nil {
		return err
	}
	if parsed.has("version") {
		fmt.Fprintln(runtime.Stdout, runtime.Version)
		return nil
	}
	if len(parsed.positional) == 0 {
		writeHelp(runtime.Stdout)
		if parsed.has("help") {
			return nil
		}
		return errNoCommand
	}
	if parsed.positional[0] == "help" {
		return help(runtime.Stdout, parsed.positional[1:])
	}
	cmd, args := lookup(parsed.positional)
	if cmd == nil {
		return fmt.Errorf("unknown command %q; run `fleet help`", strings.Join(parsed.positional, " "))
	}
	if parsed.has("help") {
		writeCommandHelp(runtime.Stdout, cmd)
		return nil
	}
	if err := cmd.validate(parsed, args); err != nil {
		return err
	}
	invocation, err := newInvocation(ctx, parsed, args, runtime)
	if err != nil {
		return err
	}
	return cmd.run(invocation)
}

func newInvocation(ctx context.Context, parsed parsedArgs, args []string, runtime Runtime) (*invocation, error) {
	name := parsed.flags["env"].value
	if name == "" {
		return nil, errors.New("--env=<name> is required")
	}
	path, err := config.Path(runtime.Getenv)
	if err != nil {
		return nil, err
	}
	file, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	environment, err := file.Environment(name)
	if err != nil {
		return nil, err
	}
	client := &admin.Client{
		Origin: environment.AdminAPIOrigin,
		HTTP:   runtime.HTTP,
		Token:  bearerToken(name, environment, runtime),
	}
	return &invocation{ctx: ctx, out: runtime.Stdout, runtime: runtime, name: name, environment: environment, admin: client, args: args, flags: parsed.flags}, nil
}
