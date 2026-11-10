package cli

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/octogee-ai/fleet/internal/admin"
)

var gatewayCommands = []*command{
	{path: words("gateway ls"), usage: "[--all]", summary: "List gateways with status, version, mounted users and last heartbeat.", flags: flagSet{"all": bareFlag}, run: gatewayList},
	{path: words("gateway inspect"), usage: "<gateway>", argc: 1, summary: "Show the full diagnostic for one gateway.", run: gatewayInspect},
	{path: words("gateway add"), usage: "--id=<id> --image-tag=<tag>", summary: "Register a shared gateway.", flags: flagSet{"id": valueFlag, "image-tag": valueFlag}, required: []string{"id", "image-tag"}, mutating: true, run: gatewayAdd},
	{path: words("gateway deploy"), usage: "<gateway> --sha=<full-sha>", argc: 1, summary: "Deploy an existing immutable build to one gateway.", flags: flagSet{"sha": valueFlag}, required: []string{"sha"}, mutating: true, run: gatewayDeploy},
	{path: words("gateway restart"), usage: "<gateway>", argc: 1, summary: "Restart a gateway.", mutating: true, run: gatewayRestart},
	{path: words("gateway park"), usage: "<gateway>", argc: 1, summary: "Pause a gateway.", mutating: true, run: gatewayDesiredStatus(statusPaused)},
	{path: words("gateway resume"), usage: "<gateway>", argc: 1, summary: "Resume a parked gateway.", mutating: true, run: gatewayDesiredStatus(statusRunning)},
	{path: words("gateway retire"), usage: "<gateway> [--retain-data] [--retain-service-account]", argc: 1, summary: "Retire an empty gateway.", flags: flagSet{"retain-data": bareFlag, "retain-service-account": bareFlag}, mutating: true, run: gatewayRetire},
	{path: words("gateway releases"), usage: "<gateway>", argc: 1, summary: "List a gateway's releases.", run: gatewayReleases},
	{path: words("gateway rollback"), usage: "<gateway> --release=<n>", argc: 1, summary: "Roll a gateway back to an earlier release.", flags: flagSet{"release": valueFlag}, required: []string{"release"}, mutating: true, run: gatewayRollback},
	{path: words("gateway recover-source"), usage: "<gateway> --sha=<full-sha> [--supersede-recovery=<id>]", argc: 1, summary: "Repair a shared gateway's source, keeping its pod storage.", flags: flagSet{"sha": valueFlag, "supersede-recovery": valueFlag}, required: []string{"sha"}, mutating: true, run: gatewayRecoverSource},
	{path: words("gateway recover-restore"), usage: "<gateway> --sha=<full-sha> --operation=<id> [--reauthorize-attempt=<id>] [--supersede-recovery=<id>]", argc: 1, summary: "Upgrade a fenced restore target without replacing its storage.", flags: flagSet{"sha": valueFlag, "operation": valueFlag, "reauthorize-attempt": valueFlag, "supersede-recovery": valueFlag}, required: []string{"sha", "operation"}, mutating: true, run: gatewayRecoverRestore},
	{path: words("gateway replace-vm"), usage: "<gateway> [--force --accept-boot-data-loss]", argc: 1, summary: "Replace an isolated gateway's VM, keeping the verified home disk.", flags: flagSet{"force": bareFlag, "accept-boot-data-loss": bareFlag}, mutating: true, run: gatewayReplaceVM},
	{path: words("gateway move-users"), usage: "<source> --to=<target> [--users=<a,b>]", argc: 1, summary: "Move the listed users, or all of them, through journaled migrations.", flags: flagSet{"to": valueFlag, "users": valueFlag}, required: []string{"to"}, mutating: true, run: gatewayMoveUsers},
}

