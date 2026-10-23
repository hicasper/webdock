package instance

import (
	"fmt"
	"os"
	"path/filepath"
)

func ValidateLayout(cfg Config) error {
	files := []string{"webdock.env", "compose.yaml"}
	files = append(files, filepath.Join("data", ".webdock-versions"))
	add := func(enabled bool, names ...string) {
		if enabled {
			files = append(files, names...)
		}
	}
	nginx, _ := cfg.Component(ComponentNginx)
	add(nginx.Enabled,
		"config/nginx/nginx.conf",
		"config/nginx/mime.types",
		"config/nginx/modules-enabled/10-mod-http-geoip2.conf",
		"config/nginx/modules-enabled/20-mod-stream.conf",
		"config/nginx/modules-enabled/30-mod-stream-geoip2.conf",
		"config/nginx/snippets/fastcgi-php.conf",
		"config/nginx/sites-available/000-default",
		"config/nginx/sites-enabled/000-default",
		"config/nginx/ssl/default.crt",
		"config/nginx/ssl/default.key",
	)
	php, _ := cfg.Component(ComponentPHP)
	add(php.Enabled, "config/php/php.ini")
	redis, _ := cfg.Component(ComponentRedis)
	add(redis.Enabled, "config/redis/valkey.conf")
	mysql, _ := cfg.Component(ComponentMySQL)
	add(mysql.Enabled, "config/mysql/my.cnf")

	for _, name := range files {
		path := filepath.Join(cfg.Root, name)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("required file %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("required file %s is not a regular file", name)
		}
	}
	if _, err := LoadVersionState(cfg.Root); err != nil {
		return err
	}
	return nil
}
