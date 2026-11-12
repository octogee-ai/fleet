// Package cli implements the octogee-fleet command line. Every command is a client of
// the Admin API: it reads what it needs, then sends at most one mutation.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
	"github.com/octogee-ai/octogee-fleet/internal/config"
	"github.com/octogee-ai/octogee-fleet/internal/oidc"
)

// Runtime is everything the command line touches outside its own memory.
type Runtime struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Getenv  func(string) string
	HTTP    admin.Doer
	Sleep   func(context.Context, time.Duration) error
	Now     func() time.Time
	Version string

	// historyLimit bounds how long logs --no-follow waits; zero means the default.
	historyLimit time.Duration
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
		noColor := slices.Contains(argv, "--no-color")
		writeError(runtime.Stderr, choosePalette(runtime.Stderr, runtime.Getenv, noColor), err)
	}
	return 1
}

// advice is an error with what to try next. Each hint is written on a line of
// its own, apart from the error, whose text may come from the Admin API.
type advice struct {
	err   error
	hints []string
}

func advise(err error, hints ...string) error { return advice{err: err, hints: hints} }

func (a advice) Error() string {
	return strings.Join(append([]string{a.err.Error()}, a.hints...), "; ")
}

func (a advice) Unwrap() error { return a.err }

func writeError(out io.Writer, style palette, err error) {
	headline, hints := err.Error(), []string(nil)
	if given, ok := err.(advice); ok {
		headline, hints = given.err.Error(), given.hints
	}
	if !style.glyphs {
		fmt.Fprintln(out, forTerminal("octogee-fleet: "+headline))
		for _, hint := range hints {
			fmt.Fprintln(out, forTerminal(hint))
		}
		return
	}
	fmt.Fprintln(out, forTerminal(style.red("✗")+" "+style.bold(headline)))
	for _, hint := range hints {
		fmt.Fprintln(out, forTerminal("  "+style.cyan("→")+" "+hint))
	}
}

func run(ctx context.Context, argv []string, runtime Runtime) error {
	parsed, err := parseArgs(argv)
	if err != nil {
		return err
	}
	out := newUI(runtime, parsed)
	if parsed.has("version") {
		fmt.Fprintln(runtime.Stdout, runtime.Version)
		return nil
	}
	if len(parsed.positional) == 0 {
		writeHelp(out)
		if parsed.has("help") {
			return nil
		}
		return errNoCommand
	}
	if parsed.positional[0] == "help" {
		return help(out, parsed.positional[1:])
	}
	cmd, args := lookup(parsed.positional)
	if cmd == nil && len(parsed.positional) == 1 && isNoun(parsed.positional[0]) {
		writeGroupHelp(out, canonical(parsed.positional)[0])
		return nil
	}
	if cmd == nil {
		return unknownCommand(parsed.positional)
	}
	if parsed.has("help") {
		writeCommandHelp(out, cmd)
		return nil
	}
	if err := cmd.validate(parsed, args); err != nil {
		return err
	}
	if cmd.standalone {
		return cmd.run(&invocation{ui: out, ctx: ctx, runtime: runtime, args: args, flags: parsed.flags})
	}
	invocation, err := newInvocation(ctx, parsed, args, runtime, out)
	if err != nil {
		return err
	}
	return invocation.explain(cmd.run(invocation))
}

// explain adds what to try next to an Admin API error.
func (i *invocation) explain(err error) error {
	var failed *admin.HTTPError
	if !errors.As(err, &failed) {
		return err
	}
	switch {
	case failed.Status == http.StatusUnauthorized && i.environment.OIDC != nil:
		return advise(err, "sign in again: octogee-fleet --env="+i.name+" login")
	case failed.Status == http.StatusUnauthorized:
		return advise(err, "check the token that "+i.name+"'s tokenCommand prints")
	case failed.Status == http.StatusForbidden:
		return advise(err, "the Admin API accepted the sign-in, but this identity may not do this; check that it is on the platform's operator list")
	case failed.Status >= 500:
		return advise(err, "the Admin API could not finish; try again in a moment")
	}
	return err
}

func newInvocation(ctx context.Context, parsed parsedArgs, args []string, runtime Runtime, out ui) (*invocation, error) {
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
	waiting := newSpinner(runtime, out.errors)
	client := &admin.Client{
		Origin: environment.AdminAPIOrigin,
		HTTP:   waitingDoer{inner: runtime.HTTP, spinner: waiting},
		Token:  bearerToken(name, environment, runtime),
	}
	return &invocation{
		ctx: ctx, ui: out, runtime: runtime, waiting: waiting, name: name, environment: environment,
		admin: client, args: args, flags: parsed.flags,
	}, nil
}
