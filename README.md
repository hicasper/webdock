# Webdock

Webdock 是一个面向单机 Linux 的轻量 Docker Compose 容器栈管理工具。它用固定的实例目录管理 Nginx、PHP、Redis、MySQL 和 PostgreSQL，适合替代传统 LNMP 的手工维护方式。

项目目前处于 `0.x` 阶段，优先保证部署简单、组件可独立管理、数据可迁移。

## 主要能力

- 一条命令初始化实例目录，配置、站点、数据、日志和备份彼此分离。
- 每个组件通过 `webdock.env` 独立启用，新实例默认只启用 Nginx。
- 支持整个容器栈启停，也可以单独启动、停止或重启某个组件。
- 内置 Nginx UI，方便管理站点和 Nginx 配置。
- Redis 组件使用 Valkey 官方镜像；MySQL 与 PostgreSQL 可以同时启用。
- 支持离线完整备份、逐组件版本冲突恢复和数据库逻辑备份。

## 组件与版本

| 组件 | 默认版本 | 当前已验证版本 | 说明 |
| --- | --- | --- | --- |
| `nginx` | 1.26 | 1.26 | Nginx、Nginx UI 和常用动态模块 |
| `php` | 8.4 | 8.4 | PHP-FPM、Composer 和常用扩展 |
| `redis` | 8 | 7、8 | 使用 `valkey/valkey` 镜像 |
| `mysql` | 8.4 | 8.4 | 可选数据库 |
| `postgres` | 16 | 16、17、18 | 使用 `tensorchord/vchord-suite` 镜像 |

组件状态和版本直接写在 `webdock.env`：

```text
ENABLE_NGINX=true
ENABLE_PHP=false
ENABLE_REDIS=false
ENABLE_MYSQL=false
ENABLE_POSTGRES=false
```

- `false`：禁用组件。
- `true`：启用并使用默认版本。
- 具体版本：启用并使用指定版本，例如 `ENABLE_MYSQL=8.4`。

Webdock 会把组件和版本拼接成镜像并检查是否可用，不维护额外的版本白名单。未验证的镜像标签可能被接受，但不在当前支持范围内。

Redis 从 7 开始支持；MySQL 5.7 已停止维护，不列为支持版本。

## 环境要求

- Linux x86-64 或 ARM64
- rootful Docker Engine
- Docker Compose v2
- 建议至少 1 vCPU、512 MB 内存；启用数据库时需预留额外内存

不支持 Docker Desktop、rootless Docker、Podman 或 SELinux 强制模式。启动前请停止占用 80/443、9000、9001、6379、3306 或 5432 的旧服务。

## 下载与安装

