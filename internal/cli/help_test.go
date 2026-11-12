package cli

import (
	"net/http"
	"strings"
	"testing"
)

func expectHelp(t *testing.T, run fleetRun) {
	t.Helper()
	if run.exit != 0 {
		t.Fatalf("exit %d: %s", run.exit, run.stderr)
	}
	if len(run.requests) != 0 {
		t.Fatalf("help sent requests: %+v", run.requests)
	}
}

func TestHelpForACommandShowsUsageFlagsAndExamples(t *testing.T) {
	for _, argv := range [][]string{
		{"help", "gateway", "park"},
		{"gateway", "park", "--help"},
		{"gw", "park", "-h"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			run := fleetWithoutEnvironment(t, argv...)
			expectHelp(t, run)
			expectContains(t, run.stdout, "octogee-fleet --env=<name> gateway park <gateway>")
			expectContains(t, run.stdout, "--apply")
			expectContains(t, run.stdout, "Without it, the Admin API only checks the change")
			expectContains(t, run.stdout, "gateway park shared-b --apply")
		})
	}
}

func TestHelpForANounListsOnlyItsCommands(t *testing.T) {
	for _, argv := range [][]string{{"help", "user"}, {"user"}} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			run := fleetWithoutEnvironment(t, argv...)
			expectHelp(t, run)
			expectContains(t, run.stdout, "user migrate")
			expectContains(t, run.stdout, "user assign")
			if strings.Contains(run.stdout, "gateway ls") {
				t.Fatalf("user help lists gateway commands:\n%s", run.stdout)
			}
		})
	}
}

func TestTheCommandListIsGroupedByNoun(t *testing.T) {
	run := fleetWithoutEnvironment(t, "help")
	expectHelp(t, run)
	order := []string{"Gateways", "gateway ls", "Users", "user ls", "Operations", "ops ls", "Builds and releases", "builds ls"}
	last := -1
	for _, text := range order {
		at := strings.Index(run.stdout, text)
		if at <= last {
			t.Fatalf("expected %q after the previous heading or command in:\n%s", text, run.stdout)
		}
		last = at
	}
}

func TestAnUnknownCommandSuggestsTheNearestOne(t *testing.T) {
	cases := map[string][]string{
		"gateway ls":      {"gateways", "ls"},
		"gateway inspect": {"gateway", "inspct", "shared-a"},
		"user migrate":    {"user", "migrat", "ada", "--to=shared-b"},
		"ops ls":          {"opps", "ls"},
	}
	for want, argv := range cases {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			run := fleet(t, argv...)
			expectNothingSent(t, run)
			expectContains(t, run.stderr, "did you mean `octogee-fleet "+want+"`?")
		})
	}
}

func TestAnUnrelatedCommandPointsAtHelp(t *testing.T) {
	run := fleet(t, "launch", "rockets")
	expectNothingSent(t, run)
	expectContains(t, run.stderr, "octogee-fleet help")
	if strings.Contains(run.stderr, "did you mean") {
		t.Fatalf("suggested a command for nonsense:\n%s", run.stderr)
	}
}

func TestAnUnknownFlagSuggestsTheNearestOne(t *testing.T) {
	run := fleet(t, "gateway", "add", "--id=shared-c", "--shaa="+sha)
	expectNothingSent(t, run)
	expectContains(t, run.stderr, "gateway add does not accept --shaa")
	expectContains(t, run.stderr, "did you mean --sha?")
}

func TestShowIsAnAliasForInspect(t *testing.T) {
	run := fleet(t, "ops", "show", "op-1")
	expectRead(t, run)
	if !run.containsRequest(adminRequest{Method: http.MethodGet, Path: "/api/admin/operations/op-1"}) {
		t.Fatalf("requests %+v", run.requests)
	}
	expectContains(t, run.stdout, "quiesce-source")
}
