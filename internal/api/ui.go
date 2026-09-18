package api

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// dist is the built UI. The directory holds a placeholder so this package
// compiles before web/dist exists; the build copies the real bundle over it.
//
// all: is deliberate — a bundler emits dotfiles and underscore-prefixed chunk
// names that the default embed pattern silently drops.
//
//go:embed all:dist
var dist embed.FS

func (s *Server) uiHandler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("api: embedded UI is missing: " + err.Error())
	}
	return http.FileServerFS(sub)
}

// serveApp serves the embedded single-page app. Every response is no-store, the
// assets are local, and nothing here reads or writes the path into a log:
// GET /r/{request_id} resolves to the UI route for a pending local request, and
// that id is a capability (R8.7d).
func (s *Server) serveApp(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		s.fail(w, r, errNoRoute)
		return
	}
	if infoOf(r.Context()).surface != surfaceLoopback {
		s.fail(w, r, errUIOnly)
		return
	}

	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		s.fail(w, r, errInternalStream)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name != "" && name != "index.html" {
		if f, err := sub.Open(name); err == nil {
			f.Close()
			s.ui.ServeHTTP(w, r)
			return
		}
	}

	// The UI reads this to fill X-Keeper-CSRF. SameSite=Strict keeps a
	// cross-site navigation from carrying it, and the Origin check in guard is
	// what the defence actually rests on: a cookie is the delivery mechanism,
	// not the boundary.
	http.SetCookie(w, &http.Cookie{
		Name:     "keeper_csrf",
		Value:    s.csrf,
		Path:     "/",
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeFileFS(w, r, sub, "index.html")
}
