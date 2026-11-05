package api

import (
	"context"
	"crypto/subtle"
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

const uiCookie = "keeper_ui"

// dist is the built UI. It is build output and untracked, apart from a .gitkeep
// so this package compiles before the UI is built; until then placeholder is
// served.
//
// all: is deliberate — a bundler emits dotfiles and underscore-prefixed chunk
// names that the default embed pattern silently drops.
//
//go:embed all:dist
var dist embed.FS

//go:embed placeholder.html
var placeholder []byte

// serveIndex serves the app's index.html, or placeholder when the UI was not
// built into this binary.
func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		_, _ = w.Write(placeholder)
		return
	}
	http.ServeFileFS(w, r, sub, "index.html")
}

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

	s.setCSRFCookie(w)
	serveIndex(w, r, sub)
}

// setCSRFCookie hands the page the token to echo in X-Keeper-CSRF.
// SameSite=Strict keeps a cross-site navigation from carrying it. It is not
// the login: any process on this machine can fetch it, which is why every human
// route also needs the keeper_ui cookie.
func (s *Server) setCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "keeper_csrf",
		Value:    s.csrf,
		Path:     "/",
		SameSite: http.SameSiteStrictMode,
	})
}

type uiSessionResponse struct {
	Base   string `json:"base"`
	Secret string `json:"secret"`
}

// uiSession is what `keeper ui` prints. It is socket only and refused to an
// agent session, because the secret opens every human route on loopback.
func (s *Server) uiSession(context.Context, *reqInfo, http.ResponseWriter, *http.Request) (any, error) {
	base, secret := s.d.UISession()
	return uiSessionResponse{Base: base, Secret: secret}, nil
}

// claimUISession trades the secret from the link's fragment for the session
// cookie. The cookie is HttpOnly, so the page's script cannot read it back.
func (s *Server) claimUISession(_ context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var body struct {
		Secret string `json:"secret"`
	}
	if err := s.readJSON(w, r, &body); err != nil {
		return nil, err
	}
	_, secret := s.d.UISession()
	if subtle.ConstantTimeCompare([]byte(body.Secret), []byte(secret)) != 1 {
		return nil, errUISessionNeeded
	}
	http.SetCookie(w, &http.Cookie{
		Name:     uiCookie,
		Value:    secret,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	return nil, nil
}

// authPage reads the secret from the fragment, which no server log and no
// Referer ever holds, claims the session, and moves on to ?next= when that is a
// path on this origin.
const authPage = `<!doctype html>
<meta charset="utf-8">
<title>keeper</title>
<body style="font:16px system-ui;margin:3rem auto;max-width:34rem">
<p id="m">Opening keeper...</p>
<script>
const secret = location.hash.slice(1);
const next = new URLSearchParams(location.search).get("next") || "/";
const csrf = (document.cookie.match(/(?:^|;\s*)keeper_csrf=([^;]+)/) || [])[1] || "";
history.replaceState(null, "", location.pathname);
fetch("/v1/ui-session/claim", {
  method: "POST",
  headers: {"Content-Type": "application/json", "X-Keeper-CSRF": decodeURIComponent(csrf)},
  body: JSON.stringify({secret}),
}).then((r) => {
  if (!r.ok) throw new Error();
  location.replace(/^\/(?![\/\\])/.test(next) ? next : "/");
}).catch(() => {
  document.getElementById("m").textContent = "This link no longer opens keeper. Run keeper ui in your terminal for a new one.";
});
</script>
`

func (s *Server) serveAuth(w http.ResponseWriter, r *http.Request) {
	if infoOf(r.Context()).surface != surfaceLoopback {
		s.fail(w, r, errUIOnly)
		return
	}
	s.setCSRFCookie(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(authPage))
}
