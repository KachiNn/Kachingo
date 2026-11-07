# 变更记录

## 2026-11-07

- 新增 SQLite schema migration、用户注册登录、bcrypt 密码哈希和可撤销 bearer session。
- 新增项目创建、owner 邀请 editor/viewer、成员权限检查和项目级资源隔离。
- 新增项目任务 CRUD、状态筛选、关键词搜索、优先级和分页。
- 保留旧版 JSON 任务接口，并在 README 说明两套接口的数据边界。
- 集成测试覆盖 owner/viewer/非成员访问和登出后的 session 撤销。
- 增加 Go 1.22 GitHub Actions 校验、非 root 多阶段 Docker 镜像、Compose 配置和 OpenAPI 3.1 契约；CI 会构建镜像并检查 Compose 配置。

## 2026-11-06

- 增加 HTTP 服务的读写、空闲连接和请求头超时保护。
- 将本地构建产物和运行数据目录加入忽略规则。
- 补充输入校验、非法任务 ID、不支持方法和损坏 JSON 数据测试。
