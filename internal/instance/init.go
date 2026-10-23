package instance

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"webdock/internal/render"
	"webdock/templates"
)

var ErrAlreadyInitialized = errors.New("webdock instance is already initialized")

type Config struct {
	Root       string
	Values     Values
	Components []Component
}

func (c Config) BridgeNetworks() []string {
	raw := c.Values["REDIS_BRIDGE_NETWORKS"]
	if raw == "" {
		return []string{"bridge"}
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	if len(result) == 0 {
		return []string{"bridge"}
	}
	return result
}

func (c Config) EnvFile() string {
	return filepath.Join(c.Root, "webdock.env")
}

func Init(root string) (Config, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve instance directory: %w", err)
	}
	root = absRoot
	envPath := filepath.Join(root, "webdock.env")
	if _, err := os.Stat(envPath); err == nil {
		return Config{}, ErrAlreadyInitialized
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("inspect %s: %w", envPath, err)
	}

	secrets := make([]string, 6)
	for i := range secrets {
		secret, err := randomSecret()
		if err != nil {
			return Config{}, err
		}
		secrets[i] = secret
	}
	values := Values{
		"TZ":                      "UTC",
		"ENABLE_NGINX":            "true",
		"ENABLE_PHP":              "false",
		"ENABLE_REDIS":            "false",
		"ENABLE_MYSQL":            "false",
		"ENABLE_POSTGRES":         "false",
		"REDIS_MAXMEMORY":         "128mb",
		"REDIS_BRIDGE_NETWORKS":   "bridge",
		"REDIS_PASSWORD":          secrets[0],
		"MYSQL_ROOT_PASSWORD":     secrets[1],
		"MYSQL_PASSWORD":          secrets[2],
		"POSTGRES_PASSWORD":       secrets[3],
		"NGINX_UI_APP_JWT_SECRET": secrets[4],
		"NGINX_UI_CRYPTO_SECRET":  secrets[5],
	}
	components, err := ParseComponents(values)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Root: root, Values: values, Components: components}

	if err := ensureDirectories(root); err != nil {
		return Config{}, err
	}
	if err := seedNginx(root); err != nil {
		return Config{}, err
	}
	if err := seedFile("php/php.ini", filepath.Join(root, "config", "php", "php.ini"), 0o644); err != nil {
		return Config{}, err
	}
	if err := seedFile("mysql/my.cnf", filepath.Join(root, "config", "mysql", "my.cnf"), 0o644); err != nil {
		return Config{}, err
	}
	if err := render.RedisConfig(root, render.RedisData{
		Bind:      "127.0.0.1",
		MaxMemory: values["REDIS_MAXMEMORY"],
		Password:  values["REDIS_PASSWORD"],
	}); err != nil {
		return Config{}, err
	}
	if err := render.Compose(root, cfg.ComposeData()); err != nil {
		return Config{}, err
	}
	if err := writeNewFile(envPath, RenderEnv(values), 0o600); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Load(root string) (Config, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve instance directory: %w", err)
	}
	envPath := filepath.Join(absRoot, "webdock.env")
	info, err := os.Lstat(envPath)
	if err != nil {
		return Config{}, fmt.Errorf("load webdock.env: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Config{}, fmt.Errorf("webdock.env must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Config{}, fmt.Errorf("webdock.env permissions are %04o; require 0600", info.Mode().Perm())
	}
	values, err := LoadEnv(envPath)
	if err != nil {
		return Config{}, fmt.Errorf("load webdock.env: %w", err)
	}
	components, err := validateRequired(values)
	if err != nil {
		return Config{}, err
	}
	return Config{Root: absRoot, Values: values, Components: components}, nil
}

func validateRequired(values Values) ([]Component, error) {
	required := []string{
		"TZ",
		"REDIS_MAXMEMORY",
		"REDIS_BRIDGE_NETWORKS",
		"REDIS_PASSWORD",
		"MYSQL_ROOT_PASSWORD",
		"MYSQL_PASSWORD",
		"POSTGRES_PASSWORD",
		"NGINX_UI_APP_JWT_SECRET",
		"NGINX_UI_CRYPTO_SECRET",
	}
	for _, key := range required {
		if strings.TrimSpace(values[key]) == "" {
			return nil, fmt.Errorf("webdock.env is missing %s", key)
		}
	}
	return ParseComponents(values)
}

