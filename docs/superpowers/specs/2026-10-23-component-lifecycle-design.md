# Webdock 组件独立化设计

- 状态：已实现
- 日期：2026-10-23
- 范围：组件启用与版本状态、独立启停、镜像校验、数据库逻辑备份和按组件恢复

## 1. 目标

把固定容器栈改成五个可独立编排的组件：

```text
nginx
php
redis
mysql
postgres
```

具体目标：

- 每个组件通过独立环境变量同时表示启用状态和软件版本。
- 新实例只启用 Nginx，其余组件默认关闭。
- 整个容器栈可以统一 `up`/`down`，单个组件也可以临时 `start`/`stop`/`restart`。
- PHP 不提供 Web 入口，必须依赖已启用并运行的 Nginx；Nginx 可以完全独立运行。
- MySQL 和 PostgreSQL 可分别启用、同时启用或都不启用。
- Redis 组件使用 Valkey 官方镜像，但服务和目录统一命名为 `redis`。
- 数据库可在运行状态下生成独立逻辑备份。
- 不维护版本白名单；由镜像引用拼接和 Docker 镜像存在性检查决定版本是否可用。
- 备份记录每个组件的启用状态和版本；恢复时逐组件处理版本冲突。
- 删除 `DB_ENGINE`、`db use` 和 `ENABLED_COMPONENTS`，不保留历史兼容。

非目标：

- 多实例、多主机、Swarm、Kubernetes。
- Compose 之外的组件控制器或常驻 daemon。
- 自动跨数据库大版本升级或数据目录迁移。
- 额外的站点、PHP 扩展或 Nginx 模块管理命令。

## 2. 环境变量与版本

实例的组件状态保存在 `webdock.env`：

```text
ENABLE_NGINX=true
ENABLE_PHP=false
ENABLE_REDIS=false
ENABLE_MYSQL=false
ENABLE_POSTGRES=false
```

取值规则：

- `false`：禁用组件。
- `true`：启用并使用该组件的默认版本。
- 其它值：启用组件，并将该值作为精确版本片段拼接镜像标签。
- `true/false` 不区分大小写；版本值只允许 `[A-Za-z0-9][A-Za-z0-9._-]*`，防止配置值破坏 Compose 模板。
- 五个变量都必须存在；缺失、空值或非法字符直接报配置错误。

默认版本和镜像映射：

| 变量 | 默认 | 镜像规则 | 已验证值 |
| --- | --- | --- | --- |
| `ENABLE_NGINX` | `1.26` | `hicasper/webdock:nginx-<version>` | `1.26` |
| `ENABLE_PHP` | `8.4` | `hicasper/webdock:php-<version>` | `8.4` |
| `ENABLE_REDIS` | `8` | `valkey/valkey:<version>` | `7`, `8` |
| `ENABLE_MYSQL` | `8.4` | `mysql:<version>` | `8.4` |
| `ENABLE_POSTGRES` | `16` | `tensorchord/vchord-suite:pg<version>-latest` | `16`, `17`, `18` |

这张表只说明默认值和已验证组合，不作为代码中的版本白名单。生成的 `webdock.env` 会在对应变量旁写入注释，记录真值对应的默认版本和当前已验证选项。

`php` 启用时必须有 `nginx`；启用 `php` 或禁用 `nginx` 导致违反该约束时直接拒绝并保持原配置。

## 3. 镜像解析与存在性检查

组件配置解析后生成：

```text
name       固定组件名
enabled    是否启用
setting    原始环境变量值，例如 true 或 8.4
version    解析后的精确版本，例如 8.4
image      最终 Docker 镜像引用
```

镜像存在性检查不依赖版本列表：

1. 先执行 `docker image inspect <image>`；本地已存在即通过。
2. 本地不存在时执行 `docker manifest inspect <image>`。
3. manifest 不存在或无法读取时，报错并明确显示组件、版本和完整镜像引用。

该检查用于 `enable`、`up`、`start`、`restart` 和恢复前的目标镜像校验，不用于 `down`、`stop`、`status` 等不需要镜像的操作。这样离线使用已有本地镜像仍然可行，也不会为了版本检查预先下载大镜像。

架构不匹配仍由 Docker/Compose 拉取阶段报告；存在性检查只负责确认标签可用。

Redis、MySQL 和 PostgreSQL 在首次成功启动后分别写入
`data/<component>/.webdock-version`。之后启动前若配置版本与标记不同，提示：

```text
redis: data was initialized with 7, configuration now uses 8. Continue anyway? [y/N]
```

