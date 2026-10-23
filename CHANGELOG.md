# Changelog

本项目的重要变更、设计调整和验证记录集中记录在这里。格式参考 Keep a Changelog；项目在 1.0 前使用 `0.x.y` 版本号。

## [Unreleased]

### Added

- 组件启用状态与版本统一改为 `ENABLE_NGINX`、`ENABLE_PHP`、`ENABLE_REDIS`、`ENABLE_MYSQL`、`ENABLE_POSTGRES`；`true` 使用默认版本，具体值启用指定版本。
- 新增 `enable <component> [version]`、`disable`、`start`、`stop` 和单组件 `restart`；`up` 收敛到持久启用状态。
- 新增基于组件和版本的动态镜像解析与 Docker 镜像存在性检查，已验证版本只记录在配置注释和文档中。
- 新增 `db backup mysql|postgres` 在线逻辑备份：MySQL 使用 gzip SQL，PostgreSQL 使用 custom dump。
- 完整备份新增精简 `manifest.json`，每个组件只记录一个启用版本字符串或 `null`。
- 恢复支持逐组件版本冲突确认：`y` 接受归档版本和数据，`n` 跳过当前组件并继续后续组件。
- Redis、MySQL、PostgreSQL 新增 `.webdock-version` 数据版本标记；版本变化时默认中止，确认后才强制启动并更新标记。

### Changed

- Valkey 组件、服务、容器、profile 和环境变量统一更名为 `redis`，实际镜像仍使用 `valkey/valkey`；Redis 支持 7、8。
- Compose 的五个服务全部使用同名 profile，Nginx、PHP、Redis、MySQL、PostgreSQL 不再绑定为固定容器栈。
- MySQL 与 PostgreSQL 可以同时启用；删除 `DB_ENGINE`、`db use` 和单数据库选择模型。
- PostgreSQL 镜像改为 `tensorchord/vchord-suite:pg<version>-latest`，已验证 16、17、18。
- 将 `edge` 服务、容器、镜像目录和 CLI 服务名统一改为 `nginx`，容器名变为 `webdock-nginx`。
- Nginx 与 PHP 镜像集中发布到同一个 `hicasper/webdock` 仓库，标签分别为 `nginx-1.26` 和 `php-8.4`；开发阶段直接覆盖，不发布 commit 或 Git tag 版本化标签。
- Nginx UI 在构建阶段执行 `strip --strip-unneeded` 并立即冒烟测试，随后卸载构建工具；合并最终运行文件 COPY 层。
- 删除 PHP 镜像中重复的 `php.ini` 和单独的 `composer --version` 构建层，PHP 配置只保留实例模板这一份来源。
- Nginx 镜像显式选择 `anacron` 满足 `logrotate` 的调度器依赖，避免引入并保留未运行的 `cron` 和 `systemd` 依赖链。
- s6 服务注册迁移到标准 `user-bundles.d/user/contents.d/` 空文件布局，移除对旧 `s6-rc.d/user` 兼容路径的依赖。
- GitHub Actions 合并为单一 `ci.yml`：文档等无关改动不触发 workflow，镜像和二进制分别按实际变更构建，PR 只验证不发布，master/main 推送按需更新镜像和滚动 Release。

### Validation

- 在 Debian amd64、1 vCPU / 512 MB Linux Docker runner 上验证默认只启动 Nginx，随后启用 PHP 8.4、Redis 7、MySQL 8.4、PostgreSQL 16 并分别启动成功。
- 验证 `stop redis`/`start redis` 不改变持久状态，Redis 7 和 8 的镜像标签可实际启动并通过认证探活。
- 验证 MySQL 和 PostgreSQL 逻辑备份可以生成有效 gzip SQL / custom dump，并可由数据库客户端或 `pg_restore --list` 读取。
- 验证完整备份的 `manifest.json` 只记录版本字符串或 `null`，备份期间运行组件和临时停止组件在恢复后各自保持原状态。
- 验证 Redis 数据版本标记从 7 变为 8 时默认 n 中止，确认 y 后才启动并更新标记。

## [0.1.0] - 2026-10-22

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
- 新增 GitHub Actions 二进制发布工作流：代码变化时构建 Linux amd64/arm64 可执行文件，并持续覆盖更新同一个 `v0.1.0` Release 中的同名资产。
- 新增 `webdock version`、`webdock --version` 和 `webdock -v`，版本号统一读取 `cmd/webdock/VERSION`。

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
- 文档拆分为精简的面向用户 `README.md`、记录变更和验证状态的 `CHANGELOG.md`，以及面向维护者的架构和修改约束 `AGENTS.md`。

