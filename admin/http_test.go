package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeCommands struct {
	revoked, removed []string
	created          []string
}

func (f *fakeCommands) Files(context.Context) ([]File, error) {
	return []File{{ID: "f1", Name: "Homepage", People: 1, Size: "1.0 KiB", Created: "2026-11-07", Idle: "0d", Expires: "never", By: "key"}}, nil
}
func (f *fakeCommands) Links(_ context.Context, file string) ([]Link, error) {
	return []Link{{ID: "L1", Access: "view", Created: "2026-11-07 12:00 UTC", Label: "Published (view)"}}, nil
}
func (f *fakeCommands) CreateLink(_ context.Context, file, access, label string) (NewLink, error) {
	f.created = append(f.created, file+"/"+access+"/"+label)
	return NewLink{ID: "L2", Access: access, File: file, Link: "figcraft://join?server=wss://h/&file=" + file + "&token=T", URL: "wss://h/?file=" + file + "&token=T"}, nil
}
func (f *fakeCommands) Revoke(_ context.Context, file, link string) error {
	f.revoked = append(f.revoked, file+"/"+link)
	return nil
}
func (f *fakeCommands) Remove(_ context.Context, file string) error {
	f.removed = append(f.removed, file)
	return nil
}

type harness struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
	cmds   *fakeCommands
	pw     string
	app    *App
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	passwords, first, err := OpenPasswords(filepath.Join(t.TempDir(), "admin"))
	if err != nil || len(first) != generatedLen {
		t.Fatalf("first password %q: %v", first, err)
	}
	cmds := &fakeCommands{}
	app := &App{Commands: cmds, Passwords: passwords, Sessions: NewSessions(), Limiter: NewLimiter(),
		WebURL: "https://figcraft.example.com/", Log: log.New(io.Discard, "", 0)}
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &harness{t: t, srv: srv, client: &http.Client{Jar: jar}, cmds: cmds, pw: first, app: app}
}

func (h *harness) do(method, path string, body any, headers map[string]string) (int, map[string]any) {
	h.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(csrfHeader, "1")
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func (h *harness) login() {
	h.t.Helper()
	if code, out := h.do("POST", "/admin/api/login", map[string]string{"password": h.pw}, nil); code != 200 {
		h.t.Fatalf("login: %d %v", code, out)
	}
}

func TestSignedOutCanDoNothing(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/admin/api/files"},
		{"GET", "/admin/api/files/f1/links"},
		{"POST", "/admin/api/files/f1/links"},
		{"DELETE", "/admin/api/files/f1/links/L1"},
		{"DELETE", "/admin/api/files/f1"},
		{"POST", "/admin/api/password"},
	} {
		var body any
		if c.method == "POST" {
			body = map[string]string{}
		}
		if code, _ := h.do(c.method, c.path, body, nil); code != http.StatusUnauthorized {
			t.Errorf("%s %s signed out: %d", c.method, c.path, code)
		}
	}
	if len(h.cmds.revoked)+len(h.cmds.removed)+len(h.cmds.created) != 0 {
		t.Fatal("a command ran while signed out")
	}
	if _, out := h.do("GET", "/admin/api/session", nil, nil); out["signedIn"] != false {
		t.Fatalf("session: %v", out)
	}
}

