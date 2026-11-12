package cli

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
)

const (
	classIsolated  = "isolated"
	statusRunning  = "running"
	statusPaused   = "paused"
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

	record json.RawMessage
}

type placedUser struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	AssignedGatewayID  string `json:"assignedGatewayId"`
	MountedGatewayID   string `json:"mountedGatewayId"`
	ProvisioningStatus string `json:"provisioningStatus"`
	DriftState         string `json:"driftState"`

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
