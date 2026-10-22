package templates

import (
	"embed"
	"fmt"
)

//go:embed compose nginx php valkey mysql
var files embed.FS

func Read(name string) ([]byte, error) {
	data, err := files.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read embedded template %q: %w", name, err)
	}
	return data, nil
}