默认回答为 `n`。回答 `y` 时继续启动，并在整次启动及健康探测成功后把标记更新为新版本；回答 `n`、直接回车或 EOF 时在启动任何容器前中止本次操作。标记只在成功后更新，失败启动不会把旧数据误标成新版本。归档恢复接受新版本时会同时替换该标记。

## 4. Compose 模型

每个服务都使用同名 Compose profile：

```yaml
services:
  nginx:
    image: hicasper/webdock:nginx-1.26
    profiles: ["nginx"]
  php:
    image: hicasper/webdock:php-8.4
    profiles: ["php"]
  redis:
    image: valkey/valkey:8
    profiles: ["redis"]
  mysql:
    image: mysql:8.4
    profiles: ["mysql"]
  postgres:
    image: tensorchord/vchord-suite:pg16-latest
    profiles: ["postgres"]
```

Compose 文件由环境变量解析结果重新渲染，因此修改 `ENABLE_*` 后下一次 `up` 会使用新的镜像引用。禁用组件仍保留默认镜像引用，以便 Compose 能完整解析所有服务；未启用组件不会执行镜像存在性检查。

CLI 根据启用状态或显式组件名传入 `--profile`。检查全部服务时使用全部 profile。不增加 `depends_on`，PHP 对 Nginx 的依赖由配置校验和运行命令处理。

## 5. 命令语义

```text
webdock init [DIR]
webdock enable <component> [version]
webdock disable <component>
webdock up [--pull]
webdock down
webdock start <component>
webdock stop <component>
webdock restart [component]
webdock status
webdock logs [component]
webdock doctor
webdock shell <component>
webdock db backup mysql|postgres
webdock backup [--with-sites] [--with-logs]
webdock restore [--force] <archive>
webdock ui
```

行为约定：

- `init` 写入默认组件状态，只启用 `nginx`。
- `enable <component>` 写入 `true`，即组件默认版本；`enable <component> <version>` 写入精确版本。两种形式都先复用镜像解析和存在性检查，失败时不修改状态。
- `disable <component>` 写入 `false`。`enable/disable` 只修改持久状态，不隐式启动或停止容器。
- `up` 按启用状态收敛：停止正在运行但已禁用的组件，然后启动全部已启用组件；没有启用组件时只执行停止和配置校验，并成功返回。
- `down` 使用全部 profile，停止并移除整个项目容器，不改变启用状态。
- `start <component>` 要求组件已启用，只启动该组件并做对应探测。
- `stop <component>` 只停止该组件，不改变启用状态。
- `restart` 无参数时重启当前正在运行的已启用组件；带组件参数时只重启该组件。
- `status` 明确输出每个组件的启用状态、版本和镜像，并继续显示 Compose 服务状态。
- `logs`、`shell` 不能绕过实例配置。

### 5.1 PHP 依赖

- `enable php` 时若 `nginx` 未启用，报错并要求先启用 Nginx。
- `disable nginx` 时若 `php` 仍启用，报错并要求先禁用 PHP。
- `start php` 和 `restart php` 要求 Nginx 容器正在运行；否则报错提示先启动 Nginx。
- `up` 同时启动已启用的 Nginx 和 PHP，不把二者绑定为同一个 Compose 服务。
- 单独停止 Nginx 不级联停止 PHP；此时 PHP 进程仍存活，但不再有 Web 入口，下一次 `up` 会恢复两者。

## 6. 运行态与探测

- 所有运行态查询均使用全部 profile，避免把已禁用但尚未停止的容器误判为不存在。
- 停止状态下的端口冲突只检查已启用组件对应的端口。
- `up` 只要求已启用组件全部运行，不再硬编码 Nginx、PHP、Redis 必选。
- `up`/`start` 在启动 Redis、MySQL 或 PostgreSQL 前检查数据目录版本标记，禁止无意跨版本使用同一数据目录。
- `doctor` 在栈完全停止时检查启用组件的端口；栈部分或全部运行时验证全部已启用组件。
- 停止禁用组件使用 Compose `stop`；容器保留到后续 `down` 清理。

对应端口：

| 组件 | 端口 |
| --- | --- |
| `nginx` | 80、443、9001 |
| `php` | 9000 |
| `redis` | 6379 |
| `mysql` | 3306 |
| `postgres` | 5432 |

固定容器名同步改为：

```text
webdock-nginx
webdock-php
webdock-redis
webdock-mysql
webdock-postgres
```

## 7. 数据库与 Redis 独立管理

数据库和 Redis 不再通过全局单选变量切换。各组件可以同时启用：

```bash
webdock enable redis
webdock enable mysql
webdock enable postgres
webdock up
webdock stop mysql
webdock start mysql
```

