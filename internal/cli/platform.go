package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
)

var platformCommands = []*command{
	{
		path: words("ops ls"), usage: "[--status=<status>] [--limit=<n>]", summary: "List operations from the journal.",
		flags: []flagSpec{
			valued("status", "<status>", "Only operations with this status, such as failed, or unresolved."),
			valued("limit", "<n>", "List at most n operations."),
		},
		examples: []string{"", "--status=failed --limit=10"},
		run:      opsList,
	},
	{
		path: words("ops inspect"), usage: "<operation>", argc: 1, summary: "Show one operation's steps and outcome.",
		examples: []string{"<operation>"},
		run:      opsInspect,
	},
	{
		path: words("builds ls"), usage: "[--service=<service>] [--limit=<n>]", summary: "List builds.",
		flags: []flagSpec{
			valued("service", "<service>", "The image whose builds to list; the Admin API names the ones it accepts."),
			valued("limit", "<n>", "List at most n builds."),
		},
		examples: []string{"", "--limit=5"},
		run:      buildsList,
	},
	{
		path: words("release status"), summary: "Show release convergence across the fleet.",
		examples: []string{""},
		run:      releaseStatus,
	},
	{
		path: words("logs"), usage: "[--level=<level>] [--service=<names>] [--user=<names>] [--migration=<id>] [--gateway=<gateway>] [--since=<age>] [--no-follow] [--format=text|json]", summary: "Tail shared-gateway and VM-gateway logs together.",
		flags: []flagSpec{
			valued("level", "<level>", "The lowest severity to show, such as warn or error."),
			valued("service", "<names>", "Only these services, separated by commas."),
			valued("user", "<names>", "Only these users, separated by commas."),
			valued("migration", "<id>", "Only this migration."),
			valued("gateway", "<gateway>", "Only this gateway."),
			valued("since", "<age>", "How far back to read, such as 30m or 2h. Needs --no-follow."),
			bare("no-follow", "Print what is there and stop, instead of following new lines."),
			valued("format", "text|json", "Print lines for people, or the Admin API's JSON lines."),
		},
		examples: []string{"--gateway=shared-a --level=warn", "--no-follow --since=30m --user=ada"},
		run:      logs,
	},
}

func limitedQuery(i *invocation, filter string) (url.Values, error) {
	query := url.Values{}
	if i.has(filter) {
		query.Set(filter, i.value(filter))
	}
	if i.has("limit") {
		limit, err := positiveInt("limit", i.value("limit"))
		if err != nil {
			return nil, err
		}
		query.Set("limit", strconv.Itoa(limit))
	}
	return query, nil
}

// operationView is an operation or an audit from the journal.
type operationView struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	What        string         `json:"what"`
	Status      string         `json:"status"`
	CreatedBy   string         `json:"createdBy"`
	CreatedAt   instant        `json:"createdAt"`
	CompletedAt instant        `json:"completedAt"`
	GatewayID   string         `json:"gatewayId"`
	Target      map[string]any `json:"target"`
	Error       any            `json:"error"`
	Steps       []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Error  any    `json:"error"`
	} `json:"steps"`
}

// label is the operation's kind, or what an audit without a kind did.
func (o operationView) label() string {
	if o.Kind != "" {
		return o.Kind
	}
	return dash(o.What)
}

func opsList(i *invocation) error {
	query, err := limitedQuery(i, "status")
	if err != nil {
		return err
	}
	var response struct {
		Operations []operationView `json:"operations"`
	}
	raw, err := i.read(admin.Path("operations"), query, &response)
	if err != nil {
		return err
	}
	if i.json {
		return i.printJSON(raw)
	}
	rows := make([][]string, 0, len(response.Operations))
	for _, operation := range response.Operations {
		rows = append(rows, []string{
			i.style.bold(operation.ID), operation.label(), i.style.state(operation.Status), dash(operation.CreatedBy), i.ago(operation.CreatedAt),
		})
	}
	i.table([]string{"id", "kind", "status", "by", "created"}, rows, "no operations")
	return nil
}

func buildsList(i *invocation) error {
	query, err := limitedQuery(i, "service")
	if err != nil {
		return err
	}
	var response struct {
		Builds []struct {
			Tag      string  `json:"tag"`
			PushedAt instant `json:"pushedAt"`
			Digest   string  `json:"digest"`
		} `json:"builds"`
	}
	raw, err := i.read(admin.Path("builds"), query, &response)
	if err != nil {
		return err
	}
	if i.json {
		return i.printJSON(raw)
	}
	rows := make([][]string, 0, len(response.Builds))
	for _, build := range response.Builds {
		rows = append(rows, []string{i.style.bold(dash(build.Tag)), i.ago(build.PushedAt), i.style.dim(shortDigest(build.Digest))})
	}
	i.table([]string{"tag", "pushed", "digest"}, rows, "no builds")
	return nil
}