func gatewayList(i *invocation) error {
	gateways, err := i.gateways(i.has("all"))
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(gateways))
	for _, gateway := range gateways {
		rows = append(rows, []string{
			gateway.GatewayID, dash(gateway.GatewayClass), dash(gateway.DesiredStatus), dash(gateway.Status),
			dash(gateway.ImageTag), strconv.Itoa(len(gateway.MountedUsers)), dash(gateway.LastHeartbeatAt),
		})
	}
	writeTable(i.out, []string{"gateway", "class", "desired", "status", "image", "mounted", "heartbeat"}, rows)
	return nil
}

func gatewayInspect(i *invocation) error {
	id := i.args[0]
	gateway, err := i.gatewayRecord(id)
	if err != nil {
		return err
	}
	var details map[string]any
	if err := i.admin.Get(i.ctx, admin.Path("gateways", id, "details"), nil, &details); err != nil {
		return err
	}
	return i.printJSON(map[string]any{"gateway": gateway, "details": details})
}

func gatewayAdd(i *invocation) error {
	id := i.value("id")
	if !gatewayIDPattern.MatchString(id) {
		return fmt.Errorf("--id must be a lowercase DNS-safe id of 1 to 32 characters, got %q", id)
	}
	gateways, err := i.gateways(true)
	if err != nil {
		return err
	}
	if _, err := findGateway(gateways, id); err == nil {
		return fmt.Errorf("gateway %q already exists", id)
	}
	return i.post(admin.Path("gateways"), map[string]any{
		"gatewayId":       id,
		"imageTag":        i.value("image-tag"),
		"gatewayClass":    "shared",
		"imageRepository": i.environment.ImageRepository,
	})
}

func gatewayDeploy(i *invocation) error {
	sha, err := fullSHA(i.value("sha"))
	if err != nil {
		return err
	}
	gateway, err := i.gateway(i.args[0])
	if err != nil {
		return err
	}
	return i.post(admin.Path("gateways", gateway.GatewayID, "deploy"), map[string]any{"sourceSha": sha})
}

func gatewayRestart(i *invocation) error {
	gateway, err := i.gateway(i.args[0])
	if err != nil {
		return err
	}
	return i.post(admin.Path("gateways", gateway.GatewayID, "restart"), nil)
}

func gatewayDesiredStatus(status string) func(*invocation) error {
	return func(i *invocation) error {
		gateway, err := i.gateway(i.args[0])
		if err != nil {
			return err
		}
		return i.mutate(admin.Request{
			Method: http.MethodPatch,
			Path:   admin.Path("gateways", gateway.GatewayID),
			Body:   map[string]any{"desiredStatus": status},
		})
	}
}

func gatewayRetire(i *invocation) error {
	gateway, err := i.gateway(i.args[0])
	if err != nil {
		return err
	}
	var query url.Values
	if i.has("retain-data") || i.has("retain-service-account") {
		query = url.Values{}
	}
	if i.has("retain-data") {
		query.Set("retainData", "true")
	}
	if i.has("retain-service-account") {
		query.Set("retainServiceAccount", "true")
	}
	return i.mutate(admin.Request{Method: http.MethodDelete, Path: admin.Path("gateways", gateway.GatewayID), Query: query})
}

func gatewayReleases(i *invocation) error {
	var response struct {
		Releases []struct {
			Release   int    `json:"release"`
			ImageTag  string `json:"imageTag"`
			CreatedAt string `json:"createdAt"`
			CreatedBy string `json:"createdBy"`
			Live      bool   `json:"live"`
			Desired   bool   `json:"desired"`
		} `json:"releases"`
	}
	if err := i.admin.Get(i.ctx, admin.Path("gateways", i.args[0], "releases"), nil, &response); err != nil {
		return err
	}
	rows := make([][]string, 0, len(response.Releases))
	for _, release := range response.Releases {
		rows = append(rows, []string{
			strconv.Itoa(release.Release), dash(release.ImageTag), dash(release.CreatedAt), dash(release.CreatedBy),
			strconv.FormatBool(release.Live), strconv.FormatBool(release.Desired),
		})
	}
	writeTable(i.out, []string{"release", "image", "created", "by", "live", "desired"}, rows)
	return nil
}

