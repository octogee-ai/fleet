package cli

import (
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

var dryRun = regexp.MustCompile(`(?i)dry run`)

var mutatingCommands = [][]string{
	{"gateway", "deploy", "shared-a", "--sha=" + sha},
	{"gateway", "park", "shared-a"},
	{"gateway", "add", "--id=shared-c"},
	{"gateway", "add", "--id=shared-c", "--sha=" + sha},
	{"user", "assign", "alan", "--to=shared-b"},
	{"user", "migrate", "ada", "--to=shared-b"},
	{"user", "add", "lin", "--email=lin@example.com"},
	{"user", "issue-passwords", "ada"},
	{"sizing", "set", "team", "--file=testdata/shared-sizing-profile.json"},
}

func TestMutatingCommandsAreDryRunWithoutApply(t *testing.T) {
	for _, argv := range mutatingCommands {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			run := fleet(t, argv...)
			expectRead(t, run)
			if !dryRun.MatchString(run.stdout) {
				t.Fatalf("expected a dry-run notice in:\n%s", run.stdout)
			}
		})
	}
}

func TestADryRunSendsTheSameRequestMarkedAsADryRun(t *testing.T) {
	for _, argv := range mutatingCommands {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			applied := fleet(t, append(append([]string{}, argv...), "--apply")...).onlyMutation(t)
			checked := fleet(t, argv...).onlyDryRun(t).adminRequest
			if len(checked.Query) != 1 {
				t.Fatalf("query %+v", checked.Query)
			}
			checked.Query = nil
			if !reflect.DeepEqual(checked, applied) {
				t.Fatalf("checked %+v, applied %+v", checked, applied)
			}
		})
	}
}

func TestADryRunPrintsWhatTheAdminAPIWouldDo(t *testing.T) {
	run := fleet(t, "gateway", "restart", "shared-a")
	expectRead(t, run)
	expectContains(t, run.stdout, "POST /api/admin/gateways/shared-a/restart\n")
	expectContains(t, run.stdout, "dry run: the Admin API would change the fleet")
	expectContains(t, run.stdout, "target: path=/gateways/shared-a/restart")
	expectContains(t, run.stdout, "current state: desiredRelease=3 desiredStatus=running")
	expectContains(t, run.stdout, "dry run, nothing changed; add --apply to do it")
}

func TestADryRunKeepsTheRequestsOwnQuery(t *testing.T) {
	checked := fleet(t, "gateway", "retire", "shared-b", "--retain-data").onlyDryRun(t)
	want := map[string]string{"retainData": "true", "dryRun": "true"}
	if !reflect.DeepEqual(checked.Query, want) {
		t.Fatalf("query %+v, want %+v", checked.Query, want)
	}
}

func TestADryRunDoesNotSpendTheIdempotencyKey(t *testing.T) {
	checked := fleet(t, "gateway", "restart", "shared-a", "--idempotency-key=restart-1").onlyDryRun(t)
	if checked.idempotencyKey != "" {
		t.Fatalf("the dry run carried idempotency key %q", checked.idempotencyKey)
	}
}

func TestADryRunTheAdminAPIRefusesExitsNonZero(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{refuseDryRunWith: http.StatusConflict}, "gateway", "restart", "shared-a")
	expectRefused(t, run)
	expectContains(t, run.stderr, "would refuse this request (409): gateway has a current retirement claim")
}

func TestNoDryRunIsSentToAnAdminAPIThatDoesNotAnswerThem(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{withoutDryRuns: true}, "gateway", "restart", "shared-a")
	expectRead(t, run)
	if sent := run.dryRuns(); len(sent) != 0 {
		t.Fatalf("sent a dry run to a server that would apply it: %+v", sent)
	}
	expectContains(t, run.stdout, "does not answer dry runs")
	expectContains(t, run.stdout, "add --apply to do it")
}

func TestAnAnswerThatIsNotAPlanFailsLoudly(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{appliesDryRuns: true}, "gateway", "restart", "shared-a")
	if run.exit == 0 {
		t.Fatalf("expected a non-zero exit: stdout=%s", run.stdout)
	}
	expectContains(t, run.stderr, "may have been applied")
	expectContains(t, run.stderr, "op-recorded")
}

func TestADryRunAnswerWithoutAPlanFailsLoudly(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{plansNothing: true}, "gateway", "restart", "shared-a")
	expectRefused(t, run)
	expectContains(t, run.stderr, "without saying what it would do")
}

func TestADryRunOfAForcedRequestSaysSo(t *testing.T) {
	run := fleet(t, "gateway", "replace-vm", "iso-grace", "--force", "--accept-boot-data-loss")
	expectRead(t, run)
	expectContains(t, run.stdout, "BREAK GLASS")
	expectContains(t, run.stdout, "forced: yes")
}

