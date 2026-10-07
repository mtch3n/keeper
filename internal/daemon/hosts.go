package daemon

import (
	"context"
	"slices"
	"strings"

	"github.com/mtchen/keeper/internal/types"
)

// HostView is one entry of GET /v1/hosts: the host and the connections
// registered on it. Human surface only — an address is where a credential
// connects, and nothing on MCP names one.
type HostView struct {
	types.Host
	Connections []string `json:"connections"`
}

// Hosts lists every host with the ids of its connections, by name.
func (d *Daemon) Hosts(ctx context.Context) ([]HostView, error) {
	hs, err := d.deps.Vault.Hosts(ctx)
	if err != nil {
		return nil, err
	}
	cs, err := d.deps.Vault.Connections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]HostView, 0, len(hs))
	for _, h := range hs {
		v := HostView{Host: *h, Connections: []string{}}
		for _, c := range cs {
			if c.HostID == h.ID {
				v.Connections = append(v.Connections, c.ID)
			}
		}
		out = append(out, v)
	}
	slices.SortStableFunc(out, func(a, b HostView) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// HostSpec is POST /v1/hosts.
type HostSpec struct {
	Name    string
	Address string
	Port    int
	SSLMode string
}

// RegisterHost stores a server's address. It opens nothing: a host holds no
// credential, so there is nothing to connect as until a connection is
// registered on it.
func (d *Daemon) RegisterHost(ctx context.Context, spec HostSpec) (*types.Host, error) {
	if spec.Name == "" || spec.Address == "" {
		return nil, errValidation("name and address", "are required")
	}
	if spec.Port == 0 {
		spec.Port = 5432
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return nil, errValidation("port", "must be between 1 and 65535")
	}
	if spec.SSLMode == "" {
		spec.SSLMode = "prefer"
	}
	if !slices.Contains(types.SSLModes, spec.SSLMode) {
		return nil, errValidation("sslmode", "must be one of "+strings.Join(types.SSLModes, ", "))
	}
	h := &types.Host{ID: timeID(), Name: spec.Name, Address: spec.Address, Port: spec.Port, SSLMode: spec.SSLMode}
	if err := d.deps.Vault.RegisterHost(ctx, h); err != nil {
		return nil, err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "host-registered", "host_id": h.ID}})
	return h, nil
}

// RemoveHost deletes a host with no connections on it. Removing the
// connections is a separate, deliberate step per connection, because each one
// takes its grants with it.
func (d *Daemon) RemoveHost(ctx context.Context, id string) error {
	if h, err := d.deps.Vault.Host(ctx, id); err != nil || h == nil {
		return errUnknownHost
	}
	cs, err := d.deps.Vault.Connections(ctx)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(cs, func(c *types.Connection) bool { return c.HostID == id }) {
		return errValidation("host", "still has connections registered on it; remove them first")
	}
	if err := d.deps.Vault.RemoveHost(ctx, id); err != nil {
		return err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "host-removed", "host_id": id}})
	return nil
}

// Settings is GET /v1/settings.
func (d *Daemon) Settings(ctx context.Context) (types.Settings, error) {
	return d.deps.Vault.Settings(ctx)
}

// SettingsPatch is PUT /v1/settings: each field set is changed, and the rest
// keep their stored value, so a screen that edits one setting cannot reset
// another it never sent.
type SettingsPatch struct {
	LogRetentionDays      *int
	ConnectionIdleMinutes *int
}

// UpdateSettings applies a patch. Retention must keep at least a day: a log that
// keeps nothing is not a log, and "keep forever" is not offered. Idle minutes
// must be at least one: closing a connection the moment a statement ends
// would reconnect for every statement.
func (d *Daemon) UpdateSettings(ctx context.Context, p SettingsPatch) (types.Settings, error) {
	s, err := d.deps.Vault.Settings(ctx)
	if err != nil {
		return types.Settings{}, err
	}
	if p.LogRetentionDays != nil {
		if *p.LogRetentionDays <= 0 {
			return types.Settings{}, errValidation("log_retention_days", "must be at least 1")
		}
		s.LogRetentionDays = *p.LogRetentionDays
	}
	if p.ConnectionIdleMinutes != nil {
		if *p.ConnectionIdleMinutes <= 0 {
			return types.Settings{}, errValidation("connection_idle_minutes", "must be at least 1")
		}
		s.ConnectionIdleMinutes = *p.ConnectionIdleMinutes
	}
	if err := d.deps.Vault.SetSettings(ctx, s); err != nil {
		return types.Settings{}, err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "settings"}})
	return s, nil
}
