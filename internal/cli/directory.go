package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
)

const (
	classIsolated  = "isolated"
	statusRunning  = "running"
	statusPaused   = "paused"
	statusRetired  = "retired"
	driftHealthy   = "healthy"
	driftInFlight  = "in-flight"
	driftUnmounted = "assigned-not-mounted"
	driftOrphan    = "mounted-orphan"
	driftPointer   = "pointer-drift"
	driftMissing   = "missing-gateway"
)

type gatewaySummary struct {
	GatewayID     string `json:"gatewayId"`
	GatewayClass  string `json:"gatewayClass"`
	DesiredStatus string `json:"desiredStatus"`
	OwnerID       string `json:"ownerId"`

	// Display fields decode leniently, so an odd value never blocks a mutation.
	Status          looseText                  `json:"status"`
	ImageTag        looseText                  `json:"imageTag"`
	OpenclawVersion looseText                  `json:"openclawVersion"`
	MountedUsers    lenient[[]json.RawMessage] `json:"mountedUsers"`
	LastHeartbeatAt instant                    `json:"lastHeartbeatAt"`
	LastError       lenient[struct {
		Message looseText `json:"message"`
	}] `json:"lastError"`
	PodDiagnostics lenient[struct {
		Pods []podDiagnostic `json:"pods"`
	}] `json:"podDiagnostics"`

	record json.RawMessage
}

// mounted is the number of mounted users, or ? when the list is unreadable.
func (g gatewaySummary) mounted() string {
	if !g.MountedUsers.ok {
		return "?"
	}
	return strconv.Itoa(len(g.MountedUsers.value))
}

// lastError is the gateway's last error message, or what was sent instead.
func (g gatewaySummary) lastError() string {
	if text := g.LastError.unreadable(); text != "" {
		return text
	}
	return string(g.LastError.value.Message)
}

// podDiagnostic is one pod as Kubernetes reports it.
type podDiagnostic struct {
	Name            looseText `json:"name"`
	Phase           looseText `json:"phase"`
	ReadyContainers int       `json:"readyContainers"`
	TotalContainers int       `json:"totalContainers"`
}

// ready is true for a running pod whose containers are all ready.
func (p podDiagnostic) ready() bool {
	return p.Phase == "Running" && p.TotalContainers > 0 && p.ReadyContainers == p.TotalContainers
}

// isolated reports whether the gateway serves one user on its own VM.
func (g gatewaySummary) isolated() bool { return g.GatewayClass == classIsolated }

// vmState is an isolated gateway's VM as the platform last saw it.
type vmState struct {
	InstanceName      string  `json:"instanceName"`
	Zone              string  `json:"zone"`
	Status            string  `json:"status"`
	StopReason        string  `json:"stopReason"`
	ProvisioningModel string  `json:"provisioningModel"`
	StartedAt         instant `json:"startedAt"`
	UpdatedAt         instant `json:"updatedAt"`
}

// state is the VM's status in lower case, with why it stopped if it did. A
// compact state has no spaces, for piped tables.
func (v *vmState) state(compact bool) string {
	if v == nil {
		return "none"
	}
	status := strings.ToLower(v.Status)
	if status == "" {
		status = "unknown"
	}
	switch {
	case v.StopReason == "" || status == "running":
	case compact:
		status += "(" + v.StopReason + ")"
	default:
		status += " (" + v.StopReason + ")"
	}
	return status
}

func (v *vmState) tone() tone {
	if v == nil {
		return toneMuted
	}
	return toneOf(v.Status)
}

type gatewayDetails struct {
	MountedUsers []struct {
		UID        string  `json:"uid"`
		Username   string  `json:"username"`
		Email      string  `json:"email"`
		LastActive instant `json:"lastActive"`
	} `json:"mountedUsers"`
	RecentOperations []operationView `json:"recentOperations"`
	Control          struct {
		DesiredRelease *int     `json:"desiredRelease"`
		LiveRelease    *int     `json:"liveRelease"`
		SizingTier     string   `json:"sizingTier"`
		VM             *vmState `json:"vm"`
	} `json:"control"`

	record   json.RawMessage
	vmRecord json.RawMessage
}

