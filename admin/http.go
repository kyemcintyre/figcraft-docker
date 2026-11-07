package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed ui/index.html ui/admin.js ui/admin.css
var ui embed.FS

// Commands is what the admin page does on the server (CLI in production, a fake in tests).
type Commands interface {
	Files(ctx context.Context) ([]File, error)
	Links(ctx context.Context, file string) ([]Link, error)
	CreateLink(ctx context.Context, file, access, label string) (NewLink, error)
	Revoke(ctx context.Context, file, link string) error
	Remove(ctx context.Context, file string) error
}

type App struct {
	Commands  Commands
	Passwords *Passwords
	Sessions  *Sessions
	Limiter   *Limiter
	// The web app's address, for the browser link to a new share (".../#join=<link>").
	WebURL string
	// Secure cookies (HTTPS only); off only in tests over plain HTTP.
	SecureCookie bool
	Log          *log.Logger
}

const (
	cookieName = "figcraft_admin"
	// Every request that changes something must carry this header: a page on another site can't
	// add it to a request without a CORS preflight, which this service never allows.
	csrfHeader = "X-Figcraft-Admin"
	maxBody    = 4 << 10
	maxLabel   = 60
)

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") })
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /admin/{$}", a.static("ui/index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /admin/admin.js", a.static("ui/admin.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /admin/admin.css", a.static("ui/admin.css", "text/css; charset=utf-8"))

	mux.HandleFunc("GET /admin/api/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"signedIn": a.Sessions.Valid(sessionToken(r))})
	})
	mux.HandleFunc("POST /admin/api/login", a.changes(a.login))
	mux.HandleFunc("POST /admin/api/logout", a.changes(a.logout))
	mux.HandleFunc("POST /admin/api/password", a.changes(a.signedIn(a.changePassword)))
	mux.HandleFunc("GET /admin/api/files", a.signedIn(a.files))
	mux.HandleFunc("GET /admin/api/files/{file}/links", a.signedIn(a.links))
	mux.HandleFunc("POST /admin/api/files/{file}/links", a.changes(a.signedIn(a.createLink)))
	mux.HandleFunc("DELETE /admin/api/files/{file}/links/{link}", a.changes(a.signedIn(a.revoke)))
	mux.HandleFunc("DELETE /admin/api/files/{file}", a.changes(a.signedIn(a.remove)))
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

func (a *App) static(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := ui.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Write(b)
	}
}

// changes guards requests that change something: the CSRF header, and an Origin (when the browser
// sends one) on this host.
func (a *App) changes(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(csrfHeader) != "1" {
			writeError(w, http.StatusForbidden, "missing "+csrfHeader+" header")
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || !strings.EqualFold(u.Host, r.Host) {
				writeError(w, http.StatusForbidden, "cross-site request refused")
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		next(w, r)
	}
}

func (a *App) signedIn(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.Sessions.Valid(sessionToken(r)) {
			writeError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		next(w, r)
	}
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// clientAddr is who's asking, for login limits: Cloudflare's CF-Connecting-IP when it's an IP
// (only Traefik reaches this service; a forged header still meets the global limit), else the
// connection's address.
func clientAddr(r *http.Request) string {
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); ip != nil {
		return ip.String()
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *App) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: value, Path: "/admin", MaxAge: maxAge,
		HttpOnly: true, Secure: a.SecureCookie, SameSite: http.SameSiteStrictMode,
	})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	addr := clientAddr(r)
	if ok, until := a.Limiter.Allowed(addr); !ok {
		mins := int(time.Until(until).Minutes()) + 1
		writeError(w, http.StatusTooManyRequests, "too many wrong passwords: try again in "+itoa(mins)+" minutes")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !a.Passwords.Check(body.Password) {
		a.Limiter.Failed(addr)
		a.Log.Printf("admin: wrong password from %s", addr)
		writeError(w, http.StatusUnauthorized, "wrong password")
		return
	}
	a.Limiter.Succeeded(addr)
	tok, err := a.Sessions.New()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "can't start a session")
		return
	}
	a.setCookie(w, tok, int((12 * time.Hour).Seconds()))
	a.Log.Printf("admin: signed in from %s", addr)
	writeJSON(w, http.StatusOK, map[string]bool{"signedIn": true})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	a.Sessions.End(sessionToken(r))
	a.setCookie(w, "", -1)
	writeJSON(w, http.StatusOK, map[string]bool{"signedIn": false})
}

func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	addr := clientAddr(r)
	if ok, _ := a.Limiter.Allowed(addr); !ok {
		writeError(w, http.StatusTooManyRequests, "too many wrong passwords: try again later")
		return
	}
	if !a.Passwords.Check(body.Current) {
		a.Limiter.Failed(addr)
		writeError(w, http.StatusUnauthorized, "the current password is wrong")
		return
	}
	if err := a.Passwords.Change(body.New); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Sessions.EndOthers(sessionToken(r))
	a.Log.Printf("admin: password changed from %s; other sessions ended", addr)
	writeJSON(w, http.StatusOK, map[string]bool{"changed": true})
}

func (a *App) files(w http.ResponseWriter, r *http.Request) {
	files, err := a.Commands.Files(r.Context())
	if err != nil {
		a.commandError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (a *App) links(w http.ResponseWriter, r *http.Request) {
	links, err := a.Commands.Links(r.Context(), r.PathValue("file"))
	if err != nil {
		a.commandError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links})
}

func (a *App) createLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Access string `json:"access"`
		Label  string `json:"label"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	label := strings.TrimSpace(body.Label)
	if len([]rune(label)) > maxLabel || strings.ContainsAny(label, "\r\n\t") {
		writeError(w, http.StatusBadRequest, "the label must be one line of up to 60 characters")
		return
	}
	file := r.PathValue("file")
	n, err := a.Commands.CreateLink(r.Context(), file, body.Access, label)
	if err != nil {
		a.commandError(w, err)
		return
	}
	a.Log.Printf("admin: made %s link %s for file %s (%s) from %s", n.Access, n.ID, file, label, clientAddr(r))
	writeJSON(w, http.StatusCreated, map[string]any{"link": n, "browser": a.browserLink(n.Link)})
}

func (a *App) revoke(w http.ResponseWriter, r *http.Request) {
	file, link := r.PathValue("file"), r.PathValue("link")
	if err := a.Commands.Revoke(r.Context(), file, link); err != nil {
		a.commandError(w, err)
		return
	}
	a.Log.Printf("admin: revoked link %s of file %s from %s", link, file, clientAddr(r))
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

func (a *App) remove(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	if err := a.Commands.Remove(r.Context(), file); err != nil {
		a.commandError(w, err)
		return
	}
	a.Log.Printf("admin: removed file %s from %s", file, clientAddr(r))
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

// browserLink opens a share in the web app: <WebURL>#join=<figcraft:// link>.
func (a *App) browserLink(link string) string {
	if a.WebURL == "" {
		return ""
	}
	return strings.TrimRight(a.WebURL, "/") + "/#join=" + link
}

func (a *App) commandError(w http.ResponseWriter, err error) {
	var ce *CommandError
	if errors.As(err, &ce) {
		writeError(w, http.StatusBadRequest, ce.Message)
		return
	}
	a.Log.Printf("admin: %v", err)
	writeError(w, http.StatusBadGateway, "figcraft-server didn't answer as expected (see the admin container's log)")
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "send JSON")
		return false
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
