package cli

import (
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestUserMutations(t *testing.T) {
	const prefix = "/api/admin/users/"
	cases := []struct {
		argv []string
		want adminRequest
	}{
		{[]string{"user", "assign", "alan", "--to=shared-a"}, adminRequest{Method: http.MethodPost, Path: prefix + "u-alan/assign", Body: map[string]any{"gatewayId": "shared-a"}}},
		{[]string{"user", "migrate", "ada", "--to=shared-b"}, adminRequest{Method: http.MethodPost, Path: prefix + "u-ada/migrate", Body: map[string]any{"to": "shared-b"}}},
		{[]string{"user", "migrate", "ada", "--to=shared-b", "--source-transfer=retained-pod"}, adminRequest{Method: http.MethodPost, Path: prefix + "u-ada/migrate", Body: map[string]any{"to": "shared-b", "sourceTransfer": "retained-pod"}}},
		{[]string{"user", "migrate", "ada", "--to-isolated=standard"}, adminRequest{Method: http.MethodPost, Path: prefix + "u-ada/isolated-gateway", Body: map[string]any{"sizingProfile": "standard"}}},
		{[]string{"user", "unassign", "ada"}, adminRequest{Method: http.MethodPost, Path: prefix + "u-ada/unassign"}},
		{[]string{"user", "replace-vm", "grace"}, adminRequest{Method: http.MethodPost, Path: prefix + "u-grace/vm/replace", Body: map[string]any{}}},
		{[]string{"user", "delete", "alan"}, adminRequest{Method: http.MethodPost, Path: prefix + "u-alan/delete", Body: map[string]any{"eraseNow": false}}},
		{[]string{"user", "delete", "alan", "--erase-now"}, adminRequest{Method: http.MethodPost, Path: prefix + "u-alan/delete", Body: map[string]any{"eraseNow": true}}},
	}
	for _, testCase := range cases {
		t.Run(strings.Join(testCase.argv, " "), func(t *testing.T) {
			expectApplied(t, fleet(t, append(append([]string{}, testCase.argv...), "--apply")...), testCase.want)
		})
	}
}

func TestUserIsNamedByNameOrID(t *testing.T) {
	byName := fleet(t, "user", "unassign", "ada", "--apply").onlyMutation(t)
	byID := fleet(t, "user", "unassign", "u-ada", "--apply").onlyMutation(t)
	if !reflect.DeepEqual(byName, byID) {
		t.Fatalf("by name %+v, by id %+v", byName, byID)
	}
}

func TestUserAssignDoesNothingWhenAlreadyOnTheTarget(t *testing.T) {
	expectRead(t, fleet(t, "user", "assign", "ada", "--to=shared-a", "--apply"))
}

func TestUserAssignPointsAPlacedUserAtMigrate(t *testing.T) {
	run := fleet(t, "user", "assign", "ada", "--to=shared-b", "--apply")
	expectRefused(t, run)
	expectContains(t, run.stderr, "user migrate")
}

func TestUserAssignRefusesAParkedGateway(t *testing.T) {
	expectRefused(t, fleet(t, "user", "assign", "alan", "--to=shared-parked", "--apply"))
}

func TestUserAssignRefusesAnotherUsersIsolatedGateway(t *testing.T) {
	expectRefused(t, fleet(t, "user", "assign", "alan", "--to=iso-grace", "--apply"))
}

func TestUserMigratePointsAnUnplacedUserAtAssign(t *testing.T) {
	run := fleet(t, "user", "migrate", "alan", "--to=shared-a", "--apply")
	expectRefused(t, run)
	expectContains(t, run.stderr, "user assign")
}

func TestUserMigrateRefusesAnUnknownSourceTransferMode(t *testing.T) {
	expectRefused(t, fleet(t, "user", "migrate", "ada", "--to=shared-b", "--source-transfer=copy", "--apply"))
}

func TestUserMigrateDoesNothingWhenAlreadyOnTheTarget(t *testing.T) {
	expectRead(t, fleet(t, "user", "migrate", "ada", "--to=shared-a", "--apply"))
}

func TestUserMigrateToIsolatedIsADryRunUntilApply(t *testing.T) {
	want := adminRequest{Method: http.MethodPost, Path: "/api/admin/users/u-ada/isolated-gateway", Body: map[string]any{"sizingProfile": "standard"}}

	checked := fleet(t, "user", "migrate", "ada", "--to-isolated=standard")
	expectRead(t, checked)
	expectContains(t, checked.stdout, "dry run: the Admin API would")
	dryRun := checked.onlyDryRun(t).adminRequest
	if !reflect.DeepEqual(dryRun.Query, map[string]string{"dryRun": "true"}) {
		t.Fatalf("query %+v", dryRun.Query)
	}
	dryRun.Query = nil
	if !reflect.DeepEqual(dryRun, want) {
		t.Fatalf("checked %+v, want %+v", dryRun, want)
	}

	applied := fleet(t, "user", "migrate", "ada", "--to-isolated=standard", "--apply")
	expectApplied(t, applied, want)
	expectContains(t, applied.stdout, "operation op-recorded")
}

func TestUserMigrateNeedsExactlyOneDestination(t *testing.T) {
	cases := map[string][]string{
		"both destinations":                     {"user", "migrate", "ada", "--to=shared-b", "--to-isolated=standard"},
		"no destination":                        {"user", "migrate", "ada"},
		"a source transfer to an isolated move": {"user", "migrate", "ada", "--to-isolated=standard", "--source-transfer=retained-pod"},
		"an empty sizing profile":               {"user", "migrate", "ada", "--to-isolated="},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			run := fleet(t, append(append([]string{}, argv...), "--apply")...)
			expectNothingSent(t, run)
			expectContains(t, run.stderr, "--to")
		})
	}
}

