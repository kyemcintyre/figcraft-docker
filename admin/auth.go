package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The admin password, kept as a PBKDF2-SHA256 hash in <dir>/password.json (0600, on the data
// volume, so it survives restarts). Made at random on first start and printed once in the log.

const (
	pbkdf2Iterations = 600_000
	minPasswordLen   = 12
	maxPasswordLen   = 256
	// No look-alike characters, so it can be typed from the log.
	passwordAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	generatedLen     = 24
)

type storedPassword struct {
	Algo       string `json:"algo"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	Hash       string `json:"hash"`
	Changed    string `json:"changed"`
}

type Passwords struct {
	path string
	mu   sync.Mutex
	cur  storedPassword
}

// OpenPasswords loads the password, or makes one when there's none yet (returned once, for the
// log; "" when one exists).
func OpenPasswords(dir string) (*Passwords, string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("can't make %s: %w", dir, err)
	}
	p := &Passwords{path: filepath.Join(dir, "password.json")}
	b, err := os.ReadFile(p.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &p.cur); err != nil || p.cur.Algo != "pbkdf2-sha256" || p.cur.Iterations < 100_000 {
			return nil, "", fmt.Errorf("%s is damaged: run reset-password to make a new one", p.path)
		}
		return p, "", nil
	case errors.Is(err, os.ErrNotExist):
		pw, err := p.Reset()
		return p, pw, err
	default:
		return nil, "", fmt.Errorf("can't read %s: %w", p.path, err)
	}
}

// Reset makes and stores a new random password and returns it.
func (p *Passwords) Reset() (string, error) {
	pw, err := randomPassword()
	if err != nil {
		return "", err
	}
	return pw, p.set(pw)
}

// Check reports whether pw is the password, taking the same time either way. The stored hash is
// read again first, so `figcraft-admin reset-password` (another process) takes effect at once.
func (p *Passwords) Check(pw string) bool {
	p.reload()
	p.mu.Lock()
	cur := p.cur
	p.mu.Unlock()
	if len(pw) > maxPasswordLen {
		pw = pw[:maxPasswordLen]
	}
	salt, err1 := base64.StdEncoding.DecodeString(cur.Salt)
	want, err2 := base64.StdEncoding.DecodeString(cur.Hash)
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, cur.Iterations, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// reload reads the stored hash, keeping the one in memory if the file can't be read.
func (p *Passwords) reload() {
	b, err := os.ReadFile(p.path)
	if err != nil {
		return
	}
	var s storedPassword
	if json.Unmarshal(b, &s) != nil || s.Algo != "pbkdf2-sha256" || s.Iterations < 100_000 {
		return
	}
	p.mu.Lock()
	p.cur = s
	p.mu.Unlock()
}

// Change sets a new password the person chose.
func (p *Passwords) Change(pw string) error {
	if n := len([]rune(pw)); n < minPasswordLen || len(pw) > maxPasswordLen {
		return fmt.Errorf("the new password must be %d to %d characters", minPasswordLen, maxPasswordLen)
	}
	return p.set(pw)
}

func (p *Passwords) set(pw string) error {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash, err := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iterations, 32)
	if err != nil {
		return err
	}
	s := storedPassword{
		Algo: "pbkdf2-sha256", Iterations: pbkdf2Iterations,
		Salt: base64.StdEncoding.EncodeToString(salt), Hash: base64.StdEncoding.EncodeToString(hash),
		Changed: time.Now().UTC().Format(time.RFC3339),
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("can't save the password: %w", err)
	}
	if err := os.Rename(tmp, p.path); err != nil {
		return fmt.Errorf("can't save the password: %w", err)
	}
	p.mu.Lock()
	p.cur = s
	p.mu.Unlock()
	return nil
}

func randomPassword() (string, error) {
	out := make([]byte, generatedLen)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = passwordAlphabet[n.Int64()]
	}
	return string(out), nil
}

// Sessions are random tokens kept in memory: a restart signs everyone out. Changing the password
// ends every other session.
type Sessions struct {
	mu       sync.Mutex
	byToken  map[string]session
	now      func() time.Time
	lifetime time.Duration
	idle     time.Duration
}

type session struct {
	created, seen time.Time
}

const maxSessions = 20

func NewSessions() *Sessions {
	return &Sessions{byToken: map[string]session{}, now: time.Now, lifetime: 12 * time.Hour, idle: 2 * time.Hour}
}

func (s *Sessions) New() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	if len(s.byToken) >= maxSessions {
		var oldest string
		for t, v := range s.byToken {
			if oldest == "" || v.seen.Before(s.byToken[oldest].seen) {
				oldest = t
			}
		}
		delete(s.byToken, oldest)
	}
	now := s.now()
	s.byToken[tok] = session{created: now, seen: now}
	return tok, nil
}

// Valid checks a token and counts the request as activity.
func (s *Sessions) Valid(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.byToken[tok]
	now := s.now()
	if !ok || now.Sub(v.created) > s.lifetime || now.Sub(v.seen) > s.idle {
		delete(s.byToken, tok)
		return false
	}
	v.seen = now
	s.byToken[tok] = v
	return true
}

func (s *Sessions) End(tok string) {
	s.mu.Lock()
	delete(s.byToken, tok)
	s.mu.Unlock()
}

// EndOthers ends every session but keep.
func (s *Sessions) EndOthers(keep string) {
	s.mu.Lock()
	for t := range s.byToken {
		if t != keep {
			delete(s.byToken, t)
		}
	}
	s.mu.Unlock()
}

func (s *Sessions) prune() {
	now := s.now()
	for t, v := range s.byToken {
		if now.Sub(v.created) > s.lifetime || now.Sub(v.seen) > s.idle {
			delete(s.byToken, t)
		}
	}
}

// Limiter slows password guessing: an address is locked out after 5 wrong passwords within 15
// minutes, and every address after 20 (so many addresses can't share the work). Both locks last
// 15 minutes. A right password clears its address.
type Limiter struct {
	mu        sync.Mutex
	now       func() time.Time
	perAddr   map[string]*failures
	global    failures
	addrLimit int
	allLimit  int
	window    time.Duration
}

type failures struct {
	times  []time.Time
	locked time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{now: time.Now, perAddr: map[string]*failures{}, addrLimit: 5, allLimit: 20, window: 15 * time.Minute}
}

// Allowed reports whether addr may try a password now, and if not, until when.
func (l *Limiter) Allowed(addr string) (bool, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Before(l.global.locked) {
		return false, l.global.locked
	}
	if f := l.perAddr[addr]; f != nil && now.Before(f.locked) {
		return false, f.locked
	}
	return true, time.Time{}
}

func (l *Limiter) Failed(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.perAddr) > 10_000 {
		l.perAddr = map[string]*failures{} // bounded memory; the global limit still holds
	}
	f := l.perAddr[addr]
	if f == nil {
		f = &failures{}
		l.perAddr[addr] = f
	}
	for _, x := range []*failures{f, &l.global} {
		x.times = append(recent(x.times, now, l.window), now)
	}
	if len(f.times) >= l.addrLimit {
		f.locked, f.times = now.Add(l.window), nil
	}
	if len(l.global.times) >= l.allLimit {
		l.global.locked, l.global.times = now.Add(l.window), nil
	}
}

func (l *Limiter) Succeeded(addr string) {
	l.mu.Lock()
	delete(l.perAddr, addr)
	l.mu.Unlock()
}

func recent(times []time.Time, now time.Time, window time.Duration) []time.Time {
	out := times[:0]
	for _, t := range times {
		if now.Sub(t) < window {
			out = append(out, t)
		}
	}
	return out
}