func TestSignInListRevokeRemoveCreate(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.do("POST", "/admin/api/login", map[string]string{"password": "wrong-password"}, nil); code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", code)
	}
	h.login()
	if _, out := h.do("GET", "/admin/api/session", nil, nil); out["signedIn"] != true {
		t.Fatalf("session: %v", out)
	}
	if code, out := h.do("GET", "/admin/api/files", nil, nil); code != 200 || len(out["files"].([]any)) != 1 {
		t.Fatalf("files: %d %v", code, out)
	}
	if code, out := h.do("GET", "/admin/api/files/f1/links", nil, nil); code != 200 || len(out["links"].([]any)) != 1 {
		t.Fatalf("links: %d %v", code, out)
	}
	code, out := h.do("POST", "/admin/api/files/f1/links", map[string]string{"access": "view", "label": " Acme "}, nil)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	if b := out["browser"].(string); b != "https://figcraft.example.com/#join=figcraft://join?server=wss://h/&file=f1&token=T" {
		t.Fatalf("browser link: %q", b)
	}
	if code, _ := h.do("DELETE", "/admin/api/files/f1/links/L1", nil, nil); code != 200 {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := h.do("DELETE", "/admin/api/files/f1", nil, nil); code != 200 {
		t.Fatalf("remove: %d", code)
	}
	if strings.Join(h.cmds.created, ",") != "f1/view/Acme" || strings.Join(h.cmds.revoked, ",") != "f1/L1" || strings.Join(h.cmds.removed, ",") != "f1" {
		t.Fatalf("commands: %+v", h.cmds)
	}
	if code, _ := h.do("POST", "/admin/api/files/f1/links", map[string]string{"access": "view", "label": "two\nlines"}, nil); code != http.StatusBadRequest {
		t.Fatalf("multi-line label: %d", code)
	}
	if code, _ := h.do("POST", "/admin/api/logout", nil, nil); code != 200 {
		t.Fatal("logout")
	}
	if code, _ := h.do("GET", "/admin/api/files", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", code)
	}
}

