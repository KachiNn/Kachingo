# Kachingo

A dependency-free task management REST API written in Go. It demonstrates common backend service patterns: HTTP routing, JSON APIs, concurrency-safe in-memory storage, graceful shutdown, and tests.

## Requirements

- Go 1.22 or newer

## Run

```sh
go run .
```

The server listens on port 8080. Set `ADDR` to change the bind address, for example `ADDR=:9090 go run .`.

## API

- `GET /healthz`: health check
- `GET /api/tasks`: list tasks
- `POST /api/tasks`: create a task with `{"title":"Write tests"}`
- `PATCH /api/tasks/{id}`: update `title` and/or `done`
- `DELETE /api/tasks/{id}`: delete a task

Example:

```sh
curl -X POST http://localhost:8080/api/tasks -H 'Content-Type: application/json' -d '{"title":"Write tests"}'
curl http://localhost:8080/api/tasks
```

Tasks are kept in memory and reset when the process stops. This is a runnable starter, not a production persistence layer.

## Test

```sh
go test ./...
```