func gatewayRollback(i *invocation) error {
	release, err := positiveInt("release", i.value("release"))
	if err != nil {
		return err
	}
	gateway, err := i.gateway(i.args[0])
	if err != nil {
		return err
	}
	return i.post(admin.Path("gateways", gateway.GatewayID, "releases", strconv.Itoa(release), "rollback"), nil)
}

func gatewayRecoverSource(i *invocation) error {
	sha, err := fullSHA(i.value("sha"))
	if err != nil {
		return err
	}
	body := map[string]any{"sourceSha": sha}
	if err := addOpaque(i, body, "supersede-recovery", "supersedeRecoveryId"); err != nil {
		return err
	}
	gateway, err := i.gateway(i.args[0])
	if err != nil {
		return err
	}
	return i.post(admin.Path("gateways", gateway.GatewayID, "recover-source"), body)
}

func gatewayRecoverRestore(i *invocation) error {
	sha, err := fullSHA(i.value("sha"))
	if err != nil {
		return err
	}
	operation, err := opaqueID("operation", i.value("operation"))
	if err != nil {
		return err
	}
	body := map[string]any{"sourceSha": sha, "operationId": operation}
	if err := addOpaque(i, body, "reauthorize-attempt", "reauthorizeAttemptId"); err != nil {
		return err
	}
	if err := addOpaque(i, body, "supersede-recovery", "supersedeRecoveryId"); err != nil {
		return err
	}
	gateway, err := i.gateway(i.args[0])
	if err != nil {
		return err
	}
	return i.post(admin.Path("gateways", gateway.GatewayID, "recover-restore"), body)
}

func addOpaque(i *invocation, body map[string]any, flag, field string) error {
	if !i.has(flag) {
		return nil
	}
	value, err := opaqueID(flag, i.value(flag))
	if err != nil {
		return err
	}
	body[field] = value
	return nil
}

func gatewayReplaceVM(i *invocation) error {
	gateway, err := i.gateway(i.args[0])
	if err != nil {
		return err
	}
	return i.replaceVM(admin.Path("gateways", gateway.GatewayID, "vm", "replace"))
}

func gatewayMoveUsers(i *invocation) error {
	gateways, err := i.gateways(false)
	if err != nil {
		return err
	}
	source, err := findGateway(gateways, i.args[0])
	if err != nil {
		return err
	}
	target, err := findGateway(gateways, i.value("to"))
	if err != nil {
		return err
	}
	if source.GatewayID == target.GatewayID {
		return errors.New("--to names the source gateway")
	}
	if target.DesiredStatus != statusRunning {
		return fmt.Errorf("gateway %q is %s, not running", target.GatewayID, target.DesiredStatus)
	}
	directory, err := i.placement()
	if err != nil {
		return err
	}
	ids, err := usersToMove(directory, source.GatewayID, i.value("users"))
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Fprintf(i.out, "no users to move from %s\n", source.GatewayID)
		return nil
	}
	return i.post(admin.Path("gateway-migrations", "bulk"), map[string]any{
		"sourceGatewayId": source.GatewayID,
		"toGatewayId":     target.GatewayID,
		"userIds":         ids,
	})
}

func usersToMove(directory placement, source, listed string) ([]string, error) {
	if listed == "" {
		var ids []string
		for _, user := range directory.Users {
			if user.AssignedGatewayID == source {
				ids = append(ids, user.ID)
			}
		}
		return ids, nil
	}
	var ids []string
	for _, reference := range strings.Split(listed, ",") {
		user, err := directory.user(reference)
		if err != nil {
			return nil, err
		}
		if user.AssignedGatewayID != source {
			return nil, fmt.Errorf("user %q is not placed on %s", user.Name, source)
		}
		ids = append(ids, user.ID)
	}
	return ids, nil
}