func TestOfflinePrintsTheRequestWithoutAskingTheAdminAPI(t *testing.T) {
	run := fleet(t, "gateway", "restart", "shared-a", "--offline")
	expectRead(t, run)
	if sent := run.dryRuns(); len(sent) != 0 {
		t.Fatalf("--offline sent a dry run: %+v", sent)
	}
	expectContains(t, run.stdout, "POST /api/admin/gateways/shared-a/restart\n")
	expectContains(t, run.stdout, "dry run, nothing sent; add --apply to do it")
}

func TestOfflineCannotBeCombinedWithApply(t *testing.T) {
	expectNothingSent(t, fleet(t, "gateway", "restart", "shared-a", "--offline", "--apply"))
}

func TestApplySendsNoDryRun(t *testing.T) {
	run := fleet(t, "gateway", "restart", "shared-a", "--apply")
	if sent := run.dryRuns(); len(sent) != 0 {
		t.Fatalf("--apply also sent a dry run: %+v", sent)
	}
}

func TestMutatingCommandsSendExactlyOneMutationWithApply(t *testing.T) {
	for _, argv := range mutatingCommands {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			run := fleet(t, append(append([]string{}, argv...), "--apply")...)
			if run.exit != 0 || len(run.mutations()) != 1 {
				t.Fatalf("exit=%d mutations=%d stderr=%s", run.exit, len(run.mutations()), run.stderr)
			}
		})
	}
}

func TestIdempotencyKeyBecomesTheRequestHeader(t *testing.T) {
	run := fleet(t, "gateway", "deploy", "shared-a", "--sha="+sha, "--idempotency-key=deploy-1", "--apply")
	if got := run.mutations()[0].idempotencyKey; got != "deploy-1" {
		t.Fatalf("idempotency key %q", got)
	}
}

func TestMalformedCommandLinesSendNothing(t *testing.T) {
	cases := map[string][]string{
		"an empty idempotency key": {"gateway", "deploy", "shared-a", "--sha=" + sha, "--idempotency-key=", "--apply"},
		"an unknown flag":          {"gateway", "park", "shared-a", "--color=blue", "--apply"},
		"a repeated flag":          {"user", "assign", "alan", "--to=shared-a", "--to=shared-b", "--apply"},
		"--apply with a value":     {"gateway", "park", "shared-a", "--apply=yes"},
		"an unknown command":       {"gateways", "list"},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			expectNothingSent(t, fleet(t, argv...))
		})
	}
}

func TestUnknownNamesAreRefusedBeforeAnyMutation(t *testing.T) {
	cases := map[string][]string{
		"an unknown user":    {"user", "assign", "nobody", "--to=shared-a", "--apply"},
		"an unknown gateway": {"user", "assign", "alan", "--to=nowhere", "--apply"},
		"an unknown target":  {"gateway", "park", "nowhere", "--apply"},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			expectRefused(t, fleet(t, argv...))
		})
	}
}

func TestTheEnvironmentIsRequired(t *testing.T) {
	expectNothingSent(t, fleetWithoutEnvironment(t, "gateway", "ls"))
}

func TestTheEnvironmentMustComeFromTheOperatorsConfiguration(t *testing.T) {
	expectNothingSent(t, fleetWithoutEnvironment(t, "--env=elsewhere", "gateway", "ls"))
}

func TestHelpPrintsTheCommandList(t *testing.T) {
	run := fleetWithoutEnvironment(t, "help")
	if run.exit != 0 {
		t.Fatalf("exit %d", run.exit)
	}
	expectContains(t, run.stdout, "octogee-fleet --env=<name> <command>")
	expectContains(t, run.stdout, "gateway")
	expectContains(t, run.stdout, "user")
}

func TestNoCommandPrintsTheListAndFails(t *testing.T) {
	run := fleetWithoutEnvironment(t)
	if run.exit == 0 {
		t.Fatal("expected a non-zero exit")
	}
	expectContains(t, run.stdout, "gateway")
}

func TestHelpForOneCommandShowsItsArguments(t *testing.T) {
	run := fleetWithoutEnvironment(t, "help", "gateway", "deploy")
	if run.exit != 0 {
		t.Fatalf("exit %d", run.exit)
	}
	expectContains(t, run.stdout, "--sha=<full-sha>")
}

func TestEveryRequestCarriesTheSignedInIDToken(t *testing.T) {
	run := fleet(t, "user", "assign", "alan", "--to=shared-a", "--apply")
	if len(run.requests) < 2 {
		t.Fatalf("expected reads and a mutation, saw %d", len(run.requests))
	}
	for _, request := range run.requests {
		if request.authorization != "Bearer "+signedInToken {
			t.Fatalf("%s %s sent authorization %q", request.Method, request.Path, request.authorization)
		}
	}
}

