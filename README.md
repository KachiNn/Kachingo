# Kachingo

Kachingo 是一个用 Go 构建的轻量级项目任务协作服务。它提供用户注册、会话认证、项目成员角色和项目内任务管理，适合本地运行、阅读代码和作为后端工程实践项目。

## 功能

- 邮箱注册与登录；密码使用 bcrypt 哈希，登录凭证为随机 bearer session，数据库仅保存 token 摘要
- 项目创建与成员邀请，角色分为 owner、editor、viewer
- 项目任务增删改查，支持状态、关键词、分页和优先级
- SQLite 持久化，外键约束、索引和按版本执行的 SQL migration
- 项目资源按成员关系隔离；viewer 只读
- 原有 `/api/tasks` JSON 文件接口暂时保留，便于兼容旧用法
- HTTP 服务超时、请求体大小限制、健康检查和优雅关闭

## 快速开始

要求 Go 1.22 或更新版本。SQLite 驱动为纯 Go 实现，常规构建不需要 CGO 或 GCC。

```sh
go run .
```

服务默认监听 `:8080`，SQLite 数据默认保存到 `data/kachingo.db`。首次启动会自动创建数据库并运行 `migrations/` 中尚未应用的迁移。

| 环境变量 | 默认值 | 用途 |
| --- | --- | --- |
| `ADDR` | `:8080` | HTTP 监听地址 |
| `DATABASE_FILE` | `data/kachingo.db` | 新版协作 API 的 SQLite 数据库 |
| `DATA_FILE` | `data/tasks.json` | 旧版 `/api/tasks` 接口的数据文件 |

## API

`GET /healthz` 检查进程存活；`GET /readyz` 通过有时限的数据库 Ping 检查服务是否可接收流量，数据库不可用时返回 503。新版业务接口统一使用 `/api/v1`。除注册和登录外，其余业务接口需要传入 `Authorization: Bearer <token>`。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/api/v1/auth/register` | 注册用户；字段 `email`、`name`、`password` |
| `POST` | `/api/v1/auth/login` | 登录并返回 24 小时 bearer session |
| `POST` | `/api/v1/auth/logout` | 撤销当前 session |
| `GET` | `/api/v1/me` | 获取当前用户 |
| `GET` / `POST` | `/api/v1/projects` | 列出已加入项目 / 创建项目 |
| `GET` | `/api/v1/projects/{projectID}/members` | 列出项目成员及角色 |
| `POST` | `/api/v1/projects/{projectID}/members` | owner 按邮箱添加 editor 或 viewer |
| `DELETE` | `/api/v1/projects/{projectID}/members/{userID}` | owner 移除成员；项目 owner 不可移除 |
| `GET` / `POST` | `/api/v1/projects/{projectID}/tasks` | 列出 / 创建项目任务 |
| `PATCH` / `DELETE` | `/api/v1/projects/{projectID}/tasks/{taskID}` | 更新 / 删除项目任务 |

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

任务状态为 `open`、`in_progress` 或 `done`；优先级为 `low`、`normal` 或 `high`。可选负责人必须是项目成员，截止日期使用 `YYYY-MM-DD`。任务列表支持 `status`、`q`、`assignee_id`、`unassigned=true`、`overdue=true`、`limit`（1-100）和 `offset`。任务响应包含 `version`；更新时必须提交刚读取到的版本，成功后版本递增，过期版本返回 409，缺少版本返回 428。PATCH 中 `assignee_id: null` 或 `due_date: null` 可清除对应字段；移除成员会自动清除其项目任务指派。没有项目成员关系时，项目返回 404；viewer 写入返回 403。

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

## API 契约与 CI

OpenAPI 3.1 契约位于 `openapi.yaml`。GitHub Actions 在 Go 1.22 上执行测试、`go vet` 和构建，并校验 Docker 镜像与 Compose 配置。

## 数据与边界

新版协作接口使用 SQLite，适合单实例部署和教学演示；SQLite 文件应纳入备份。多实例生产部署需要 PostgreSQL 等服务端数据库，目前尚未提供 PostgreSQL 驱动或部署配置。旧版 JSON API 和新版 SQLite API 是两套独立存储，旧 JSON 数据不会自动迁移。

## 开发记录

本仓库的阶段性变更记录见 [CHANGELOG.md](CHANGELOG.md)。
