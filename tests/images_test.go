package tests

import (
	"os"
	"strings"
	"testing"
)

func TestNginxImageIncludesRequestedComponents(t *testing.T) {
	text := read(t, "../images/nginx/Dockerfile")
	for _, want := range []string{
		"FROM debian:trixie-slim",
		"ARG S6_OVERLAY_VERSION=",
		"ARG NGINX_UI_VERSION=",
		"ARG NGINX_UI_SHA256_AMD64=",
		"NGINX_UI_SHA256_ARM64=",
		"TARGETARCH:-$(dpkg --print-architecture)",
		"sha256sum -c",
		"libnginx-mod-http-geoip2",
		"libnginx-mod-http-ndk",
		"libnginx-mod-stream",
		"libnginx-mod-stream-geoip2",
		"libnginx-mod-rtmp",
		"libnginx-mod-http-lua",
		"logrotate",
		"anacron",
		"binutils",
		"strip --strip-unneeded /out/nginx-ui",
		"/out/nginx-ui --version",
		"apt-get purge -y --auto-remove binutils",
		"FROM scratch AS runtime-files",
		"COPY --from=runtime-files / /",
		"ENTRYPOINT [\"/init\"]",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("nginx Dockerfile missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(text), "alpine") {
		t.Fatal("nginx image must not use Alpine")
	}
}

func TestLuaModuleHasNDKDependency(t *testing.T) {
	text := read(t, "../templates/nginx/modules/05-mod-http-ndk.conf")
	if !strings.Contains(text, "load_module modules/ndk_http_module.so;") {
		t.Fatal("NDK module config is missing or uses the wrong module path")
	}
}

func TestNginxUIRunProtectsPersistentSecrets(t *testing.T) {
	text := read(t, "../images/nginx/rootfs/etc/s6-overlay/s6-rc.d/nginx-ui/run")
	for _, want := range []string{
		"umask 077",
		"chmod 700 /etc/nginx-ui",
		"chmod 600 /etc/nginx-ui/app.ini",
		"chmod 600 /etc/nginx-ui/database.db",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("nginx-ui run script missing %q", want)
		}
	}
}

func TestS6ServicesUseStandardBundleLayout(t *testing.T) {
	for _, name := range []string{"nginx", "nginx-ui"} {
		t.Run(name, func(t *testing.T) {
			registration := read(t, "../images/nginx/rootfs/etc/s6-overlay/user-bundles.d/user/contents.d/"+name)
			if registration != "" {
				t.Fatalf("s6 bundle registration for %s must be empty, got %q", name, registration)
			}

			serviceType := strings.TrimSpace(read(t, "../images/nginx/rootfs/etc/s6-overlay/s6-rc.d/"+name+"/type"))
			if serviceType != "longrun" {
				t.Fatalf("s6 service %s type = %q, want longrun", name, serviceType)
			}

			runPath := "../images/nginx/rootfs/etc/s6-overlay/s6-rc.d/" + name + "/run"
			info, err := os.Stat(runPath)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0o111 == 0 {
				t.Fatalf("s6 run script for %s is not executable", name)
			}
		})
	}

	legacyBundle := "../images/nginx/rootfs/etc/s6-overlay/s6-rc.d/user"
	if _, err := os.Stat(legacyBundle); !os.IsNotExist(err) {
		t.Fatalf("legacy s6 user bundle path must not exist: %v", err)
	}
}

func TestPHPImageExtensions(t *testing.T) {
	text := read(t, "../images/php/Dockerfile")
	for _, want := range []string{
		"FROM php:8.4-fpm-trixie",
		"bcmath",
		"exif",
		"gd",
		"intl",
		"mysqli",
		"pdo_mysql",
		"pdo_pgsql",
		"pgsql",
		"redis",
		"sockets",
		"zip",
		"composer",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("PHP Dockerfile missing %q", want)
		}
	}
	for _, forbidden := range []string{"apcu", "pcntl", "xsl", "soap", "imagick", "memcached", "mongodb", "amqp", "grpc", "swoole", "xdebug"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("PHP Dockerfile unexpectedly contains %q", forbidden)
		}
	}
	if strings.Contains(strings.ToLower(text), "alpine") {
		t.Fatal("PHP image must not use Alpine")
	}
	if strings.Contains(text, "RUN composer --version") {
		t.Fatal("PHP image must not add a separate composer version layer")
	}
	if strings.Contains(text, "php.ini") {
		t.Fatal("PHP ini must come from the mounted instance template")
	}
}

