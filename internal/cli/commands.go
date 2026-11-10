package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/octogee-ai/fleet/internal/admin"
	"github.com/octogee-ai/fleet/internal/config"
)

type command struct {
	path     []string
	usage    string
	summary  string
	argc     int
	flags    flagSet
	required []string
	mutating bool
	run      func(*invocation) error
}

func (c *command) name() string { return strings.Join(c.path, " ") }

func (c *command) synopsis() string {
	parts := []string{c.name()}
	if c.usage != "" {
		parts = append(parts, c.usage)
	}
	if c.mutating {
		parts = append(parts, "[--idempotency-key=<key>] [--apply]")
	}
	return strings.Join(parts, " ")
}

func words(text string) []string { return strings.Fields(text) }

func allCommands() []*command {
	commands := append([]*command{}, authCommands...)
	commands = append(commands, gatewayCommands...)
	commands = append(commands, userCommands...)
	return append(commands, platformCommands...)
}

func lookup(positional []string) (*command, []string) {
	path := append([]string{}, positional...)
	if path[0] == "gw" {
		path[0] = "gateway"
	}
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

func help(out io.Writer, target []string) error {
	if len(target) == 0 {
		writeHelp(out)
		return nil
	}
	cmd, args := lookup(target)
	if cmd == nil || len(args) != 0 {
		return fmt.Errorf("unknown command %q; run `fleet help`", strings.Join(target, " "))
	}
	writeCommandHelp(out, cmd)
	return nil
}

func writeHelp(out io.Writer) {
	fmt.Fprintln(out, "fleet: operate a fleet of OpenClaw gateways through the Admin API")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "usage: fleet --env=<name> <command> [arguments]")
	fmt.Fprintln(out, "Mutating commands are a dry run until --apply. `gw` is short for `gateway`.")
	fmt.Fprintln(out)
	table := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, cmd := range allCommands() {
		fmt.Fprintf(table, "  %s\t%s\n", cmd.name(), cmd.summary)
	}
	table.Flush()
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Run `fleet help <command>` for one command's arguments.")
}

func writeCommandHelp(out io.Writer, cmd *command) {
	fmt.Fprintln(out, cmd.summary)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "usage: fleet --env=<name> %s\n", cmd.synopsis())
}

type invocation struct {
	ctx         context.Context
	out         io.Writer
	runtime     Runtime
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

func (i *invocation) mutate(request admin.Request, notes ...string) error {
	data, applied, err := i.sendMutation(request, notes...)
	if err != nil || !applied {
		return err
	}
	i.printOutcome(data)
	return nil
}

func (i *invocation) sendMutation(request admin.Request, notes ...string) ([]byte, bool, error) {
	request.IdempotencyKey = i.value("idempotency-key")
	fmt.Fprintf(i.out, "%s %s\n", request.Method, request.Target())
	if request.Body != nil {
		if err := i.printJSON(request.Body); err != nil {
			return nil, false, err
		}
	}
	for _, note := range notes {
		fmt.Fprintln(i.out, note)
	}
	if !i.has("apply") {
		fmt.Fprintln(i.out, "dry run: pass --apply to send this request")
		return nil, false, nil
	}
	data, err := i.admin.Send(i.ctx, request)
	if err != nil {
		return nil, false, err
	}
	fmt.Fprintln(i.out, "applied")
	return data, true, nil
}

func (i *invocation) printOutcome(data []byte) {
	var outcome struct {
		OperationID string `json:"operationId"`
	}
	if err := json.Unmarshal(data, &outcome); err == nil && outcome.OperationID != "" {
		fmt.Fprintf(i.out, "operation %s\n", outcome.OperationID)
		return
	}
	if len(data) > 0 {
		fmt.Fprintln(i.out, strings.TrimSpace(string(data)))
	}
}

func (i *invocation) printJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	fmt.Fprintln(i.out, string(data))
	return nil
}

func (i *invocation) post(path string, body map[string]any, notes ...string) error {
	return i.mutate(admin.Request{Method: http.MethodPost, Path: path, Body: body}, notes...)
}

func (i *invocation) replaceVM(path string) error {
	force, accept := i.has("force"), i.has("accept-boot-data-loss")
	if force != accept {
		return errors.New("replacing a VM with boot-disk loss requires both --force and --accept-boot-data-loss")
	}
	if !force {
		return i.post(path, map[string]any{})
	}
	return i.post(path, map[string]any{"force": true, "acceptBootDataLoss": true},
		"BREAK GLASS: data on the boot disk, including unpublished work, may be lost; the verified home disk is kept")
}

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

func opaqueID(flag, value string) (string, error) {
	if strings.Contains(value, "/") {
		return "", fmt.Errorf("--%s must be an id, got %q", flag, value)
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

func wholeInt(flag, value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("--%s must be a whole number, got %q", flag, value)
	}
	return number, nil
}

func writeTable(out io.Writer, header []string, rows [][]string) {
	if len(rows) == 0 {
		fmt.Fprintln(out, "(none)")
		return
	}
	table := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(table, strings.ToUpper(strings.Join(header, "\t")))
	for _, row := range rows {
		fmt.Fprintln(table, strings.Join(row, "\t"))
	}
	table.Flush()
}

func dash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
