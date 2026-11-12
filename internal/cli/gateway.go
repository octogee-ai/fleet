package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
)

var gatewayCommands = []*command{
	{
		path: words("gateway ls"), usage: "[--all]", summary: "List gateways with their state, build, mounted users and last heartbeat.",
		flags:    []flagSpec{bare("all", "Include retired gateways.")},
		examples: []string{"", "--all"},
		run:      gatewayList,
	},
	{
		path: words("gateway inspect"), usage: "<gateway>", argc: 1, summary: "Show the full diagnostic for one gateway.",
		examples: []string{"shared-a"},
		run:      gatewayInspect,
	},
	{
		path: words("gateway add"), usage: "--id=<id> [--sha=<full-sha>]", summary: "Add a shared gateway on an immutable build, or on the platform's build.",
		flags: []flagSpec{
			valued("id", "<id>", "The new gateway's id: lowercase letters, digits and hyphens."),
			valued("sha", "<full-sha>", "The build to run, as a full commit sha. Omit it for the platform's build."),
		},
		required: []string{"id"}, mutating: true,
		examples: []string{"--id=shared-c --sha=<full-sha>", "--id=shared-c --sha=<full-sha> --apply"},
		run:      gatewayAdd,
	},
	{
		path: words("gateway deploy"), usage: "<gateway> --sha=<full-sha>", argc: 1, summary: "Deploy an existing immutable build to one gateway.",
		flags:    []flagSpec{valued("sha", "<full-sha>", "The build to deploy, as a full commit sha.")},
		required: []string{"sha"}, mutating: true,
		examples: []string{"iso-ada --sha=<full-sha>"},
		run:      gatewayDeploy,
	},
	{
		path: words("gateway restart"), usage: "<gateway>", argc: 1, summary: "Restart a gateway.", mutating: true,
		examples: []string{"shared-a", "shared-a --apply"},
		run:      gatewayRestart,
	},
	{
		path: words("gateway park"), usage: "<gateway>", argc: 1, summary: "Pause a gateway.", mutating: true,
		examples: []string{"shared-b", "shared-b --apply"},
		run:      gatewayDesiredStatus(statusPaused),
	},
	{
		path: words("gateway resume"), usage: "<gateway>", argc: 1, summary: "Resume a parked gateway.", mutating: true,
		examples: []string{"shared-b --apply"},
		run:      gatewayDesiredStatus(statusRunning),
	},
	{
		path: words("gateway retire"), usage: "<gateway> [--retain-data] [--retain-service-account]", argc: 1, summary: "Retire an empty gateway.",
		flags: []flagSpec{
			bare("retain-data", "Keep the gateway's data instead of deleting it."),
			bare("retain-service-account", "Keep the gateway's service account."),
		},
		mutating: true,
		examples: []string{"shared-b", "shared-b --retain-data --apply"},
		run:      gatewayRetire,
	},
	{
		path: words("gateway releases"), usage: "<gateway>", argc: 1, summary: "List a gateway's releases.",
		examples: []string{"iso-ada"},
		run:      gatewayReleases,
	},
	{
		path: words("gateway rollback"), usage: "<gateway> --release=<n>", argc: 1, summary: "Roll a gateway back to an earlier release.",
		flags:    []flagSpec{valued("release", "<n>", "The release to return to, from gateway releases.")},
		required: []string{"release"}, mutating: true,
		examples: []string{"iso-ada --release=3"},
		run:      gatewayRollback,
	},
	{
		path: words("gateway replace-vm"), usage: "<gateway> [--force --accept-boot-data-loss]", argc: 1, summary: "Replace an isolated gateway's VM, keeping the verified home disk.",
		flags:    breakGlassFlags,
		mutating: true,
		examples: []string{"iso-ada", "iso-ada --force --accept-boot-data-loss --apply"},
		run:      gatewayReplaceVM,
	},
}

var breakGlassFlags = []flagSpec{
	bare("force", "Replace even if boot-disk data may be lost. Needs --accept-boot-data-loss."),
	bare("accept-boot-data-loss", "Accept that data on the boot disk may be lost. Needs --force."),
}

func gatewayList(i *invocation) error {
	gateways, err := i.gateways(i.has("all"))
	if err != nil {
		return err
	}
	if i.json {
		return i.printJSON(map[string]any{"gateways": recordsOf(gateways, func(g gatewaySummary) json.RawMessage { return g.record })})
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
	gateways, err := i.gateways(true)
	if err != nil {
		return err
	}
	gateway, err := findGateway(gateways, i.args[0])
	if err != nil {
		return err
	}
	details, err := i.read(admin.Path("gateways", gateway.GatewayID, "details"), nil, nil)
	if err != nil {
		return err
	}
	return i.printJSON(map[string]any{"gateway": gateway.record, "details": details})
}

func gatewayAdd(i *invocation) error {
	id := i.value("id")
	if !gatewayIDPattern.MatchString(id) {
		return fmt.Errorf("--id must be a lowercase DNS-safe id of 1 to 32 characters, got %q", id)
	}
	body := map[string]any{"gatewayId": id}
	build := "the platform's build"
	if i.has("sha") {
		sha, err := fullSHA(i.value("sha"))
		if err != nil {
			return err
		}
		body["sourceSha"] = sha
		build = "build " + shortSHA(sha)
	}
	gateways, err := i.gateways(true)
	if err != nil {
		return err
	}
	if _, err := findGateway(gateways, id); err == nil {
		return fmt.Errorf("gateway %q already exists", id)
	}
	return i.apply(change{request: post(admin.Path("gateways"), body), done: fmt.Sprintf("shared gateway %s added on %s", id, build)})
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
	return i.apply(change{
		request: post(admin.Path("gateways", gateway.GatewayID, "deploy"), map[string]any{"sourceSha": sha}),
		done:    fmt.Sprintf("deploy of build %s to %s queued", shortSHA(sha), gateway.GatewayID),
	})
}

func gatewayRestart(i *invocation) error {
	gateway, err := i.gateway(i.args[0])
	if err != nil {
		return err
	}
	return i.apply(change{request: post(admin.Path("gateways", gateway.GatewayID, "restart"), nil), done: "restart of " + gateway.GatewayID + " queued"})
}

func gatewayDesiredStatus(status string) func(*invocation) error {
	return func(i *invocation) error {
		gateway, err := i.gateway(i.args[0])
		if err != nil {
			return err
		}
		return i.apply(change{
			request: admin.Request{Method: http.MethodPatch, Path: admin.Path("gateways", gateway.GatewayID), Body: map[string]any{"desiredStatus": status}},
			done:    gateway.GatewayID + " set to " + status,
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
	return i.apply(change{
		request: admin.Request{Method: http.MethodDelete, Path: admin.Path("gateways", gateway.GatewayID), Query: query},
		done:    "retirement of " + gateway.GatewayID + " queued",
	})
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
	raw, err := i.read(admin.Path("gateways", i.args[0], "releases"), nil, &response)
	if err != nil {
		return err
	}
	if i.json {
		return i.printJSON(raw)
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
	return i.apply(change{
		request: post(admin.Path("gateways", gateway.GatewayID, "releases", strconv.Itoa(release), "rollback"), nil),
		done:    fmt.Sprintf("rollback of %s to release %d queued", gateway.GatewayID, release),
	})
}

func gatewayReplaceVM(i *invocation) error {
	gateway, err := i.gateway(i.args[0])
	if err != nil {
		return err
	}
	return i.replaceVM(admin.Path("gateways", gateway.GatewayID, "vm", "replace"), "VM replacement for "+gateway.GatewayID+" queued")
}
