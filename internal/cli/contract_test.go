package cli

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

var dryRun = regexp.MustCompile(`(?i)dry run`)

var mutatingCommands = [][]string{
	{"gateway", "deploy", "shared-a", "--sha=" + sha},
	{"gateway", "park", "shared-a"},
	{"user", "assign", "alan", "--to=shared-b"},
	{"user", "migrate", "ada", "--to=shared-b"},
	{"user", "add", "lin", "--email=lin@example.com"},
	{"user", "issue-passwords", "ada"},
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
	expectContains(t, run.stdout, "usage: octogee-fleet --env=<name>")
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

func TestARejectedSignInStopsBeforeAnyMutation(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			run := fleetWith(t, fixture, runOptions{rejectWith: status}, "gateway", "park", "shared-a", "--apply")
			expectRefused(t, run)
			expectContains(t, run.stderr, "rejected this sign-in")
		})
	}
}

func TestAnAppliedMutationPrintsItsOperationID(t *testing.T) {
	run := fleet(t, "gateway", "park", "shared-a", "--apply")
	expectContains(t, run.stdout, "op-recorded")
}