### Fixed

- 移除 MySQL 的 `log_error = stderr` 覆盖；该值会在数据目录生成 `stderr.err`。现在保持官方镜像默认 stderr 日志，日志由 Docker 管理且不会混入持久数据。
- 修复 Valkey 8.0 健康检查认证变量兼容问题，统一使用 `REDISCLI_AUTH`。
- 修复 MySQL、Valkey 健康检查将密码放入进程参数的问题，改为容器内环境变量传递。
- 修复 PHP 配置覆盖官方 FPM 行为的问题，删除不必要的 `clear_env` 修改，并在实例 `php.ini` 中显式关闭错误回显、开启错误日志。
- 修复 Nginx UI 运行时数据权限过宽问题，容器启动时将配置目录和数据文件收紧到仅 root 可访问。
- 修复恢复期间可能因归档软链接父路径导致目录穿越的问题，并补充目录级软链接往返测试。
- 修复实例配置损坏且固定容器仍运行时，恢复到错误地尝试直接停止容器的问题；现在拒绝绕过经过校验的 Compose 配置。
- 修复 Nginx UI ARM64 下载选择与构建时 `TARGETARCH` 为空时的架构回退问题。
- 修复对运行中实例执行缺少 `--force` 的恢复时先停栈再报错的问题；现在会在触碰 Docker 前直接拒绝。
- 调整 PHP-FPM 动态进程池：最多 20 个 worker、启动并保留 5 个空闲进程，每 5000 个请求回收，并将单请求硬超时限制为 60 秒。

### Validation

2026-10-22 至 2026-10-23 在 Debian amd64、1 vCPU / 512 MB 内存的 Linux Docker runner 上完成以下集成验证：

- `go test ./...`、`go test -race ./...`、`go vet ./...`、`staticcheck ./...` 和格式检查通过。
- edge 与 PHP 镜像成功构建并启动，核心容器栈可初始化、启动、重启、停止并保持健康。
- Nginx 1.26.3、Nginx UI 2.9.1、默认站点和全部已安装动态模块可加载；Nginx UI 的 reload、stop、restart 控制路径可用，s6 可恢复异常退出的 Nginx。
- Nginx → PHP-FPM 请求链路正常，PHP 8.4、Composer 和预装扩展可用。
- Valkey ACL、AOF、回环地址和 Docker bridge gateway 访问正常，未认证请求被拒绝；配置文件只保存密码哈希。
- MySQL 8.4 与 PostgreSQL 18 均可启停、切换、跨 `down/up` 持久化，并可完成运行状态下的备份恢复。
- 默认、`--with-sites`、`--with-logs` 三种备份路径均已验证；MySQL socket、Nginx UI socket 等运行时文件不会进入归档。
- amd64 测试镜像体积：edge 约 308 MB，PHP 约 575 MB。
- 两个 GitHub Actions 工作流均已通过 `actionlint`、YAML 解析和 shell 语法检查，引用的 Action 主版本标签已确认存在；真实 GitHub Runner 发布尚未触发。
- 发布流程已在本地模拟：Linux amd64/arm64 静态二进制构建成功，SHA256 校验通过，`webdock --version` 输出 `webdock 0.1.0`。
- 从公开 GitHub Release 下载 amd64 二进制并校验 SHA256；从 Docker Hub 拉取 edge/PHP 镜像后重新初始化并启动，确认发布产物可用。
- 创建 2 个静态站和 2 个动态站，验证域名隔离、HTTP/HTTPS、gzip、默认主机 `444`、共享 PHP-FPM 与独立 Nginx 日志正常。
- 验证 MySQL 与 PostgreSQL 切换、数据持久化、默认/完整备份、完整恢复及恢复到全新实例目录；恢复后的站点、软链接、日志和数据库数据均可启动使用。
- 压测确认静态请求由 Nginx 独立承载；1 vCPU / 512 MB 环境下，PHP 短请求在 40 并发、1000 请求时无失败。100 并发突发会触发资源饱和，PHP 上限应按实际单请求内存和机器规格评估。

### Known Limitations

- 尚未完成 arm64 实机容器运行验证；Docker Hub 多架构发布工作流已成功执行。
- 尚未完成长时间运行、宿主机重启和跨大版本升级演练。
- 不支持 Docker Desktop、rootless Docker、Podman 和 SELinux 强制模式。
- 当前没有稳定版本或兼容性承诺；实例目录格式变化需要在后续版本中明确迁移说明。
