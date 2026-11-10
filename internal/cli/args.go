package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type flagKind int

const (
	valueFlag flagKind = iota
	bareFlag
)

type flagSet map[string]flagKind

type parsedFlag struct {
	value    string
	hasValue bool
}

type parsedArgs struct {
	positional []string
	flags      map[string]parsedFlag
}

func (p parsedArgs) has(name string) bool {
	_, ok := p.flags[name]
	return ok
}

func parseArgs(argv []string) (parsedArgs, error) {
	parsed := parsedArgs{flags: map[string]parsedFlag{}}
	for _, arg := range argv {
		if arg == "-h" {
			arg = "--help"
		}
		if !strings.HasPrefix(arg, "--") {
			parsed.positional = append(parsed.positional, arg)
			continue
		}
		name, value, hasValue := strings.Cut(arg[2:], "=")
		if name == "" {
			return parsed, fmt.Errorf("malformed flag %q", arg)
		}
		if parsed.has(name) {
			return parsed, fmt.Errorf("--%s was given more than once", name)
		}
		parsed.flags[name] = parsedFlag{value: value, hasValue: hasValue}
	}
	return parsed, nil
}

func (c *command) acceptedFlags() flagSet {
	accepted := flagSet{}
	for name, kind := range c.flags {
		accepted[name] = kind
	}
	if c.mutating {
		accepted["apply"] = bareFlag
		accepted["idempotency-key"] = valueFlag
	}
	return accepted
}

func (c *command) validate(parsed parsedArgs, args []string) error {
	if len(args) != c.argc {
		return fmt.Errorf("usage: fleet --env=<name> %s", c.synopsis())
	}
	accepted := c.acceptedFlags()
	names := make([]string, 0, len(parsed.flags))
	for name := range parsed.flags {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "env" {
			continue
		}
		if err := checkFlag(c, name, parsed.flags[name], accepted); err != nil {
			return err
		}
	}
	for _, name := range c.required {
		if !parsed.has(name) {
			return fmt.Errorf("%s requires --%s; usage: fleet --env=<name> %s", c.name(), name, c.synopsis())
		}
	}
	return nil
}

func checkFlag(c *command, name string, flag parsedFlag, accepted flagSet) error {
	kind, ok := accepted[name]
	if !ok {
		return fmt.Errorf("%s does not accept --%s", c.name(), name)
	}
	if kind == bareFlag && flag.hasValue {
		return fmt.Errorf("--%s takes no value", name)
	}
	if kind == valueFlag && flag.value == "" {
		return fmt.Errorf("--%s requires a value", name)
	}
	return nil
}

var errNoCommand = errors.New("no command given")
