package cli

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

type flagKind int

const (
	valueFlag flagKind = iota
	bareFlag
)

type flagSet map[string]flagKind

// flagSpec declares one flag: how it parses and how help describes it.
type flagSpec struct {
	name  string
	kind  flagKind
	value string
	help  string
}

func bare(name, help string) flagSpec { return flagSpec{name: name, kind: bareFlag, help: help} }

func valued(name, value, help string) flagSpec {
	return flagSpec{name: name, kind: valueFlag, value: value, help: help}
}

func (f flagSpec) label() string {
	if f.kind == bareFlag {
		return "--" + f.name
	}
	return "--" + f.name + "=" + f.value
}

var (
	globalFlagSpecs = []flagSpec{
		valued("env", "<name>", "The environment to act on, from your configuration. Required."),
		bare("no-color", "Print without colour. Setting NO_COLOR does the same."),
		bare("help", "Show help for a command. -h does the same."),
	}
	mutatingFlagSpecs = []flagSpec{
		bare("apply", "Send the change. Without it, the Admin API only checks the change."),
		bare("offline", "Print the request without asking the Admin API to check it."),
		valued("idempotency-key", "<key>", "Send this Idempotency-Key, so a retry changes nothing twice."),
	}
	formatFlag = valued("format", "text|json", "Print text for people, or JSON for scripts.")
)

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

// flagSpecs are the command's own flags, then the flags every mutation takes,
// then --format for every command that prints data.
func (c *command) flagSpecs() []flagSpec {
	specs := append([]flagSpec{}, c.flags...)
	if c.mutating {
		specs = append(specs, mutatingFlagSpecs...)
	}
	if c.printsData() && !slices.ContainsFunc(specs, func(f flagSpec) bool { return f.name == formatFlag.name }) {
		specs = append(specs, formatFlag)
	}
	return specs
}

func (c *command) printsData() bool { return !c.standalone && !c.messagesOnly }

func (c *command) acceptedFlags() flagSet {
	accepted := flagSet{}
	for _, spec := range append(c.flagSpecs(), globalFlagSpecs...) {
		accepted[spec.name] = spec.kind
	}
	delete(accepted, "help")
	return accepted
}

func (c *command) validate(parsed parsedArgs, args []string) error {
	if len(args) != c.argc {
		return fmt.Errorf("usage: octogee-fleet --env=<name> %s", c.synopsis())
	}
	accepted := c.acceptedFlags()
	names := make([]string, 0, len(parsed.flags))
	for name := range parsed.flags {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := checkFlag(c, name, parsed.flags[name], accepted); err != nil {
			return err
		}
	}
	if format := parsed.flags["format"].value; parsed.has("format") && format != formatText && format != formatJSON {
		return fmt.Errorf("--format must be text or json, got %q", format)
	}
	if parsed.has("apply") && parsed.has("offline") {
		return errors.New("--offline prints a request without sending it, so it cannot be combined with --apply")
	}
	for _, name := range c.required {
		if !parsed.has(name) {
			return fmt.Errorf("%s requires --%s; usage: octogee-fleet --env=<name> %s", c.name(), name, c.synopsis())
		}
	}
	return nil
}

func checkFlag(c *command, name string, flag parsedFlag, accepted flagSet) error {
	kind, ok := accepted[name]
	if !ok {
		if suggestion := suggestFlag(name, accepted); suggestion != "" {
			return fmt.Errorf("%s does not accept --%s\ndid you mean --%s?", c.name(), name, suggestion)
		}
		return fmt.Errorf("%s does not accept --%s\nrun `octogee-fleet help %s` for its flags", c.name(), name, c.name())
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
