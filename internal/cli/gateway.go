package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
)

var gatewayCommands = []*command{
	{path: words("gateway ls"), usage: "[--all]", summary: "List gateways with status, version, mounted users and last heartbeat.", flags: flagSet{"all": bareFlag}, run: gatewayList},
	{path: words("gateway inspect"), usage: "<gateway>", argc: 1, summary: "Show the full diagnostic for one gateway.", run: gatewayInspect},
	{path: words("gateway add"), usage: "--id=<id> [--sha=<full-sha>]", summary: "Add a shared gateway on an existing immutable build, or on the platform's running build.", flags: flagSet{"id": valueFlag, "sha": valueFlag}, required: []string{"id"}, mutating: true, run: gatewayAdd},
	{path: words("gateway deploy"), usage: "<gateway> --sha=<full-sha>", argc: 1, summary: "Deploy an existing immutable build to one gateway.", flags: flagSet{"sha": valueFlag}, required: []string{"sha"}, mutating: true, run: gatewayDeploy},
	{path: words("gateway restart"), usage: "<gateway>", argc: 1, summary: "Restart a gateway.", mutating: true, run: gatewayRestart},
	{path: words("gateway park"), usage: "<gateway>", argc: 1, summary: "Pause a gateway.", mutating: true, run: gatewayDesiredStatus(statusPaused)},
	{path: words("gateway resume"), usage: "<gateway>", argc: 1, summary: "Resume a parked gateway.", mutating: true, run: gatewayDesiredStatus(statusRunning)},
	{path: words("gateway retire"), usage: "<gateway> [--retain-data] [--retain-service-account]", argc: 1, summary: "Retire an empty gateway.", flags: flagSet{"retain-data": bareFlag, "retain-service-account": bareFlag}, mutating: true, run: gatewayRetire},
	{path: words("gateway releases"), usage: "<gateway>", argc: 1, summary: "List a gateway's releases.", run: gatewayReleases},
	{path: words("gateway rollback"), usage: "<gateway> --release=<n>", argc: 1, summary: "Roll a gateway back to an earlier release.", flags: flagSet{"release": valueFlag}, required: []string{"release"}, mutating: true, run: gatewayRollback},
	{path: words("gateway replace-vm"), usage: "<gateway> [--force --accept-boot-data-loss]", argc: 1, summary: "Replace an isolated gateway's VM, keeping the verified home disk.", flags: flagSet{"force": bareFlag, "accept-boot-data-loss": bareFlag}, mutating: true, run: gatewayReplaceVM},
}

func gatewayList(i *invocation) error {
	gateways, err := i.gateways(i.has("all"))
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(gateways))
	for _, gateway := range gateways {
		rows = append(rows, []string{
			i.style.bold(gateway.GatewayID), dash(gateway.GatewayClass), i.style.state(gateway.DesiredStatus), i.style.state(gateway.Status),
			dash(gateway.ImageTag), strconv.Itoa(len(gateway.MountedUsers)), i.ago(gateway.LastHeartbeatAt),
		})
	}
	i.table([]string{"gateway", "class", "desired", "status", "image", "mounted", "heartbeat"}, rows, "no gateways")
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
	body := map[string]any{"gatewayId": id}
	if i.has("sha") {
		sha, err := fullSHA(i.value("sha"))
		if err != nil {
			return err
		}
		body["sourceSha"] = sha
	}
	gateways, err := i.gateways(true)
	if err != nil {
		return err
	}
	if _, err := findGateway(gateways, id); err == nil {
		return fmt.Errorf("gateway %q already exists", id)
	}
	return i.post(admin.Path("gateways"), body)
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
			Release   int     `json:"release"`
			ImageTag  string  `json:"imageTag"`
			CreatedAt instant `json:"createdAt"`
			CreatedBy string  `json:"createdBy"`
			Live      bool    `json:"live"`
			Desired   bool    `json:"desired"`
		} `json:"releases"`
	}
	if err := i.admin.Get(i.ctx, admin.Path("gateways", i.args[0], "releases"), nil, &response); err != nil {
		return err
	}
	rows := make([][]string, 0, len(response.Releases))
	for _, release := range response.Releases {
		rows = append(rows, []string{
			i.style.bold(strconv.Itoa(release.Release)), dash(release.ImageTag), i.ago(release.CreatedAt), dash(release.CreatedBy),
			i.check(release.Live), i.check(release.Desired),
		})
	}
	i.table([]string{"release", "image", "created", "by", "live", "desired"}, rows, "no releases")
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

func gatewayReplaceVM(i *invocation) error {
	gateway, err := i.gateway(i.args[0])
	if err != nil {
		return err
	}
	return i.replaceVM(admin.Path("gateways", gateway.GatewayID, "vm", "replace"))
}