func TestChangesNeedCSRFHeaderAndSameOrigin(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.do("POST", "/admin/api/login", map[string]string{"password": h.pw}, map[string]string{csrfHeader: ""}); code != http.StatusForbidden {
		t.Fatalf("login without header: %d", code)
	}
	h.login()
	if code, _ := h.do("DELETE", "/admin/api/files/f1", nil, map[string]string{csrfHeader: ""}); code != http.StatusForbidden {
		t.Fatalf("delete without header: %d", code)
	}
	if code, _ := h.do("DELETE", "/admin/api/files/f1", nil, map[string]string{"Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("cross-origin delete: %d", code)
	}
	if len(h.cmds.removed) != 0 {
		t.Fatal("a refused request removed a file")
	}
	host := strings.TrimPrefix(h.srv.URL, "http://")
	if code, _ := h.do("DELETE", "/admin/api/files/f1", nil, map[string]string{"Origin": "http://" + host}); code != 200 {
		t.Fatalf("same-origin delete: %d", code)
	}
}

func TestPasswordChangeEndsOtherSessions(t *testing.T) {
	h := newHarness(t)
	h.login()
	other := newClientLike(h)
	if code, _ := h.do("POST", "/admin/api/password", map[string]string{"current": "nope", "new": "a-new-long-password"}, nil); code != http.StatusUnauthorized {
		t.Fatalf("wrong current: %d", code)
	}
	if code, _ := h.do("POST", "/admin/api/password", map[string]string{"current": h.pw, "new": "short"}, nil); code != http.StatusBadRequest {
		t.Fatalf("short new: %d", code)
	}
	if code, out := h.do("POST", "/admin/api/password", map[string]string{"current": h.pw, "new": "a-new-long-password"}, nil); code != 200 {
		t.Fatalf("change: %d %v", code, out)
	}
	if code, _ := h.do("GET", "/admin/api/files", nil, nil); code != 200 {
		t.Fatalf("this session should stay: %d", code)
	}
	if code, _ := other.do("GET", "/admin/api/files", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("the other session should end: %d", code)
	}
	h.pw = "a-new-long-password"
	fresh := newClientLike(h)
	fresh.login()
}

// newClientLike signs in a second browser against the same server.
func newClientLike(h *harness) *harness {
	jar, _ := cookiejar.New(nil)
	o := *h
	o.client = &http.Client{Jar: jar}
	o.login()
	return &o
}

func TestLoginLockout(t *testing.T) {
	h := newHarness(t)
	ip := map[string]string{"CF-Connecting-IP": "203.0.113.9"}
	for i := 0; i < 5; i++ {
		if code, _ := h.do("POST", "/admin/api/login", map[string]string{"password": "guess"}, ip); code != http.StatusUnauthorized {
			t.Fatalf("guess %d: %d", i, code)
		}
	}
	if code, _ := h.do("POST", "/admin/api/login", map[string]string{"password": h.pw}, ip); code != http.StatusTooManyRequests {
		t.Fatalf("locked address with the right password: %d", code)
	}
	// Another address still gets in.
	if code, _ := h.do("POST", "/admin/api/login", map[string]string{"password": h.pw}, map[string]string{"CF-Connecting-IP": "198.51.100.1"}); code != 200 {
		t.Fatalf("other address: %d", code)
	}
}

func TestGlobalLockout(t *testing.T) {
	l := NewLimiter()
	now := time.Unix(1_700_000_000, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 20; i++ {
		l.Failed("10.0.0." + itoa(i)) // one guess each from 20 addresses
	}
	if ok, _ := l.Allowed("192.0.2.1"); ok {
		t.Fatal("global limit didn't lock")
	}
	now = now.Add(16 * time.Minute)
	if ok, _ := l.Allowed("192.0.2.1"); !ok {
		t.Fatal("global lock didn't expire")
	}
}

func TestSessionsExpire(t *testing.T) {
	s := NewSessions()
	now := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return now }
	tok, _ := s.New()
	now = now.Add(time.Hour)
	if !s.Valid(tok) {
		t.Fatal("active session ended")
	}
	now = now.Add(2*time.Hour + time.Minute)
	if s.Valid(tok) {
		t.Fatal("idle session still valid")
	}
}

func TestPasswordStoreAndReset(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "admin")
	p, first, err := OpenPasswords(dir)
	if err != nil || first == "" {
		t.Fatal(err)
	}
	if strings.ContainsAny(first, "0O1lI") {
		t.Fatalf("generated password has look-alike characters: %q", first)
	}
	if !p.Check(first) || p.Check(first+"x") || p.Check("") {
		t.Fatal("check")
	}
	info, err := os.Stat(filepath.Join(dir, "password.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("password file mode: %v %v", info.Mode(), err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "password.json"))
	if strings.Contains(string(b), first) {
		t.Fatal("password stored in clear")
	}
	// Opening again keeps it and prints nothing.
	p2, again, err := OpenPasswords(dir)
	if err != nil || again != "" || !p2.Check(first) {
		t.Fatalf("reopen: %q %v", again, err)
	}
	// A reset by another process takes effect in the running one.
	other := &Passwords{path: filepath.Join(dir, "password.json")}
	fresh, err := other.Reset()
	if err != nil {
		t.Fatal(err)
	}
	if p.Check(first) || !p.Check(fresh) {
		t.Fatal("running process didn't pick up the reset")
	}
}

func TestPagesAndHeaders(t *testing.T) {
	h := newHarness(t)
	res, err := h.client.Get(h.srv.URL + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "FigCraft admin") {
		t.Fatalf("index: %d", res.StatusCode)
	}
	for _, hdr := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options"} {
		if res.Header.Get(hdr) == "" {
			t.Errorf("missing %s", hdr)
		}
	}
	for _, p := range []string{"/admin/admin.js", "/admin/admin.css", "/healthz"} {
		res, err := h.client.Get(h.srv.URL + p)
		if err != nil || res.StatusCode != 200 {
			t.Errorf("%s: %v %v", p, res.StatusCode, err)
		}
		res.Body.Close()
	}
	res, _ = h.client.Get(h.srv.URL + "/admin/ui/index.html")
	if res.StatusCode != 404 {
		t.Errorf("embedded paths shouldn't be served directly: %d", res.StatusCode)
	}
	res.Body.Close()
}
