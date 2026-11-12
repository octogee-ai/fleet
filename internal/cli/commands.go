package cli

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
	"github.com/octogee-ai/octogee-fleet/internal/config"
)

type command struct {
	path     []string
	usage    string
	summary  string
	argc     int
	flags    []flagSpec
	required []string
	mutating bool
	examples []string
	// standalone commands need no environment and call no Admin API.
	standalone bool
	run        func(*invocation) error
}

func (c *command) name() string { return strings.Join(c.path, " ") }

func (c *command) synopsis() string {
	parts := []string{c.name()}
	if c.usage != "" {
		parts = append(parts, c.usage)
	}
	if c.mutating {
		parts = append(parts, "[--idempotency-key=<key>] [--apply | --offline]")
	}
	return strings.Join(parts, " ")
}

func words(text string) []string { return strings.Fields(text) }

func allCommands() []*command {
	commands := append([]*command{}, gatewayCommands...)
	commands = append(commands, userCommands...)
	commands = append(commands, platformCommands...)
	commands = append(commands, authCommands...)
	return append(commands, completionCommand)
}

func lookup(positional []string) (*command, []string) {
	path := canonical(positional)
	var best *command
	for _, candidate := range allCommands() {
		if hasPrefix(path, candidate.path) && (best == nil || len(candidate.path) > len(best.path)) {
			best = candidate
		}
	}
	if best == nil {
		return nil, nil
	}
	return best, path[len(best.path):]
}

func hasPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for index, word := range prefix {
		if path[index] != word {
			return false
		}
	}
	return true
}

type invocation struct {
	ui
	ctx         context.Context
	runtime     Runtime
	waiting     *spinner
	name        string
	environment config.Environment
	admin       *admin.Client
	args        []string
	flags       map[string]parsedFlag
}

func (i *invocation) has(name string) bool {
	_, ok := i.flags[name]
	return ok
}

func (i *invocation) value(name string) string { return i.flags[name].value }

var (
	fullSHAPattern   = regexp.MustCompile(`^[a-f0-9]{40}$`)
	gatewayIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
)

func fullSHA(value string) (string, error) {
	if !fullSHAPattern.MatchString(value) {
		return "", fmt.Errorf("--sha must be a full lowercase commit sha, got %q", value)
	}
	return value, nil
}

func positiveInt(flag, value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("--%s must be a positive integer, got %q", flag, value)
	}
	return number, nil
}