func TestUserMigrateToIsolatedPointsAnUnplacedUserAtAssign(t *testing.T) {
	run := fleet(t, "user", "migrate", "alan", "--to-isolated=standard", "--apply")
	expectRefused(t, run)
	expectContains(t, run.stderr, "user assign")
}

func TestUserMigrateToIsolatedRefusalExitsNonZero(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{refuseDryRunWith: http.StatusConflict}, "user", "migrate", "ada", "--to-isolated=standard")
	expectRefused(t, run)
	expectContains(t, run.stderr, "would refuse this request (409)")
}

func TestUserReplaceVMRequiresBothBreakGlassFlags(t *testing.T) {
	expectRefused(t, fleet(t, "user", "replace-vm", "grace", "--accept-boot-data-loss", "--apply"))

	full := fleet(t, "user", "replace-vm", "grace", "--force", "--accept-boot-data-loss", "--apply")
	if body := full.onlyMutation(t).Body; !reflect.DeepEqual(body, map[string]any{"force": true, "acceptBootDataLoss": true}) {
		t.Fatalf("body %+v", body)
	}
}

func TestUserListFiltersByGateway(t *testing.T) {
	run := fleet(t, "user", "ls", "--gateway=shared-a")
	expectRead(t, run)
	expectContains(t, run.stdout, "ada")
	if strings.Contains(run.stdout, "grace") {
		t.Fatalf("grace is not on shared-a:\n%s", run.stdout)
	}
}

func TestUserListRefusesAnUnknownGatewayFilter(t *testing.T) {
	expectRefused(t, fleet(t, "user", "ls", "--gateway=nowhere"))
}

var driftWord = regexp.MustCompile(`(?i)drift`)

func TestUserListIsSilentAboutDriftWhenPlacementMatches(t *testing.T) {
	run := fleet(t, "user", "ls")
	expectRead(t, run)
	if driftWord.MatchString(run.stdout) {
		t.Fatalf("expected no drift report:\n%s", run.stdout)
	}
}