从 [GitHub Releases](https://github.com/hicasper/webdock/releases) 下载对应架构的 `webdock-linux-amd64` 或 `webdock-linux-arm64`。

```bash
chmod +x webdock-linux-amd64
sudo install -m 0755 webdock-linux-amd64 /usr/local/bin/webdock
webdock --version
```

也可以使用同页的 `.sha256` 文件校验下载内容。

## 快速开始

```bash
webdock init /srv/webdock
cd /srv/webdock
webdock doctor
webdock up
webdock status
```

初始化后默认只运行 Nginx。需要 PHP、Redis 或数据库时：

```bash
webdock enable php
webdock enable redis
webdock enable mysql 8.4
webdock up
```

`webdock enable <component>` 使用默认版本；`webdock enable <component> <version>` 使用指定版本。启用或禁用命令只修改持久状态，运行中的容器由 `up`、`start`、`stop` 或 `restart` 控制。

PHP 必须与 Nginx 一起使用。启用 PHP 前需要启用 Nginx，PHP 容器启动时也要求 Nginx 正在运行。

## 常用命令

以下命令默认在当前实例目录执行，也可以用 `webdock -C /srv/webdock <command>`。

| 命令 | 用途 |
| --- | --- |
| `webdock init [DIR]` | 初始化实例，默认只启用 Nginx |
| `webdock enable <component> [version]` | 持久启用组件，可指定版本 |
| `webdock disable <component>` | 持久禁用组件 |
| `webdock up [--pull]` | 启动或收敛全部已启用组件 |
| `webdock down` | 停止并移除整个容器栈 |
| `webdock start <component>` | 启动一个已启用组件 |
| `webdock stop <component>` | 临时停止一个组件，不改变启用状态 |
| `webdock restart [component]` | 重启运行中的组件 |
| `webdock status` | 查看启用状态和容器状态 |
| `webdock logs [component]` | 查看并跟随日志 |
| `webdock doctor` | 检查端口、配置和服务健康状态 |
| `webdock shell <component>` | 进入指定容器 |
| `webdock ui` | 显示 Nginx UI 地址和 SSH 隧道命令 |
| `webdock db backup mysql\|postgres` | 在线导出数据库逻辑备份 |
| `webdock backup` | 创建离线完整备份 |
| `webdock restore --force <archive>` | 恢复完整备份 |

## 实例目录

```text
/srv/webdock/
├── webdock.env       # 凭据、组件状态和版本
├── compose.yaml      # Webdock 生成的 Compose 配置
├── config/
│   ├── nginx/
│   ├── php/
│   ├── redis/
│   └── mysql/
├── sites/            # 站点源码，挂载为 /var/www
├── data/             # 组件数据及 .webdock-versions
├── logs/nginx/       # Nginx 文件日志
└── backups/          # 完整备份和数据库逻辑备份
```

`config/`、`data/` 和 `webdock.env` 是实例核心状态。迁移实例时应整体迁移这些内容，或使用 Webdock 备份功能。

## Nginx 与 PHP

Nginx 提供 Debian 风格的站点、模块和 stream 配置目录。GeoIP2、Stream 和 Stream-GeoIP2 默认启用；Brotli、RTMP、Lua、Headers More 等模块已安装，需要时可在 `modules-enabled/` 中启用。默认站点返回 `444`。

Nginx UI 只监听 `127.0.0.1:9001`，建议通过 SSH 隧道访问：

```bash
webdock ui
ssh -L 9001:127.0.0.1:9001 <user>@<server>
```

PHP 镜像包含 Composer 和常用扩展。PHP-FPM 只监听回环地址，使用受限的动态进程池，适合低内存单机。

## Redis、MySQL 与 PostgreSQL

Redis 组件使用 Valkey 镜像，服务和目录名统一为 `redis`。默认只监听回环地址和经检测属于本机的 Docker bridge 网关，使用 ACL、AOF 和 `allkeys-lru`。

如需让其他 Docker bridge 网络访问 Redis，可修改：

```text
REDIS_BRIDGE_NETWORKS=bridge,myproject_default
```

Webdock 不会退化为监听 `0.0.0.0`。

MySQL 和 PostgreSQL 可以独立启停，也可以同时运行：

```bash
webdock enable mysql
webdock enable postgres
webdock up
webdock stop mysql
webdock start mysql
```

数据库逻辑备份写入 `backups/db/`。MySQL 生成 `.sql.gz`，PostgreSQL 生成 `.dump`。

Redis、MySQL 和 PostgreSQL 的实际数据版本集中记录在 `data/.webdock-versions`。配置版本变化时会提示确认；默认不继续启动，选择 `y` 后才会强制使用新版本，并在成功启动后更新记录。

## 备份与恢复

```bash
webdock backup
webdock backup --with-sites --with-logs
webdock restore --force backups/webdock-YYYYMMDD-HHMMSS.tar.gz
```

默认备份包含 `webdock.env`、`config/` 和 `data/`，不包含站点源码和日志。备份期间会停止运行中的容器，完成后只恢复原本正在运行的组件。

归档中的 `webdock.env` 和 `data/.webdock-versions` 记录组件配置与实际数据版本，不再生成额外的 manifest 文件。恢复时如果归档版本与当前实例不一致，会逐组件询问：

- `y`：接受归档版本并恢复该组件的数据。
- `n` 或直接回车：跳过当前组件，继续处理下一个组件。

无效输入会重新提示；输入流结束会中止恢复，避免在非交互环境中误跳过组件。

## 安全提示

- Nginx UI、PHP-FPM、数据库和 Redis 均不直接监听公网地址。
- Nginx 使用 host 网络，最终暴露端口由实例中的 Nginx 配置决定。
- 备份包含密码和全部服务数据，应按敏感文件保存。
- `restore --force` 会替换现有实例数据，执行前请确认归档来源。

## 参考项目

- [garylab/dnmp](https://github.com/garylab/dnmp)
- [hicasper/lnmp](https://github.com/hicasper/lnmp)
- [0xJacky/nginx-ui](https://github.com/0xJacky/nginx-ui)
