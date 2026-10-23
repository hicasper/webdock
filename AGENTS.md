# AGENTS.md

本文件面向后续维护 Webdock 的工程代理和开发者。修改前先确认当前需求是否与这里记录的边界一致；若需求确实需要改变边界，必须同时更新代码、测试、README、CHANGELOG 和本文件。

## 项目目标

Webdock 是单机 Linux 上的轻量 Compose 容器栈管理工具，用来替代传统手工 LNMP。目标优先级依次为：

1. 部署和恢复行为可预测，不因配置文件损坏或容器状态异常而破坏数据。
2. 尽量少改官方镜像，生产服务使用 Debian slim/trixie，不使用 Alpine。
3. 在 1 vCPU / 512 MB 级机器上保持较低常驻开销。
4. 目录结构固定、可迁移、可备份，用户配置与程序生成配置的边界清晰。
5. 不为了假想中的大规模场景增加抽象、控制器、CRD、复杂网络层或额外服务。

## 不可随意改变的约束

- 仅支持 Linux、rootful Docker Engine 和 Docker Compose v2。
- 固定容器名为 `webdock-nginx`、`webdock-php`、`webdock-valkey`、`webdock-mysql`、`webdock-postgres`。这些名称参与恢复前的运行态检测，修改时必须同步 `internal/app/runtime.go`、Compose 模板、测试和文档。
- 所有服务使用 `network_mode: host` 和 `restart: unless-stopped`。
- Nginx 对外监听 80/443；PHP-FPM 监听 `127.0.0.1:9000`；Nginx UI 监听 `127.0.0.1:9001`；MySQL/PostgreSQL 只监听回环地址。
- Valkey 只能监听 `127.0.0.1` 和经本地接口验证过的 IPv4 Docker bridge 网关，绝不能退化到 `0.0.0.0`。
- Nginx UI 自行管理 Nginx 配置和 reload/restart，不要在 Webdock 中另做一套 Nginx UI 配置控制器。
- `webdock.env` 必须是普通文件且权限为 `0600`。任何新凭据都不得写入日志、进程参数或 Compose 的明文命令参数。
- 默认备份包含 `webdock.env`、`config/`、`data/`；`sites/` 和 `logs/` 只在显式选项下包含。恢复必须先在临时目录完成校验，再替换现有数据。
- 归档恢复可以保留实例内部的相对软链接，但必须拒绝绝对路径、越界目标、重复 entry，以及通过软链接父目录继续写入。
- 镜像标签只保证大版本兼容，不锁 digest。Nginx 与 PHP 发布到同一个仓库，当前稳定标签为 `hicasper/webdock:nginx-1.26` 和 `hicasper/webdock:php-8.4`；开发阶段直接覆盖这两个标签，不发布 commit 或 Git tag 版本化镜像。
- 不提交 `runner-info.test`、`runner-key.test` 或其他 `runner-*.test` 文件；它们可能包含真实 runner 的访问信息。
- 项目在 1.0 前使用 `0.x.y` 版本号；唯一版本源是 `cmd/webdock/VERSION`，Release tag 由它生成。

## 仓库目录

```text
.
├── .github/workflows/       # GitHub Actions CI/CD 与二进制 Release
├── cmd/webdock/             # CLI 入口
│   ├── VERSION              # 0.x.y 唯一版本源
│   └── main.go              # 嵌入版本、信号处理和 app.Run 调用
├── internal/app/            # 命令编排、生命周期、运行探测和跨包流程
├── internal/backup/         # tar.gz 备份、恢复、路径与软链接校验
├── internal/docker/         # Docker 命令执行器和 Compose 参数封装
├── internal/doctor/         # /proc 监听器解析、端口与监听地址规则
├── internal/instance/       # 实例配置、初始化、布局校验、默认证书和环境文件
├── internal/network/        # Docker bridge 网关发现和本机地址验证
├── internal/render/         # 模板渲染和原子文件替换
├── templates/               # 嵌入二进制的 Compose、Nginx、PHP、MySQL、Valkey 模板
├── images/nginx/            # Nginx + Nginx UI + 动态模块镜像
├── images/php/              # PHP 8.4 FPM + 扩展 + Composer 镜像
├── tests/                   # 跨镜像静态契约测试
├── docs/                    # 预留设计文档目录；不要用运行数据填充
├── Makefile                 # build/test/vet/race 快捷命令
├── go.mod                   # Go 模块和工具链版本
├── README.md                # 面向用户的功能、安装和使用文档
└── CHANGELOG.md             # 变更、修复、验证状态和已知限制
```

