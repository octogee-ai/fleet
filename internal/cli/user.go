package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
)

const provisioningComplete = "complete"

var userCommands = []*command{
	{
		path: words("user ls"), usage: "[--gateway=<gateway>]", summary: "List users and their placement, and report drift in both directions.",
		flags:    []flagSpec{valued("gateway", "<gateway>", "Only users placed on or mounted on this gateway.")},
		examples: []string{"", "--gateway=shared-a"},
		run:      userList,
	},
	{
		path: words("user inspect"), usage: "<user>", argc: 1, summary: "Show the full diagnostic for one user.",
		examples: []string{"ada"},
		run:      userInspect,
	},
	{
		path: words("user add"), usage: "<name> --email=<address>", argc: 1, summary: "Create and admit a user.",
		flags:    []flagSpec{valued("email", "<address>", "The user's email address.")},
		required: []string{"email"}, mutating: true,
		examples: []string{"lin --email=lin@example.com", "lin --email=lin@example.com --apply"},
		run:      userAdd,
	},
	{
		path: words("user issue-passwords"), usage: "<user>", argc: 1, summary: "Set new chat and git passwords and print them once.", mutating: true,
		examples: []string{"lin --apply"},
		run:      userIssuePasswords,
	},
	{
		path: words("user assign"), usage: "<user> --to=<gateway>", argc: 1, summary: "Place a user who has no gateway.",
		flags:    []flagSpec{valued("to", "<gateway>", "The gateway to place the user on.")},
		required: []string{"to"}, mutating: true,
		examples: []string{"lin --to=shared-a", "lin --to=shared-a --apply"},
		run:      userAssign,
	},
	{
		path: words("user migrate"), usage: "<user> (--to=<gateway> [--source-transfer=retained-pod] | --to-isolated=<sizing profile>)", argc: 1, summary: "Move a placed user to another gateway, or to a new isolated gateway.",
		flags: []flagSpec{
			valued("to", "<gateway>", "The gateway to move the user to."),
			valued("source-transfer", "retained-pod", "Carry the user's state over from their retained pod."),
			valued("to-isolated", "<sizing profile>", "Create an isolated gateway of this sizing profile and move the user to it."),
		},
		mutating: true,
		examples: []string{"ada --to=shared-b", "ada --to-isolated=standard --apply"},
		run:      userMigrate,
	},
	{
		path: words("user unassign"), usage: "<user>", argc: 1, summary: "Remove a user's placement.", mutating: true,
		examples: []string{"ada"},
		run:      userUnassign,
	},
	{
		path: words("user replace-vm"), usage: "<user> [--force --accept-boot-data-loss]", argc: 1, summary: "Replace the VM of the user's isolated gateway.",
		flags:    breakGlassFlags,
		mutating: true,
		examples: []string{"ada"},
		run:      userReplaceVM,
	},
	{
		path: words("user delete"), usage: "<user> [--erase-now]", argc: 1, summary: "Schedule a user's deletion; --erase-now erases at once.",
		flags:    []flagSpec{bare("erase-now", "Erase the user at once instead of after the waiting period.")},
		mutating: true,
		examples: []string{"lin", "lin --erase-now --apply"},
		run:      userDelete,
	},
	{
		path: words("user broker"), usage: "<user>", argc: 1, summary: "Show the user's model broker diagnostics.",
		examples: []string{"ada"},
		run:      userBroker,
	},
}

func userList(i *invocation) error {
	filter := i.value("gateway")
	if filter != "" {
		if _, err := i.gateway(filter); err != nil {
			return err
		}
	}
	directory, err := i.placement()
	if err != nil {
		return err
	}
	var listed []placedUser
	for _, user := range directory.Users {
		if filter == "" || user.AssignedGatewayID == filter || user.MountedGatewayID == filter {
			listed = append(listed, user)
		}
	}
	if i.json {
		return i.printJSON(map[string]any{"users": recordsOf(listed, func(u placedUser) json.RawMessage { return u.record })})
	}
	var rows [][]string
	var drift []string
	for _, user := range listed {
		rows = append(rows, []string{i.style.bold(user.Name), i.style.dim(user.ID), dash(user.AssignedGatewayID), dash(user.MountedGatewayID), i.style.state(user.ProvisioningStatus)})
		if line := user.drift(); line != "" {
			drift = append(drift, line)
		}
	}
	i.table([]string{"name", "id", "gateway", "mounted", "status"}, rows, "no users")
	for _, line := range drift {
		i.warn(line)
	}
	return nil
}

