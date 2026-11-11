package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
)

const provisioningComplete = "complete"

var userCommands = []*command{
	{path: words("user ls"), usage: "[--gateway=<gateway>]", summary: "List users and their placement, and report drift in both directions.", flags: flagSet{"gateway": valueFlag}, run: userList},
	{path: words("user inspect"), usage: "<user>", argc: 1, summary: "Show the full diagnostic for one user.", run: userInspect},
	{path: words("user add"), usage: "<name> --email=<address>", argc: 1, summary: "Create and admit a user.", flags: flagSet{"email": valueFlag}, required: []string{"email"}, mutating: true, run: userAdd},
	{path: words("user issue-passwords"), usage: "<user>", argc: 1, summary: "Set new chat and git passwords and print them once.", mutating: true, run: userIssuePasswords},
	{path: words("user assign"), usage: "<user> --to=<gateway>", argc: 1, summary: "Place a user who has no gateway.", flags: flagSet{"to": valueFlag}, required: []string{"to"}, mutating: true, run: userAssign},
	{path: words("user migrate"), usage: "<user> --to=<gateway> [--source-transfer=retained-pod]", argc: 1, summary: "Move a placed user to another gateway.", flags: flagSet{"to": valueFlag, "source-transfer": valueFlag}, required: []string{"to"}, mutating: true, run: userMigrate},
	{path: words("user unassign"), usage: "<user>", argc: 1, summary: "Remove a user's placement.", mutating: true, run: userAction("unassign", nil)},
	{path: words("user replace-vm"), usage: "<user> [--force --accept-boot-data-loss]", argc: 1, summary: "Replace the VM of the user's isolated gateway.", flags: flagSet{"force": bareFlag, "accept-boot-data-loss": bareFlag}, mutating: true, run: userReplaceVM},
	{path: words("user delete"), usage: "<user> [--erase-now]", argc: 1, summary: "Schedule a user's deletion; --erase-now erases at once.", flags: flagSet{"erase-now": bareFlag}, mutating: true, run: userDelete},
	{path: words("user broker"), usage: "<user>", argc: 1, summary: "Show the user's model broker diagnostics.", run: userBroker},
	{path: words("user lease request"), usage: "<user>", argc: 1, summary: "Request the user's activation lease.", mutating: true, run: userAction("activation-lease", nil)},
	{path: words("user lease cancel"), usage: "<user> --lease=<id> --generation=<n>", argc: 1, summary: "Cancel the user's activation lease.", flags: leaseFlags, required: leaseRequired, mutating: true, run: leaseAction("cancel")},
	{path: words("user lease terminate"), usage: "<user> --lease=<id> --generation=<n>", argc: 1, summary: "Terminate the user's activation lease.", flags: leaseFlags, required: leaseRequired, mutating: true, run: leaseAction("terminate")},
	{path: words("user lease return"), usage: "<user> --lease=<id> --generation=<n>", argc: 1, summary: "Return the user's activation lease.", flags: leaseFlags, required: leaseRequired, mutating: true, run: leaseAction("return")},
	{path: words("user lease retry-return"), usage: "<user> --operation=<id>", argc: 1, summary: "Retry a failed lease return.", flags: flagSet{"operation": valueFlag}, required: []string{"operation"}, mutating: true, run: leaseRetryReturn},
}

var (
	leaseFlags    = flagSet{"lease": valueFlag, "generation": valueFlag}
	leaseRequired = []string{"lease", "generation"}
)

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
	var rows [][]string
	var drift []string
	for _, user := range directory.Users {
		if filter != "" && user.AssignedGatewayID != filter && user.MountedGatewayID != filter {
			continue
		}
		rows = append(rows, []string{user.Name, user.ID, dash(user.AssignedGatewayID), dash(user.MountedGatewayID), dash(user.ProvisioningStatus)})
		if line := user.drift(); line != "" {
			drift = append(drift, line)
		}
	}
	writeTable(i.out, []string{"name", "id", "gateway", "mounted", "status"}, rows)
	for _, line := range drift {
		fmt.Fprintln(i.out, line)
	}
	return nil
}

