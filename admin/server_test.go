package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fileRow formats a row the way figcraft-server's files_table does:
// "{:<16}  {:<30}  {:>6}  {:<9}  {:<10}  {:>4}  {:<10}  {:>7}  {}" (widths in characters).
func fileRow(id, name string, people int, size, created, idle, expires string, reports int, by string) string {
	pad := func(s string, n int) string {
		if c := len([]rune(s)); c < n {
			return s + strings.Repeat(" ", n-c)
		}
		return s
	}
	return fmt.Sprintf("%s  %s  %6d  %s  %s  %4s  %s  %7d  %s",
		pad(id, 16), pad(name, 30), people, pad(size, 9), pad(created, 10), idle, pad(expires, 10), reports, by)
}

const filesHeader = "ID                NAME                            PEOPLE  SIZE       CREATED     IDLE  EXPIRES     REPORTS  BY"

func TestParseFiles(t *testing.T) {
	long := "Kaz N Brad wedding stationery…" // already cut to 30 by the server
	if len([]rune(long)) != 30 {
		t.Fatalf("fixture name is %d characters", len([]rune(long)))
	}
	out := strings.Join([]string{
		filesHeader,
		fileRow("a1b2c3d4e5f6a7b8", "Homepage", 2, "1.2 MiB", "2026-11-07", "0d", "never", 0, "key"),
		fileRow("x9", "Brand  kit", 0, "512 B", "2026-11-01", "6d", "never", 1, "key"),
		fileRow("abcdefghijklmnopqrstuvwxyz012345", long, 13, "88.0 KiB", "2026-10-20", "18d", "2026-12-01", 0, "203.0.113.5"),
	}, "\n") + "\n"
	files, err := parseFiles(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []File{
		{ID: "a1b2c3d4e5f6a7b8", Name: "Homepage", People: 2, Size: "1.2 MiB", Created: "2026-11-07", Idle: "0d", Expires: "never", Reports: 0, By: "key"},
		{ID: "x9", Name: "Brand  kit", People: 0, Size: "512 B", Created: "2026-11-01", Idle: "6d", Expires: "never", Reports: 1, By: "key"},
		{ID: "abcdefghijklmnopqrstuvwxyz012345", Name: long, People: 13, Size: "88.0 KiB", Created: "2026-10-20", Idle: "18d", Expires: "2026-12-01", Reports: 0, By: "203.0.113.5"},
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %+v", len(files), len(want), files)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Errorf("file %d:\n got %+v\nwant %+v", i, files[i], want[i])
		}
	}
}

func TestParseFilesEmptyAndBad(t *testing.T) {
	files, err := parseFiles("No files yet (publish one from FigCraft: File > Publish to server…).\n")
	if err != nil || len(files) != 0 {
		t.Fatalf("empty: %v %v", files, err)
	}
	if _, err := parseFiles("something else\n"); err == nil {
		t.Fatal("unexpected header accepted")
	}
	if _, err := parseFiles(filesHeader + "\nshort row\n"); err == nil {
		t.Fatal("short row accepted")
	}
	if _, err := parseFiles(filesHeader + "\n" + fileRow("bad/id", "x", 0, "1 B", "2026-01-01", "0d", "never", 0, "key") + "\n"); err == nil {
		t.Fatal("bad id accepted")
	}
}

func TestParseLinks(t *testing.T) {
	out := "LINK              ACCESS  CREATED               LABEL\n" +
		fmt.Sprintf("%-16s  %-6s  %-20s  %s\n", "Lk1", "edit", "2026-11-07 12:10 UTC", "Published (edit)") +
		fmt.Sprintf("%-16s  %-6s  %-20s  %s\n", "Lk2", "view", "2026-11-07 12:10 UTC", "Published (view)") +
		fmt.Sprintf("%-16s  %-6s  %-20s  %s\n", "Lk3-long-identifier-xyz", "comment", "2026-11-08 01:02 UTC", "") +
		fmt.Sprintf("%-16s  %-6s  %-20s  %s\n", "Lk4", "view", "2026-11-08 01:03 UTC", "Acme   client  ")
	links, err := parseLinks(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []Link{
		{ID: "Lk1", Access: "edit", Created: "2026-11-07 12:10 UTC", Label: "Published (edit)"},
		{ID: "Lk2", Access: "view", Created: "2026-11-07 12:10 UTC", Label: "Published (view)"},
		{ID: "Lk3-long-identifier-xyz", Access: "comment", Created: "2026-11-08 01:02 UTC", Label: ""},
		{ID: "Lk4", Access: "view", Created: "2026-11-08 01:03 UTC", Label: "Acme   client"},
	}
	if len(links) != len(want) {
		t.Fatalf("got %d links: %+v", len(links), links)
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("link %d:\n got %+v\nwant %+v", i, links[i], want[i])
		}
	}
	empty, err := parseLinks("No links (make one with `figcraft-server links create FILE --access view`).\n")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty: %v %v", empty, err)
	}
	if _, err := parseLinks("LINK  ACCESS\nLk1  owner  2026-11-07 12:10 UTC  x\n"); err == nil {
		t.Fatal("unknown access accepted")
	}
}

