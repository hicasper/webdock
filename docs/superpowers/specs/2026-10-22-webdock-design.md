# Webdock 精简设计

- 状态：实现中（阶段 1-3 离线验证）
- 日期：2026-10-22
- 定位：单机、简单部署、替代传统 LNMP 的 Docker Compose 工具

## 1. 目标

Webdock 由一个轻量 Go CLI、一个生成的 Compose 实例和三个核心服务组成：

- `edge`：Nginx 1.26 + Nginx UI，s6-overlay 管理两个进程。
- `php`：PHP 8.4 FPM。
- `valkey`：Valkey 8。
- 可选 `mysql` 8.4 或 `postgres` 18。

设计原则：

- 单机、Linux host 网络、固定端口、固定容器名。
- 使用官方镜像作为数据库和 Valkey 基础；只自建 edge 和 php。
- Nginx 配置由用户和 Nginx UI 管理，Webdock 只负责首次落地基线。
- 数据与配置目录可整体迁移。
- 不在低配机器上要求每次启动重新构建镜像。
- 不实现多实例、多主机、扩展管理平台或自定义 daemon。

## 2. 支持环境

第一版只支持：

- Linux。
- rootful Docker Engine。
- Docker Compose v2。
- `network_mode: host`。
- Debian/Ubuntu 优先，amd64/arm64。

明确不支持 Docker Desktop、rootless Docker、Podman 和 SELinux 强制模式。检测到不满足条件时直接失败并说明原因。

## 3. 版本与镜像

| 组件 | 版本 |
|---|---|
| Nginx | Debian trixie 的 1.26 系列 |
| Nginx UI | 2.9.1 |
| PHP | 8.4 |
| Valkey | `valkey/valkey:8-trixie` |
| MySQL | `mysql:8.4` |
| PostgreSQL | `postgres:18` |
| Composer | 2 |

自建镜像发布到 Docker Hub：

```text
hicasper/webdock-edge:latest
hicasper/webdock-php:8.4
```

不锁 digest，不要求补丁版本恒定。`latest` 和 `8.4` 可在同一大版本线内更新；跨大版本必须修改模板并人工迁移。

CI 构建 edge 时固定具体的 Nginx UI 和 s6-overlay 发布版本，并校验下载文件；用户侧不维护 digest 锁文件。

## 4. 网络与端口

所有服务使用 `network_mode: host`。

| 服务 | 监听地址 | 端口 |
|---|---|---:|
| Nginx HTTP | `0.0.0.0` | 80 |
| Nginx HTTPS | `0.0.0.0` | 443 |
| PHP-FPM | `127.0.0.1` | 9000 |
| Nginx UI | `127.0.0.1` | 9001 |
| Valkey | `127.0.0.1` + bridge gateway | 6379 |
| MySQL | `127.0.0.1` | 3306 |
| PostgreSQL | `127.0.0.1` | 5432 |

模板直接写入安全监听地址；不提供 `doctor --fix`：

```text
PHP-FPM      listen = 127.0.0.1:9000
MySQL        bind-address = 127.0.0.1
MySQL        mysqlx = 0
PostgreSQL   listen_addresses = '127.0.0.1'
Nginx UI     NGINX_UI_SERVER_HOST = 127.0.0.1
Valkey       bind 127.0.0.1 <检测到的 bridge gateway>
```

禁止 Valkey 监听 `0.0.0.0` 或 IPv6 通配地址。

`doctor` 检查：

- 80/443、9000、9001、3306、5432、6379 是否冲突。
- 非公网服务是否监听在预期地址。
- 是否意外出现 `0.0.0.0:9000/9001/3306/5432/6379` 或 IPv6 通配监听。
- MySQL X Protocol 33060 是否关闭。
- `nginx -t`、PHP-FPM 配置和实际进程是否正常。
- 容器健康状态。

## 5. Valkey

镜像：

```text
valkey/valkey:8-trixie
```

Compose 关键配置：

```yaml
network_mode: host
restart: unless-stopped
command: ["valkey-server", "/etc/valkey/valkey.conf"]
```

初始配置：

