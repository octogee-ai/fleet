package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
)

var platformCommands = []*command{
	{path: words("ops ls"), usage: "[--status=<status>] [--limit=<n>]", summary: "List operations from the journal.", flags: flagSet{"status": valueFlag, "limit": valueFlag}, run: opsList},
	{path: words("ops inspect"), usage: "<operation>", argc: 1, summary: "Show one operation's steps and outcome.", run: opsInspect},
	{path: words("builds ls"), usage: "[--service=<service>] [--limit=<n>]", summary: "List builds.", flags: flagSet{"service": valueFlag, "limit": valueFlag}, run: buildsList},
	{path: words("release status"), summary: "Show release convergence across the fleet.", run: releaseStatus},
	{path: words("pool show"), summary: "Show the shared-gateway placement pool.", run: poolShow},
	{path: words("pool autoscaling"), usage: "[--enabled=true|false] [--users-per-gateway=<n>]", summary: "Set the shared-gateway pool's autoscaling.", flags: flagSet{"enabled": valueFlag, "users-per-gateway": valueFlag}, mutating: true, run: poolAutoscaling},
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
		rows = append(rows, []string{operation.ID, dash(operation.Kind), dash(operation.Status), dash(operation.CreatedBy)})
	}
	writeTable(i.out, []string{"id", "kind", "status", "by"}, rows)
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
	fmt.Fprintf(i.out, "%s: %d of %d serving gateways converged\n", dash(response.Summary.Status), response.Summary.Converged, response.Summary.Serving)
	if len(response.Summary.Blockers) > 0 {
		fmt.Fprintf(i.out, "outstanding: %s\n", strings.Join(response.Summary.Blockers, "; "))
	}
	rows := make([][]string, 0, len(response.Gateways))
	for _, gateway := range response.Gateways {
		rows = append(rows, []string{gateway.GatewayID, release(gateway.Desired), release(gateway.Live), release(gateway.Runtime), dash(gateway.Status)})
	}
	writeTable(i.out, []string{"gateway", "desired", "live", "runtime", "status"}, rows)
	return nil
}

func release(value *int) string {
	if value == nil {
		return "-"
	}
	return strconv.Itoa(*value)
}

func poolShow(i *invocation) error {
	directory, err := i.placement()
	if err != nil {
		return err
	}
	config := directory.Config
	fmt.Fprintf(i.out, "default gateway: %s\n", dash(config.DefaultGatewayID))
	fmt.Fprintf(i.out, "shared pool: %s\n", dash(strings.Join(config.SharedGatewayIDs, ", ")))
	fmt.Fprintf(i.out, "autoscaling: enabled=%t users-per-gateway=%d\n", config.Autoscaling.Enabled, config.Autoscaling.UsersPerGateway)
	rows := make([][]string, 0, len(directory.Gateways))
	for _, gateway := range directory.Gateways {
		rows = append(rows, []string{
			gateway.GatewayID, dash(gateway.GatewayClass), dash(gateway.DesiredStatus),
			strconv.Itoa(gateway.AssignedCount), strconv.Itoa(gateway.MountedCount), strconv.Itoa(gateway.DriftCount),
		})
	}
	writeTable(i.out, []string{"gateway", "class", "desired", "assigned", "mounted", "mismatched"}, rows)
	return nil
}

func poolAutoscaling(i *invocation) error {
	if !i.has("enabled") && !i.has("users-per-gateway") {
		return errors.New("pool autoscaling requires --enabled or --users-per-gateway")
	}
	var enabled *bool
	if i.has("enabled") {
		value, err := strconv.ParseBool(i.value("enabled"))
		if err != nil {
			return fmt.Errorf("--enabled must be true or false, got %q", i.value("enabled"))
		}
		enabled = &value
	}
	usersPerGateway := 0
	if i.has("users-per-gateway") {
		value, err := positiveInt("users-per-gateway", i.value("users-per-gateway"))
		if err != nil {
			return err
		}
		usersPerGateway = value
	}
	directory, err := i.placement()
	if err != nil {
		return err
	}
	next := directory.Config.Autoscaling
	if enabled != nil {
		next.Enabled = *enabled
	}
	if usersPerGateway != 0 {
		next.UsersPerGateway = usersPerGateway
	}
	return i.mutate(admin.Request{
		Method: http.MethodPatch,
		Path:   admin.Path("placement", "autoscaling"),
		Body:   map[string]any{"enabled": next.Enabled, "usersPerGateway": next.UsersPerGateway},
	})
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
