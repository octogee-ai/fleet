package cli

import (
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestGatewayMutations(t *testing.T) {
	const prefix = "/api/admin/gateways/"
	cases := []struct {
		argv []string
		want adminRequest
	}{
		{[]string{"gateway", "deploy", "shared-a", "--sha=" + sha}, adminRequest{Method: http.MethodPost, Path: prefix + "shared-a/deploy", Body: map[string]any{"sourceSha": sha}}},
		{[]string{"gateway", "restart", "shared-a"}, adminRequest{Method: http.MethodPost, Path: prefix + "shared-a/restart"}},
		{[]string{"gateway", "park", "shared-a"}, adminRequest{Method: http.MethodPatch, Path: prefix + "shared-a", Body: map[string]any{"desiredStatus": "paused"}}},
		{[]string{"gateway", "resume", "shared-parked"}, adminRequest{Method: http.MethodPatch, Path: prefix + "shared-parked", Body: map[string]any{"desiredStatus": "running"}}},
		{[]string{"gateway", "retire", "shared-b"}, adminRequest{Method: http.MethodDelete, Path: prefix + "shared-b"}},
		{[]string{"gateway", "retire", "shared-b", "--retain-data", "--retain-service-account"}, adminRequest{Method: http.MethodDelete, Path: prefix + "shared-b", Query: map[string]string{"retainData": "true", "retainServiceAccount": "true"}}},
		{[]string{"gateway", "rollback", "shared-a", "--release=7"}, adminRequest{Method: http.MethodPost, Path: prefix + "shared-a/releases/7/rollback"}},
		{[]string{"gateway", "replace-vm", "iso-grace"}, adminRequest{Method: http.MethodPost, Path: prefix + "iso-grace/vm/replace", Body: map[string]any{}}},
	}
	for _, testCase := range cases {
		t.Run(strings.Join(testCase.argv, " "), func(t *testing.T) {
			expectApplied(t, fleet(t, append(append([]string{}, testCase.argv...), "--apply")...), testCase.want)
		})
	}
}

func TestGatewayAddNamesTheBuildBySha(t *testing.T) {
	expectApplied(t, fleet(t, "gateway", "add", "--id=shared-c", "--sha="+sha, "--apply"), adminRequest{
		Method: http.MethodPost, Path: "/api/admin/gateways",
		Body: map[string]any{"gatewayId": "shared-c", "sourceSha": sha},
	})
}

func TestGatewayAddWithoutAShaLeavesTheBuildToThePlatform(t *testing.T) {
	expectApplied(t, fleet(t, "gateway", "add", "--id=shared-c", "--apply"), adminRequest{
		Method: http.MethodPost, Path: "/api/admin/gateways",
		Body: map[string]any{"gatewayId": "shared-c"},
	})
}

func TestGatewayAddSendsNoImageFields(t *testing.T) {
	for _, argv := range [][]string{
		{"gateway", "add", "--id=shared-c"},
		{"gateway", "add", "--id=shared-c", "--sha=" + sha},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			for name := range fleet(t, append(argv, "--apply")...).onlyMutation(t).Body {
				if strings.Contains(strings.ToLower(name), "image") {
					t.Fatalf("sent %q", name)
				}
			}
		})
	}
}

func TestGatewayAddNoLongerTakesAnImageTag(t *testing.T) {
	run := fleet(t, "gateway", "add", "--id=shared-c", "--image-tag=v42", "--apply")
	expectNothingSent(t, run)
	expectContains(t, run.stderr, "does not accept --image-tag")
}

func TestGatewayAddRefusals(t *testing.T) {
	cases := map[string][]string{
		"an id that already exists":  {"gateway", "add", "--id=shared-a", "--apply"},
		"an id that is not DNS safe": {"gateway", "add", "--id=Not_Safe", "--apply"},
		"an isolated gateway":        {"gateway", "add", "--id=c", "--class=isolated", "--apply"},
		"an owner":                   {"gateway", "add", "--id=c", "--owner=alan", "--apply"},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			expectRefused(t, fleet(t, argv...))
		})
	}
}

func TestGatewayRefusesAnythingButAFullLowercaseSha(t *testing.T) {
	for _, argv := range [][]string{
		{"gateway", "deploy", "shared-a", "--sha=main"},
		{"gateway", "deploy", "shared-a", "--sha=" + strings.ToUpper(sha)},
		{"gateway", "deploy", "shared-a", "--sha=" + sha[:12]},
		{"gateway", "add", "--id=shared-c", "--sha=main"},
		{"gateway", "add", "--id=shared-c", "--sha=" + strings.ToUpper(sha)},
		{"gateway", "add", "--id=shared-c", "--sha=" + sha[:12]},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			expectRefused(t, fleet(t, append(argv, "--apply")...))
		})
	}
}

