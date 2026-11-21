# Kachingo

[![CI](https://github.com/KachiNn/Kachingo/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/KachiNn/Kachingo/actions/workflows/ci.yml)

Kachingo 是一个用 Go 构建的轻量级项目任务协作服务。它提供用户注册、会话认证、项目成员角色和项目内任务管理，适合本地运行、阅读代码和作为后端工程实践项目。

## 项目背景

课程作业、竞赛和毕业设计常靠群聊与共享表格协调。讨论、分工和截止时间混在消息里，组员很难在开会或交付前确认谁在做什么、哪些任务已经完成。Kachingo 把任务、负责人、截止日期和任务讨论放在同一个项目中，让组员打开看板就能查看进度。

主要面向做课程设计、竞赛或毕业设计的大学生小组，也适合需要轻量任务看板的小团队。组长可以添加已注册的同学并分配角色，组员通过浏览器更新状态和留言。负责人指派目前可通过 API 操作。

## 开发分工与质量控制

仓库记录的交付工作包括梳理小组协作流程，搭建 Go API 和 SQLite 持久化，接入网页看板与实时推送，以及补齐测试、文档和 CI。个人贡献可按实际提交逐项核对。[提交记录](https://github.com/KachiNn/Kachingo/commits/main/)

开发过程中使用 AI 辅助实现和补充测试。验收以代码差异和可重跑的结果为准。权限、任务版本冲突和数据恢复用 Go 测试验证，网页主流程用浏览器操作验证，竞态和容器启动交给 CI。浏览器曾发现发布评论后实时刷新会收起评论区，调整刷新逻辑后重新验证了评论可见且面板保持展开。[网页工作台提交](https://github.com/KachiNn/Kachingo/commit/499373b20b4c1030d71c047c8928777402a4ebaa)

## 技术取舍

| 选择 | 原因 |
| --- | --- |
| Go 标准库 HTTP | Go 1.22 的路由支持方法和路径参数，直接使用标准库便于阅读、测试和分发。 |
| SQLite 与纯 Go 驱动 | 项目和成员关系可用事务及外键约束，本地启动只需数据文件，常规构建无需 CGO。 |
| 原生网页与 go:embed | 页面随服务发布，组员打开浏览器即可使用，本地运行无需前端构建工具。 |
| WebSocket | 把任务更新通知送到在线成员的页面，减少协作时手动刷新。广播依赖单实例内存。 |
| Docker 与 GitHub Actions | 让提交自动经过测试、竞态检查和容器启动验证，便于复查。 |

## 可验证的成果

- 从浏览器完成注册登录、创建项目和任务、移动任务状态并发布评论。用下方快速开始即可复现。
- Go 测试覆盖项目成员权限、看板、连接中移除成员后的广播隔离，以及 SQLite 备份恢复。可以运行 `go test -count=1 ./...` 复查。
- 网页工作台提交的 `verify` 和 `race` 已通过，容器冒烟验证实际启动服务并请求 `/readyz` 与 `/healthz`。[GitHub Actions 运行记录](https://github.com/KachiNn/Kachingo/actions/runs/44493929404)

## 功能

- 邮箱注册与登录；密码使用 bcrypt 哈希，登录凭证为随机 bearer session，数据库仅保存 token 摘要
- 项目创建与成员邀请，角色分为 owner、editor、viewer
- 项目任务增删改查，支持状态、关键词、分页和优先级
- SQLite 持久化，外键约束、索引和按版本执行的 SQL migration
- 项目资源按成员关系隔离；viewer 只读
- **WebSocket 实时协作**：任务变更、评论发布实时推送给项目在线成员；服务端每 30 秒 ping，60 秒无响应断开
- 原有 `/api/tasks` JSON 文件接口暂时保留，便于兼容旧用法
- 内嵌网页看板：登录、项目创建、任务看板、状态流转、评论和成员管理；使用 WebSocket 实时刷新，适合大学生小组直接使用
- HTTP 服务超时、请求体大小限制、健康检查和优雅关闭

## 快速开始

要求 Go 1.22 或更新版本。SQLite 驱动为纯 Go 实现，常规构建不需要 CGO 或 GCC。

```sh
go run .
```

打开 `http://localhost:8080`，注册后即可创建项目和任务。服务默认监听 `:8080`，SQLite 数据默认保存到 `data/kachingo.db`。首次启动会自动创建数据库并运行 `migrations/` 中尚未应用的迁移。

| 环境变量 | 默认值 | 用途 |
| --- | --- | --- |
| `ADDR` | `:8080` | HTTP 监听地址 |
| `DATABASE_FILE` | `data/kachingo.db` | 新版协作 API 的 SQLite 数据库 |
| `DATA_FILE` | `data/tasks.json` | 旧版 `/api/tasks` 接口的数据文件 |
| `LOGIN_RATE_LIMIT` | `10` | 每个客户端 IP 每分钟允许的失败登录次数（1-10000） |

登录接口按 TCP 客户端 IP 统计失败次数，超过阈值返回 `429` 和 `Retry-After`；成功登录后清零。计数仅存于进程内，服务重启后清空；不读取 `X-Forwarded-For` 等代理头。该实现适用于单实例部署，多实例环境应在可信反向代理或共享存储层实现限流。

将旧版 JSON 任务导入新版 SQLite 项目任务时，先确保目标项目和 owner/editor 账号已存在，然后执行：

```sh
go run . import-legacy --project-id 1 --actor-id 1
```

默认读取 `DATA_FILE` 与 `DATABASE_FILE` 指定的文件，也可用 `--file`、`--database` 覆盖。导入需要明确指定项目和操作者；viewer 或非项目成员不能执行。`done` 映射为 `done`，未完成任务映射为 `open`，优先级设为 `normal`，描述为空，任务创建时间尽量保留。任务 ID 由 SQLite 分配，导入操作者会写入任务创建活动。整个文件在一个事务中导入，出错会回滚；源 JSON 不会被修改。相同源文件内容再次导入同一项目会被拒绝，避免重复创建任务；修改后的文件内容会被视为另一次导入。此工具不会改变旧版 `/api/tasks` 的读写行为。

## API

`GET /healthz` 检查进程存活；`GET /readyz` 通过有时限的数据库 Ping 检查服务是否可接收流量，数据库不可用时返回 503。新版业务接口统一使用 `/api/v1`。除注册和登录外，其余业务接口需要传入 `Authorization: Bearer <token>`。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/api/v1/auth/register` | 注册用户；字段 `email`、`name`、`password` |
| `POST` | `/api/v1/auth/login` | 登录并返回 24 小时 bearer session |
| `POST` | `/api/v1/auth/logout` | 撤销当前 session |
| `GET` | `/api/v1/me` | 获取当前用户 |
| `GET` / `POST` | `/api/v1/projects` | 列出已加入项目 / 创建项目 |
| `PATCH` | `/api/v1/projects/{projectID}` | owner 重命名项目 |
| `GET` | `/api/v1/projects/{projectID}/members` | 列出项目成员及角色 |
| `POST` | `/api/v1/projects/{projectID}/members` | owner 按邮箱添加 editor 或 viewer |
| `PATCH` | `/api/v1/projects/{projectID}/members/{userID}` | owner 修改成员角色；项目 owner 角色受保护 |
| `DELETE` | `/api/v1/projects/{projectID}/members/{userID}` | owner 移除成员；项目 owner 不可移除 |
| `GET` | `/api/v1/projects/{projectID}/board` | 按 open/in_progress/done 分列返回项目任务；`limit` 控制每列最多返回数量 |
| `GET` / `POST` | `/api/v1/projects/{projectID}/tasks` | 列出 / 创建项目任务 |
| `GET` | `/api/v1/projects/{projectID}/tasks/{taskID}` | 获取单个项目任务及当前 `version` |
| `PATCH` / `DELETE` | `/api/v1/projects/{projectID}/tasks/{taskID}` | 更新 / 删除项目任务 |
| `GET` | `/api/v1/projects/{projectID}/tasks/{taskID}/events` | 项目成员查看任务创建与状态流转历史 |
| `GET` / `POST` | `/api/v1/projects/{projectID}/tasks/{taskID}/comments` | 项目成员查看评论 / owner 和 editor 发布评论 |

注册请求示例：

```sh
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"dev@example.com","name":"Dev","password":"change-this-password"}'
```

创建项目并添加任务需要先登录，在请求头中使用登录响应里的 `token`：

```sh
curl -X POST http://localhost:8080/api/v1/projects \
  -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' \
  -d '{"name":"Interview Prep"}'

curl -X POST http://localhost:8080/api/v1/projects/1/tasks \
  -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' \
  -d '{"title":"Review Go concurrency","priority":"high"}'
```

任务状态为 `open`、`in_progress` 或 `done`；优先级为 `low`、`normal` 或 `high`。可选负责人必须是项目成员，截止日期使用 `YYYY-MM-DD`。任务列表支持 `status`、`q`、`assignee_id`、`unassigned=true`、`overdue=true`、`limit`（1-100）和 `offset`。`q` 对标题和描述执行不区分大小写的子串搜索，`%`、`_` 等字符按字面匹配。任务响应包含 `version`；更新时必须提交刚读取到的版本，成功后版本递增，过期版本返回 409，缺少版本返回 428。PATCH 中 `assignee_id: null` 或 `due_date: null` 可清除对应字段；移除成员会自动清除其项目任务指派。任务创建和状态变化会原子记录操作者与状态前后值，活动历史按发生顺序读取。任务评论限制为 1-5000 个 Unicode 字符，按创建顺序返回；viewer 只读，删除任务时评论一并清理。没有项目成员关系时，项目返回 404；viewer 写入返回 403。

**WebSocket 实时推送**：`GET /ws?token=<bearer_token>` 升级到 WebSocket 连接。服务端每 30 秒发送 ping，客户端需响应 pong。任务创建、更新和评论发布会实时推送 JSON 消息给项目在线成员：`{"type": "task_created"|"task_updated"|"comment_created", "project_id": <id>, "data": <resource>}`。客户端应实现断线自动重连。

## 代码结构

- `main.go`：旧接口、HTTP 服务启动和关闭
- `server_v1.go`：新版路由、HTTP 参数校验与响应映射
- `auth_project_repository.go`、`project_task_repository.go`：认证、项目成员和任务的数据访问接口及 SQLite 实现
- `database.go`：SQLite 初始化、migration 执行和响应模型
- `auth.go`：密码校验及 session token 生成
- `http_middleware.go`：request ID 和 HTTP 访问日志
- `migrations/`：数据库结构变更脚本，按序号执行
- `*_test.go`：旧接口测试和新版 HTTP/SQLite 集成测试

## 校验

```sh
go test ./...
go vet ./...
go test -race ./...
go build -o kachingo .
```

`-race` 需要 CGO 和可用的 C 编译器；其他命令不需要 GCC。

## 本地容器运行

安装 Docker Compose 后，在仓库根目录运行：

```sh
docker compose up --build
```

容器以非 root 用户运行，Docker healthcheck 通过 `/readyz` 检查数据库连接，数据保存在命名卷 `kachingo-data`。停止服务使用 `docker compose down`；保留数据卷。

## 备份与恢复

在线备份命令示例：

    docker compose run --rm kachingo backup --file /data/backups/kachingo-$(date +%Y%m%d-%H%M%S).db

备份使用 SQLite online backup API，可在服务运行时执行；输出文件必须不存在。恢复前停止服务，并指定一个尚不存在的数据库路径：

    docker compose stop kachingo
    docker compose run --rm --entrypoint sh kachingo -c 'mv /data/kachingo.db /data/kachingo.db.before-restore'
    docker compose run --rm kachingo restore --file /data/backups/kachingo-YYYYMMDD-HHMMSS.db --database /data/kachingo.db
    docker compose up -d

恢复会先检查备份完整性，且拒绝覆盖已有文件。恢复成功后启动服务，应用会按需执行数据库迁移。将备份另存到独立存储，避免与数据卷故障同时丢失。

## API 契约与 CI

OpenAPI 3.1 契约位于 `openapi.yaml`。GitHub Actions 在 Go 1.22 上执行测试、`go vet` 和构建，并构建 Docker 镜像、校验 Compose 配置。CI 还会启动镜像，通过容器映射端口请求 `/readyz` 和 `/healthz`，验证 SQLite 就绪和 HTTP 服务可达。

## 数据与边界

当前以单体 Go 服务和本地 SQLite 文件支撑单实例部署，适合校内小组和教学演示。SQLite 文件应纳入备份。多实例部署规划包括 PostgreSQL、跨实例事件分发和共享限流，需要分别验证数据访问和实时通知。旧版 JSON API 与新版 SQLite API 各自存储，迁移旧任务需要运行上文的导入命令。

| 范围 | 当前边界与改进方向 |
| --- | --- |
| 网页会话 | Bearer token 保存在 localStorage，页面脚本可读取。后续考虑使用 HttpOnly、Secure Cookie，并配套 CSRF 防护。 |
| 实时连接 | WebSocket 握手使用 URL 查询参数传递 token，部署时需避免代理及日志系统记录查询字符串。后续可评估短期连接凭证。 |
| 限流与部署 | 登录限流计数随进程重启清空。多实例可使用共享存储或可信网关统一限流。公开部署还需配置 HTTPS 反向代理和监控告警。 |
| 网页与 API | 组长可在网页成员列表切换编辑和只读角色。创建任务时可选项目成员为负责人，卡片显示分工。已有任务改派和逾期筛选可通过 API 操作。 |
| 产品规划 | 邮件邀请与通知、附件、任务筛选与统计、操作审计与管理员管理、找回密码均在待实现范围。 |

## 开发记录

本仓库的阶段性变更记录见 [CHANGELOG.md](CHANGELOG.md)。
