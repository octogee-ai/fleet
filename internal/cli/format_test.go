package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

var dataCommands = [][]string{
	{"gateway", "ls"},
	{"gateway", "inspect", "iso-grace"},
	{"gateway", "releases", "shared-a"},
	{"user", "ls"},
	{"user", "inspect", "grace"},
	{"user", "broker", "ada"},
	{"ops", "ls"},
	{"ops", "inspect", "op-1"},
	{"builds", "ls"},
	{"release", "status"},
	{"gateway", "restart", "shared-a"},
	{"gateway", "restart", "shared-a", "--offline"},
	{"gateway", "park", "shared-a", "--apply"},
	{"user", "assign", "ada", "--to=shared-a"},
}

func decodeJSON(t *testing.T, run fleetRun) map[string]any {
	t.Helper()
	if run.exit != 0 {
		t.Fatalf("exit %d: %s", run.exit, run.stderr)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(run.stdout), &document); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, run.stdout)
	}
	return document
}

func TestEveryCommandThatPrintsDataPrintsJSONWithoutDecoration(t *testing.T) {
	for _, argv := range dataCommands {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			run := fleetWith(t, fixture, forcedColor, append(append([]string{}, argv...), "--format=json")...)
			decodeJSON(t, run)
			for _, decoration := range []string{escape, "✓", "●", "→"} {
				if strings.Contains(run.stdout, decoration) {
					t.Fatalf("JSON carries %q:\n%s", decoration, run.stdout)
				}
			}
		})
	}
}

func TestADryRunInJSONCarriesTheRequestAndThePlan(t *testing.T) {
	document := decodeJSON(t, fleet(t, "gateway", "restart", "shared-a", "--format=json"))
	if document["dryRun"] != true || document["checked"] != true {
		t.Fatalf("document %+v", document)
	}
	request := document["request"].(map[string]any)
	if request["method"] != "POST" || request["path"] != "/api/admin/gateways/shared-a/restart" {
		t.Fatalf("request %+v", request)
	}
	if plans := document["plans"].([]any); len(plans) != 1 {
		t.Fatalf("plans %+v", plans)
	}
}

func TestAnAppliedChangeInJSONCarriesTheAdminAPIResponse(t *testing.T) {
	run := fleet(t, "gateway", "restart", "shared-a", "--apply", "--format=json")
	document := decodeJSON(t, run)
	if document["dryRun"] != false || document["response"].(map[string]any)["operationId"] != "op-recorded" {
		t.Fatalf("document %+v", document)
	}
	if len(run.mutations()) != 1 {
		t.Fatalf("mutations %+v", run.mutations())
	}
}

func TestJSONKeepsAbsoluteTimes(t *testing.T) {
	run := fleet(t, "gateway", "ls", "--format=json")
	decodeJSON(t, run)
	expectContains(t, run.stdout, "2026-11-10T00:02:00Z")
}

func TestABreakGlassWarningGoesToStderrInJSON(t *testing.T) {
	run := fleet(t, "gateway", "replace-vm", "iso-grace", "--force", "--accept-boot-data-loss", "--format=json")
	decodeJSON(t, run)
	expectContains(t, run.stderr, "BREAK GLASS")
}

func TestAnUnknownFormatIsRefused(t *testing.T) {
	run := fleet(t, "gateway", "ls", "--format=yaml")
	expectNothingSent(t, run)
	expectContains(t, run.stderr, "--format must be text or json")
}

func TestSignInTakesNoFormat(t *testing.T) {
	expectNothingSent(t, fleet(t, "logout", "--format=json"))
}
