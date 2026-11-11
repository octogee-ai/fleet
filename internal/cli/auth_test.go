package cli

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/octogee-ai/octogee-fleet/internal/oidc"
)

func TestTheDefaultClientRefusesToRedirectTheBearerToPlainText(t *testing.T) {
	options := runOptions{defaultHTTP: true, redirectTo: "http://insecure.example/api/admin/gateways"}
	run := fleetWith(t, fixture, options, "gateway", "ls")
	expectRefused(t, run)
	expectContains(t, run.stderr, "plain text")
	if len(run.requests) != 1 {
		t.Fatalf("expected only the first request, saw %+v", run.requests)
	}
}

func TestSigningInSavesThePrivateSessionAndNamesTheOperator(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{session: notSignedIn}, "login")
	if run.exit != 0 {
		t.Fatalf("exit %d: %s", run.exit, run.stderr)
	}
	expectContains(t, run.stdout, "https://issuer.example/activate")
	expectContains(t, run.stdout, "ABCD-EFGH")
	expectContains(t, run.stdout, "signed in to local as ada@example.test")
	if len(run.requests) != 0 {
		t.Fatalf("signing in reached the Admin API: %+v", run.requests)
	}
	info, err := os.Stat(run.sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("session mode %o", got)
	}
	data, err := os.ReadFile(run.sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	expectContains(t, string(data), signedInToken)
	expectContains(t, string(data), `"clientId":"fleet-cli"`)
	if strings.Contains(run.stdout, signedInToken) || strings.Contains(run.stderr, signedInToken) {
		t.Fatal("signing in printed the ID token")
	}
}

func TestSigningOutForgetsTheSession(t *testing.T) {
	run := fleet(t, "logout")
	if run.exit != 0 {
		t.Fatalf("exit %d: %s", run.exit, run.stderr)
	}
	if _, err := os.Stat(run.sessionPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("session still present: %v", err)
	}
}

func TestWithoutASignInNothingIsSentAndTheRemedyIsNamed(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{session: notSignedIn}, "gateway", "ls")
	expectNothingSent(t, run)
	expectContains(t, run.stderr, "octogee-fleet --env=local login")
}

func TestAnExpiredSessionIsRenewedAndTheRenewalIsKept(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{session: expiredSession}, "gateway", "ls")
	expectRead(t, run)
	for _, request := range run.requests {
		if request.authorization != "Bearer "+refreshedToken {
			t.Fatalf("%s %s sent authorization %q", request.Method, request.Path, request.authorization)
		}
	}
	if len(run.issuerForms) != 1 || run.issuerForms[0].Get("refresh_token") != "refresh-1" {
		t.Fatalf("issuer saw %v", run.issuerForms)
	}
	data, err := os.ReadFile(run.sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	expectContains(t, string(data), refreshedToken)
	expectContains(t, string(data), "refresh-2")
	expectContains(t, string(data), `"clientId":"fleet-cli"`)
}

func TestASessionFromAnotherConfigurationIsNeitherSentNorRenewed(t *testing.T) {
	changes := map[string]func(*oidc.Binding){
		"issuer":     func(binding *oidc.Binding) { binding.Issuer = "https://other.example" },
		"client":     func(binding *oidc.Binding) { binding.ClientID = "other-client" },
		"api origin": func(binding *oidc.Binding) { binding.APIOrigin = "https://other.example" },
		"unbound":    func(binding *oidc.Binding) { *binding = oidc.Binding{} },
	}
	states := map[string]sessionState{"valid": signedIn, "expired": expiredSession}
	for changed, alter := range changes {
		for state, session := range states {
			t.Run(changed+" "+state, func(t *testing.T) {
				run := fleetWith(t, fixture, runOptions{session: session, alterBinding: alter}, "gateway", "ls")
				expectNothingSent(t, run)
				if len(run.issuerForms) != 0 {
					t.Fatalf("issuer saw %v", run.issuerForms)
				}
				expectContains(t, run.stderr, "octogee-fleet --env=local login")
			})
		}
	}
}

func TestAValidSessionIsNotRenewed(t *testing.T) {
	run := fleet(t, "gateway", "ls")
	expectRead(t, run)
	if len(run.issuerForms) != 0 {
		t.Fatalf("issuer saw %v", run.issuerForms)
	}
}

func TestARefusedRenewalSendsNothingAndNamesTheRemedy(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{session: expiredSession, refreshFails: true}, "gateway", "park", "shared-a", "--apply")
	expectNothingSent(t, run)
	expectContains(t, run.stderr, "revoked")
	expectContains(t, run.stderr, "octogee-fleet --env=local login")
}

func TestAnExpiredSessionWithoutARefreshTokenAsksForASignIn(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{session: expiredWithoutRefresh}, "gateway", "ls")
	expectNothingSent(t, run)
	expectContains(t, run.stderr, "octogee-fleet --env=local login")
}

func TestATokenCommandSuppliesTheBearerForAutomation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	options := runOptions{session: notSignedIn, tokenCommand: []string{"sh", "-c", "echo token-from-command"}}
	run := fleetWith(t, fixture, options, "gateway", "ls")
	expectRead(t, run)
	if run.requests[0].authorization != "Bearer token-from-command" {
		t.Fatalf("authorization %q", run.requests[0].authorization)
	}
}

func TestSigningInIsRefusedWhenAnEnvironmentUsesATokenCommand(t *testing.T) {
	options := runOptions{session: notSignedIn, tokenCommand: []string{"printer"}}
	run := fleetWith(t, fixture, options, "login")
	expectNothingSent(t, run)
	expectContains(t, run.stderr, "command")
	if len(run.issuerForms) != 0 {
		t.Fatalf("issuer saw %v", run.issuerForms)
	}
}
