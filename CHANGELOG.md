# Changelog

本项目的重要变更、设计调整和验证记录集中记录在这里。格式参考 Keep a Changelog；当前尚未发布稳定版本，变更先归入 `Unreleased`。

## [Unreleased]

### Added

- 新增 Go 单二进制 CLI `webdock`，提供实例初始化、启停、状态、日志、诊断、Shell、Nginx UI 入口、数据库切换、备份和恢复命令。
- 新增固定实例布局，将凭据、配置、站点、服务数据和日志拆分到 `webdock.env`、`config/`、`sites/`、`data/`、`logs/`、`backups/`。
- 新增 `edge` 镜像：Debian trixie slim、Nginx 1.26.3、Nginx UI 2.9.1、s6-overlay、logrotate，以及 Brotli、GeoIP2、Stream、Stream-GeoIP2、RTMP、Lua 等动态模块。
- 新增 `php` 镜像：PHP 8.4 FPM、Composer，以及 `bcmath`、`exif`、`gd`、`intl`、`mysqli`、`pdo_mysql`、`pdo_pgsql`、`pgsql`、`redis`、`sockets`、`zip` 扩展。
- 新增 Valkey 8 默认缓存服务，包括 ACL 认证、AOF、`allkeys-lru`、日志级别 `warning` 和 Docker bridge 网关自动探测。
- 新增可选 MySQL 8.4 与 PostgreSQL 18 服务，并通过 Compose profile 和 `webdock db use` 离线切换。
- 新增 Nginx 目录规范和 Nginx UI 数据目录，支持 `sites-enabled`、`modules-enabled`、`streams-enabled` 等目录级软链接。
- 新增离线备份与恢复：默认备份核心实例状态，可选包含站点与日志；恢复支持预校验、覆盖前回滚和软链接安全检查。
- 新增 GitHub Actions CI/CD 工作流：Go 格式、测试、race、vet、staticcheck、CLI 构建，以及 Docker 镜像构建和 Docker Hub 多架构发布；同时保留默认关闭的 GHCR 发布代码。

### Changed

- 选择 Debian slim/trixie 作为生产镜像基础，不使用 Alpine；PHP 与 Nginx 只保留 8.4 和 Debian 提供的 1.26 系列，避免自行升版造成动态模块 ABI 不匹配。
- Nginx 改为安装常用动态模块但不默认启用，只默认启用 GeoIP2、Stream 和 Stream-GeoIP2；具体站点配置交由用户或 Nginx UI 管理。
- Nginx UI 不增加反向代理层，直接监听 `127.0.0.1:9001`，由 SSH 隧道访问。
- 所有服务采用固定容器名、`restart: unless-stopped` 和 host 网络；Nginx 对外监听 80/443，其余服务限定在回环地址或显式检测到的 Docker bridge 网关。
- 全局 keepalive 调整为 100 秒；TLS 禁用 1.0，保留 1.1，并启用 1.2/1.3。
- Docker 日志统一使用 json-file，限制为 10 MB × 2；Nginx access log 文件位置已预留，其余服务默认保留 stdout/stderr，避免额外文件轮转和权限复杂度。
- 数据库日志保持在容器 stderr；MySQL 显式设置 `log_error = stderr`，避免配置覆盖后写入容器临时盘。
- Valkey 配置从 `0.0.0.0` 改为仅绑定回环地址和实际存在且属于本机的 bridge 网关，避免意外暴露公网。
- 备份与恢复改为基于固定容器名检测运行态，不再依赖 `webdock.env` 或 `compose.yaml` 是否可加载来决定是否存在活动容器。
- 恢复流程先在临时目录完成解压和实例校验，再替换目标数据；归档中的软链接允许正常目录链接，但禁止绝对路径、越界目标和通过软链接父目录继续写入。
- CI 镜像发布标签暂定为 `hicasper/webdock-edge:latest` 和 `hicasper/webdock-php:8.4`，不锁定 image digest。
- 文档拆分为面向用户的 `README.md`、记录变更和验证状态的 `CHANGELOG.md`，以及面向维护者的架构和修改约束 `AGENTS.md`。

### Fixed

- 修复 Valkey 8.0 健康检查认证变量兼容问题，统一使用 `REDISCLI_AUTH`。
- 修复 MySQL、Valkey 健康检查将密码放入进程参数的问题，改为容器内环境变量传递。
- 修复 PHP 配置覆盖官方 FPM 行为的问题，删除不必要的 `clear_env` 修改，并在实例 `php.ini` 中显式关闭错误回显、开启错误日志。
- 修复 Nginx UI 运行时数据权限过宽问题，容器启动时将配置目录和数据文件收紧到仅 root 可访问。
- 修复恢复期间可能因归档软链接父路径导致目录穿越的问题，并补充目录级软链接往返测试。
- 修复实例配置损坏且固定容器仍运行时，恢复到错误地尝试直接停止容器的问题；现在拒绝绕过经过校验的 Compose 配置。
- 修复 Nginx UI ARM64 下载选择与构建时 `TARGETARCH` 为空时的架构回退问题。

### Validation

2026-10-22 在 Debian amd64、1 vCPU / 512 MB 内存的 Linux Docker runner 上完成以下集成验证：

- `go test ./...`、`go test -race ./...`、`go vet ./...`、`staticcheck ./...` 和格式检查通过。
- edge 与 PHP 镜像成功构建并启动，核心容器栈可初始化、启动、重启、停止并保持健康。
- Nginx 1.26.3、Nginx UI 2.9.1、默认站点和全部已安装动态模块可加载；Nginx UI 的 reload、stop、restart 控制路径可用，s6 可恢复异常退出的 Nginx。
- Nginx → PHP-FPM 请求链路正常，PHP 8.4、Composer 和预装扩展可用。
- Valkey ACL、AOF、回环地址和 Docker bridge gateway 访问正常，未认证请求被拒绝；配置文件只保存密码哈希。
- MySQL 8.4 与 PostgreSQL 18 均可启停、切换、跨 `down/up` 持久化，并可完成运行状态下的备份恢复。
- 默认、`--with-sites`、`--with-logs` 三种备份路径均已验证；MySQL socket、Nginx UI socket 等运行时文件不会进入归档。
- amd64 测试镜像体积：edge 约 308 MB，PHP 约 575 MB。
- `.github/workflows/ci.yml` 已通过 `actionlint` 和 YAML 解析检查，引用的 GitHub Actions 主版本标签已确认存在；真实 GitHub Runner 发布尚未触发。

### Known Limitations

- 尚未完成 arm64 实机构建与运行验证。
- Docker Hub 多架构自动发布尚未实际触发验证。
- 尚未完成长时间运行、宿主机重启和跨大版本升级演练。
- 不支持 Docker Desktop、rootless Docker、Podman 和 SELinux 强制模式。
- 当前没有稳定版本或兼容性承诺；实例目录格式变化需要在后续版本中明确迁移说明。
