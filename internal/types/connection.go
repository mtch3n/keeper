package types

import (
	"encoding/json"
	"net/netip"
	"strings"
	"time"
)

// Reads is when a profile's reads ask a person first: always, only when the
// statement is uncertain, or never. Anything else behaves as ReadsAlways.
type Reads string

const (
	ReadsAlways    Reads = "always"
	ReadsUncertain Reads = "uncertain"
	ReadsNever     Reads = "never"
)

// Valid reports whether r is a setting keeper knows.
func (r Reads) Valid() bool { return r == ReadsAlways || r == ReadsUncertain || r == ReadsNever }

// Detection is how far a profile's pattern detection reaches. Scan, the default,
// examines scan columns only; all also examines the text of allow columns; off
// examines nothing, so scan cells are redacted whole. Anything else, and empty,
// behaves as scan.
type Detection string

const (
	DetectionOff  Detection = "off"
	DetectionScan Detection = "scan"
	DetectionAll  Detection = "all"
)

// Valid reports whether d is a setting keeper knows.
func (d Detection) Valid() bool { return d == DetectionOff || d == DetectionScan || d == DetectionAll }

// Effective is the setting keeper acts on: scan for an empty or unknown value.
func (d Detection) Effective() Detection {
	if d.Valid() {
		return d
	}
	return DetectionScan
}

// UnmarshalJSON reads a string and treats any other value, such as the stage
// list an earlier version stored, as unset.
func (d *Detection) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		*d = ""
		return nil
	}
	*d = Detection(s)
	return nil
}

// RelationRef names a relation. keeper stores names and resolves them to OIDs at
// connect time; SPEC R5.1 explains why the reverse does not work.
type RelationRef struct {
	Schema   string `json:"schema"`
	Relation string `json:"relation"`
}

func (r RelationRef) String() string { return r.Schema + "." + r.Relation }

// Limits are the per-connection operator settings of SPEC §4.5. Every one of them
// is set through the CLI or the UI and is unreachable from MCP.
type Limits struct {
	// MaxRowsCeiling bounds query(max_rows); the agent cannot raise it.
	MaxRowsCeiling int `json:"max_rows_ceiling"`
	// MaxBytes bounds a result's row data, so one wide column cannot flood
	// the agent's context under the row ceiling.
	MaxBytes int `json:"max_bytes"`
	// StatementTimeout is the SET LOCAL value for every statement.
	StatementTimeout time.Duration `json:"statement_timeout"`
	// ScanSample is how many rows the model layer of a scan column sees.
	ScanSample int `json:"scan_sample"`
}

// DefaultLimits are keeper's starting values. SPEC §15 lists them as tuning
// values rather than design decisions, so they are here rather than in the spec.
func DefaultLimits() Limits {
	return Limits{
		MaxRowsCeiling:   1000,
		MaxBytes:         1 << 20,
		StatementTimeout: 30 * time.Second,
		ScanSample:       300, // ~299 rows finds a 1% rate at 95% confidence, SPEC R8.5b
	}
}

// Host is one database server, entered once and shared by every connection
// registered on it. It carries where the server is and nothing about who logs
// in: credentials belong to a connection. No field here reaches MCP.
type Host struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	// SSLMode is libpq's sslmode, passed to the server as-is.
	SSLMode string `json:"sslmode"`
}

// SSLModes are the sslmode values libpq accepts.
var SSLModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

// DefaultSSLMode is the sslmode keeper uses for a host when none is asked for:
// require, except on this machine, where the server is reached over loopback.
func DefaultSSLMode(address string) string {
	if ip, err := netip.ParseAddr(address); strings.EqualFold(address, "localhost") || err == nil && ip.IsLoopback() {
		return "prefer"
	}
	return "require"
}

// Connection is one database and role on a Host: what an agent queries. Host,
// password and connection string never appear in an MCP response; SPEC §6.1
// returns the fields marked exposed.
//
// It is a connection, not the host, that owns a catalog and token keys: a
// (tableOID, attnum) pair means nothing outside its database.
type Connection struct {
	ID       string `json:"id"`
	HostID   string `json:"host_id"`
	Name     string `json:"name"`   // exposed
	Engine   string `json:"engine"` // exposed
	Database string `json:"database"`
	Role     string `json:"role"` // exposed: the role is the username, deliberately
	Version  string `json:"version,omitzero"`

	// CatalogPath is empty for the profile's own file in keeper's state directory;
	// a path shared by several profiles is always explicit.
	CatalogPath string `json:"catalog_path"`

	Reads     Reads     `json:"reads"`
	Writes    Writes    `json:"writes"`
	Limits    Limits    `json:"limits"`
	Detection Detection `json:"detection,omitzero"`
}

// Writes is a profile's write setting. Off is the default: its sessions are
// read-only, and a write is refused rather than run as some other profile. Ask
// waits for a person; allow commits without a question and without a preview.
// Anything else behaves as off.
type Writes string

const (
	WritesOff   Writes = "off"
	WritesAsk   Writes = "ask"
	WritesAllow Writes = "allow"
)

// Valid reports whether w is a setting keeper knows.
func (w Writes) Valid() bool { return w == WritesOff || w == WritesAsk || w == WritesAllow }

// Settings are the daemon-wide choices an operator makes in Settings. They live
// in the vault beside the connections and are unreachable from MCP.
type Settings struct {
	// LogRetentionDays is how long the activity log keeps a record.
	LogRetentionDays int `json:"log_retention_days"`
	// ConnectionIdleMinutes is how long a database's connections stay open
	// with nobody using them; the next use reconnects.
	ConnectionIdleMinutes int `json:"connection_idle_minutes"`
}

// DefaultSettings are a fresh vault's settings.
func DefaultSettings() Settings { return Settings{LogRetentionDays: 30, ConnectionIdleMinutes: 5} }