// shortDigest keeps an image digest's algorithm and its first 12 hex digits.
func shortDigest(digest string) string {
	algorithm, hex, found := strings.Cut(digest, ":")
	if !found {
		return dash(shortSHA(digest))
	}
	return algorithm + ":" + shortSHA(hex)
}

func opsInspect(i *invocation) error {
	var response struct {
		Operation operationView `json:"operation"`
	}
	raw, err := i.read(admin.Path("operations", i.args[0]), nil, &response)
	if err != nil {
		return err
	}
	if i.json {
		return i.printJSON(raw)
	}
	operation := response.Operation
	i.line(i.style.bold(operation.ID) + "  " + operation.label())
	what := ""
	if operation.Kind != "" {
		what = operation.What
	}
	failure := ""
	if operation.Error != nil {
		failure = i.style.red(fieldText(operation.Error))
	}
	i.fields([]field{
		{"status", i.style.dot(operation.Status)},
		{"what", what},
		{"by", operation.CreatedBy},
		{"created", i.stamp(operation.CreatedAt)},
		{"completed", i.stamp(operation.CompletedAt)},
		{"gateway", operation.GatewayID},
		{"target", fieldList(operation.Target)},
		{"error", failure},
	})
	if len(operation.Steps) > 0 {
		i.line("")
		rows := make([][]string, 0, len(operation.Steps))
		for _, step := range operation.Steps {
			problem := ""
			if step.Error != nil {
				problem = i.style.red(fieldText(step.Error))
			}
			rows = append(rows, []string{step.Name, i.style.state(step.Status), problem})
		}
		i.table([]string{"step", "status", "error"}, rows, "")
	}
	return nil
}

func releaseStatus(i *invocation) error {
	var response struct {
		Summary struct {
			Status    string   `json:"status"`
			Converged int      `json:"converged"`
			Serving   int      `json:"serving"`
			Blockers  []string `json:"blockers"`
		} `json:"summary"`
		Gateways []struct {
			GatewayID string `json:"gatewayId"`
			Desired   *int   `json:"desiredRelease"`
			Live      *int   `json:"liveRelease"`
			Runtime   *int   `json:"runtimeRelease"`
			Status    string `json:"observation"`
		} `json:"gateways"`
	}
	raw, err := i.read(admin.Path("release-status"), nil, &response)
	if err != nil {
		return err
	}
	if i.json {
		return i.printJSON(raw)
	}
	i.line(fmt.Sprintf("%s: %d of %d serving gateways converged", i.style.dot(response.Summary.Status), response.Summary.Converged, response.Summary.Serving))
	for _, blocker := range response.Summary.Blockers {
		i.warn("outstanding: " + blocker)
	}
	rows := make([][]string, 0, len(response.Gateways))
	for _, gateway := range response.Gateways {
		rows = append(rows, []string{i.style.bold(gateway.GatewayID), release(gateway.Desired), release(gateway.Live), release(gateway.Runtime), i.style.state(gateway.Status)})
	}
	i.table([]string{"gateway", "desired", "live", "runtime", "status"}, rows, "no gateways")
	return nil
}

func release(value *int) string {
	if value == nil {
		return "-"
	}
	return strconv.Itoa(*value)
}

func logs(i *invocation) error {
	format := i.value("format")
	query := url.Values{}
	for _, name := range []string{"level", "service", "user", "migration", "gateway", "since"} {
		if i.has(name) {
			query.Set(name, i.value(name))
		}
	}
	if i.has("no-follow") {
		query.Set("follow", "false")
	}
	return i.admin.Stream(i.ctx, admin.Request{Method: http.MethodGet, Path: admin.Path("logs"), Query: query}, func(line []byte) error {
		fmt.Fprintln(i.out, renderLogLine(line, format))
		return nil
	})
}

func renderLogLine(line []byte, format string) string {
	if format == "json" {
		return string(line)
	}
	var entry struct {
		Timestamp string `json:"timestamp"`
		Severity  string `json:"severity"`
		Service   string `json:"service"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(line, &entry); err != nil {
		return string(line)
	}
	return fmt.Sprintf("%s %s %s %s", entry.Timestamp, entry.Severity, entry.Service, entry.Message)
}
