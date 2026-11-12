package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOpsListFiltersByStatusAndLimit(t *testing.T) {
	run := fleet(t, "ops", "ls", "--status=running", "--limit=20")
	expectRead(t, run)
	if !run.containsRequest(adminRequest{Method: http.MethodGet, Path: "/api/admin/operations", Query: map[string]string{"status": "running", "limit": "20"}}) {
		t.Fatalf("requests %+v", run.requests)
	}
	expectContains(t, run.stdout, "op-1")
}

func TestOpsInspectShowsStepsAndFailureReason(t *testing.T) {
	run := fleet(t, "ops", "inspect", "op-1")
	expectRead(t, run)
	if !run.containsRequest(adminRequest{Method: http.MethodGet, Path: "/api/admin/operations/op-1"}) {
		t.Fatalf("requests %+v", run.requests)
	}
	expectContains(t, run.stdout, "quiesce-source")
	expectContains(t, run.stdout, "source drain timed out")
}

func lineWith(t *testing.T, text, want string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	t.Fatalf("no line contains %q:\n%s", want, text)
	return ""
}

func TestOpsListNamesAnAuditWithoutAKindByWhatItDid(t *testing.T) {
	run := fleetWith(t, fixture, forcedColor, "ops", "ls")
	expectContains(t, lineWith(t, run.stdout, "audit-1"), "update gateway desired configuration")
}

func TestOpsListSaysHowLongAgoEachOperationStarted(t *testing.T) {
	run := fleet(t, "ops", "ls")
	expectContains(t, lineWith(t, fleetWith(t, fixture, forcedColor, "ops", "ls").stdout, "op-1"), "3m ago")
	expectContains(t, lineWith(t, run.stdout, "op-1"), "2026-11-10T00:02:00Z")
}

func TestOpsInspectShowsTheOperationAsFields(t *testing.T) {
	run := fleet(t, "ops", "inspect", "op-1")
	if strings.HasPrefix(strings.TrimSpace(run.stdout), "{") {
		t.Fatalf("expected fields, not JSON:\n%s", run.stdout)
	}
	expectContains(t, lineWith(t, run.stdout, "error"), "migration failed at quiesce-source")
	expectContains(t, lineWith(t, run.stdout, "created"), "2026-11-10T00:02:00Z")
	terminal := fleetWith(t, fixture, forcedColor, "ops", "inspect", "op-1")
	expectContains(t, ansiPattern.ReplaceAllString(lineWith(t, terminal.stdout, "created"), ""), "2026-11-10 00:02:00 UTC (3m ago)")
	expectContains(t, lineWith(t, run.stdout, "reserve-target"), "completed")
}

func TestOpsInspectReportsAnUnknownOperation(t *testing.T) {
	run := fleet(t, "ops", "inspect", "op-missing")
	if run.exit == 0 {
		t.Fatalf("expected a failure, got: %s", run.stdout)
	}
	expectContains(t, run.stderr, "404")
}

func TestBuildsListFiltersByServiceAndLimit(t *testing.T) {
	run := fleet(t, "builds", "ls", "--service=gateway", "--limit=5")
	expectRead(t, run)
	if !run.containsRequest(adminRequest{Method: http.MethodGet, Path: "/api/admin/builds", Query: map[string]string{"service": "gateway", "limit": "5"}}) {
		t.Fatalf("requests %+v", run.requests)
	}
}

func TestBuildsListIsATable(t *testing.T) {
	run := fleet(t, "builds", "ls")
	expectContains(t, run.stdout, "TAG")
	row := lineWith(t, run.stdout, "v42")
	expectContains(t, row, "2026-11-09T22:05:00Z")
	expectContains(t, row, "sha256:0123456789ab")
	if strings.Contains(row, "0123456789abcdef0123") {
		t.Fatalf("expected a short digest:\n%s", row)
	}
}

func TestReleaseStatusIsReadOnly(t *testing.T) {
	run := fleet(t, "release", "status")
	expectRead(t, run)
	expectContains(t, run.stdout, "1 of 1 serving gateways converged")
}

func TestLimitMustBeAPositiveInteger(t *testing.T) {
	expectNothingSent(t, fleet(t, "ops", "ls", "--limit=all"))
}

func TestLogsStreamsTheFilteredLines(t *testing.T) {
	run := fleet(t, "logs", "--level=warn", "--service=api", "--user=ada", "--migration=m-1", "--no-follow")
	expectRead(t, run)
	if !run.containsRequest(adminRequest{Method: http.MethodGet, Path: "/api/admin/logs", Query: map[string]string{"level": "warn", "service": "api", "user": "ada", "migration": "m-1", "follow": "false"}}) {
		t.Fatalf("requests %+v", run.requests)
	}
	expectContains(t, run.stdout, "WARNING api a line from the log stream")
}

func TestLogsPassesJSONThroughUnchanged(t *testing.T) {
	run := fleet(t, "logs", "--no-follow", "--format=json")
	expectContains(t, run.stdout, `"severity":"WARNING"`)
}

func TestLogsWithoutFollowStopsWhenTheAdminAPIDoesNotFinish(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{logsHang: true, historyLimit: 50 * time.Millisecond}, "logs", "--no-follow", "--since=2h")
	if run.exit == 0 {
		t.Fatalf("expected a failure, got: %s", run.stdout)
	}
	expectContains(t, run.stderr, "older entries may not have been read")
	expectContains(t, run.stderr, "shorten --since")
}

func TestLogsSaysWhenOlderEntriesWereNotRead(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{logsTruncated: true}, "logs", "--no-follow", "--since=2h")
	if run.exit != 0 {
		t.Fatalf("exit %d: %s", run.exit, run.stderr)
	}
	expectContains(t, run.stderr, "older entries in this window were not read; shorten --since or add a filter")
	if strings.Contains(run.stdout, "Older entries") {
		t.Fatalf("the truncation marker is not a log line:\n%s", run.stdout)
	}
}

func TestLogsInJSONKeepTheTruncationMarker(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{logsTruncated: true}, "logs", "--no-follow", "--format=json")
	expectContains(t, run.stdout, `"truncated":true`)
	expectContains(t, run.stderr, "older entries in this window were not read")
}

func TestLogsColourTheSeverityOnATerminal(t *testing.T) {
	run := fleetWith(t, fixture, forcedColor, "logs", "--no-follow")
	expectContains(t, run.stdout, ansiYellow+"WARNING")
}

func TestLogsRefusesAnUnknownFormat(t *testing.T) {
	expectNothingSent(t, fleet(t, "logs", "--format=xml"))
}