func TestUserListReportsDriftInBothDirections(t *testing.T) {
	drifted := fixture
	drifted.gateways = append([]fixtureGateway{}, fixture.gateways...)
	for index, gateway := range drifted.gateways {
		switch gateway.id {
		case "shared-a":
			drifted.gateways[index].mounted = []string{"u-ada", "u-alan"}
		case "iso-grace":
			drifted.gateways[index].mounted = nil
		}
	}
	run := fleetWith(t, drifted, runOptions{}, "user", "ls")
	expectRead(t, run)
	var driftLines []string
	for _, line := range strings.Split(run.stdout, "\n") {
		if driftWord.MatchString(line) {
			driftLines = append(driftLines, line)
		}
	}
	report := strings.Join(driftLines, "\n")
	expectContains(t, report, "alan")
	expectContains(t, report, "grace")
	if len(driftLines) != 2 {
		t.Fatalf("expected one line per drifted user:\n%s", report)
	}
}

func withUser(name string, change func(*fixtureUser)) fleetFixture {
	changed := fixture
	changed.users = append([]fixtureUser{}, fixture.users...)
	for index := range changed.users {
		if changed.users[index].name == name {
			change(&changed.users[index])
		}
	}
	return changed
}

func TestDriftNamesThePlacementTarget(t *testing.T) {
	drifted := withUser("ada", func(u *fixtureUser) { u.target = "shared-b" })
	run := fleetWith(t, drifted, runOptions{}, "user", "ls")
	expectContains(t, run.stdout, "drift: ada is placed on shared-a but the placement target is shared-b")
}

func TestPipedDriftLinesKeepTheirText(t *testing.T) {
	drifted := withUser("ada", func(u *fixtureUser) { u.target = "shared-b" })
	run := fleetWith(t, drifted, runOptions{}, "user", "ls")
	if !strings.HasPrefix(lineWith(t, run.stdout, "placement target"), "drift: ada") {
		t.Fatalf("a piped drift line starts with drift:\n%s", run.stdout)
	}
}

func TestDriftNamesWhereAMountedUserIsPlaced(t *testing.T) {
	drifted := withUser("ada", func(u *fixtureUser) { u.gatewayID, u.target = "shared-b", "shared-b" })
	run := fleetWith(t, drifted, runOptions{}, "user", "ls")
	expectContains(t, run.stdout, "drift: ada is mounted on shared-a but placed on shared-b")
}

func TestDriftNamesAGatewayThatIsGone(t *testing.T) {
	drifted := withUser("ada", func(u *fixtureUser) { u.gatewayID, u.target = "shared-gone", "shared-gone" })
	run := fleetWith(t, drifted, runOptions{}, "user", "inspect", "ada")
	expectContains(t, run.stdout, "drift: ada is placed on shared-gone, which is retired or unknown")
}

func TestUserInspectIsReadOnly(t *testing.T) {
	run := fleet(t, "user", "inspect", "grace")
	expectRead(t, run)
	expectContains(t, run.stdout, "iso-grace")
}

func TestUserInspectShowsTheUsersIsolatedGatewayAndItsVM(t *testing.T) {
	run := fleet(t, "user", "inspect", "grace")
	if strings.HasPrefix(strings.TrimSpace(run.stdout), "{") {
		t.Fatalf("expected fields, not JSON:\n%s", run.stdout)
	}
	row := lineWith(t, run.stdout, "isolated gateway")
	expectContains(t, row, "iso-grace")
	expectContains(t, row, "suspended (idle)")
}

func TestUserInspectOfAUserWithoutAnIsolatedGatewayReadsNoDetails(t *testing.T) {
	run := fleet(t, "user", "inspect", "ada")
	expectRead(t, run)
	expectContains(t, lineWith(t, run.stdout, "gateway"), "shared-a")
	if strings.Contains(run.stdout, "isolated gateway") {
		t.Fatalf("ada has no isolated gateway:\n%s", run.stdout)
	}
	for _, request := range run.requests {
		if strings.HasSuffix(request.Path, "/details") {
			t.Fatalf("read details: %+v", run.requests)
		}
	}
}

func TestUserInspectInJSONCarriesTheIsolatedGateway(t *testing.T) {
	document := decodeJSON(t, fleet(t, "user", "inspect", "grace", "--format=json"))
	gateways := document["isolatedGateways"].([]any)
	if len(gateways) != 1 || gateways[0].(map[string]any)["vm"].(map[string]any)["status"] != "SUSPENDED" {
		t.Fatalf("document %+v", document)
	}
}