func TestTheCommandLineHasNoDependenciesOutsideTheStandardLibrary(t *testing.T) {
	data, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "require") {
		t.Fatalf("go.mod must require nothing:\n%s", data)
	}
}

func TestARejectedSignInStopsBeforeAnyMutationAndNamesTheRemedy(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{rejectWith: http.StatusUnauthorized}, "gateway", "park", "shared-a", "--apply")
	expectRefused(t, run)
	expectContains(t, run.stderr, "rejected this sign-in (401)")
	expectContains(t, run.stderr, "octogee-fleet --env=local login")
}

func TestAForbiddenRequestIsNotPermittedRatherThanARejectedSignIn(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{rejectWith: http.StatusForbidden, errorCode: "tenant-enforcement"}, "gateway", "park", "shared-a", "--apply")
	expectRefused(t, run)
	expectContains(t, run.stderr, "not permitted (403 tenant-enforcement): token rejected")
	if strings.Contains(run.stderr, "sign-in (") {
		t.Fatalf("a 403 was reported as a rejected sign-in:\n%s", run.stderr)
	}
}

func TestARefusalNamesTheServersCode(t *testing.T) {
	dryRun := fleetWith(t, fixture, runOptions{refuseDryRunWith: http.StatusConflict, errorCode: "user-has-isolated-gateway"}, "user", "migrate", "ada", "--to-isolated=standard")
	expectRefused(t, dryRun)
	expectContains(t, dryRun.stderr, "would refuse this request (409 user-has-isolated-gateway): gateway has a current retirement claim")

	applied := fleetWith(t, fixture, runOptions{refuseApplyWith: http.StatusConflict, errorCode: "migration-in-flight"}, "gateway", "restart", "shared-a", "--apply")
	if applied.exit == 0 {
		t.Fatalf("expected a non-zero exit:\n%s", applied.stdout)
	}
	expectContains(t, applied.stderr, "refused this request (409 migration-in-flight): a migration is in flight for this gateway")
}

func TestAnAppliedMutationPrintsItsOperationIDAndHowToFollowIt(t *testing.T) {
	run := fleet(t, "gateway", "restart", "shared-a", "--apply")
	expectContains(t, run.stdout, "applied: restart of shared-a queued")
	expectContains(t, run.stdout, "operation op-recorded")
	expectContains(t, run.stdout, "octogee-fleet --env=local ops inspect op-recorded")
}

func TestAnAppliedMutationWithoutAnOperationIDPointsAtTheJournal(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{omitOperationIDs: true}, "gateway", "park", "shared-a", "--apply")
	expectContains(t, run.stdout, "applied: shared-a set to paused")
	expectContains(t, run.stdout, "returned no operation id")
	expectContains(t, run.stdout, "octogee-fleet --env=local ops ls")
}

func TestEveryAppliedChangeSaysWhatChangedInsteadOfPrintingTheResponse(t *testing.T) {
	cases := []struct {
		argv []string
		want []string
	}{
		{[]string{"gateway", "park", "shared-a"}, []string{"applied: shared-a set to paused", "operation audit-patch", "octogee-fleet --env=local ops inspect audit-patch"}},
		{[]string{"gateway", "resume", "shared-parked"}, []string{"applied: shared-parked set to running", "operation audit-patch"}},
		{[]string{"user", "assign", "alan", "--to=shared-b"}, []string{"applied: alan assigned to shared-b", "operation audit-assign", "ops inspect audit-assign"}},
		{[]string{"user", "unassign", "ada"}, []string{"applied: ada unassigned from shared-a", "operation audit-unassign", "ops inspect audit-unassign"}},
		{[]string{"user", "delete", "alan"}, []string{"applied: alan scheduled for deletion", "suspension operation op-suspend", "erasure operation op-erase", "ops inspect op-erase"}},
	}
	for _, testCase := range cases {
		t.Run(strings.Join(testCase.argv, " "), func(t *testing.T) {
			run := fleet(t, append(append([]string{}, testCase.argv...), "--apply")...)
			if run.exit != 0 {
				t.Fatalf("exit %d: %s", run.exit, run.stderr)
			}
			for _, want := range testCase.want {
				expectContains(t, run.stdout, want)
			}
			for _, raw := range []string{`"updated"`, `"gatewayProfile"`, `"unassigned"`, `"deletionId"`} {
				if strings.Contains(run.stdout, raw) {
					t.Fatalf("printed the raw response:\n%s", run.stdout)
				}
			}
		})
	}
}