func TestParseNewLink(t *testing.T) {
	out := "New view link Lk9 for file a1b2c3d4e5f6a7b8:\n" +
		"  figcraft://join?server=wss://figcraft.example.com:443/&file=a1b2c3d4e5f6a7b8&token=SECRET\n" +
		"  wss://figcraft.example.com:443/?file=a1b2c3d4e5f6a7b8&token=SECRET\n" +
		"This is the only time the link is shown (the server keeps only a hash of its token).\n"
	n, err := parseNewLink(out)
	if err != nil {
		t.Fatal(err)
	}
	want := NewLink{ID: "Lk9", Access: "view", File: "a1b2c3d4e5f6a7b8",
		Link: "figcraft://join?server=wss://figcraft.example.com:443/&file=a1b2c3d4e5f6a7b8&token=SECRET",
		URL:  "wss://figcraft.example.com:443/?file=a1b2c3d4e5f6a7b8&token=SECRET"}
	if n != want {
		t.Fatalf("got %+v", n)
	}
	if _, err := parseNewLink("nonsense\n"); err == nil {
		t.Fatal("nonsense accepted")
	}
}

// fakeServer writes a stand-in figcraft-server that records its arguments and answers from a file.
func fakeServer(t *testing.T, stdout string, exit int) (CLI, string) {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	out := filepath.Join(dir, "out")
	if err := os.WriteFile(out, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nif [ %d -ne 0 ]; then echo 'figcraft-server: no file abc' >&2; exit %d; fi\ncat %q\n", args, exit, exit, out)
	bin := filepath.Join(dir, "figcraft-server")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return CLI{Bin: bin, Config: "/etc/server.toml", Timeout: 5 * time.Second}, args
}

func readArgs(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func TestCLIArgumentsAndErrors(t *testing.T) {
	ctx := context.Background()
	c, args := fakeServer(t, "No links (none).\n", 0)
	if _, err := c.Links(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(readArgs(t, args), " "); got != "--config /etc/server.toml links list abc" {
		t.Fatalf("args: %q", got)
	}

	c, args = fakeServer(t, "New edit link L1 for file abc:\n  figcraft://join?x\n  wss://h/?x\nThis is the only time.\n", 0)
	if _, err := c.CreateLink(ctx, "abc", "edit", "Acme client"); err != nil {
		t.Fatal(err)
	}
	if got := readArgs(t, args); strings.Join(got, "|") != "--config|/etc/server.toml|links|create|abc|--access|edit|--label|Acme client" {
		t.Fatalf("args: %q", got)
	}

	c, _ = fakeServer(t, "", 1)
	err := c.Remove(ctx, "abc")
	var ce *CommandError
	if !errors.As(err, &ce) || !strings.Contains(ce.Message, "no file abc") {
		t.Fatalf("want the server's message, got %v", err)
	}

	// IDs are checked before anything runs.
	c, args = fakeServer(t, "", 0)
	for _, bad := range []string{"", "../x", "a b", "--access", strings.Repeat("a", 65)} {
		if err := c.Revoke(ctx, bad, "L1"); !errors.As(err, &ce) {
			t.Errorf("file %q: %v", bad, err)
		}
		if err := c.Remove(ctx, bad); !errors.As(err, &ce) {
			t.Errorf("remove %q: %v", bad, err)
		}
	}
	if _, err := c.CreateLink(ctx, "abc", "owner", ""); !errors.As(err, &ce) {
		t.Errorf("access owner: %v", err)
	}
	if _, err := os.Stat(args); err == nil {
		t.Error("figcraft-server ran for an invalid request")
	}
}
