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