```text
bind 127.0.0.1 172.17.0.1
protected-mode yes
port 6379
appendonly yes
appendfsync everysec
maxmemory 128mb
maxmemory-policy allkeys-lru
loglevel warning
user default off
user webdock on ><generated-password> ~* &* +@all
```

`maxmemory` 可由 `webdock.env` 覆盖。

### 5.1 bridge 地址探测

每次 `init`、`up` 和 `restart`：

1. 读取 `VALKEY_BRIDGE_NETWORKS`，默认 `bridge`。
2. 执行 `docker network inspect <name>`。
3. 读取 `IPAM.Config[].Gateway`。
4. 只保留本机实际存在且合法的 IPv4 地址。
5. 去重排序。
6. 生成 `bind 127.0.0.1 <gateway...>`。
7. 没有有效网关时只写 `bind 127.0.0.1`。

配置使用临时文件加原子替换。重启后执行认证 `PING` 并检查实际监听；失败时恢复旧配置并重启。若网关在重启时消失，回退到仅 loopback 并输出 warning。

绑定 bridge gateway 不等于网络隔离。验收标准是：

- 匿名访问失败。
- 认证访问成功。
- 公网接口不可访问。
- 不出现 `0.0.0.0:6379` 或 `[::]:6379`。

## 6. Nginx 基线

edge 使用 Debian trixie slim、Debian Nginx 1.26、Nginx UI 2.9.1 和 s6-overlay。

配置位于标准位置：

```text
/etc/nginx/
├── conf.d/
├── fastcgi.conf
├── fastcgi_params
├── mime.types
├── modules-available/
├── modules-enabled/
├── nginx.conf
├── proxy_params
├── scgi_params
├── sites-available/
├── sites-enabled/
├── snippets/
│   └── fastcgi-php.conf
├── ssl/
├── streams-available/
├── streams-enabled/
└── uwsgi_params
```

首次初始化复制必要的 Debian 支持文件，增加 streams 和 ssl 目录。不复制 `/var/www/html`，不保留 Nginx UI 的公网反向代理 vhost。

### 6.1 nginx.conf

以用户现有 Dockerfile 配置为基线，仅做容器目录和新语法所需的必要调整：

```text
user www-data;
worker_processes auto;
include /etc/nginx/modules-enabled/*.conf;
error_log /var/log/nginx/error.log crit;
pid /var/run/nginx.pid;

events {
    use epoll;
    worker_connections 51200;
    multi_accept off;
    accept_mutex off;
}

stream {
    include /etc/nginx/streams-enabled/*;
}

http {
    sendfile on;
    tcp_nopush on;
    tcp_nodelay on;
    types_hash_max_size 2048;
    server_tokens off;
    server_names_hash_bucket_size 128;
    client_header_buffer_size 32k;
    large_client_header_buffers 4 32k;
    client_body_buffer_size 128k;
    client_max_body_size 1024m;
    keepalive_timeout 100;

    include /etc/nginx/mime.types;
    default_type application/octet-stream;

    ssl_protocols TLSv1.1 TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers on;

    gzip on;
    gzip_min_length 1k;
    gzip_buffers 4 16k;
    gzip_http_version 1.1;
    gzip_comp_level 2;
    gzip_types text/plain application/javascript application/x-javascript text/javascript text/css application/xml application/xml+rss;
    gzip_vary on;
    gzip_proxied expired no-cache no-store private auth;
    gzip_disable "MSIE [1-6]\.";

    log_format main '$remote_addr - $remote_user [$time_local] "$request" '
                    '$status $body_bytes_sent "$http_referer" '
                    '"$http_user_agent" "$http_x_forwarded_for"';
    access_log off;

    include /etc/nginx/conf.d/*.conf;
    include /etc/nginx/sites-enabled/*;
}
```

明确不包含 TLS 1.0。按用户现有配置暂时保留 TLS 1.1；后续可只修改 `nginx.conf` 停用。

### 6.2 默认站点

`sites-available/000-default` 默认启用，监听 80/443 并返回 `444`：

