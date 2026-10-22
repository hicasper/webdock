# Webdock

Webdock 是一个面向单机 Linux 的轻量 Docker Compose 容器栈管理工具，用固定的实例目录管理 Nginx、PHP、Valkey 和可选数据库，适合替代传统 LNMP 的重复手工配置。

项目目前处于 `0.x` 阶段，优先保证结构清晰、部署简单和备份可迁移，适合单机、小规模使用。

## 主要能力

- 一条命令初始化完整实例目录，配置、站点、数据、日志和备份彼此分离。
- 管理 Nginx、PHP-FPM、Valkey、MySQL/PostgreSQL 的启停、状态和日志。
- 内置 Nginx UI，方便管理站点和 Nginx 配置。
- MySQL 与 PostgreSQL 可选，可在不删除旧数据的情况下切换。
- Valkey 默认开启认证、AOF 和内存限制，并支持受控的 Docker bridge 访问。
- 支持离线备份与恢复，敏感文件、目录结构和软链接都会被校验。

## 组件

| 服务 | 当前系列 | 说明 |
| --- | --- | --- |
| `edge` | Nginx 1.26 LTS 系列、Nginx UI 2.x | HTTP/HTTPS、配置管理和常用动态模块 |
| `php` | PHP 8.4 系列 | PHP-FPM、Composer 和常用扩展 |
| `valkey` | Valkey 8.x | 缓存、AOF 持久化和 ACL 认证 |
| `mysql` | MySQL 8.4 LTS 系列 | 可选数据库 |
| `postgres` | PostgreSQL 18.x | 可选数据库 |

镜像按主版本或稳定系列维护，升级时以兼容当前实例目录为前提。

## 环境要求

- Linux x86-64 或 ARM64
- rootful Docker Engine
- Docker Compose v2
- 建议至少 1 vCPU、512 MB 内存；启用数据库时需预留额外内存

不支持 Docker Desktop、rootless Docker、Podman 或 SELinux 强制模式。启动前请停止占用 80/443、9000、9001、6379 或所选数据库端口的旧服务。

## 下载与安装

