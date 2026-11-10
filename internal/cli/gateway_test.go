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
		{[]string{"gateway", "recover-source", "shared-a", "--sha=" + sha}, adminRequest{Method: http.MethodPost, Path: prefix + "shared-a/recover-source", Body: map[string]any{"sourceSha": sha}}},
		{[]string{"gateway", "recover-source", "shared-a", "--sha=" + sha, "--supersede-recovery=rec-1"}, adminRequest{Method: http.MethodPost, Path: prefix + "shared-a/recover-source", Body: map[string]any{"sourceSha": sha, "supersedeRecoveryId": "rec-1"}}},
		{[]string{"gateway", "recover-restore", "shared-a", "--sha=" + sha, "--operation=op-1"}, adminRequest{Method: http.MethodPost, Path: prefix + "shared-a/recover-restore", Body: map[string]any{"sourceSha": sha, "operationId": "op-1"}}},
		{[]string{"gateway", "recover-restore", "shared-a", "--sha=" + sha, "--operation=op-1", "--reauthorize-attempt=a-1", "--supersede-recovery=rec-1"}, adminRequest{Method: http.MethodPost, Path: prefix + "shared-a/recover-restore", Body: map[string]any{"sourceSha": sha, "operationId": "op-1", "reauthorizeAttemptId": "a-1", "supersedeRecoveryId": "rec-1"}}},
		{[]string{"gateway", "replace-vm", "iso-grace"}, adminRequest{Method: http.MethodPost, Path: prefix + "iso-grace/vm/replace", Body: map[string]any{}}},
	}
	for _, testCase := range cases {
		t.Run(strings.Join(testCase.argv, " "), func(t *testing.T) {
			expectApplied(t, fleet(t, append(append([]string{}, testCase.argv...), "--apply")...), testCase.want)
		})
	}
}

func TestGatewayAddTakesTheImageRepositoryFromTheEnvironment(t *testing.T) {
	expectApplied(t, fleet(t, "gateway", "add", "--id=shared-c", "--image-tag=v42", "--apply"), adminRequest{
		Method: http.MethodPost, Path: "/api/admin/gateways",
		Body: map[string]any{"gatewayId": "shared-c", "imageTag": "v42", "gatewayClass": "shared", "imageRepository": "registry.example/gateway"},
	})
}

func TestGatewayAddRefusals(t *testing.T) {
	cases := map[string][]string{
		"an id that already exists":  {"gateway", "add", "--id=shared-a", "--image-tag=v42", "--apply"},
		"an id that is not DNS safe": {"gateway", "add", "--id=Not_Safe", "--image-tag=v42", "--apply"},
		"an isolated gateway":        {"gateway", "add", "--id=c", "--image-tag=v42", "--class=isolated", "--apply"},
		"an owner":                   {"gateway", "add", "--id=c", "--image-tag=v42", "--owner=alan", "--apply"},
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
		{"gateway", "recover-source", "shared-a", "--sha=abc"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			expectRefused(t, fleet(t, append(argv, "--apply")...))
		})
	}
}

func TestGatewayRefusesARecoveryIDThatIsAPath(t *testing.T) {
	expectRefused(t, fleet(t, "gateway", "recover-source", "shared-a", "--sha="+sha, "--supersede-recovery=a/b", "--apply"))
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

func bulkMove(userIDs ...any) adminRequest {
	return adminRequest{Method: http.MethodPost, Path: "/api/admin/gateway-migrations/bulk", Body: map[string]any{"sourceGatewayId": "shared-a", "toGatewayId": "shared-b", "userIds": userIDs}}
}

func TestGatewayMoveUsersMovesEveryUserOnTheSource(t *testing.T) {
	expectApplied(t, fleet(t, "gateway", "move-users", "shared-a", "--to=shared-b", "--apply"), bulkMove("u-ada"))
}

func TestGatewayMoveUsersMovesTheListedUsers(t *testing.T) {
	expectApplied(t, fleet(t, "gateway", "move-users", "shared-a", "--to=shared-b", "--users=ada", "--apply"), bulkMove("u-ada"))
}

func TestGatewayMoveUsersRefusals(t *testing.T) {
	cases := map[string][]string{
		"a listed user on another gateway": {"gateway", "move-users", "shared-a", "--to=shared-b", "--users=grace", "--apply"},
		"a listed user who does not exist": {"gateway", "move-users", "shared-a", "--to=shared-b", "--users=nobody", "--apply"},
		"a parked target":                  {"gateway", "move-users", "shared-a", "--to=shared-parked", "--apply"},
		"a target that is the source":      {"gateway", "move-users", "shared-a", "--to=shared-a", "--apply"},
		"an unknown source":                {"gateway", "move-users", "nowhere", "--to=shared-b", "--apply"},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			expectRefused(t, fleet(t, argv...))
		})
	}
}

func TestGatewayMoveUsersHasNothingToDoOnAnEmptySource(t *testing.T) {
	expectRead(t, fleet(t, "gateway", "move-users", "shared-b", "--to=shared-a", "--apply"))
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

func TestGatewayReleasesIsReadOnly(t *testing.T) {
	expectRead(t, fleet(t, "gateway", "releases", "shared-a"))
}

func TestGwIsShortForGateway(t *testing.T) {
	expectApplied(t, fleet(t, "gw", "park", "shared-a", "--apply"), adminRequest{Method: http.MethodPatch, Path: "/api/admin/gateways/shared-a", Body: map[string]any{"desiredStatus": "paused"}})
}