func (i *invocation) details(id string) (gatewayDetails, error) {
	var details gatewayDetails
	raw, err := i.read(admin.Path("gateways", id, "details"), nil, &details)
	if err != nil {
		return details, err
	}
	var control struct {
		Control struct {
			VM json.RawMessage `json:"vm"`
		} `json:"control"`
	}
	if err := json.Unmarshal(raw, &control); err != nil {
		return details, fmt.Errorf("decode the details of %s: %w", id, err)
	}
	details.record, details.vmRecord = raw, control.Control.VM
	return details, nil
}

// detailsReads bounds how many gateway details are read at once.
const detailsReads = 4

// vmReads are the VM states of some isolated gateways, each read or failed.
type vmReads struct {
	gateways []gatewaySummary
	details  []gatewayDetails
	failures []error
}

// readVMs reads each gateway's details, a few at a time, and warns about each
// one it could not read. An interrupt stops the command.
func (i *invocation) readVMs(gateways []gatewaySummary) (vmReads, error) {
	reads := vmReads{gateways: gateways, details: make([]gatewayDetails, len(gateways)), failures: make([]error, len(gateways))}
	slots := make(chan struct{}, detailsReads)
	var group sync.WaitGroup
	for index, gateway := range gateways {
		group.Add(1)
		slots <- struct{}{}
		go func() {
			defer group.Done()
			defer func() { <-slots }()
			reads.details[index], reads.failures[index] = i.details(gateway.GatewayID)
		}()
	}
	group.Wait()
	if err := i.ctx.Err(); err != nil {
		return reads, fmt.Errorf("interrupted while reading VM states: %w", err)
	}
	for index, gateway := range gateways {
		if reads.failures[index] != nil {
			i.notice(fmt.Sprintf("could not read the VM state of %s: %v", gateway.GatewayID, reads.failures[index]))
		}
	}
	return reads, nil
}

// cell is the gateway's VM state for a table or field, or unknown.
func (r vmReads) cell(style palette, index int) string {
	if r.failures[index] != nil {
		return style.dim("unknown")
	}
	vm := r.details[index].Control.VM
	return style.toned(vm.tone(), vm.state(!style.glyphs))
}

// failed is the error a command returns once it has printed what it read.
func (r vmReads) failed() error {
	count := 0
	for _, failure := range r.failures {
		if failure != nil {
			count++
		}
	}
	if count == 0 {
		return nil
	}
	return fmt.Errorf("could not read the VM state of %d of %d isolated gateways", count, len(r.gateways))
}

type placedUser struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	AssignedGatewayID  string `json:"assignedGatewayId"`
	MountedGatewayID   string `json:"mountedGatewayId"`
	ProvisioningStatus string `json:"provisioningStatus"`
	DriftState         string `json:"driftState"`

	// Display fields decode leniently, so an odd value never blocks a mutation.
	TargetGatewayID looseText `json:"policyTargetGatewayId"`
	Email           looseText `json:"email"`
	LatestOperation lenient[*struct {
		ID     looseText `json:"id"`
		Kind   looseText `json:"kind"`
		Status looseText `json:"status"`
	}] `json:"latestOperation"`

	record json.RawMessage
}

type placement struct {
	Users []placedUser
}

// read gets an Admin API resource and decodes it into view, if one is given.
// It returns the response as sent, for --format=json.
func (i *invocation) read(path string, query url.Values, view any) (json.RawMessage, error) {
	var response json.RawMessage
	if err := i.admin.Get(i.ctx, path, query, &response); err != nil {
		return nil, err
	}
	if view == nil {
		return response, nil
	}
	if err := json.Unmarshal(response, view); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return response, nil
}

