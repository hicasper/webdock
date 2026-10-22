package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestCreateDefaultAndOptionalEntries(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "webdock.env"), "DB_ENGINE=none\n", 0o600)
	mustWrite(t, filepath.Join(root, "config", "nginx", "nginx.conf"), "events {}\n", 0o644)
	mustWrite(t, filepath.Join(root, "data", "valkey", "appendonly.aof"), "data", 0o600)
	mustWrite(t, filepath.Join(root, "sites", "app", "index.php"), "<?php\n", 0o644)
	mustWrite(t, filepath.Join(root, "logs", "nginx", "error.log"), "warning\n", 0o644)

	archive := filepath.Join(root, "backups", "default.tar.gz")
	if err := Create(root, archive, Options{}); err != nil {
		t.Fatal(err)
	}
	entries := tarEntries(t, archive)
	for _, want := range []string{"webdock.env", "config", "config/nginx", "config/nginx/nginx.conf", "data", "data/valkey/appendonly.aof"} {
		if !entries[want] {
			t.Errorf("default backup missing %q: %v", want, keys(entries))
		}
	}
	for _, unexpected := range []string{"sites", "sites/app/index.php", "logs", "logs/nginx/error.log"} {
		if entries[unexpected] {
			t.Errorf("default backup unexpectedly contains %q", unexpected)
		}
	}

	archive = filepath.Join(root, "backups", "full.tar.gz")
	if err := Create(root, archive, Options{WithSites: true, WithLogs: true}); err != nil {
		t.Fatal(err)
	}
	entries = tarEntries(t, archive)
	for _, want := range []string{"sites/app/index.php", "logs/nginx/error.log"} {
		if !entries[want] {
			t.Errorf("full backup missing %q", want)
		}
	}
}

func TestCreateDoesNotOverwriteExistingArchive(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "webdock.env"), "one\n", 0o600)
	mustWrite(t, filepath.Join(root, "config", "a"), "one\n", 0o644)
	mustWrite(t, filepath.Join(root, "data", "a"), "one\n", 0o600)
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := Create(root, archive, Options{}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "webdock.env"), "two\n", 0o600)
	if err := Create(root, archive, Options{}); err == nil {
		t.Fatal("Create overwrote an existing archive")
	}
}

func TestRestoreRoundTripAndForce(t *testing.T) {
	source := t.TempDir()
	mustWrite(t, filepath.Join(source, "webdock.env"), "DB_ENGINE=none\n", 0o600)
	mustWrite(t, filepath.Join(source, "config", "nginx", "nginx.conf"), "old\n", 0o644)
	mustWrite(t, filepath.Join(source, "config", "nginx", "sites-available", "000-default"), "site\n", 0o644)
	mustWrite(t, filepath.Join(source, "data", "valkey", "dump.rdb"), "state\n", 0o600)
	if err := os.Symlink("../nginx.conf", filepath.Join(source, "config", "nginx", "alias.conf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../sites-available", filepath.Join(source, "config", "nginx", "sites-enabled")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "data", "mysql"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/var/run/mysqld/mysqld.sock", filepath.Join(source, "data", "mysql", "mysql.sock")); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := Create(source, archive, Options{}); err != nil {
		t.Fatal(err)
	}
	if entries := tarEntries(t, archive); entries["data/mysql/mysql.sock"] {
		t.Fatal("backup included runtime MySQL socket symlink")
	}

	target := t.TempDir()
	if err := Restore(target, archive, false); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(target, "config", "nginx", "nginx.conf")); got != "old\n" {
		t.Fatalf("restored config = %q", got)
	}
	if link, err := os.Readlink(filepath.Join(target, "config", "nginx", "alias.conf")); err != nil || link != "../nginx.conf" {
		t.Fatalf("restored symlink = %q, %v", link, err)
	}
	if link, err := os.Readlink(filepath.Join(target, "config", "nginx", "sites-enabled")); err != nil || link != "../sites-available" {
		t.Fatalf("restored directory symlink = %q, %v", link, err)
	}
	if got := mustRead(t, filepath.Join(target, "config", "nginx", "sites-available", "000-default")); got != "site\n" {
		t.Fatalf("restored symlinked site = %q", got)
	}
	info, err := os.Stat(filepath.Join(target, "webdock.env"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("webdock.env mode = %04o", info.Mode().Perm())
	}
	if err := Restore(target, archive, false); !errors.Is(err, ErrForceRequired) {
		t.Fatalf("restore without force = %v, want ErrForceRequired", err)
	}
	if err := Restore(target, archive, true); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRejectsPathTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "bad.tar.gz")
	writeTar(t, archive, []tarEntry{
		{name: "../escaped", body: "bad"},
	})
	target := t.TempDir()
	if err := Restore(target, archive, false); err == nil {
		t.Fatal("Restore accepted path traversal")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), "escaped")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("escaped file exists: %v", err)
	}
}