- 使用实例初始化时生成的自签名证书。
- 使用 `http2 on;`，不采用已弃用的 `listen ... http2` 写法。
- 不包含 `stub_status`。
- 不包含业务欢迎页或 `/var/www/html`。

所有未知域名不会落入第一个业务站点。

### 6.3 Nginx 模块

安装以下模块配置和二进制：

```text
brotli
cache-purge
dav-ext
echo
geoip2
headers-more
image-filter
lua
ndk
rtmp
stream
stream-geoip2
subs-filter
upstream-fair
xslt
```

`ndk` 是 Lua 的运行依赖，必须排在 `lua` 前。所有模块放入 `modules-available`，仅默认启用：

```text
geoip2
stream
stream-geoip2
```

其他模块不记录状态，也不提供管理命令；用户需要时自行在 Nginx UI/Nginx 配置中启用。GeoIP2 数据库不随镜像提供。

### 6.4 Nginx UI

Nginx UI 与 Nginx 同容器，但不经 Nginx 反向代理：

```text
NGINX_UI_SERVER_HOST=127.0.0.1
NGINX_UI_SERVER_PORT=9001
NGINX_UI_NGINX_CONFIG_DIR=/etc/nginx
NGINX_UI_WORKING_DIR=/etc/nginx-ui
NGINX_UI_OFFICIAL_DOCKER=true
NGINX_UI_LOGROTATE_ENABLED=true
NGINX_UI_LOGROTATE_INTERVAL=1440
```

JWT 和加密密钥由 `webdock.env` 生成并固定。UI 数据挂载到 `data/nginx-ui`。

不自定义 Nginx UI 的 reload/restart 实现，不替换它的 Nginx 测试命令，也不挂 Docker socket。Nginx UI 使用自身官方容器模式管理 Nginx；s6-overlay 只负责进程守护。Nginx 配置损坏时 UI 仍可通过 SSH 隧道访问。

Nginx UI 自身升级随 edge 镜像进行，不使用应用内 OTA。

### 6.5 Nginx 日志

全局 access log 关闭。初始化时创建空的 access/error 日志文件，错误日志写入：

```text
logs/nginx/error.log
```

镜像安装 `logrotate`，提供每日轮转、保留 7 份、压缩的配置，并启用 Nginx UI 的 logrotate 任务。站点需要 access log 时由用户自行开启。

## 7. PHP

基础镜像：

```text
php:8.4-fpm-trixie
```

预装扩展：

```text
bcmath exif gd intl mysqli pdo_mysql pdo_pgsql pgsql redis sockets zip
```

不安装：

```text
apcu pcntl xsl soap imagick memcached mongodb amqp grpc swoole xdebug
```

使用官方 `install-php-extensions` 的默认行为，不创建 `ext-available`、`PHP_INI_SCAN_DIR` 或自定义扩展目录，不提供扩展管理 CLI。缺失扩展通过重建 PHP 镜像增加。

Composer 使用多阶段复制：

```dockerfile
COPY --from=composer:2 /usr/bin/composer /usr/local/bin/composer
```

实例只保留一个 `config/php/php.ini`。PHP-FPM 监听 `127.0.0.1:9000`，日志级别 warning，默认关闭 access log。未额外配置的进程日志仍由 Docker 日志驱动管理。

## 8. 数据库

数据库是可选 profile：

```text
mysql
postgres
none
```

`init` 默认 `none`，只启动其中一个。`db use` 先停止当前引擎，再启动目标引擎；不清空、不迁移另一套数据。

MySQL：

```text
bind-address = 127.0.0.1
mysqlx = 0
```

PostgreSQL：

```text
listen_addresses = '127.0.0.1'
log_min_messages = warning
```

数据目录：

```text
data/mysql
data/postgres
```

两者只监听 loopback，不发布公网端口。

## 9. 日志与持久化

实例目录：

```text
instance/
├── compose.yaml            # CLI 生成，不手工编辑
├── webdock.env             # 0600，运行参数和生成凭据
├── config/
│   ├── nginx/
│   ├── php/
│   ├── valkey/
│   └── mysql/
├── sites/                  # 挂载到 /var/www
├── data/
│   ├── nginx-ui/
│   ├── valkey/
│   ├── mysql/
│   └── postgres/
├── logs/
│   └── nginx/
└── backups/
```

