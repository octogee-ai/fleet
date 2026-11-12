package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
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
		return nil, false, i.dryRun(request)
	}
	data, err := i.admin.Send(i.ctx, request)
	if err != nil {
		return nil, false, err
	}
	fmt.Fprintln(i.out, "applied")
	return data, true, nil
}

func (i *invocation) dryRun(request admin.Request) error {
	if i.has("offline") {
		fmt.Fprintln(i.out, "dry run: pass --apply to send this request")
		return nil
	}
	if !i.admin.Supports(admin.DryRun) {
		fmt.Fprintln(i.out, "dry run: this Admin API does not answer dry runs, so it has not checked this request; pass --apply to send this request")
		return nil
	}
	data, err := i.admin.Send(i.ctx, request.AsDryRun())
	if err != nil {
		return dryRunRefusal(err)
	}
	var answer dryRunAnswer
	if err := json.Unmarshal(data, &answer); err != nil || !answer.DryRun {
		return fmt.Errorf("the Admin API did not answer with a plan, so the change may have been applied; it returned: %s", strings.TrimSpace(string(data)))
	}
	if len(answer.Plans) == 0 {
		return fmt.Errorf("the Admin API answered the dry run without saying what it would do; it returned: %s", strings.TrimSpace(string(data)))
	}
	answer.print(i.out)
	fmt.Fprintln(i.out, "dry run: checks made while a change is applied are not predicted; pass --apply to send this request")
	return nil
}

func dryRunRefusal(err error) error {
	var refused *admin.HTTPError
	if !errors.As(err, &refused) || refused.Status == http.StatusUnauthorized || refused.Status == http.StatusForbidden {
		return err
	}
	return fmt.Errorf("dry run: the Admin API would refuse this request (%s): %s\nnothing was changed", refused.Label(), refused.Message)
}

type dryRunAnswer struct {
	DryRun bool `json:"dryRun"`
	Plans  []struct {
		What         string         `json:"what"`
		Target       map[string]any `json:"target"`
		CurrentState map[string]any `json:"preActionStatus"`
		Forced       bool           `json:"forced"`
	} `json:"plans"`
}

func (a dryRunAnswer) print(out io.Writer) {
	for _, plan := range a.Plans {
		fmt.Fprintf(out, "dry run: the Admin API would %s\n", plan.What)
		printFields(out, "target", plan.Target)
		printFields(out, "current state", plan.CurrentState)
		if plan.Forced {
			fmt.Fprintln(out, "  forced: yes")
		}
	}
}

func printFields(out io.Writer, label string, fields map[string]any) {
	if len(fields) == 0 {
		return
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+fieldText(fields[name]))
	}
	fmt.Fprintf(out, "  %s: %s\n", label, strings.Join(parts, " "))
}

func fieldText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
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

func positiveInt(flag, value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("--%s must be a positive integer, got %q", flag, value)
	}
	return number, nil
}