func TestPHPFPMDirectivesUseCorrectSections(t *testing.T) {
	text := read(t, "../images/php/fpm.conf")
	www := strings.Index(text, "[www]")
	if www < 0 {
		t.Fatal("fpm.conf missing [www] section")
	}
	if !strings.Contains(text[:www], "log_level = warning") {
		t.Fatal("PHP-FPM log_level must be in [global]")
	}
	if !strings.Contains(text[www:], "access.log = /dev/null") {
		t.Fatal("PHP-FPM access.log must be in [www]")
	}
	if strings.Contains(text, "clear_env") {
		t.Fatal("PHP-FPM should rely on the official docker.conf clear_env default")
	}
	for _, want := range []string{
		"pm = ",
		"pm.max_children = ",
		"pm.start_servers = ",
		"pm.min_spare_servers = ",
		"pm.max_spare_servers = ",
		"pm.max_requests = ",
		"request_terminate_timeout = ",
	} {
		if !strings.Contains(text[www:], want) {
			t.Errorf("PHP-FPM pool missing %q", want)
		}
	}
}

func TestPHPIniHasSingleInstanceTemplate(t *testing.T) {
	compose := read(t, "../templates/compose/compose.yaml.tmpl")
	if !strings.Contains(compose, "./config/php/php.ini:/usr/local/etc/php/conf.d/zz-webdock.ini:ro") {
		t.Fatal("Compose must mount the instance php.ini")
	}
	instanceTemplate := read(t, "../templates/php/php.ini")
	for _, want := range []string{"display_errors = Off", "display_startup_errors = Off", "log_errors = On"} {
		if !strings.Contains(instanceTemplate, want) {
			t.Errorf("PHP ini missing %q", want)
		}
	}
}

func TestPersistentDataMountsUseImageVolumePaths(t *testing.T) {
	compose := read(t, "../templates/compose/compose.yaml.tmpl")
	for _, want := range []string{
		"./data/redis:/data",
		"./data/mysql:/var/lib/mysql",
		"./data/postgres:/var/lib/postgresql/data",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("Compose data mount missing %q", want)
		}
	}
}

func TestNginxBaseline(t *testing.T) {
	text := read(t, "../templates/nginx/nginx.conf")
	for _, want := range []string{
		"keepalive_timeout 100",
		"error_log  /var/log/nginx/error.log crit",
		"access_log off",
		"stream {",
		"include /etc/nginx/sites-enabled/*",
		"ssl_protocols TLSv1.1 TLSv1.2 TLSv1.3",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("nginx.conf missing %q", want)
		}
	}
	if strings.Contains(text, "TLSv1 TLSv1.1") || strings.Contains(text, "TLSv1.0") {
		t.Fatal("nginx.conf enables TLS 1.0")
	}
	defaultSite := read(t, "../templates/nginx/default-site")
	for _, want := range []string{"http2 on;", "return 444;", "/etc/nginx/ssl/default.crt"} {
		if !strings.Contains(defaultSite, want) {
			t.Errorf("default site missing %q", want)
		}
	}
	if strings.Contains(defaultSite, "stub_status") {
		t.Fatal("default site must not enable stub_status")
	}
}

func TestComposeHealthchecksKeepPasswordsOutOfArguments(t *testing.T) {
	text := read(t, "../templates/compose/compose.yaml.tmpl")
	for _, want := range []string{`REDISCLI_AUTH=\"$$REDIS_PASSWORD\"`, `MYSQL_PWD=\"$$MYSQL_ROOT_PASSWORD\"`} {
		if !strings.Contains(text, want) {
			t.Errorf("compose healthcheck missing %q", want)
		}
	}
	for _, forbidden := range []string{`-a \"$$REDIS_PASSWORD\"`, `-p\"$$MYSQL_ROOT_PASSWORD\"`} {
		if strings.Contains(text, forbidden) {
			t.Errorf("compose healthcheck exposes a password argument %q", forbidden)
		}
	}
}

func TestMySQLKeepsOfficialStderrLogging(t *testing.T) {
	text := read(t, "../templates/mysql/my.cnf")
	if strings.Contains(text, "log_error") {
		t.Fatal("MySQL must not override log_error; log_error=stderr creates stderr.err in datadir")
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