// records decodes each record and keeps it as the Admin API sent it, for JSON
// output.
func records[T any](raw []json.RawMessage, keep func(*T, json.RawMessage)) ([]T, error) {
	decoded := make([]T, len(raw))
	for index, record := range raw {
		if err := json.Unmarshal(record, &decoded[index]); err != nil {
			return nil, err
		}
		keep(&decoded[index], record)
	}
	return decoded, nil
}

func (i *invocation) gateways(all bool) ([]gatewaySummary, error) {
	var response struct {
		Gateways []json.RawMessage `json:"gateways"`
	}
	var query url.Values
	if all {
		query = url.Values{"all": {"true"}}
	}
	if err := i.admin.Get(i.ctx, admin.Path("gateways"), query, &response); err != nil {
		return nil, err
	}
	gateways, err := records(response.Gateways, func(g *gatewaySummary, raw json.RawMessage) { g.record = raw })
	if err != nil {
		return nil, fmt.Errorf("decode gateways: %w", err)
	}
	return gateways, nil
}

func findGateway(gateways []gatewaySummary, id string) (gatewaySummary, error) {
	for _, gateway := range gateways {
		if gateway.GatewayID == id {
			return gateway, nil
		}
	}
	return gatewaySummary{}, fmt.Errorf("unknown gateway %q", id)
}

func (i *invocation) gateway(id string) (gatewaySummary, error) {
	gateways, err := i.gateways(false)
	if err != nil {
		return gatewaySummary{}, err
	}
	return findGateway(gateways, id)
}

func (i *invocation) placement() (placement, error) {
	var response struct {
		Users []json.RawMessage `json:"users"`
	}
	if err := i.admin.Get(i.ctx, admin.Path("placement"), nil, &response); err != nil {
		return placement{}, err
	}
	users, err := records(response.Users, func(u *placedUser, raw json.RawMessage) { u.record = raw })
	if err != nil {
		return placement{}, fmt.Errorf("decode users: %w", err)
	}
	return placement{Users: users}, nil
}

func (p placement) user(reference string) (placedUser, error) {
	for _, user := range p.Users {
		if user.ID == reference {
			return user, nil
		}
	}
	var matches []placedUser
	for _, user := range p.Users {
		if user.Name == reference {
			matches = append(matches, user)
		}
	}
	switch len(matches) {
	case 0:
		return placedUser{}, fmt.Errorf("unknown user %q", reference)
	case 1:
		return matches[0], nil
	default:
		return placedUser{}, fmt.Errorf("more than one user is named %q; use the user id", reference)
	}
}

func (i *invocation) namedUser() (placedUser, error) {
	directory, err := i.placement()
	if err != nil {
		return placedUser{}, err
	}
	return directory.user(i.args[0])
}

// drift says how the user's placement disagrees with itself, naming the
// gateways as the Admin API's own placement warnings do.
func (u placedUser) drift() string {
	placed, mounted, target := dash(u.AssignedGatewayID), dash(u.MountedGatewayID), dash(string(u.TargetGatewayID))
	switch u.DriftState {
	case "", driftHealthy, driftInFlight:
		return ""
	case driftUnmounted:
		return fmt.Sprintf("drift: %s is placed on %s but not mounted there", u.Name, placed)
	case driftOrphan:
		if u.AssignedGatewayID == "" {
			return fmt.Sprintf("drift: %s is mounted on %s but not placed there", u.Name, mounted)
		}
		return fmt.Sprintf("drift: %s is mounted on %s but placed on %s", u.Name, mounted, placed)
	case driftPointer:
		return fmt.Sprintf("drift: %s is placed on %s but the placement target is %s", u.Name, placed, target)
	case driftMissing:
		if u.TargetGatewayID == "" || string(u.TargetGatewayID) == u.AssignedGatewayID {
			return fmt.Sprintf("drift: %s is placed on %s, which is retired or unknown", u.Name, placed)
		}
		return fmt.Sprintf("drift: %s is placed on %s with placement target %s; one of them is retired or unknown", u.Name, placed, target)
	}
	return fmt.Sprintf("drift: %s is %s (placed on %s, placement target %s, mounted on %s)", u.Name, u.DriftState, placed, target, mounted)
}