func TestRestoreRejectsEscapingSymlink(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "bad.tar.gz")
	writeTar(t, archive, []tarEntry{
		{name: "config/link", link: "../../outside", typeflag: tar.TypeSymlink},
	})
	if err := Restore(t.TempDir(), archive, false); err == nil {
		t.Fatal("Restore accepted escaping symlink")
	}
}

func TestRestoreRejectsSymlinkParentTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "bad.tar.gz")
	writeTar(t, archive, []tarEntry{
		{name: "config/link", link: "data", typeflag: tar.TypeSymlink},
		{name: "config/link/escaped", body: "bad"},
	})
	if err := Restore(t.TempDir(), archive, false); err == nil {
		t.Fatal("Restore accepted a path through a symlink parent")
	}
}

func TestRestoreRejectsMalformedRequiredTree(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "bad.tar.gz")
	writeTar(t, archive, []tarEntry{
		{name: "webdock.env", body: "DB_ENGINE=none\n"},
		{name: "config", body: "not a directory"},
		{name: "data", typeflag: tar.TypeDir},
	})
	if err := Restore(t.TempDir(), archive, false); err == nil || !strings.Contains(err.Error(), "config is not a directory") {
		t.Fatalf("Restore() error = %v, want malformed config error", err)
	}
}

func TestRestoreValidatedRejectsBeforeReplacingData(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	writeTar(t, archive, []tarEntry{
		{name: "webdock.env", body: "new\n"},
		{name: "config/value", body: "new\n"},
		{name: "data/value", body: "new\n"},
	})
	target := t.TempDir()
	mustWrite(t, filepath.Join(target, "webdock.env"), "old\n", 0o600)
	mustWrite(t, filepath.Join(target, "config", "value"), "old\n", 0o644)
	mustWrite(t, filepath.Join(target, "data", "value"), "old\n", 0o600)
	wantErr := errors.New("invalid staged instance")
	if err := RestoreValidated(target, archive, true, func(string) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("RestoreValidated() error = %v, want %v", err, wantErr)
	}
	if got := mustRead(t, filepath.Join(target, "webdock.env")); got != "old\n" {
		t.Fatalf("webdock.env was replaced with %q", got)
	}
	if got := mustRead(t, filepath.Join(target, "config", "value")); got != "old\n" {
		t.Fatalf("config/value was replaced with %q", got)
	}
	if got := mustRead(t, filepath.Join(target, "data", "value")); got != "old\n" {
		t.Fatalf("data/value was replaced with %q", got)
	}
}

type tarEntry struct {
	name     string
	body     string
	link     string
	typeflag byte
}

func writeTar(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: 0o644, Typeflag: typeflag, Linkname: entry.link, Size: int64(len(entry.body))}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := io.WriteString(tw, entry.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func tarEntries(t *testing.T, path string) map[string]bool {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	result := make(map[string]bool)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return result
		}
		if err != nil {
			t.Fatal(err)
		}
		result[header.Name] = true
	}
}

func mustWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func keys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
