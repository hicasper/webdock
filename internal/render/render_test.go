package render

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValkeyConfig(t *testing.T) {
	root := t.TempDir()
	err := ValkeyConfig(root, ValkeyData{
		Bind:      "127.0.0.1 172.17.0.1",
		MaxMemory: "128mb",
		Password:  "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "config", "valkey", "valkey.conf"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	hash := sha256.Sum256([]byte("secret"))
	if strings.Contains(text, ">secret") || strings.Contains(text, "secret") {
		t.Fatal("rendered Valkey config contains the plaintext password")
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
			t.Errorf("Valkey config missing %q", want)
		}
	}
}

func TestComposeUsesFixedNamesAndHostNetwork(t *testing.T) {
	root := t.TempDir()
	if err := Compose(root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"image: hicasper/webdock-edge:latest",
		"image: hicasper/webdock-php:8.4",
		"container_name: webdock-edge",
		"network_mode: host",
		"restart: unless-stopped",
		"max-size: 10m",
		`profiles: ["mysql"]`,
		`profiles: ["postgres"]`,
		"NGINX_UI_SERVER_HOST: \"127.0.0.1\"",
		"NGINX_UI_SERVER_RUN_MODE: \"release\"",
		"REDISCLI_AUTH",
		"--user webdock",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Compose config missing %q", want)
		}
	}
	if strings.Contains(text, "live-restore") {
		t.Fatal("unexpected feature")
	}
}
