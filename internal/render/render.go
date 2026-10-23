package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"webdock/templates"
)

type RedisData struct {
	Bind      string
	MaxMemory string
	Password  string
}

type ComposeData struct {
	NginxImage    string
	PHPImage      string
	RedisImage    string
	MySQLImage    string
	PostgresImage string
}

func Compose(root string, data ComposeData) error {
	raw, err := templates.Read("compose/compose.yaml.tmpl")
	if err != nil {
		return err
	}
	tmpl, err := template.New("compose").Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return fmt.Errorf("parse Compose template: %w", err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return fmt.Errorf("render Compose: %w", err)
	}
	return WriteFile(filepath.Join(root, "compose.yaml"), out.Bytes(), 0o644)
}

func RedisConfig(root string, data RedisData) error {
	if data.Password == "" {
		return fmt.Errorf("render Redis config: password is empty")
	}
	raw, err := templates.Read("valkey/valkey.conf.tmpl")
	if err != nil {
		return err
	}
	tmpl, err := template.New("valkey").Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return fmt.Errorf("parse Redis template: %w", err)
	}
	hash := sha256.Sum256([]byte(data.Password))
	templateData := struct {
		Bind         string
		MaxMemory    string
		PasswordHash string
	}{
		Bind:         data.Bind,
		MaxMemory:    data.MaxMemory,
		PasswordHash: hex.EncodeToString(hash[:]),
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, templateData); err != nil {
		return fmt.Errorf("render Redis config: %w", err)
	}
	return WriteFile(filepath.Join(root, "config", "redis", "valkey.conf"), out.Bytes(), 0o644)
}

// WriteFile replaces path atomically while preserving the previous file if any
// step fails. The file is created with mode.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temporary file for %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary file for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	cleanup = false
	return nil
}