- `logs/` 独立于 `data/`，不进入默认备份。
- 只有 Nginx 使用 `logs/nginx/`；PHP、Valkey、MySQL、PostgreSQL 和 Nginx UI 的日志由 Docker json-file 驱动管理。MySQL 显式使用 `log_error = stderr`，避免挂载配置后误写容器盘。
- Docker json-file 日志限制为 `max-size=10m`、`max-file=2`。
- `webdock.env` 生成一次，后续命令不覆盖。
- 数据库和 Valkey 数据始终保留。

## 10. Compose 与容器

Compose 是 CLI 管理的生成文件，在 `init`、`up`、`restart` 和 `db use` 时重新渲染。用户只修改 `webdock.env`。

所有服务：

```yaml
restart: unless-stopped
network_mode: host
```

固定容器名：

```text
webdock-edge
webdock-php
webdock-valkey
webdock-mysql
webdock-postgres
```

所有长驻服务提供健康检查。edge 不挂 Docker socket。

日志公共配置：

```yaml
x-logging: &default-logging
  driver: json-file
  options:
    max-size: 10m
    max-file: "2"
```

## 11. CLI

```text
webdock init [DIR] [--db mysql|postgres|none]
webdock up [--pull]
webdock down
webdock restart
webdock status
webdock logs [service]
webdock doctor
webdock shell <service>
webdock db use mysql|postgres|none
webdock ui
webdock backup [--with-sites] [--with-logs]
webdock restore [--force] <archive>
```

约定：

- 其他命令默认使用当前目录，支持全局 `-C/--dir`，不向上搜索实例。
- `init` 非交互，默认 `none`，不覆盖已有 `webdock.env`。
- `up` 默认只用本机镜像；`--pull` 显式拉取同一标签并重建。
- `doctor` 严格只读，不自动修复。
- `ui` 输出 `http://127.0.0.1:9001` 和建议的 SSH 隧道命令。
- 不提供 `site`、`php ext`、`nginx module`、`update` 或 `db dump`。

## 12. 备份与恢复

`webdock backup` 使用简单的离线方案：

1. 记录原本运行状态并停止 stack。
2. 打包 `webdock.env`、`config/`、`data/`，跳过 socket、pid 等运行时文件。
3. 默认排除 `logs/` 和 `sites/`。
4. `--with-sites` 加入 `sites/`。
5. `--with-logs` 加入 `logs/`。
6. 无论成功或失败都尝试恢复原运行状态。

`webdock restore`：

1. 通过 `docker ps --all` 按固定容器名识别 `running`、`paused`、`restarting` 状态；若有活动容器，加载当前实例并用 Compose 停止，确认全部停止后才继续，否则拒绝恢复。已退出容器不会阻止恢复。
2. 解压到实例内的临时目录，拒绝目录穿越、越界符号链接和经符号链接父目录写入的条目；校验必需文件、`webdock.env` 权限和实例布局。
3. 校验通过后恢复配置与数据。
4. 重新渲染 Compose。
5. 若恢复前栈正在运行，则强制重建容器并执行完整检查；若原本停止，则保持停止并提示运行 `webdock up`。

不做在线 SQLite 特殊备份，不承诺跨服务原子升级。

## 13. 实现结构

```text
cmd/webdock/
internal/
├── app/          # 命令分发和生命周期
├── backup/       # tar 备份、恢复和路径安全
├── docker/       # docker/compose 命令与输出解析
├── doctor/       # 只读检查和监听验证
├── instance/     # 目录、env、配置初始化和校验
├── network/      # Docker bridge gateway 探测
└── render/       # Compose、Valkey、Nginx 模板渲染
templates/
├── compose/
├── mysql/
├── nginx/
├── php/
└── valkey/
images/
├── edge/
└── php/
tests/
docs/
```

实现约束：

- Go 单二进制，优先标准库。
- 模板通过 `go:embed` 嵌入。
- 外部命令通过小接口封装，便于测试。
- 不引入额外配置库、ORM 或 daemon。
- Compose 是运行时唯一编排来源。