### `cmd/webdock`

`main.go` 通过 `go:embed` 读取 `VERSION`，创建可取消的 context、处理 SIGINT/SIGTERM，然后调用 `app.New(version).Run`。业务逻辑不要放在这里。版本命令由 `internal/app` 输出，版本文件由发布工作流读取。

### `internal/app`

- `app.go`：命令路由、全局 `-C/--dir` 参数、实例加载、帮助输出和依赖注入字段。
- `init.go`：调用实例初始化并输出路径。
- `lifecycle.go`：`up`、`down`、`restart` 及启动/重建流程。
- `runtime.go`：Valkey 配置渲染、服务探测、固定容器运行态检测、监听规则和 Valkey bind 解析。
- `database.go`：数据库 profile 切换、失败回滚。
- `inspect.go`：`status`、`logs`、`shell`、`ui`。
- `doctor.go`：只读诊断入口。
- `backup.go`：停止运行栈、创建备份、校验并恢复归档。

`App` 的文件、命令执行器、工作目录、监听器和平台字段用于测试注入。新增外部副作用时优先通过 `docker.Runner` 或可注入函数实现，避免测试依赖真实 Docker。

### `internal/backup`

创建归档时跳过 socket、`.sock`、`.sock.lock` 和 `.pid`，不跟随目录软链接。恢复时使用 staging 目录、校验回调、旧数据临时目录和回滚移动，保证校验失败不会替换目标。

`extract` 中的 `seen`、`validateEntry` 和 `ensureParentPath` 是安全边界。不要为了让某个归档“能解压”而放宽这些检查；应修正归档生成逻辑。

### `internal/docker`

`Runner` 是唯一的 Docker 进程边界。`ExecRunner.Output` 合并 stdout/stderr 用于命令型调用；`Attach` 直连终端用于 logs 和 shell。`Compose` 固定传入 `--ansi never`、`--env-file webdock.env`、`-f compose.yaml` 和 profile。

不要在内部绕过 `Compose` 拼接另一套参数顺序。数据库 profile 只应通过 `Profiles`/`AllProfiles` 生成。

### `internal/doctor`

`ReadListeners` 读取 `/proc/net/tcp` 和 `/proc/net/tcp6`，`ParseProcNet` 只识别 `0A` 监听状态。`ValidateListeners` 检查必需监听、禁止监听、通配地址和 Valkey/数据库地址；`PortConflicts` 用于容器栈停止时检查旧服务占端口。

这里只做只读分析，不执行修复。新增平台支持时要保持非 Linux 路径可跳过，而不是 panic。

### `internal/instance`

- `init.go`：生成随机凭据、创建完整目录树、写入默认 Nginx/PHP/MySQL/Valkey 配置、证书和 `webdock.env`。
- `env.go`：严格的 `KEY=VALUE` 解析、稳定排序输出和读取。不要加入隐式 shell 展开。
- `validate.go`：运行前的关键文件存在性检查。改变实例布局时必须同步这里。
- `cert.go`：生成仅用于默认站点的自签名 RSA 证书。

初始化遵循“已存在用户文件不覆盖”。修改模板时不要改成每次启动强制覆盖实例配置。

### `internal/network`

`Discover` 读取指定 Docker 网络的 IPAM gateway，并用本机 interface 地址验证后才返回。任何 Docker 命令失败、网络不存在或 JSON 异常都只产生 warning 并跳过，不应阻止无网关联通需求的启动。

