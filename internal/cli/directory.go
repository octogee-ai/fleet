package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
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
)

type gatewaySummary struct {
	GatewayID       string   `json:"gatewayId"`
	GatewayClass    string   `json:"gatewayClass"`
	DesiredStatus   string   `json:"desiredStatus"`
	Status          string   `json:"status"`
	ImageTag        string   `json:"imageTag"`
	OpenclawVersion string   `json:"openclawVersion"`
	MountedUsers    []string `json:"mountedUsers"`
	LastHeartbeatAt instant  `json:"lastHeartbeatAt"`
	OwnerID         string   `json:"ownerId"`
	LastError       *struct {
		Message string `json:"message"`
	} `json:"lastError"`
	PodDiagnostics *struct {
		Pods []struct {
			Name  string `json:"name"`
			Ready bool   `json:"ready"`
		} `json:"pods"`
	} `json:"podDiagnostics"`

	record json.RawMessage
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

// state is the VM's status in lower case, with why it stopped if it did.
func (v *vmState) state() string {
	if v == nil {
		return "none"
	}
	status := strings.ToLower(v.Status)
	if status == "" {
		status = "unknown"
	}
	if v.StopReason != "" && status != "running" {
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
const detailsReads = 8

// detailsOf reads the details of each gateway at once, a few at a time.
func (i *invocation) detailsOf(gateways []gatewaySummary) ([]gatewayDetails, []error) {
	details := make([]gatewayDetails, len(gateways))
	failures := make([]error, len(gateways))
	slots := make(chan struct{}, detailsReads)
	var group sync.WaitGroup
	for index, gateway := range gateways {
		group.Add(1)
		slots <- struct{}{}
		go func() {
			defer group.Done()
			defer func() { <-slots }()
			details[index], failures[index] = i.details(gateway.GatewayID)
		}()
	}
	group.Wait()
	return details, failures
}

type placedUser struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	AssignedGatewayID  string `json:"assignedGatewayId"`
	MountedGatewayID   string `json:"mountedGatewayId"`
	ProvisioningStatus string `json:"provisioningStatus"`
	DriftState         string `json:"driftState"`
	Email              string `json:"email"`
	LatestOperation    *struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Status string `json:"status"`
	} `json:"latestOperation"`

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

// records decodes each record and keeps it as the Admin API sent it.
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
	if _, err := i.read(admin.Path("gateways"), query, &response); err != nil {
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
	if _, err := i.read(admin.Path("placement"), nil, &response); err != nil {
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

func (u placedUser) drift() string {
	switch u.DriftState {
	case "", driftHealthy, driftInFlight:
		return ""
	case driftUnmounted:
		return fmt.Sprintf("drift: %s is placed on %s but not mounted there", u.Name, u.AssignedGatewayID)
	case driftOrphan:
		return fmt.Sprintf("drift: %s is mounted on %s but not placed there", u.Name, u.MountedGatewayID)
	default:
		return fmt.Sprintf("drift: %s is %s (placed on %s, mounted on %s)", u.Name, u.DriftState, dash(u.AssignedGatewayID), dash(u.MountedGatewayID))
	}
}
