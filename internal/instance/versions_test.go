package instance

import (
	"os"
	"strings"
	"testing"
)

func TestVersionStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	want := Values{"REDIS": "7", "MYSQL": "8.4", "POSTGRES": "16"}
	if err := WriteVersionState(root, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadVersionState(root)
	if err != nil {
		t.Fatal(err)
	}
	for key, version := range want {
		if got[key] != version {
			t.Errorf("%s version = %q, want %q", key, got[key], version)
		}
	}
}

func TestVersionStateComponentsComeFromComponentSpecs(t *testing.T) {
	for name, want := range map[string]string{
		ComponentRedis:    "REDIS",
		ComponentMySQL:    "MYSQL",
		ComponentPostgres: "POSTGRES",
	} {
		if got, ok := VersionStateKey(name); !ok || got != want {
			t.Errorf("VersionStateKey(%q) = %q, %v; want %q, true", name, got, ok, want)
		}
	}
	for _, name := range []string{ComponentNginx, ComponentPHP, "unknown"} {
		if _, ok := VersionStateKey(name); ok {
			t.Errorf("VersionStateKey(%q) unexpectedly succeeded", name)
		}
	}
}

func TestVersionStateRejectsUnknownComponentAndInvalidVersion(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string]Values{
		"unknown": {"NGINX": "1.26"},
		"invalid": {"REDIS": "8 0"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := WriteVersionState(root, values); err == nil {
				t.Fatalf("WriteVersionState(%v) succeeded", values)
			}
			if err := os.WriteFile(VersionStatePath(root), RenderVersionState(values), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadVersionState(root); err == nil || !strings.Contains(err.Error(), "data version state") {
				t.Fatalf("LoadVersionState() error = %v", err)
			}
		})
	}
}

func TestValidateLayoutRejectsMalformedVersionState(t *testing.T) {
	root := t.TempDir()
	cfg, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(VersionStatePath(root), []byte("NGINX=1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLayout(cfg); err == nil || !strings.Contains(err.Error(), "unknown component") {
		t.Fatalf("ValidateLayout() error = %v", err)
	}
}