不要使用 `docker0` 固定地址作为假设。地址应由 Docker 网络 inspect 得到。

### `internal/render`

Compose 和 Valkey 配置由嵌入模板渲染。`WriteFile` 通过同目录临时文件、`fsync` 和 rename 原子替换；不要改回直接 `os.WriteFile`。

Valkey 模板只接收密码哈希，不把明文密码写入 `config/valkey/valkey.conf`。

### `templates`

`templates/templates.go` 使用 `go:embed` 将整个模板树嵌入二进制。模板文件按相对路径读取，例如 `nginx/nginx.conf`。

- `templates/compose/compose.yaml.tmpl`：服务编排、端口、健康检查、日志和固定挂载。
- `templates/nginx/`：全局配置、默认站点、Debian 支持文件、模块入口和 FastCGI snippet。
- `templates/php/php.ini`：通过 `embed.FS` 注入实例 `config/php/php.ini`。
- `templates/mysql/my.cnf`：MySQL 的监听和禁用 mysqlx 配置。不要设置 `log_error = stderr`；MySQL 会把它当成数据目录下的 `stderr.err` 文件。保持官方镜像默认的 stderr 日志行为，避免日志进入数据备份。
- `templates/valkey/valkey.conf.tmpl`：Valkey 运行时配置。

实例布局、模板路径或默认值变化时，至少检查 `internal/instance/init.go`、`internal/instance/validate.go`、`internal/render/render_test.go` 和 `tests/images_test.go`。

### `images/nginx`

基础为 `debian:trixie-slim`。一个构建阶段下载并校验 Nginx UI 发布包，另一个阶段安装 s6-overlay，最终阶段安装 Debian 提供的 Nginx 1.26 系列和动态模块。

- Nginx UI 默认版本和 SHA256 位于 Dockerfile 的 `ARG`；升级时同时更新 amd64/arm64 哈希和 `tests/images_test.go` 中相关契约。
- `rootfs/etc/s6-overlay/s6-rc.d/` 中定义 Nginx 与 Nginx UI 两个长期服务；服务名以空文件注册到 `rootfs/etc/s6-overlay/user-bundles.d/user/contents.d/`。不要改回已弃用的 `s6-rc.d/user/contents.d/`。Nginx 使用 `daemon off`，Nginx UI 以 `umask 077` 启动并收紧持久文件权限。
- Nginx UI 在构建阶段使用 `strip --strip-unneeded` 去除 Go 符号表，并在同一阶段执行版本冒烟测试后卸载 `binutils`；不要把构建工具带进最终镜像。
- `rootfs/etc/logrotate.d/nginx` 由 Nginx UI 内置定时任务直接调用，不依赖 cron/systemd。安装 `logrotate` 时显式选择 `anacron` 满足其调度器依赖，避免拉入 `cron` 和 `systemd`；`anacron` 同样不启动，不要改成删除依赖包或自行实现日志轮转。
- 不要默认启动所有动态模块。默认启用 GeoIP2、Stream、Stream-GeoIP2，RTMP 等模块保留在 `modules-available/`。
- Nginx UI 的端口、工作目录和 Nginx 配置目录由 Compose 环境变量传入；不要重新设计其内部配置。

### `images/php`

基础为官方 `php:8.4-fpm-trixie`。只通过 `mlocati/php-extension-installer` 安装扩展，不手写编译流程；Composer 从官方 Composer 镜像复制。

PHP 的 `php.ini` 只维护实例模板 `templates/php/php.ini`，通过 Compose 挂载生效；镜像内不要再复制第二份，也不要为 `composer --version`增加单独构建层。`fpm.conf` 只保留必要覆盖，不要让实例配置意外覆盖官方 FPM 环境变量行为。当前动态池保持 5 个空闲 worker，并将 `pm.max_children` 上限设为 20；20 是突发上限而不是 20 个重型 PHP 请求的资源保证，调整前要同时考虑宿主机内存和单请求实际占用。