从 [GitHub Releases](https://github.com/hicasper/webdock/releases) 下载对应架构的 `webdock-linux-amd64` 或 `webdock-linux-arm64`。Release 中的二进制会随代码更新而覆盖，具体构建信息见同页的 `build-info.txt`。

```bash
chmod +x webdock-linux-amd64
sudo install -m 0755 webdock-linux-amd64 /usr/local/bin/webdock
webdock --version
```

也可以使用对应的 `.sha256` 文件校验下载内容。

## 快速开始

```bash
webdock init /srv/webdock --db none
cd /srv/webdock
webdock doctor
webdock up
webdock status
```

初始化会生成随机凭据、默认证书、Nginx 配置和 Compose 文件。保存凭据的 `webdock.env` 必须保持 `0600` 权限，也不要提交到版本库。

需要数据库时，可在初始化时选择，或稍后切换：

```bash
webdock init /srv/webdock --db mysql
webdock -C /srv/webdock db use postgres
webdock -C /srv/webdock db use none
```

切换数据库只会启停对应服务，不会删除或迁移另一套数据库数据。

## Nginx UI

Nginx UI 只监听 `127.0.0.1:9001`，建议通过 SSH 隧道访问：

```bash
webdock -C /srv/webdock ui
ssh -L 9001:127.0.0.1:9001 <user>@<server>
```

然后打开 <http://127.0.0.1:9001>。Nginx UI 负责管理实例中的 Nginx 配置；Webdock 负责目录、挂载和进程守护。

## 常用命令

以下命令默认在实例目录中执行；也可以统一使用 `webdock -C /srv/webdock <command>`。

| 命令 | 用途 |
| --- | --- |
| `webdock version` | 查看当前版本 |
| `webdock up [--pull]` | 启动或更新容器栈 |
| `webdock down` | 停止并移除容器栈 |
| `webdock restart` | 重启容器栈 |
| `webdock status` | 查看服务状态 |
| `webdock logs [service]` | 查看并跟随日志 |
| `webdock doctor` | 检查端口、配置和服务健康状态 |
| `webdock shell <service>` | 进入指定容器 |
| `webdock ui` | 显示 Nginx UI 地址和隧道命令 |
| `webdock backup` | 创建离线备份 |
| `webdock restore --force <archive>` | 恢复备份 |

管理非当前目录的实例：

```bash
webdock -C /srv/webdock status
webdock -C /srv/webdock up
```

## 实例目录

```text
/srv/webdock/
├── webdock.env       # 凭据和运行参数
├── compose.yaml      # Webdock 生成的 Compose 配置
├── config/           # Nginx、PHP、Valkey、MySQL 配置
├── sites/            # 站点源码，挂载为 /var/www
├── data/             # 服务持久化数据
├── logs/nginx/       # Nginx 文件日志
└── backups/          # 默认备份目录
```

`config/`、`data/` 和 `webdock.env` 是实例核心状态。迁移实例时应整体迁移这些内容，或使用 Webdock 备份功能。

## Nginx 与 PHP

Nginx 提供 Debian 风格的站点、模块和 stream 配置目录。GeoIP2、Stream 和 Stream-GeoIP2 默认启用；Brotli、RTMP、Lua、Headers More 等模块已安装，需要时可在 `modules-enabled/` 中启用。默认站点返回 `444`，避免未配置域名直接暴露内容。

Nginx 使用 100 秒 keepalive；TLS 禁用 1.0，保留 1.1，并启用 1.2/1.3。面向公网时可进一步按业务需要收紧 TLS 1.1。

PHP 镜像已包含 Composer 和 `bcmath`、`exif`、`gd`、`intl`、MySQL/PostgreSQL、Redis、sockets、zip 等常用扩展。实例默认关闭错误回显、开启错误日志；FPM 使用受限的动态进程池，限制单请求最长 60 秒并定期回收 worker，适合低内存单机。

## Valkey

Valkey 默认只监听回环地址，使用认证、AOF、`allkeys-lru` 和内存上限，避免意外占用整机内存。

如需让其他 Docker bridge 网络访问，在 `webdock.env` 中配置：

```text
VALKEY_BRIDGE_NETWORKS=bridge,myproject_default
```

Webdock 只会绑定实际存在且属于本机的网关地址，不会退化为监听 `0.0.0.0`。

## 日志

- Nginx 日志位于 `logs/nginx/`，其中 access log 位置已预留。
- 其他服务默认输出到 Docker stdout/stderr，由 json-file 驱动限制大小。
- 使用 `webdock logs [service]` 查看，不需要进入容器。

## 备份与恢复

```bash
webdock backup
webdock backup --with-sites --with-logs
webdock restore --force backups/webdock-YYYYMMDD-HHMMSS.tar.gz
```

默认备份包含 `webdock.env`、`config/` 和 `data/`，不包含站点源码和日志。备份期间会停止容器栈，完成后恢复原运行状态。恢复会先校验归档，校验失败不会覆盖当前数据。

数据库和 Valkey 可能将文件权限调整为容器内 UID，建议使用 root 或具备完整目录读写权限的用户执行备份与恢复。

## 安全提示

- Nginx UI、PHP-FPM、数据库和 Valkey 均不直接监听公网地址。
- Nginx 使用 host 网络，最终暴露端口由实例中的 Nginx 配置决定。
- 备份包含密码和全部服务数据，应按敏感文件保存。
- `restore --force` 会替换现有实例数据，执行前请确认归档来源。

## 参考项目

- [garylab/dnmp](https://github.com/garylab/dnmp)
- [hicasper/lnmp](https://github.com/hicasper/lnmp)
- [0xJacky/nginx-ui](https://github.com/0xJacky/nginx-ui)