func ValidateEnvironment(values Values) ([]Component, error) {
	return validateRequired(values)
}

func ensureDirectories(root string) error {
	dirs := []string{
		"config/nginx/conf.d",
		"config/nginx/modules-available",
		"config/nginx/modules-enabled",
		"config/nginx/sites-available",
		"config/nginx/sites-enabled",
		"config/nginx/snippets",
		"config/nginx/ssl",
		"config/nginx/streams-available",
		"config/nginx/streams-enabled",
		"config/php",
		"config/redis",
		"config/mysql",
		"sites",
		"data/nginx-ui",
		"data/redis",
		"data/mysql",
		"data/postgres",
		"logs/nginx",
		"backups",
	}
	for _, dir := range dirs {
		path := filepath.Join(root, dir)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
	}
	return nil
}

func seedNginx(root string) error {
	nginxDir := filepath.Join(root, "config", "nginx")
	if err := seedFile("nginx/nginx.conf", filepath.Join(nginxDir, "nginx.conf"), 0o644); err != nil {
		return err
	}
	support := []string{
		"fastcgi.conf",
		"fastcgi_params",
		"koi-utf",
		"koi-win",
		"mime.types",
		"proxy_params",
		"scgi_params",
		"uwsgi_params",
		"win-utf",
	}
	for _, name := range support {
		if err := seedFile("nginx/support/"+name, filepath.Join(nginxDir, name), 0o644); err != nil {
			return err
		}
	}
	if err := seedFile("nginx/snippets/fastcgi-php.conf", filepath.Join(nginxDir, "snippets", "fastcgi-php.conf"), 0o644); err != nil {
		return err
	}
	if err := seedFile("nginx/default-site", filepath.Join(nginxDir, "sites-available", "000-default"), 0o644); err != nil {
		return err
	}
	if err := ensureSymlink(filepath.Join(nginxDir, "sites-enabled", "000-default"), "../sites-available/000-default"); err != nil {
		return err
	}

	modules := map[string]bool{
		"05-mod-http-ndk.conf":                 false,
		"10-mod-http-geoip2.conf":              true,
		"20-mod-stream.conf":                   true,
		"30-mod-stream-geoip2.conf":            true,
		"40-mod-http-brotli.conf":              false,
		"40-mod-http-cache-purge.conf":         false,
		"40-mod-http-dav-ext.conf":             false,
		"40-mod-http-echo.conf":                false,
		"40-mod-http-headers-more-filter.conf": false,
		"40-mod-http-image-filter.conf":        false,
		"40-mod-http-lua.conf":                 false,
		"40-mod-http-subs-filter.conf":         false,
		"40-mod-http-upstream-fair.conf":       false,
		"40-mod-http-xslt-filter.conf":         false,
		"40-mod-rtmp.conf":                     false,
	}
	for name, enabled := range modules {
		if err := seedFile("nginx/modules/"+name, filepath.Join(nginxDir, "modules-available", name), 0o644); err != nil {
			return err
		}
		if enabled {
			if err := ensureSymlink(filepath.Join(nginxDir, "modules-enabled", name), "../modules-available/"+name); err != nil {
				return err
			}
		}
	}
	if err := generateDefaultCertificate(nginxDir); err != nil {
		return err
	}
	for _, name := range []string{"access.log", "error.log"} {
		path := filepath.Join(root, "logs", "nginx", name)
		if err := writeIfMissing(path, nil, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func seedFile(templateName, path string, mode os.FileMode) error {
	data, err := templates.Read(templateName)
	if err != nil {
		return err
	}
	return writeIfMissing(path, data, mode)
}

func writeIfMissing(path string, data []byte, mode os.FileMode) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s exists and is not a regular file", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

func writeNewFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrAlreadyInitialized
		}
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

func ensureSymlink(path, target string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s exists and is not a symlink", path)
		}
		current, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read symlink %s: %w", path, err)
		}
		if current != target {
			return fmt.Errorf("symlink %s points to %q, want %q", path, current, target)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect symlink %s: %w", path, err)
	}
	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("create symlink %s: %w", path, err)
	}
	return nil
}

func randomSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
