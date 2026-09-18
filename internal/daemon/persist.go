package daemon

import (
	json "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/mtchen/keeper/internal/types"
)

// Standing grants outlive the daemon; session grants do not.
//
// R9.3d gives a standing grant a 30-day expiry and R9.3e a list a human reviews
// over time — "granted Sep 11, last used 2d ago" is a sentence about days, and a
// grant that evaporated when the daemon restarted would not be the thing either
// requirement describes. So the standing ones are written to disk and the
// session ones never are: a session grant dies with its session by definition,
// and persisting it would resurrect an authorization whose owner is gone.
//
// The file holds paths, ceilings, dates and counts. Nothing in it is secret,
// which is why it sits beside the audit log rather than inside the vault — a
// permission list you cannot read because the vault is locked is a permission
// list nobody reviews.

const grantsFile = "grants.json"

type grantDoc struct {
	Version int           `json:"version"`
	Grants  []types.Grant `json:"grants"`
}

func (d *Daemon) grantsPath() string {
	if d.cfg.StateDir == "" {
		return ""
	}
	return filepath.Join(d.cfg.StateDir, grantsFile)
}

// loadGrants reads the standing grants back at startup.
//
// A missing file is the normal first run. A corrupt one is reported and then
// ignored: refusing to start because a permission list will not parse would
// take the whole daemon down over the least important thing it owns.
func (d *Daemon) loadGrants() {
	path := d.grantsPath()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			d.cfg.Logger.Warn("could not read stored grants", "err", err)
		}
		return
	}

	var doc grantDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		d.cfg.Logger.Warn("stored grants do not parse; starting with none", "err", err)
		return
	}

	now := d.now()
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range doc.Grants {
		g := doc.Grants[i]
		if g.Lifetime != types.GrantStanding {
			// A session grant in the file is a bug somewhere upstream, not an
			// authorization to honour: the session it belonged to is gone.
			continue
		}
		if !g.ExpiresAt.IsZero() && now.After(g.ExpiresAt) {
			continue
		}
		g.SessionID = ""
		d.grants[g.ID] = &g
	}
	if n := len(d.grants); n > 0 {
		d.cfg.Logger.Info("restored standing grants", "count", n)
	}
}

// saveGrants writes the standing grants out. Callers hold no lock.
func (d *Daemon) saveGrants() {
	path := d.grantsPath()
	if path == "" {
		return
	}

	d.mu.Lock()
	var standing []types.Grant
	for _, g := range d.grants {
		if g.Lifetime == types.GrantStanding {
			standing = append(standing, *g)
		}
	}
	d.mu.Unlock()

	slices.SortFunc(standing, func(a, b types.Grant) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})

	raw, err := json.Marshal(grantDoc{Version: 1, Grants: standing})
	if err != nil {
		d.cfg.Logger.Warn("could not encode grants", "err", err)
		return
	}
	if err := writeFileAtomic(path, raw); err != nil {
		d.cfg.Logger.Warn("could not write grants", "err", err)
	}
}

// writeFileAtomic replaces a file without a window where it is half-written.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".grants-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
