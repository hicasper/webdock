package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Manifest struct {
	CreatedAt      string             `json:"created_at"`
	WebdockVersion string             `json:"webdock_version,omitempty"`
	Components     map[string]*string `json:"components"`
}

func (m Manifest) Validate(componentNames []string) error {
	if strings.TrimSpace(m.CreatedAt) == "" {
		return fmt.Errorf("manifest created_at is empty")
	}
	if m.Components == nil {
		return fmt.Errorf("manifest components are missing")
	}
	for _, name := range componentNames {
		version, ok := m.Components[name]
		if !ok {
			return fmt.Errorf("manifest is missing component %s", name)
		}
		if version != nil && strings.TrimSpace(*version) == "" {
			return fmt.Errorf("manifest component %s has an empty version", name)
		}
	}
	return nil
}

func LoadManifest(stage string) (Manifest, error) {
	path := filepath.Join(stage, "manifest.json")
	info, err := os.Lstat(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("inspect manifest.json: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Manifest{}, fmt.Errorf("manifest.json must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open manifest.json: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest.json: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, errors.New("manifest.json contains multiple JSON values")
		}
		return Manifest{}, fmt.Errorf("decode trailing manifest data: %w", err)
	}
	return manifest, nil
}

func manifestJSON(manifest Manifest) ([]byte, error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode manifest.json: %w", err)
	}
	return append(data, '\n'), nil
}
