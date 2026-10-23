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
	cfg, err := Init(root)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if cfg.Root != root {
		t.Fatalf("Root = %q, want %q", cfg.Root, root)
	}
	if got := cfg.EnabledComponents(); len(got) != 1 || got[0] != ComponentNginx {
		t.Fatalf("enabled components = %v, want [nginx]", got)
	}
	envInfo, err := os.Stat(filepath.Join(root, "webdock.env"))
	if err != nil {
		t.Fatal(err)
	}
	if got := envInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("webdock.env mode = %04o, want 0600", got)
	}
	env := readFile(t, filepath.Join(root, "webdock.env"))
	for _, want := range []string{
		"ENABLE_NGINX=true\n",
		"ENABLE_PHP=false\n",
		"ENABLE_REDIS=false\n",
		"ENABLE_MYSQL=false\n",
		"ENABLE_POSTGRES=false\n",
		"# Verified defaults/versions:",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("webdock.env missing %q", want)
		}
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
		"config/redis/valkey.conf",
		"data/.webdock-versions",
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
	for _, path := range []string{"config/valkey", "data/valkey", "config/postgres", "logs/php", "logs/redis", "logs/mysql", "logs/postgres", "logs/nginx-ui"} {
		if _, err := os.Stat(filepath.Join(root, path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unused directory %s was created, got %v", path, err)
		}
	}

	redisConfig := readFile(t, filepath.Join(root, "config/redis/valkey.conf"))
	if !strings.Contains(redisConfig, "bind 127.0.0.1\n") {
		t.Fatalf("initial Redis bind = %q", redisConfig)
	}
	if strings.Contains(redisConfig, "0.0.0.0") {
		t.Fatal("Redis must not bind 0.0.0.0")
	}
	if !strings.Contains(redisConfig, "maxmemory-policy allkeys-lru") {
		t.Fatal("Redis LRU policy missing")
	}

	compose := readFile(t, filepath.Join(root, "compose.yaml"))
	for _, want := range []string{
		`image: "hicasper/webdock:nginx-1.26"`,
		`image: "valkey/valkey:8"`,
		`profiles: ["nginx"]`,
		`profiles: ["redis"]`,
		"container_name: webdock-redis",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose.yaml missing %q", want)
		}
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
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second Init() error = %v, want ErrAlreadyInitialized", err)
	}
}

func TestLoadRejectsInsecureEnvPermissions(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
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

func TestResolveComponentImagesAndDependencies(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		version string
		image   string
	}{
		{ComponentNginx, "true", "1.26", "hicasper/webdock:nginx-1.26"},
		{ComponentPHP, "8.4", "8.4", "hicasper/webdock:php-8.4"},
		{ComponentRedis, "7", "7", "valkey/valkey:7"},
		{ComponentMySQL, "8.4", "8.4", "mysql:8.4"},
		{ComponentPostgres, "17", "17", "tensorchord/vchord-suite:pg17-latest"},
	}
	for _, tt := range tests {
		component, err := ResolveComponent(tt.name, tt.setting)
		if err != nil {
			t.Fatal(err)
		}
		if component.Version != tt.version || component.Image != tt.image {
			t.Fatalf("%s = %+v", tt.name, component)
		}
	}
	if _, err := ResolveComponent(ComponentMySQL, "bad/version"); err == nil {
		t.Fatal("invalid version was accepted")
	}

	values := Values{
		"ENABLE_NGINX":    "false",
		"ENABLE_PHP":      "true",
		"ENABLE_REDIS":    "false",
		"ENABLE_MYSQL":    "false",
		"ENABLE_POSTGRES": "false",
	}
	if _, err := ParseComponents(values); err == nil || !strings.Contains(err.Error(), "php requires nginx") {
		t.Fatalf("ParseComponents() error = %v", err)
	}
}

func TestSetComponentStatePreservesSecretsAndPermissions(t *testing.T) {
	root := t.TempDir()
	cfg, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	beforeSecret := cfg.Values["REDIS_PASSWORD"]
	next, err := SetComponentState(root, ComponentMySQL, "8.4")
	if err != nil {
		t.Fatal(err)
	}
	mysql, ok := next.Component(ComponentMySQL)
	if !ok || !mysql.Enabled || mysql.Version != "8.4" {
		t.Fatalf("mysql component = %+v", mysql)
	}
	if next.Values["REDIS_PASSWORD"] != beforeSecret {
		t.Fatal("Redis secret changed")
	}
	info, err := os.Stat(filepath.Join(root, "webdock.env"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("webdock.env mode = %04o, want 0600", got)
	}
	if _, err := SetComponentState(root, ComponentPHP, "true"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetComponentState(root, ComponentNginx, "false"); err == nil {
		t.Fatal("disabled nginx while php enabled")
	}
}

func TestDisabledComponentLayoutIsNotRequired(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "config", "mysql", "my.cnf")); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateLayout(cfg); err != nil {
		t.Fatalf("disabled MySQL config blocked validation: %v", err)
	}
	if _, err := SetComponentState(root, ComponentMySQL, "true"); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateLayout(cfg); err == nil {
		t.Fatal("enabled MySQL accepted a missing config")
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
