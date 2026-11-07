package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The figcraft-server binary, run as its own management commands: with a server running on the
// data directory, each one asks it over its port with the server key (from the data directory),
// so the key never leaves the pod. Their output is fixed-width text (apps/figcraft-server/src/cli.rs
// files_table, links_table, new_link), parsed here.
type CLI struct {
	Bin     string
	Config  string
	Timeout time.Duration
}

// IDs of files and links, as figcraft-server makes and accepts them (store.rs valid_id), but never
// starting with "-": they're passed as command-line arguments, where one would read as an option.
var validID = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,63}$`)

var accesses = map[string]bool{"view": true, "edit": true, "comment": true}

type File struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	People  int    `json:"people"`
	Size    string `json:"size"`
	Created string `json:"created"`
	Idle    string `json:"idle"`
	Expires string `json:"expires"`
	Reports int    `json:"reports"`
	By      string `json:"by"`
}

type Link struct {
	ID      string `json:"id"`
	Access  string `json:"access"`
	Created string `json:"created"`
	Label   string `json:"label"`
}

type NewLink struct {
	ID     string `json:"id"`
	Access string `json:"access"`
	File   string `json:"file"`
	Link   string `json:"link"`
	URL    string `json:"url"`
}

// CommandError is a command figcraft-server ran and refused, with its own message.
type CommandError struct{ Message string }

func (e *CommandError) Error() string { return e.Message }

func (c CLI) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Bin, append([]string{"--config", c.Config}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("figcraft-server %s timed out", args[0])
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = strings.TrimSpace(stdout.String())
			}
			if msg == "" {
				msg = exit.Error()
			}
			return "", &CommandError{Message: clip(msg, 500)}
		}
		return "", fmt.Errorf("can't run figcraft-server: %w", err)
	}
	return stdout.String(), nil
}

func (c CLI) Files(ctx context.Context) ([]File, error) {
	out, err := c.run(ctx, "files", "list", "--sort", "idle")
	if err != nil {
		return nil, err
	}
	return parseFiles(out)
}

func (c CLI) Links(ctx context.Context, file string) ([]Link, error) {
	if !validID.MatchString(file) {
		return nil, &CommandError{Message: "not a file ID"}
	}
	out, err := c.run(ctx, "links", "list", file)
	if err != nil {
		return nil, err
	}
	return parseLinks(out)
}

func (c CLI) CreateLink(ctx context.Context, file, access, label string) (NewLink, error) {
	if !validID.MatchString(file) {
		return NewLink{}, &CommandError{Message: "not a file ID"}
	}
	if !accesses[access] {
		return NewLink{}, &CommandError{Message: "access must be view, comment or edit"}
	}
	args := []string{"links", "create", file, "--access", access}
	if label != "" {
		args = append(args, "--label", label)
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return NewLink{}, err
	}
	return parseNewLink(out)
}

func (c CLI) Revoke(ctx context.Context, file, link string) error {
	if !validID.MatchString(file) || !validID.MatchString(link) {
		return &CommandError{Message: "not a file or link ID"}
	}
	_, err := c.run(ctx, "links", "revoke", file, link)
	return err
}

func (c CLI) Remove(ctx context.Context, file string) error {
	if !validID.MatchString(file) {
		return &CommandError{Message: "not a file ID"}
	}
	_, err := c.run(ctx, "files", "remove", file)
	return err
}

// parseFiles reads `files list`: a header, then per file
// "{id:<16}  {name:<30}  {people:>6}  {size:<9}  {created:<10}  {idle:>4}  {expires:<10}  {reports:>7}  {by}"
// (name cut to 30 characters with "…"; size is "12 B", "3.4 KiB" or "1.2 MiB").
func parseFiles(out string) ([]File, error) {
	files := []File{}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || strings.HasPrefix(lines[0], "No files") {
		return files, nil
	}
	if !strings.HasPrefix(lines[0], "ID ") || !strings.Contains(lines[0], "NAME") {
		return nil, fmt.Errorf("unexpected files list header: %q", clip(lines[0], 100))
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f, err := parseFileRow(line)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

func parseFileRow(line string) (File, error) {
	bad := func(why string) (File, error) {
		return File{}, fmt.Errorf("unexpected files list row (%s): %q", why, clip(line, 120))
	}
	id, _, _ := strings.Cut(line, " ")
	if !validID.MatchString(id) {
		return bad("id")
	}
	r := []rune(line)
	// The id column is 16 wide, wider when the id is; two spaces, then the name, 30 wide.
	start := max(16, utf8.RuneCountInString(id)) + 2
	if len(r) < start+30 {
		return bad("short")
	}
	name := strings.TrimSpace(string(r[start : start+30]))
	rest := strings.Fields(string(r[start+30:]))
	if len(rest) != 8 {
		return bad("columns")
	}
	people, err1 := strconv.Atoi(rest[0])
	reports, err2 := strconv.Atoi(rest[6])
	if err1 != nil || err2 != nil {
		return bad("numbers")
	}
	return File{
		ID: id, Name: name, People: people, Size: rest[1] + " " + rest[2], Created: rest[3],
		Idle: rest[4], Expires: rest[5], Reports: reports, By: rest[7],
	}, nil
}

// parseLinks reads `links list FILE`: a header, then per link
// "{id:<16}  {access:<6}  {created:<20}  {label}" (created is "YYYY-MM-DD HH:MM UTC").
func parseLinks(out string) ([]Link, error) {
	links := []Link{}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || strings.HasPrefix(lines[0], "No links") {
		return links, nil
	}
	if !strings.HasPrefix(lines[0], "LINK ") {
		return nil, fmt.Errorf("unexpected links list header: %q", clip(lines[0], 100))
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rest := line
		var tok [5]string
		for i := range tok {
			rest = strings.TrimLeft(rest, " ")
			var ok bool
			tok[i], rest, ok = strings.Cut(rest, " ")
			if tok[i] == "" || (!ok && i < 4) {
				return nil, fmt.Errorf("unexpected links list row: %q", clip(line, 120))
			}
		}
		if !validID.MatchString(tok[0]) || !accesses[tok[1]] || tok[4] != "UTC" {
			return nil, fmt.Errorf("unexpected links list row: %q", clip(line, 120))
		}
		links = append(links, Link{
			ID: tok[0], Access: tok[1], Created: tok[2] + " " + tok[3] + " UTC", Label: strings.TrimSpace(rest),
		})
	}
	return links, nil
}

// parseNewLink reads `links create`:
//
//	New view link <id> for file <file>:
//	  figcraft://join?...
//	  wss://.../?file=...&token=...
//	This is the only time the link is shown ...
func parseNewLink(out string) (NewLink, error) {
	lines := strings.Split(out, "\n")
	var n NewLink
	if len(lines) < 3 {
		return n, fmt.Errorf("unexpected links create output")
	}
	head := strings.Fields(strings.TrimSuffix(strings.TrimSpace(lines[0]), ":"))
	// New <access> link <id> for file <file>
	if len(head) != 7 || head[0] != "New" || head[2] != "link" || head[4] != "for" || head[5] != "file" ||
		!accesses[head[1]] || !validID.MatchString(head[3]) || !validID.MatchString(head[6]) {
		return n, fmt.Errorf("unexpected links create output: %q", clip(lines[0], 100))
	}
	n.Access, n.ID, n.File = head[1], head[3], head[6]
	n.Link = strings.TrimSpace(lines[1])
	n.URL = strings.TrimSpace(lines[2])
	if !strings.HasPrefix(n.Link, "figcraft://join?") || !(strings.HasPrefix(n.URL, "ws://") || strings.HasPrefix(n.URL, "wss://")) {
		return NewLink{}, fmt.Errorf("unexpected links create output")
	}
	return n, nil
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
