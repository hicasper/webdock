package instance

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesUsableLayout(t *testing.T) {
	root := t.TempDir()
	cfg, err := Init(root, "none")
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if cfg.Root != root {
		t.Fatalf("Root = %q, want %q", cfg.Root, root)
	}
	envInfo, err := os.Stat(filepath.Join(root, "webdock.env"))
	if err != nil {
		t.Fatal(err)
	}
	if got := envInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("webdock.env mode = %04o, want 0600", got)
	}
	for _, path := range []string{
		"compose.yaml",
		"config/nginx/nginx.conf",
		"config/nginx/modules-available/05-mod-http-ndk.conf",
		"config/nginx/modules-enabled/10-mod-http-geoip2.conf",
		"config/nginx/modules-enabled/20-mod-stream.conf",
		"config/nginx/modules-enabled/30-mod-stream-geoip2.conf",
		"config/nginx/modules-available/40-mod-rtmp.conf",
		"config/nginx/sites-enabled/000-default",
		"config/nginx/ssl/default.crt",
		"config/nginx/ssl/default.key",
		"config/php/php.ini",
		"config/valkey/valkey.conf",
		"logs/nginx/access.log",
		"logs/nginx/error.log",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
	if err := ValidateLayout(cfg); err != nil {
		t.Fatalf("ValidateLayout() error = %v", err)
	}

	enabled, err := os.Readlink(filepath.Join(root, "config/nginx/modules-enabled/20-mod-stream.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if enabled != "../modules-available/20-mod-stream.conf" {
		t.Fatalf("module symlink = %q", enabled)
	}
	if _, err := os.Lstat(filepath.Join(root, "config/nginx/modules-enabled/40-mod-rtmp.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RTMP must not be enabled by default, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "config/nginx/modules-enabled/05-mod-http-ndk.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("NDK must be available but not enabled by default, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "data", "logs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logs must not be stored under data, got %v", err)
	}
	for _, path := range []string{"config/postgres", "logs/php", "logs/valkey", "logs/mysql", "logs/postgres", "logs/nginx-ui"} {
		if _, err := os.Stat(filepath.Join(root, path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unused directory %s was created, got %v", path, err)
		}
	}

	valkey := readFile(t, filepath.Join(root, "config/valkey/valkey.conf"))
	if !strings.Contains(valkey, "bind 127.0.0.1\n") {
		t.Fatalf("initial Valkey bind = %q", valkey)
	}
	if strings.Contains(valkey, "0.0.0.0") {
		t.Fatal("Valkey must not bind 0.0.0.0")
	}
	if !strings.Contains(valkey, "maxmemory-policy allkeys-lru") {
		t.Fatal("Valkey LRU policy missing")
	}

	keyPEM := []byte(readFile(t, filepath.Join(root, "config/nginx/ssl/default.key")))
	block, _ := pem.Decode(keyPEM)
	if block == nil || block.Type != "RSA PRIVATE KEY" {
		t.Fatalf("unexpected private key PEM")
	}
	if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		t.Fatalf("parse private key: %v", err)
	}
}

func TestInitRejectsExistingInstance(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root, "mysql"); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second Init() error = %v, want ErrAlreadyInitialized", err)
	}
}

func TestLoadRejectsInsecureEnvPermissions(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "webdock.env"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "require 0600") {
		t.Fatalf("Load() error = %v, want permission error", err)
	}
}

func TestParseEnvRejectsDuplicatesAndInvalidLines(t *testing.T) {
	for _, input := range []string{
		"A=1\nA=2\n",
		"bad line\n",
		"not-valid=value\n",
	} {
		if _, err := ParseEnv([]byte(input)); err == nil {
			t.Fatalf("ParseEnv(%q) succeeded", input)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSetDBEnginePreservesSecretsAndPermissions(t *testing.T) {
	root := t.TempDir()
	cfg, err := Init(root, "none")
	if err != nil {
		t.Fatal(err)
	}
	beforeSecret := cfg.Values["VALKEY_PASSWORD"]
	next, err := SetDBEngine(root, "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if next.DBEngine() != "mysql" {
		t.Fatalf("DB engine = %q", next.DBEngine())
	}
	if next.Values["VALKEY_PASSWORD"] != beforeSecret {
		t.Fatal("Valkey secret changed")
	}
	info, err := os.Stat(filepath.Join(root, "webdock.env"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("webdock.env mode = %04o, want 0600", got)
	}
}
