package cli

import (
	"net/http"
	"testing"
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

func TestPoolShowReadsPlacement(t *testing.T) {
	run := fleet(t, "pool", "show")
	expectRead(t, run)
	if !run.containsRequest(adminRequest{Method: http.MethodGet, Path: "/api/admin/placement"}) {
		t.Fatalf("requests %+v", run.requests)
	}
	expectContains(t, run.stdout, "shared-a, shared-b")
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

func TestLogsRefusesAnUnknownFormat(t *testing.T) {
	expectNothingSent(t, fleet(t, "logs", "--format=xml"))
}
