package render

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedisConfig(t *testing.T) {
	root := t.TempDir()
	err := RedisConfig(root, RedisData{
		Bind:      "127.0.0.1 172.17.0.1",
		MaxMemory: "128mb",
		Password:  "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "config", "redis", "valkey.conf"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	hash := sha256.Sum256([]byte("secret"))
	if strings.Contains(text, ">secret") || strings.Contains(text, "secret") {
		t.Fatal("rendered Redis config contains the plaintext password")
	}
	for _, want := range []string{
		"bind 127.0.0.1 172.17.0.1",
		"appendonly yes",
		"appendfsync everysec",
		"maxmemory 128mb",
		"maxmemory-policy allkeys-lru",
		"user default off",
		"user webdock on #" + hex.EncodeToString(hash[:]) + " ~* &* +@all",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Redis config missing %q", want)
		}
	}
}

func TestComposeUsesDynamicImagesProfilesAndHostNetwork(t *testing.T) {
	root := t.TempDir()
	if err := Compose(root, ComposeData{
		NginxImage:    "example/nginx:1.26",
		PHPImage:      "example/php:8.4",
		RedisImage:    "valkey/valkey:7",
		MySQLImage:    "mysql:8.4",
		PostgresImage: "tensorchord/vchord-suite:pg17-latest",
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`image: "example/nginx:1.26"`,
		`image: "example/php:8.4"`,
		`image: "valkey/valkey:7"`,
		`image: "mysql:8.4"`,
		`image: "tensorchord/vchord-suite:pg17-latest"`,
		"container_name: webdock-nginx",
		"container_name: webdock-redis",
		"network_mode: host",
		"restart: unless-stopped",
		"max-size: 10m",
		`profiles: ["nginx"]`,
		`profiles: ["php"]`,
		`profiles: ["redis"]`,
		`profiles: ["mysql"]`,
		`profiles: ["postgres"]`,
		"NGINX_UI_SERVER_HOST: \"127.0.0.1\"",
		"REDISCLI_AUTH",
		"--user webdock",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Compose config missing %q", want)
		}
	}
	if strings.Contains(text, "webdock-valkey") {
		t.Fatal("legacy valkey service name remains")
	}
}