## 14. 安全与故障边界

- 公网只开放 80/443。
- PHP-FPM、Nginx UI、数据库只监听 loopback。
- Valkey 只监听 loopback 和显式探测到的 bridge gateway。
- Nginx UI 不挂 Docker socket，不通过 Nginx 暴露；其数据目录权限为 `0700`，`app.ini` 和数据库为 `0600`。
- MySQL X Protocol 关闭。
- 不打印生成密码；Valkey 配置文件只保存 ACL 密码的 SHA-256 哈希。
- Nginx 配置损坏时 UI 仍可通过 9001 修复。
- 启动前端口检查与启动后实际监听检查都必须通过。
- Valkey 配置更新失败时恢复旧文件。

## 15. 测试

单元测试：

- bridge gateway 解析和非法地址过滤。
- Valkey 配置渲染及失败回退。
- 端口与监听地址判断。
- 实例初始化和重复初始化保护。
- Compose 渲染和 profile 切换。
- 备份清单、路径穿越和恢复校验。
- 恢复前运行态检测、归档符号链接父目录绕过和凭据文件权限。
- Compose 健康检查不把数据库/Valkey 密码放入命令参数。
- 日志目录不在默认备份中。
- Nginx 配置文件复制完整性。

集成测试：

- host 网络启动。
- 固定容器名、`restart: unless-stopped` 和日志限制。
- 所有监听地址检查。
- Nginx 配置损坏时 9001 仍可访问。
- 必需 Nginx 模块可加载，RTMP 已安装但默认不启用。
- PHP 扩展存在且 Composer 可运行。
- MySQL/PostgreSQL 连接和 loopback 限制。
- Valkey bridge 地址探测、ACL、PONG 和 AOF。
- bridge 地址消失后的 loopback 回退。
- Nginx 日志轮转。
- `down/up` 后配置和数据保留。
- 简单备份、离线恢复及可选 sites/logs。
- `up --pull` 更新路径。

发布检查：

- `go test -race`
- `go vet`
- `staticcheck`
- `hadolint`
- `shellcheck`
- `docker compose config`
- amd64 镜像构建、干净启动、健康检查和镜像体积记录
- Nginx → PHP-FPM 请求链路、全部 Nginx 模块加载和 Nginx 控制路径
- MySQL/PostgreSQL 切换、持久化和运行态备份恢复

真实集成测试已在 Debian amd64、1 vCPU / 512MB Linux Docker runner 上完成。arm64 构建和 Docker Hub 多架构推送仍需在发布环境执行。

## 16. 实施顺序

### 阶段 1：骨架与核心

- Go CLI、`init`、实例目录和 `webdock.env`。
- Compose 渲染、路径参数化。
- edge、php、valkey。
- Nginx 配置复制、Nginx UI 9001。
- bridge gateway 探测。
- `up/down/restart/status/logs/shell/ui/doctor`。

### 阶段 2：数据库

- MySQL 8.4。
- PostgreSQL 18。
- `db use`、健康检查和连接验证。

### 阶段 3：备份与发布

- 离线 backup/restore。
- Docker Hub 多架构构建和发布。
- 文档、迁移说明和完整集成测试。

## 17. 参考资料

- [garylab/dnmp](https://github.com/garylab/dnmp)
- [hicasper/lnmp](https://github.com/hicasper/lnmp)
- [Nginx UI](https://github.com/0xJacky/nginx-ui)
- [Nginx UI 配置说明](https://github.com/0xJacky/nginx-ui/blob/dev/docs/guide/config-nginx.md)
- [Docker Compose 网络](https://docs.docker.com/compose/how-tos/networking/)
- [Docker PHP 官方镜像](https://github.com/docker-library/php)
- [install-php-extensions](https://github.com/mlocati/docker-php-extension-installer)
- [Debian nginx](https://packages.debian.org/trixie/nginx)
- [Valkey](https://valkey.io/download/)
- [MySQL 生命周期](https://www.mysql.com/support/eol-notice.html)
- [PostgreSQL 版本支持](https://www.postgresql.org/docs/current/versioning.html)