### `tests`

`images_test.go` 是不需要 Docker daemon 的契约测试，覆盖镜像组件、s6 服务布局、PHP 扩展、Nginx 基线、健康检查不泄漏密码、PHP 配置单一来源等。修改镜像或模板时要先扩展这些测试，再实现变更。

真实 Docker 集成测试需要 Linux runner；本地通过 `runner-info.test` 连接的 runner 只能作为人工验证环境，不能把凭据或 runner 文件带回仓库。

### `.github/workflows`

`ci.yml` 负责持续集成与容器发布：

1. 仅在 Go、模板、镜像或工作流文件变化时触发；纯文档、配置示例和其他无关文件的变化不启动 workflow。tag push 不触发镜像重新发布。
2. `changes` job 检测 Nginx/PHP 镜像目录；矩阵只包含实际变化的镜像。手动触发时构建两个镜像。
3. Go 测试在 Linux 上执行格式检查、`go test`、race、vet 和构建。
4. Docker 构建使用 Buildx；只有非 PR 发布才启用 QEMU 和 arm64。PR 只构建 amd64 且不推送，master/main 推送构建 amd64/arm64 并覆盖基础标签。
5. Docker Hub 登录使用 `DOCKERHUB_USERNAME` 和 `DOCKERHUB_TOKEN` secrets。Nginx 与 PHP 发布到同一个 `webdock` 仓库，只覆盖 `nginx-1.26` 和 `php-8.4`；开发阶段不发布 commit 或 Git tag 版本化镜像。
6. GHCR 发布代码保留但默认关闭，由工作流中的 `PUBLISH_GHCR` 变量控制；启用前需确认仓库 `GITHUB_TOKEN` 有 `packages: write` 权限，并将变量设为 `true`。

`release-binaries.yml` 负责 CLI 二进制发布：

1. 仅在 `cmd/`、`internal/`、`templates/`、`go.mod` 或 `go.sum` 发生变化的 master/main 推送时触发，也支持手动触发。测试代码、文档、镜像和工作流自身变化不会重建二进制。
2. 测试、race、vet 和 staticcheck 通过后，以 `CGO_ENABLED=0` 构建 Linux amd64/arm64 二进制并生成 SHA256 文件。
3. 从 `cmd/webdock/VERSION` 得到 `v0.x.y` tag，创建或更新对应 Release。
4. 同名二进制、校验文件和 `build-info.txt` 使用 GitHub CLI 的 `--clobber` 原地覆盖，不随每次提交累积资产。
5. 修改版本文件会创建新的 Release tag；版本不变时只更新当前 Release 资产。

不要在工作流中写入明文 Docker Hub token，也不要把 registry 凭据烘焙进镜像。

## 实例运行时目录

`webdock init` 生成的结构：

```text
<instance>/
├── webdock.env
├── compose.yaml
├── config/
│   ├── nginx/
│   │   ├── nginx.conf
│   │   ├── conf.d/
│   │   ├── modules-available/
│   │   ├── modules-enabled/
│   │   ├── sites-available/
│   │   ├── sites-enabled/
│   │   ├── snippets/
│   │   ├── ssl/
│   │   ├── streams-available/
│   │   └── streams-enabled/
│   ├── php/php.ini
│   ├── valkey/valkey.conf
│   └── mysql/my.cnf
├── sites/
├── data/
│   ├── nginx-ui/
│   ├── valkey/
│   ├── mysql/
│   └── postgres/
├── logs/nginx/
└── backups/
```

`config/nginx/modules-enabled` 是启用状态，`modules-available` 是模块入口来源。`sites-enabled` 链接到 `sites-available`，`streams-enabled` 用于 stream 配置。默认证书由 init 生成；`webdock.env` 由 init 生成且不会自动迁移旧格式。

## 主要流程

### Init