func userInspect(i *invocation) error {
	user, err := i.namedUser()
	if err != nil {
		return err
	}
	if err := i.printJSON(user); err != nil {
		return err
	}
	if line := user.drift(); line != "" {
		fmt.Fprintln(i.out, line)
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
	return i.post(admin.Path("users"), map[string]any{"name": name, "email": i.value("email")},
		fmt.Sprintf("once `octogee-fleet user inspect %s` shows provisioning complete, run `octogee-fleet user issue-passwords %s --apply`", name, name))
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
	data, applied, err := i.sendMutation(admin.Request{Method: http.MethodPost, Path: admin.Path("users", user.ID, "one-time-passwords")})
	if err != nil || !applied {
		return err
	}
	return i.printOneTimePasswords(data)
}

func (i *invocation) printOneTimePasswords(data []byte) error {
	var issued oneTimePasswords
	if err := json.Unmarshal(data, &issued); err != nil || issued.Chat.Password == "" || issued.Git.Password == "" {
		return errors.New("the server returned no passwords; run this command again to issue new ones")
	}
	fmt.Fprintf(i.out, "operation %s\n", issued.OperationID)
	writeTable(i.out, []string{"system", "username", "password"}, [][]string{
		{"chat", issued.Chat.Username, issued.Chat.Password},
		{"git", issued.Git.Username, issued.Git.Password},
	})
	fmt.Fprintln(i.out, "These passwords are shown once and are not stored. Git asks for a new one at first sign-in; ask the user to change the chat password too.")
	return nil
}

func userAssign(i *invocation) error {
	user, target, err := i.userAndTarget()
	if err != nil {
		return err
	}
	if user.AssignedGatewayID == target.GatewayID {
		fmt.Fprintf(i.out, "%s is already on %s; nothing to do\n", user.Name, target.GatewayID)
		return nil
	}
	if user.AssignedGatewayID != "" {
		return fmt.Errorf("%s is already on %s; use `octogee-fleet user migrate` to move them", user.Name, user.AssignedGatewayID)
	}
	if target.GatewayClass == classIsolated && target.OwnerID != user.ID {
		return fmt.Errorf("gateway %q is isolated for another user", target.GatewayID)
	}
	return i.post(admin.Path("users", user.ID, "assign"), map[string]any{"gatewayId": target.GatewayID})
}

func userMigrate(i *invocation) error {
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
		fmt.Fprintf(i.out, "%s is already on %s; nothing to do\n", user.Name, target.GatewayID)
		return nil
	}
	body := map[string]any{"to": target.GatewayID}
	if transfer != "" {
		body["sourceTransfer"] = transfer
	}
	return i.post(admin.Path("users", user.ID, "migrate"), body)
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

func userAction(action string, body map[string]any) func(*invocation) error {
	return func(i *invocation) error {
		user, err := i.namedUser()
		if err != nil {
			return err
		}
		return i.post(admin.Path("users", user.ID, action), body)
	}
}

func userReplaceVM(i *invocation) error {
	user, err := i.namedUser()
	if err != nil {
		return err
	}
	return i.replaceVM(admin.Path("users", user.ID, "vm", "replace"))
}

func userDelete(i *invocation) error {
	user, err := i.namedUser()
	if err != nil {
		return err
	}
	return i.post(admin.Path("users", user.ID, "delete"), map[string]any{"eraseNow": i.has("erase-now")})
}

func userBroker(i *invocation) error {
	user, err := i.namedUser()
	if err != nil {
		return err
	}
	var diagnostics map[string]any
	if err := i.admin.Get(i.ctx, admin.Path("users", user.ID, "broker-diagnostics"), nil, &diagnostics); err != nil {
		return err
	}
	return i.printJSON(diagnostics)
}

func leaseAction(action string) func(*invocation) error {
	return func(i *invocation) error {
		lease, err := opaqueID("lease", i.value("lease"))
		if err != nil {
			return err
		}
		generation, err := wholeInt("generation", i.value("generation"))
		if err != nil {
			return err
		}
		user, err := i.namedUser()
		if err != nil {
			return err
		}
		return i.post(admin.Path("users", user.ID, "activation-lease", action), map[string]any{"leaseId": lease, "generation": generation})
	}
}

func leaseRetryReturn(i *invocation) error {
	operation, err := opaqueID("operation", i.value("operation"))
	if err != nil {
		return err
	}
	user, err := i.namedUser()
	if err != nil {
		return err
	}
	return i.post(admin.Path("users", user.ID, "activation-lease", "return", "retry"), map[string]any{"operationId": operation})
}