func TestGatewayRollbackRefusesAReleaseThatIsNotAPositiveInteger(t *testing.T) {
	for _, release := range []string{"latest", "0", "-1"} {
		t.Run(release, func(t *testing.T) {
			expectRefused(t, fleet(t, "gateway", "rollback", "shared-a", "--release="+release, "--apply"))
		})
	}
}

func TestGatewayReplaceVMRequiresBothBreakGlassFlags(t *testing.T) {
	expectRefused(t, fleet(t, "gateway", "replace-vm", "iso-grace", "--force", "--apply"))

	full := fleet(t, "gateway", "replace-vm", "iso-grace", "--force", "--accept-boot-data-loss", "--apply")
	if body := full.onlyMutation(t).Body; !reflect.DeepEqual(body, map[string]any{"force": true, "acceptBootDataLoss": true}) {
		t.Fatalf("body %+v", body)
	}
	if !regexp.MustCompile(`(?i)boot disk`).MatchString(full.stdout) {
		t.Fatalf("expected a boot-disk warning in:\n%s", full.stdout)
	}
}

func TestGatewayRestartRefusesExtraPositionalArguments(t *testing.T) {
	expectNothingSent(t, fleet(t, "gateway", "restart", "shared-a", "shared-b", "--apply"))
}

func TestGatewayRestartHasNoForceMode(t *testing.T) {
	expectRefused(t, fleet(t, "gateway", "restart", "iso-grace", "--force", "--apply"))
	expectRefused(t, fleet(t, "gateway", "restart", "iso-grace", "--resolve-force=audit-1", "--apply"))
}

func TestGatewayListIsReadOnlyAndNamesEveryGateway(t *testing.T) {
	run := fleet(t, "gateway", "ls")
	expectRead(t, run)
	for _, id := range []string{"shared-a", "shared-b", "iso-grace", "shared-parked"} {
		expectContains(t, run.stdout, id)
	}
}

func TestGatewayListAllAsksForRetiredGateways(t *testing.T) {
	run := fleet(t, "gateway", "ls", "--all")
	expectRead(t, run)
	if !run.containsRequest(adminRequest{Method: http.MethodGet, Path: "/api/admin/gateways", Query: map[string]string{"all": "true"}}) {
		t.Fatalf("requests %+v", run.requests)
	}
}

func TestGatewayInspectIsReadOnly(t *testing.T) {
	run := fleet(t, "gateway", "inspect", "iso-grace")
	expectRead(t, run)
	expectContains(t, run.stdout, "iso-grace")
}

func TestGatewayInspectShowsPodsAndLastError(t *testing.T) {
	run := fleet(t, "gateway", "inspect", "iso-grace")
	expectRead(t, run)
	expectContains(t, run.stdout, "pod-iso-grace")
	expectContains(t, run.stdout, "ensure failed on iso-grace")
}

func TestGatewayListShowsTheVMStateOfIsolatedGateways(t *testing.T) {
	run := fleet(t, "gateway", "ls")
	expectRead(t, run)
	expectContains(t, run.stdout, "VM")
	expectContains(t, lineWith(t, run.stdout, "iso-grace"), "suspended (idle)")
	if strings.Contains(lineWith(t, run.stdout, "shared-a"), "suspended") {
		t.Fatalf("a shared gateway has no VM:\n%s", run.stdout)
	}
	if !run.containsRequest(adminRequest{Method: http.MethodGet, Path: "/api/admin/gateways/iso-grace/details"}) {
		t.Fatalf("requests %+v", run.requests)
	}
	if run.containsRequest(adminRequest{Method: http.MethodGet, Path: "/api/admin/gateways/shared-a/details"}) {
		t.Fatalf("read details of a shared gateway: %+v", run.requests)
	}
}

func TestGatewayListSaysWhenAVMStateCouldNotBeRead(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{detailsFailWith: http.StatusInternalServerError}, "gateway", "ls")
	if run.exit != 0 {
		t.Fatalf("exit %d: %s", run.exit, run.stderr)
	}
	expectContains(t, lineWith(t, run.stdout, "iso-grace"), "unknown")
	expectContains(t, run.stderr, "iso-grace")
	expectContains(t, run.stderr, "details are unavailable")
}