`app.cmdInit` → `instance.Init` → `ensureDirectories` → `seedNginx`/`seedFile` → `render.ValkeyConfig` → `render.Compose` → 写入 `webdock.env`。只要 `webdock.env` 已存在就视为已初始化，避免误覆盖。

### Up / Restart

`up` 先校验布局，渲染临时 Valkey bind 地址，检查停止状态下的端口冲突和 Compose preflight，执行 `docker compose up -d --wait --wait-timeout 300`，再通过 `nginx -t`、`php-fpm -t`、`valkey-cli`、`pg_isready`/`mysqladmin` 和监听规则验证运行时。若渲染后的 Valkey 配置需要重载且 Valkey 已在运行，只重启 Valkey，不重启整个栈。失败时恢复旧 Valkey 配置。

### 数据库切换

`db use` 先校验引擎和实例布局。运行中的栈只停止旧数据库服务，更新 `webdock.env`，渲染 Compose，再启动目标 profile；目标启动或探测失败时回滚配置并尝试恢复旧引擎。不得把迁移数据伪装成切换数据库。

### Backup / Restore

备份先通过固定容器名判断栈是否运行，必要时通过 Compose stop；归档完成后按原状态启动。恢复同样先检测固定容器，再从 staging 校验实例，最后替换 `webdock.env`、`config/`、`data/`、可选的 `sites/` 和 `logs/`。恢复失败时应重新启动原栈。

## 安全与质量规则

- 错误信息不得包含密码；凭据通过环境变量或 `REDISCLI_AUTH`/`MYSQL_PWD` 传递。
- 所有写文件操作必须考虑部分失败和原子性；已有文件优先保留，除非命令语义明确要求替换（例如恢复、Valkey 渲染）。
- 新增目录或挂载时同时考虑备份、恢复、权限、容器 UID 和实例迁移。
- 不引入需要常驻额外容器的依赖，不把简单单机部署拆成微服务控制平面。
- 不跟随不可信归档中的软链接；不要为了兼容性关闭 `validateEntry` 或 `ensureParentPath`。
- 不在运行逻辑中加入“发现容器就 `docker stop`”的旁路。恢复必须在可能时通过可加载的 Compose 文件停止，并且停止后再次确认。
- Nginx 模块必须通过 Debian 包来源安装，保留模块文件与启用软链接的分离；用户自行写 `conf` 时不应要求修改 Go 代码。
- 镜像契约测试只验证必要的组件、配置键和安全边界，不要把可独立升级的补丁版本或可调参数数值写死；版本升级不应仅因常量变化导致测试失败。
- 不把 README 写成开发手册；构建细节、发布流程和内部约束应放在 `AGENTS.md` 或 `CHANGELOG.md`。

## 修改与验证清单

每次提交前至少执行：

```bash
gofmt -l cmd internal tests templates
go test ./...
go vet ./...
go test -race ./...
go build -trimpath -o bin/webdock ./cmd/webdock
./bin/webdock --version
```

涉及 Dockerfile 或模板时额外执行：

```bash
docker build -t webdock:nginx-test images/nginx
docker build -t webdock:php-test images/php
```

具备真实 Docker 环境时再执行：

```bash
webdock init /tmp/webdock-test --db none
cd /tmp/webdock-test
webdock up
webdock status
webdock doctor
webdock --version
webdock backup
webdock restore --force backups/<archive>.tar.gz
webdock down
```

对于生命周期、数据库切换、备份恢复和网络发现的改动，必须补充 fake runner 单元测试；不要在单元测试中依赖宿主机 Docker。修改 `compose.yaml.tmpl`、实例目录或容器名时，必须同时检查 `tests/images_test.go`、`internal/app/app_test.go`、`internal/instance/instance_test.go` 和 `internal/render/render_test.go`。

改动完成后更新 `CHANGELOG.md`；尚未进入新版本的改动放入新的 `Unreleased` 段，不重写已发布的 `0.1.0` 记录。如果修改会影响用户安装、目录、命令、配置或端口，再同步 `README.md`。
