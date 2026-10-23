package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestUsesOneValuePerComponent(t *testing.T) {
	version := "8.4"
	manifest := Manifest{
		CreatedAt:      "2026-10-23T00:00:00Z",
		WebdockVersion: "0.2.0",
		Components: map[string]*string{
			"nginx":    stringPtr("1.26"),
			"php":      nil,
			"redis":    stringPtr("8"),
			"mysql":    &version,
			"postgres": nil,
		},
	}
	data, err := manifestJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["format"]; ok {
		t.Fatal("manifest contains redundant format field")
	}
	components, ok := raw["components"].(map[string]any)
	if !ok {
		t.Fatalf("components = %#v", raw["components"])
	}
	if components["php"] != nil || components["nginx"] != "1.26" || components["mysql"] != "8.4" {
		t.Fatalf("components = %#v", components)
	}
}

func TestSelectiveRestorePlanPreservesUnselectedComponent(t *testing.T) {
	source := t.TempDir()
	mustWrite(t, filepath.Join(source, "webdock.env"), "new-env\n", 0o600)
	mustWrite(t, filepath.Join(source, "config", "nginx", "nginx.conf"), "new-nginx\n", 0o644)
	mustWrite(t, filepath.Join(source, "config", "mysql", "my.cnf"), "new-mysql\n", 0o644)
	mustWrite(t, filepath.Join(source, "data", "mysql", "archive"), "archive\n", 0o600)
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := Create(source, archive, Options{}, Manifest{CreatedAt: time.Now().UTC().Format(time.RFC3339), Components: map[string]*string{}}); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	mustWrite(t, filepath.Join(target, "webdock.env"), "old-env\n", 0o600)
	mustWrite(t, filepath.Join(target, "config", "nginx", "nginx.conf"), "old-nginx\n", 0o644)
	mustWrite(t, filepath.Join(target, "config", "mysql", "my.cnf"), "old-mysql\n", 0o644)
	mustWrite(t, filepath.Join(target, "data", "mysql", "current"), "current\n", 0o600)
	stage, err := PrepareRestore(target, archive, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := stage.Apply(target, true, RestorePlan{
		Env:     []byte("merged-env\n"),
		Replace: []string{"config/nginx"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(target, "webdock.env")); got != "merged-env\n" {
		t.Fatalf("env = %q", got)
	}
	if got := mustRead(t, filepath.Join(target, "config", "nginx", "nginx.conf")); got != "new-nginx\n" {
		t.Fatalf("nginx = %q", got)
	}
	if got := mustRead(t, filepath.Join(target, "config", "mysql", "my.cnf")); got != "old-mysql\n" {
		t.Fatalf("mysql config = %q", got)
	}
	if got := mustRead(t, filepath.Join(target, "data", "mysql", "current")); got != "current\n" {
		t.Fatalf("mysql data = %q", got)
	}
	if _, err := os.Stat(filepath.Join(target, "data", "mysql", "archive")); !os.IsNotExist(err) {
		t.Fatalf("archive mysql data was restored: %v", err)
	}
}

func TestSelectiveRestoreRejectsSymlinkParent(t *testing.T) {
	source := t.TempDir()
	mustWrite(t, filepath.Join(source, "webdock.env"), "new-env\n", 0o600)
	mustWrite(t, filepath.Join(source, "config", "nginx", "nginx.conf"), "new-nginx\n", 0o644)
	mustWrite(t, filepath.Join(source, "data", "value"), "data\n", 0o600)
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := Create(source, archive, Options{}, Manifest{CreatedAt: time.Now().UTC().Format(time.RFC3339), Components: map[string]*string{}}); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(target, "config")); err != nil {
		t.Fatal(err)
	}
	stage, err := PrepareRestore(target, archive, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	err = stage.Apply(target, true, RestorePlan{Replace: []string{"config/nginx"}})
	if err == nil || !strings.Contains(err.Error(), "traverses symlink") {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "nginx", "nginx.conf")); !os.IsNotExist(err) {
		t.Fatalf("restore escaped through symlink: %v", err)
	}
}

func stringPtr(value string) *string {
	return &value
}