func TestGatewayInspectShowsTheGatewayAsFields(t *testing.T) {
	run := fleet(t, "gateway", "inspect", "iso-grace")
	if strings.HasPrefix(strings.TrimSpace(run.stdout), "{") {
		t.Fatalf("expected fields, not JSON:\n%s", run.stdout)
	}
	expectContains(t, lineWith(t, run.stdout, "vm"), "suspended (idle)")
	expectContains(t, lineWith(t, run.stdout, "owner"), "u-grace")
	expectContains(t, lineWith(t, run.stdout, "grace@example.test"), "grace")
	expectContains(t, lineWith(t, run.stdout, "op-9"), "restart-gateway")
}

func firstPod(summary map[string]any) map[string]any {
	return summary["podDiagnostics"].(map[string]any)["pods"].([]map[string]any)[0]
}

func TestGatewayInspectReadsPodReadinessFromItsContainers(t *testing.T) {
	run := fleet(t, "gateway", "inspect", "iso-grace")
	row := lineWith(t, run.stdout, "pod-iso-grace")
	expectContains(t, row, "ready 2/2")
	if strings.Contains(row, "not ready") {
		t.Fatalf("a running pod with every container ready is ready:\n%s", row)
	}
}

func TestAPodWithAContainerThatIsNotReadyIsNotReady(t *testing.T) {
	alter := func(_ string, summary map[string]any) { firstPod(summary)["readyContainers"] = 1 }
	run := fleetWith(t, fixture, runOptions{alterSummary: alter}, "gateway", "inspect", "iso-grace")
	expectContains(t, lineWith(t, run.stdout, "pod-iso-grace"), "not ready 1/2")
}

func TestAPodThatIsNotRunningIsNotReadyAndNamesItsPhase(t *testing.T) {
	alter := func(_ string, summary map[string]any) { firstPod(summary)["phase"] = "Pending" }
	run := fleetWith(t, fixture, runOptions{alterSummary: alter}, "gateway", "inspect", "iso-grace")
	row := lineWith(t, run.stdout, "pod-iso-grace")
	expectContains(t, row, "not ready 2/2")
	expectContains(t, row, "Pending")
}

func oddDisplayFields(id string, summary map[string]any) {
	if id != "shared-a" {
		return
	}
	summary["lastError"] = map[string]any{"message": 42}
	summary["podDiagnostics"] = map[string]any{"pods": "unavailable"}
	summary["mountedUsers"] = "two"
	summary["status"] = 7
	summary["openclawVersion"] = map[string]any{"major": 2026}
}

func TestOddDisplayFieldsNeverBlockAMutation(t *testing.T) {
	for _, argv := range [][]string{
		{"gateway", "park", "shared-a"},
		{"gateway", "restart", "shared-a"},
		{"gateway", "deploy", "shared-a", "--sha=" + strings.Repeat("a", 40)},
	} {
		run := fleetWith(t, fixture, runOptions{alterSummary: oddDisplayFields}, argv...)
		if run.exit != 0 {
			t.Fatalf("%v: exit %d: %s", argv, run.exit, run.stderr)
		}
		run.onlyDryRun(t)
	}
}

func TestOddDisplayFieldsAreShownAsSent(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{alterSummary: oddDisplayFields}, "gateway", "inspect", "shared-a")
	if run.exit != 0 {
		t.Fatalf("exit %d: %s", run.exit, run.stderr)
	}
	expectContains(t, lineWith(t, run.stdout, "last error"), "42")
	expectContains(t, lineWith(t, run.stdout, "pods"), "unavailable")
	listed := fleetWith(t, fixture, runOptions{alterSummary: oddDisplayFields}, "gateway", "ls")
	expectContains(t, lineWith(t, listed.stdout, "shared-a"), "7")
}

func TestGatewayInspectOfAnUnknownGatewaySendsNoDetailsRead(t *testing.T) {
	run := fleet(t, "gateway", "inspect", "nowhere")
	expectRefused(t, run)
	expectContains(t, run.stderr, `unknown gateway "nowhere"`)
}

func TestGatewayReleasesIsReadOnly(t *testing.T) {
	expectRead(t, fleet(t, "gateway", "releases", "shared-a"))
}

func TestGwIsShortForGateway(t *testing.T) {
	expectApplied(t, fleet(t, "gw", "park", "shared-a", "--apply"), adminRequest{Method: http.MethodPatch, Path: "/api/admin/gateways/shared-a", Body: map[string]any{"desiredStatus": "paused"}})
}