func userInspect(i *invocation) error {
	user, err := i.namedUser()
	if err != nil {
		return err
	}
	gateways, err := i.gateways(false)
	if err != nil {
		return err
	}
	var owned []gatewaySummary
	for _, gateway := range gateways {
		if gateway.isolated() && gateway.OwnerID == user.ID {
			owned = append(owned, gateway)
		}
	}
	details, failures := i.detailsOf(owned)
	for index, gateway := range owned {
		if failures[index] != nil {
			i.notice(fmt.Sprintf("could not read the VM state of %s: %v", gateway.GatewayID, failures[index]))
		}
	}
	if i.json {
		isolated := make([]map[string]any, 0, len(owned))
		for index, gateway := range owned {
			entry := map[string]any{"gateway": gateway.record}
			if failures[index] == nil {
				entry["vm"] = details[index].vmRecord
			}
			isolated = append(isolated, entry)
		}
		return i.printJSON(map[string]any{"user": user.record, "isolatedGateways": isolated})
	}
	i.line(i.style.bold(user.Name) + "  " + i.style.dim(user.ID))
	target := ""
	if string(user.TargetGatewayID) != user.AssignedGatewayID {
		target = dash(string(user.TargetGatewayID))
	}
	userFields := []field{
		{"email", string(user.Email)},
		{"gateway", dash(user.AssignedGatewayID)},
		{"placement target", target},
		{"mounted on", dash(user.MountedGatewayID)},
		{"provisioning", i.style.dot(user.ProvisioningStatus)},
		{"placement", i.style.state(user.DriftState)},
	}
	for index, gateway := range owned {
		vm := i.style.dim("unknown")
		if failures[index] == nil {
			machine := details[index].Control.VM
			vm = i.style.toned(machine.tone(), machine.state())
		}
		userFields = append(userFields, field{"isolated gateway", fmt.Sprintf("%s %s, VM %s", i.style.bold(gateway.GatewayID), i.style.state(gateway.DesiredStatus), vm)})
	}
	if text := user.LatestOperation.unreadable(); text != "" {
		userFields = append(userFields, field{"latest operation", text})
	} else if operation := user.LatestOperation.value; operation != nil {
		userFields = append(userFields, field{"latest operation", fmt.Sprintf("%s %s %s", i.style.bold(string(operation.ID)), operation.Kind, i.style.state(string(operation.Status)))})
	}
	i.fields(userFields)
	if line := user.drift(); line != "" {
		i.warn(line)
	}
	return nil
}

func userAdd(i *invocation) error {
	name := i.args[0]
	directory, err := i.placement()
	if err != nil {
		return err
	}
	if _, err := directory.user(name); err == nil {
		return fmt.Errorf("the name %q is already taken", name)
	}
	return i.apply(change{
		request: post(admin.Path("users"), map[string]any{"name": name, "email": i.value("email")}),
		done:    "user " + name + " created",
		next: []string{
			"place them on a gateway: " + i.command("user assign "+name+" --to=<gateway> --apply"),
			"once user inspect shows provisioning complete: " + i.command("user issue-passwords "+name+" --apply"),
		},
	})
}

type signIn struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type oneTimePasswords struct {
	OperationID string `json:"operationId"`
	Chat        signIn `json:"chat"`
	Git         signIn `json:"git"`
}

func userIssuePasswords(i *invocation) error {
	user, err := i.namedUser()
	if err != nil {
		return err
	}
	if user.ProvisioningStatus != provisioningComplete {
		return fmt.Errorf("%s has not finished provisioning (status %s)", user.Name, dash(user.ProvisioningStatus))
	}
	request, data, applied, err := i.send(change{request: post(admin.Path("users", user.ID, "one-time-passwords"), nil)})
	if err != nil || !applied {
		return err
	}
	return i.printOneTimePasswords(request, data)
}

const passwordsShownOnce = "These passwords are shown once and are not stored. Git asks for a new one at first sign-in; ask the user to change the chat password too."

func (i *invocation) printOneTimePasswords(request admin.Request, data []byte) error {
	var issued oneTimePasswords
	if err := json.Unmarshal(data, &issued); err != nil || issued.Chat.Password == "" || issued.Git.Password == "" {
		return errors.New("the server returned no passwords; run this command again to issue new ones")
	}
	if i.json {
		i.notice(passwordsShownOnce)
		return i.printJSON(changeDocument{Request: describeRequest(request), Response: rawJSON(data)})
	}
	if i.style.glyphs {
		i.ok("new passwords issued")
	} else {
		i.line("applied: new passwords issued")
	}
	i.detail("operation " + i.style.bold(issued.OperationID))
	i.table([]string{"system", "username", "password"}, [][]string{
		{"chat", issued.Chat.Username, i.style.bold(issued.Chat.Password)},
		{"git", issued.Git.Username, i.style.bold(issued.Git.Password)},
	}, "")
	i.warn(passwordsShownOnce)
	return nil
}

func userAssign(i *invocation) error {
	user, target, err := i.userAndTarget()
	if err != nil {
		return err
	}
	if user.AssignedGatewayID == target.GatewayID {
		return i.unchanged(fmt.Sprintf("%s is already on %s", user.Name, target.GatewayID))
	}
	if user.AssignedGatewayID != "" {
		return fmt.Errorf("%s is already on %s; use `octogee-fleet user migrate` to move them", user.Name, user.AssignedGatewayID)
	}
	if target.GatewayClass == classIsolated && target.OwnerID != user.ID {
		return fmt.Errorf("gateway %q is isolated for another user", target.GatewayID)
	}
	return i.apply(change{
		request: post(admin.Path("users", user.ID, "assign"), map[string]any{"gatewayId": target.GatewayID}),
		done:    user.Name + " assigned to " + target.GatewayID,
	})
}