Redis 使用 `valkey/valkey:<version>` 镜像，但以下名称统一为 `redis`：

```text
service/container/profile: redis
config dir: config/redis
data dir: data/redis
env: ENABLE_REDIS, REDIS_PASSWORD, REDIS_MAXMEMORY, REDIS_BRIDGE_NETWORKS
```

数据库逻辑备份：

```bash
webdock db backup mysql
webdock db backup postgres
```

约束和实现：

- 目标数据库必须已启用且正在运行。
- MySQL 使用 `mysqldump --single-transaction --quick --routines --events --triggers`，输出 gzip 压缩的 SQL。
- PostgreSQL 使用 `pg_dump --format=custom`，保留自定义格式的压缩和恢复能力。
- 文件写入 `backups/db/`：MySQL 为 `.sql.gz`，PostgreSQL 为 `.dump`。
- 使用临时文件、单独 stderr 捕获、成功关闭后 rename，避免半截备份覆盖已有文件。
- 密码通过容器内环境变量展开，不进入命令行参数和日志。
- 逻辑备份不停止数据库；需要一致性文件级备份时继续使用完整的 `webdock backup`。

本次只新增数据库备份命令，不新增数据库恢复命令。完整实例恢复仍由 `webdock restore` 负责；逻辑备份用于外部保存和人工导入。

## 8. 完整备份

完整 tar.gz 备份包含 `webdock.env`、`config/`、`data/`，可选包含 `sites/` 和 `logs/`。另外在归档根目录写入 `manifest.json`：

```json
{
  "created_at": "2026-10-23T00:00:00Z",
  "webdock_version": "0.x.y",
  "components": {
    "nginx": "1.26",
    "php": null,
    "redis": null,
    "mysql": null,
    "postgres": null
  }
}
```

`components` 中每个组件只有一个字符串或 `null` 参数：字符串是备份时的精确启用版本，`null` 表示未启用。镜像引用由版本重新解析，不在 manifest 中重复保存。`format` 字段没有实际迁移用途且项目尚未承诺历史兼容，因此不写入；manifest 只接受当前结构。

运行态也按组件记录。备份流程：

1. 记录备份前实际运行的全部组件。
2. 停止全部容器。
3. 创建包含 manifest 的归档。
4. 只重新启动备份前实际运行的组件，不启动其它已启用但原本临时停止的组件。

## 9. 按组件恢复与版本冲突

旧归档格式不兼容；恢复必须存在可解析的 `manifest.json`，且必须包含全部五个组件键。恢复先把归档安全解压到 staging，校验路径、软链接、环境文件和实例布局，然后逐组件比较归档版本与当前版本。

组件恢复边界：

| 组件 | 恢复路径及环境键 |
| --- | --- |
| `nginx` | `config/nginx/`、`data/nginx-ui/`、`ENABLE_NGINX`、Nginx UI 密钥 |
| `php` | `config/php/`、`ENABLE_PHP` |
| `redis` | `config/redis/`、`data/redis/`、`ENABLE_REDIS`、Redis 密码与参数 |
| `mysql` | `config/mysql/`、`data/mysql/`、`ENABLE_MYSQL`、MySQL 密码 |
| `postgres` | `data/postgres/`、`ENABLE_POSTGRES`、PostgreSQL 密码 |

`sites/` 和 `logs/` 不属于单个组件，仍按归档是否包含它们整体恢复。

版本处理：

- manifest 中组件为 `null`：不提示，恢复后保持禁用，保留现有组件数据。
- 归档与当前都启用且解析版本一致：直接恢复该组件的配置、数据和环境键。
- 归档启用，但当前禁用或版本不同：逐组件提示：

```text
mysql: backup uses 8.4, current uses false. Restore 8.4 data and change version? [y/N]
```

- 输入 `y`：把该组件环境变量写成归档的精确版本，恢复其配置、数据和环境键。
- 输入 `n`：跳过该组件的版本、配置和数据恢复，保留当前组件状态与数据，继续询问或恢复下一个组件。
- 其它输入重新提示；输入流 EOF 时整体中止恢复，避免非交互环境误跳过组件。

所有需要恢复的归档镜像先执行存在性检查，通过后才停止容器和替换文件。恢复应用阶段：

1. 以归档 `webdock.env` 为基础，对被跳过的组件恢复当前环境键。
2. 原子替换合并后的 `webdock.env`。
3. 仅原子替换被接受组件的配置和数据子目录；被跳过组件保持原样。
4. 重新渲染 Compose。
5. 只重建“恢复前曾运行”且“恢复后仍启用”的组件。
6. 任一步失败时回滚已替换目录和环境文件；成功后保持其它原有临时停止状态。

