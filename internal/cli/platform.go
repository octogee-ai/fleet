package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
)

var platformCommands = []*command{
	{path: words("ops ls"), usage: "[--status=<status>] [--limit=<n>]", summary: "List operations from the journal.", flags: flagSet{"status": valueFlag, "limit": valueFlag}, run: opsList},
	{path: words("ops inspect"), usage: "<operation>", argc: 1, summary: "Show one operation's steps and outcome.", run: opsInspect},
	{path: words("builds ls"), usage: "[--service=<service>] [--limit=<n>]", summary: "List builds.", flags: flagSet{"service": valueFlag, "limit": valueFlag}, run: buildsList},
	{path: words("release status"), summary: "Show release convergence across the fleet.", run: releaseStatus},
	{path: words("logs"), usage: "[--level=<level>] [--service=<names>] [--user=<names>] [--migration=<id>] [--gateway=<gateway>] [--since=<age>] [--no-follow] [--format=text|json]", summary: "Tail shared-gateway and VM-gateway logs together.", flags: flagSet{"level": valueFlag, "service": valueFlag, "user": valueFlag, "migration": valueFlag, "gateway": valueFlag, "since": valueFlag, "no-follow": bareFlag, "format": valueFlag}, run: logs},
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

func opsList(i *invocation) error {
	query, err := limitedQuery(i, "status")
	if err != nil {
		return err
	}
	var response struct {
		Operations []struct {
			ID        string `json:"id"`
			Kind      string `json:"kind"`
			Status    string `json:"status"`
			CreatedBy string `json:"createdBy"`
		} `json:"operations"`
	}
	if err := i.admin.Get(i.ctx, admin.Path("operations"), query, &response); err != nil {
		return err
	}
	rows := make([][]string, 0, len(response.Operations))
	for _, operation := range response.Operations {
		rows = append(rows, []string{i.style.bold(operation.ID), dash(operation.Kind), i.style.state(operation.Status), dash(operation.CreatedBy)})
	}
	i.table([]string{"id", "kind", "status", "by"}, rows, "no operations")
	return nil
}

func buildsList(i *invocation) error {
	query, err := limitedQuery(i, "service")
	if err != nil {
		return err
	}
	var response map[string]any
	if err := i.admin.Get(i.ctx, admin.Path("builds"), query, &response); err != nil {
		return err
	}
	return i.printJSON(response["builds"])
}

func opsInspect(i *invocation) error {
	var response struct {
		Operation map[string]any `json:"operation"`
	}
	if err := i.admin.Get(i.ctx, admin.Path("operations", i.args[0]), nil, &response); err != nil {
		return err
	}
	return i.printJSON(response.Operation)
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
	if err := i.admin.Get(i.ctx, admin.Path("release-status"), nil, &response); err != nil {
		return err
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
	if i.has("format") && format != "text" && format != "json" {
		return fmt.Errorf("--format must be text or json, got %q", format)
	}
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