func userMigrate(i *invocation) error {
	if i.has("to") == i.has("to-isolated") {
		return errors.New("user migrate takes exactly one of --to=<gateway> and --to-isolated=<sizing profile>")
	}
	if i.has("to-isolated") {
		return userMigrateToIsolated(i)
	}
	transfer := i.value("source-transfer")
	if i.has("source-transfer") && transfer != "retained-pod" {
		return fmt.Errorf("--source-transfer must be retained-pod, got %q", transfer)
	}
	user, target, err := i.userAndTarget()
	if err != nil {
		return err
	}
	if user.AssignedGatewayID == "" {
		return fmt.Errorf("%s has no gateway to move from; use `octogee-fleet user assign` to place them", user.Name)
	}
	if user.AssignedGatewayID == target.GatewayID {
		return i.unchanged(fmt.Sprintf("%s is already on %s", user.Name, target.GatewayID))
	}
	body := map[string]any{"to": target.GatewayID}
	if transfer != "" {
		body["sourceTransfer"] = transfer
	}
	return i.apply(change{
		request: post(admin.Path("users", user.ID, "migrate"), body),
		done:    fmt.Sprintf("migration of %s from %s to %s queued", user.Name, user.AssignedGatewayID, target.GatewayID),
	})
}

func userMigrateToIsolated(i *invocation) error {
	if i.has("source-transfer") {
		return errors.New("--source-transfer applies only to a move with --to=<gateway>")
	}
	user, err := i.namedUser()
	if err != nil {
		return err
	}
	if user.AssignedGatewayID == "" {
		return fmt.Errorf("%s has no gateway to move from; use `octogee-fleet user assign` to place them", user.Name)
	}
	profile := i.value("to-isolated")
	return i.apply(change{
		request: post(admin.Path("users", user.ID, "isolated-gateway"), map[string]any{"sizingProfile": profile}),
		done:    fmt.Sprintf("isolated gateway (%s) requested for %s, and their migration queued", profile, user.Name),
	})
}

func (i *invocation) userAndTarget() (placedUser, gatewaySummary, error) {
	user, err := i.namedUser()
	if err != nil {
		return placedUser{}, gatewaySummary{}, err
	}
	target, err := i.gateway(i.value("to"))
	if err != nil {
		return placedUser{}, gatewaySummary{}, err
	}
	if user.AssignedGatewayID != target.GatewayID && target.DesiredStatus != statusRunning {
		return placedUser{}, gatewaySummary{}, fmt.Errorf("gateway %q is %s, not running", target.GatewayID, target.DesiredStatus)
	}
	return user, target, nil
}

func userUnassign(i *invocation) error {
	user, err := i.namedUser()
	if err != nil {
		return err
	}
	done := user.Name + " unassigned"
	if user.AssignedGatewayID != "" {
		done += " from " + user.AssignedGatewayID
	}
	return i.apply(change{request: post(admin.Path("users", user.ID, "unassign"), nil), done: done})
}

func userReplaceVM(i *invocation) error {
	user, err := i.namedUser()
	if err != nil {
		return err
	}
	return i.replaceVM(admin.Path("users", user.ID, "vm", "replace"), "VM replacement for "+user.Name+"'s isolated gateway queued")
}

func userDelete(i *invocation) error {
	user, err := i.namedUser()
	if err != nil {
		return err
	}
	done := user.Name + " scheduled for deletion"
	if i.has("erase-now") {
		done = user.Name + " scheduled for erasure now"
	}
	return i.apply(change{request: post(admin.Path("users", user.ID, "delete"), map[string]any{"eraseNow": i.has("erase-now")}), done: done})
}

func userBroker(i *invocation) error {
	user, err := i.namedUser()
	if err != nil {
		return err
	}
	var response struct {
		Windows []struct {
			Start    instant `json:"windowStartMs"`
			Tokens   int64   `json:"totalTokens"`
			Requests int64   `json:"requestCount"`
			Cost     float64 `json:"costUsd"`
		} `json:"windows"`
	}
	diagnostics, err := i.read(admin.Path("users", user.ID, "broker-diagnostics"), nil, &response)
	if err != nil {
		return err
	}
	if i.json {
		return i.printJSON(diagnostics)
	}
	rows := make([][]string, 0, len(response.Windows))
	for _, window := range response.Windows {
		rows = append(rows, []string{i.stampShort(window.Start), strconv.FormatInt(window.Requests, 10), strconv.FormatInt(window.Tokens, 10), fmt.Sprintf("$%.2f", window.Cost)})
	}
	i.line(i.style.bold(user.Name) + "  " + i.style.dim("model broker usage by window"))
	i.table([]string{"window", "requests", "tokens", "cost"}, rows, "no usage in the recent windows")
	i.line(i.style.dim("--format=json adds daily usage by model, the token breakdown and recent calls"))
	return nil
}