func TestOddUserDisplayFieldsNeverBlockAMutation(t *testing.T) {
	odd := func(name string, user map[string]any) {
		if name == "ada" {
			user["email"] = 7
			user["latestOperation"] = "op-1"
			user["policyTargetGatewayId"] = map[string]any{"id": "shared-a"}
		}
	}
	run := fleetWith(t, fixture, runOptions{alterUser: odd}, "user", "migrate", "ada", "--to=shared-b")
	if run.exit != 0 {
		t.Fatalf("exit %d: %s", run.exit, run.stderr)
	}
	run.onlyDryRun(t)
}

func TestUserBrokerIsReadOnly(t *testing.T) {
	run := fleet(t, "user", "broker", "ada")
	expectRead(t, run)
	if !run.containsRequest(adminRequest{Method: http.MethodGet, Path: "/api/admin/users/u-ada/broker-diagnostics"}) {
		t.Fatalf("requests %+v", run.requests)
	}
}

func TestUserBrokerListsUsageWindowsAsATable(t *testing.T) {
	run := fleet(t, "user", "broker", "ada")
	expectContains(t, run.stdout, "REQUESTS")
	row := lineWith(t, run.stdout, "1h ago")
	for _, want := range []string{"3", "12000", "$0.42"} {
		expectContains(t, row, want)
	}
}

func TestUserAddCreatesTheUserThroughOneRequest(t *testing.T) {
	run := fleet(t, "user", "add", "lin", "--email=lin@example.com", "--apply")
	expectApplied(t, run, adminRequest{Method: http.MethodPost, Path: "/api/admin/users", Body: map[string]any{"name": "lin", "email": "lin@example.com"}})
	expectContains(t, run.stdout, "applied: user lin created")
	expectContains(t, run.stdout, "op-recorded")
	expectContains(t, run.stdout, "octogee-fleet --env=local user assign lin --to=<gateway> --apply")
	expectContains(t, run.stdout, "octogee-fleet --env=local user issue-passwords lin --apply")
}

func TestUserAddRequiresAnEmail(t *testing.T) {
	expectNothingSent(t, fleet(t, "user", "add", "lin", "--apply"))
}

func TestUserAddRefusesATakenName(t *testing.T) {
	expectRefused(t, fleet(t, "user", "add", "ada", "--email=ada@example.com", "--apply"))
}

func TestIssuePasswordsPrintsBothOnce(t *testing.T) {
	run := fleet(t, "user", "issue-passwords", "ada", "--apply")
	expectApplied(t, run, adminRequest{Method: http.MethodPost, Path: "/api/admin/users/u-ada/one-time-passwords"})
	expectContains(t, run.stdout, "operation op-passwords")
	for _, secret := range []string{"chat-secret", "git-secret"} {
		if got := strings.Count(run.stdout, secret); got != 1 {
			t.Fatalf("%s appeared %d times in:\n%s", secret, got, run.stdout)
		}
	}
	expectContains(t, run.stdout, "ws-ada")
}

func TestIssuePasswordsDryRunShowsNoPasswords(t *testing.T) {
	run := fleet(t, "user", "issue-passwords", "ada")
	expectRead(t, run)
	if strings.Contains(run.stdout, "secret") {
		t.Fatalf("dry run showed a password:\n%s", run.stdout)
	}
}

func TestIssuePasswordsRefusesAUserStillProvisioning(t *testing.T) {
	custom := fixture
	custom.users = []fixtureUser{{id: "u-new", name: "new", provisioning: "in_progress"}}
	expectRefused(t, fleetWith(t, custom, runOptions{}, "user", "issue-passwords", "new", "--apply"))
}

func TestIssuePasswordsFailsWithoutPrintingAnUnreadableReply(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{omitPasswords: true}, "user", "issue-passwords", "ada", "--apply")
	if run.exit == 0 {
		t.Fatalf("expected a failure, got:\n%s", run.stdout)
	}
	expectContains(t, run.stderr, "run this command again")
}
