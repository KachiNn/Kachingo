# Kachingo 任务管理 API

一个使用 Go 标准库编写的轻量级任务管理 REST API，适合本地运行、学习 Go 后端服务结构，以及作为小型服务的起点。无需安装第三方依赖。

## 功能

- 任务创建、列表、修改和删除（CRUD）
- JSON 文件持久化，服务重启后自动恢复任务
- 并发安全的数据访问，任务按 ID 顺序返回
- 请求体大小限制、JSON 格式校验和明确的错误状态码
- 健康检查接口与优雅关闭
- 自动化测试覆盖任务生命周期、数据恢复和无效请求

## 环境要求

- Go 1.22 或更新版本

## 启动

在仓库根目录执行：

```sh
go run .
```

默认监听地址为 `:8080`，数据保存在 `data/tasks.json`。可以通过环境变量修改：

PowerShell：

```powershell
$env:ADDR = ":9090"
$env:DATA_FILE = "./data/tasks.json"
go run .
```

macOS / Linux：

```sh
ADDR=:9090 DATA_FILE=./data/tasks.json go run .
```

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/healthz` | 健康检查 |
| `GET` | `/api/tasks` | 获取全部任务 |
| `POST` | `/api/tasks` | 创建任务，必填 `title` |
| `PATCH` | `/api/tasks/{id}` | 修改 `title` 和/或 `done` |
| `DELETE` | `/api/tasks/{id}` | 删除任务 |

创建任务示例：

```sh
curl -X POST http://localhost:8080/api/tasks \
  -H "Content-Type: application/json" \
  -d '{"title":"完成 Go 服务"}'
```

返回示例：

```json
{
  "id": 1,
  "title": "完成 Go 服务",
  "done": false,
  "created_at": "2025-01-01T00:00:00Z"
}
```

## 测试与构建

```sh
go test ./...
go vet ./...
go build -o kachingo .
```

## 数据说明

任务数据默认写入 `data/tasks.json`。删除或移动该文件会影响已有数据；如需更换位置，请在启动时设置 `DATA_FILE`。当前存储适用于单个服务实例，不适合多个实例同时写入同一个文件的生产部署。
