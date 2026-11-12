package cli

import (
	"fmt"

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
}

type placedUser struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	AssignedGatewayID  string `json:"assignedGatewayId"`
	MountedGatewayID   string `json:"mountedGatewayId"`
	ProvisioningStatus string `json:"provisioningStatus"`
	DriftState         string `json:"driftState"`
}

type placement struct {
	Users []placedUser `json:"users"`
}

func (i *invocation) gateways(all bool) ([]gatewaySummary, error) {
	var response struct {
		Gateways []gatewaySummary `json:"gateways"`
	}
	query := map[string][]string{}
	if all {
		query["all"] = []string{"true"}
	}
	if err := i.admin.Get(i.ctx, admin.Path("gateways"), query, &response); err != nil {
		return nil, err
	}
	return response.Gateways, nil
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

func (i *invocation) gatewayRecord(id string) (map[string]any, error) {
	var response struct {
		Gateways []map[string]any `json:"gateways"`
	}
	if err := i.admin.Get(i.ctx, admin.Path("gateways"), nil, &response); err != nil {
		return nil, err
	}
	for _, gateway := range response.Gateways {
		if gateway["gatewayId"] == id {
			return gateway, nil
		}
	}
	return nil, fmt.Errorf("unknown gateway %q", id)
}

func (i *invocation) placement() (placement, error) {
	var response placement
	err := i.admin.Get(i.ctx, admin.Path("placement"), nil, &response)
	return response, err
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