这样 `n` 只中断当前组件，不影响后续组件恢复，也不会把临时 `stop` 意外扩大成整个栈重启。

## 10. 代码调整

### `internal/instance`

- 新增五个 `ENABLE_*` 变量的解析、默认版本、镜像引用和 `Component` 模型。
- 生成的环境文件为 `ENABLE_*` 添加默认版本和已验证版本注释。
- `enable` 支持可选的精确版本参数，并复用镜像引用和存在性校验。
- 删除 `DB_ENGINE`、`ENABLED_COMPONENTS`、`ValidateDBEngine` 和 `SetDBEngine`。
- `Init` 只写默认启用 Nginx。
- 环境文件继续通过同目录临时文件、`0600` 权限和 rename 更新。

### `internal/docker`

- `Profiles` 接收组件名列表，不再理解数据库引擎。
- `AllProfiles` 返回五个固定组件。
- 新增通过 `image inspect`/`manifest inspect` 检查镜像引用的执行器方法。
- Compose 参数顺序保持不变。

### `internal/render`

- Compose 模板改为 Go template，接收五个镜像引用。
- Redis 配置模板路径和渲染目标改为 `config/redis`。

### `internal/app`

- `lifecycle.go` 实现启用集合收敛、单组件启停、镜像预检、版本标记提示和按集合启动。
- `database.go` 改为数据库逻辑备份。
- `runtime.go` 的端口、必需服务、Redis 重启和探测全部改为组件驱动。
- `backup.go` 写入精简 manifest，保存并恢复精确运行组件集合。
- 新增按组件恢复计划、交互提示、环境合并、目录替换和回滚。
- `status` 输出持久启用状态、解析版本和镜像。

### 模板与测试

- Compose 模板为五个服务增加同名 profile 和动态镜像字段。
- 契约测试验证默认环境变量、五组件 profile、Redis 名称和无旧变量残留。
- 单元测试覆盖版本解析、`enable [version]`、镜像拼接、PHP 依赖、`up` 收敛、单组件启停、数据库备份、精简 manifest 和逐组件版本冲突。
- 通过 fake runner 验证镜像不存在、启动版本标记的默认 n/y/EOF、restore 的 y/n/EOF 分支和部分恢复回滚，不依赖真实 Docker。
- Linux Docker runner 做真实多组合启停、端口、备份恢复和镜像存在性验证。

## 11. 验收标准

- 新实例 `webdock.env` 只启用 Nginx，并带有各组件默认/已验证版本注释。
- `webdock up` 后只运行 `webdock-nginx`。
- 将 `ENABLE_PHP=true` 后 `webdock up` 运行 Nginx 和 PHP，站点可正常访问。
- Nginx 禁用时无法启用 PHP；PHP 启用时无法禁用 Nginx。
- Redis、MySQL、PostgreSQL 可分别启动、停止、同时启用和跨重启持久化。
- 不存在的镜像标签在启动前报出组件、版本和镜像引用。
- 启动时数据版本标记与配置不同，默认 n 中止；y 强制继续且仅在成功后更新标记。
- `webdock stop mysql` 不改变启用状态；`webdock start mysql` 只恢复 MySQL。
- `webdock down` 停止整个项目，再次 `up` 恢复全部已启用组件。
- MySQL/PostgreSQL 逻辑备份文件可被对应官方客户端识别。
- 备份 manifest 用每个组件一个字符串或 `null` 准确记录启用版本，不保存冗余 `format`、镜像或重复状态字段。
- 恢复版本不一致时，`y` 接受归档版本和数据，`n` 跳过当前组件并继续后续组件，EOF 整体中止。
- 备份和恢复不会启动原本临时停止的已启用组件。
- 所有现有 Go、race、vet、staticcheck、契约和 Linux 集成检查通过。

## 12. 已采用的取舍

- `enable/disable` 是纯持久状态操作，不隐式启停容器。
- `up` 是启用状态的收敛命令；`start/stop` 是临时运行态操作。
- 版本不使用白名单，镜像引用拼接后由 Docker 实际检查，已验证值只写在配置注释和文档中。
- MySQL 与 PostgreSQL 允许同时启用，不保留二选一约束。
- Redis 从 7 开始支持，不使用 Redis 官方镜像；组件名仍统一为 `redis`。
- 不增加 Compose `depends_on`，PHP 对 Nginx 的要求由 CLI 和配置校验处理。
- 数据库逻辑备份只做在线导出，不扩展为数据库迁移或恢复编排。
