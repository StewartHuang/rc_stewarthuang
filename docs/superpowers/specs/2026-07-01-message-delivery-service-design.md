# 消息投递服务设计文档

## 概述

企业内部多个业务系统在关键事件发生时，需要调用外部系统供应商提供的 HTTP(S) API 进行通知。本服务负责接收业务系统提交的外部 HTTP 通知请求，并尽可能可靠地投递到目标地址。

- 不同供应商的 API 请求地址、Header、Body 格式不同
- 业务系统不需要关心外部 API 的返回值
- 业务系统只需确保通知请求能够被稳定、可靠地送达

## 架构

**单体 Outbox 模式** — 单进程运行 HTTP API Server + Background Worker，使用 SQLite 持久化存储。

```
┌──────────────┐   POST /api/v1/notifications
│  业务系统 A   │ ──────────────► ┌────────────────────────────────────┐
│  业务系统 B   │                 │        Delivery Service              │
│  业务系统 C   │                 │  单进程 (HTTP Server + Worker)        │
└──────────────┘                 │  ┌─────────┐  ┌───────────────────┐  │
                                 │  │ Gin API │  │ Background Worker │  │
                                 │  └────┬────┘  │ (轮询 + 投递)      │  │
                                 │       │        └────────┬──────────┘  │
                                 │       ▼                  ▼            │
                                 │  ┌─────────────────────────────────┐  │
                                 │  │          SQLite                │  │
                                 │  │  notifications / suppliers     │  │
                                 │  │  delivery_attempts             │  │
                                 │  └─────────────────────────────────┘  │
                                 └────────────────────────────────────┘
                                               │
                                               ▼
                                      ┌────────────────────┐
                                      │  外部供应商 API      │
                                      └────────────────────┘
```

**核心组件**:
- **Gin HTTP Server** — 接收业务系统的通知提交请求和管理 API 请求
- **Background Worker** — 定期轮询待投递消息，执行 HTTP 投递，处理重试和死信
- **SQLite 存储** — 使用 `modernc.org/sqlite`（纯 Go，无需 CGO）持久化所有数据

## API 设计

### 提交通知

```
POST /api/v1/notifications
Content-Type: application/json

{
  "supplier": "ad-system",
  "url": "https://override-url.com",    // 可选，覆盖供应商默认 URL
  "method": "POST",                      // 可选，覆盖供应商默认方法
  "headers": {"X-Extra": "value"},      // 可选，与供应商默认 headers 合并
  "body": {"user_id": 123, "action": "register"},
  "idempotency_key": "evt_abc123"       // 可选，幂等键
}

→ 202 Accepted
{
  "id": "notif_uuid",
  "status": "accepted"
}
```

- `idempotency_key` 用于去重，相同 key 的重复请求返回已有记录

### 查询通知状态

```
GET /api/v1/notifications/:id

→ 200 OK
{
  "id": "notif_uuid",
  "supplier": "ad-system",
  "status": "pending",
  "attempt_count": 2,
  "next_retry_at": "2026-07-01T12:00:00Z",
  "created_at": "2026-07-01T10:00:00Z",
  "updated_at": "2026-07-01T10:00:05Z"
}
```

状态取值: `pending`, `delivered`, `failed`, `dead`

### 死信管理

```
GET /api/v1/notifications?status=dead
  → 获取所有死信

POST /api/v1/notifications/:id/replay
  → 重新投递死信（重置状态为 pending，清空 next_retry_at）
```

### 供应商管理

```
GET    /api/v1/suppliers              → 列出所有供应商
GET    /api/v1/suppliers/:name        → 查看单个供应商
POST   /api/v1/suppliers              → 创建供应商
PUT    /api/v1/suppliers/:name        → 更新供应商
DELETE /api/v1/suppliers/:name        → 删除供应商
```

## 数据库设计

使用 SQLite，纯 Go 驱动 `modernc.org/sqlite`。

### 表结构

```sql
CREATE TABLE suppliers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    url TEXT NOT NULL,
    method TEXT NOT NULL DEFAULT 'POST',
    headers TEXT NOT NULL DEFAULT '{}',
    retry_max_attempts INTEGER NOT NULL DEFAULT 15,
    retry_base_delay_ms INTEGER NOT NULL DEFAULT 1000,
    retry_max_delay_ms INTEGER NOT NULL DEFAULT 240000,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE notifications (
    id TEXT PRIMARY KEY,
    supplier TEXT NOT NULL,
    url TEXT NOT NULL,
    method TEXT NOT NULL DEFAULT 'POST',
    headers TEXT NOT NULL DEFAULT '{}',
    body TEXT NOT NULL DEFAULT '{}',
    idempotency_key TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 15,
    next_retry_at TEXT,
    dead_reason TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE delivery_attempts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    notification_id TEXT NOT NULL,
    attempt_number INTEGER NOT NULL,
    status TEXT NOT NULL,
    response_status INTEGER,
    response_body TEXT,
    error_message TEXT,
    attempted_at TEXT NOT NULL
);

CREATE INDEX idx_notifications_status_next_retry
    ON notifications(status, next_retry_at);
CREATE UNIQUE INDEX idx_notifications_idempotency
    ON notifications(idempotency_key);
```

- 所有校验逻辑在应用层实现，DB 层不做 CHECK 约束
- 逻辑外键无数据库级约束
- 死信通过 `notifications.status = 'dead'` + `dead_reason` 字段表达，无独立 dead_letters 表

### 重试策略

指数退避 + 随机抖动:
```
delay = min(base_delay_ms * 2^attempt, max_delay_ms) + random(0, 50%)
```

默认参数:
- `base_delay_ms = 1000` (1s)
- `max_delay_ms = 240000` (4min)
- `max_attempts = 15`

每个供应商可在配置中独立覆盖以上参数。

## 配置文件

`config.yaml`，启动时加载。供应商配置同时也写入 DB（启动时同步）。

```yaml
server:
  port: 8080

database:
  path: ./delivery.db

worker:
  poll_interval: 500ms
  max_concurrency: 10
  http_timeout: 30s

suppliers:
  - name: ad-system
    url: https://api.adsystem.com/notify
    method: POST
    headers:
      Authorization: "Bearer ${AD_SYSTEM_TOKEN}"
      Content-Type: application/json
    retry:
      max_attempts: 15
      base_delay: 1s
      max_delay: 240s
```

支持 `${ENV_VAR}` 语法从环境变量注入敏感信息。

## Worker 投递流程

1. 每 500ms 轮询 `notifications` 表
2. 查询条件: `status IN ('pending', 'failed') AND (next_retry_at IS NULL OR next_retry_at <= datetime('now'))`
3. 批次获取，最多 `max_concurrency` 条
4. 对每条消息启动独立 goroutine 执行 HTTP 投递
5. 成功 (2xx): 更新 `status = 'delivered'`
6. 失败: `attempt_count++`；计算下次重试时间；若超过 `max_attempts` 则 `status = 'dead'` + 写 `dead_reason`
7. 每次投递记录写入 `delivery_attempts` 表

## 项目目录结构

```
.
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── api/
│   │   ├── router.go
│   │   ├── notification.go
│   │   ├── supplier.go
│   │   └── dead_letter.go
│   ├── worker/
│   │   └── worker.go
│   ├── db/
│   │   ├── db.go
│   │   ├── notification.go
│   │   ├── supplier.go
│   │   └── delivery.go
│   ├── model/
│   │   └── types.go
│   └── config/
│       └── config.go
├── config.yaml
├── go.mod
└── Makefile
```

## 错误处理

- **投递失败**: 记录到 `delivery_attempts`，按退避策略重试
- **超时投递**: 使用 `context.WithTimeout`，默认 30s，返回失败
- **幂等性**: UNIQUE 索引保证相同 `idempotency_key` 不会重复创建
- **优雅关闭**: 捕获 SIGTERM/SIGINT，等待 Worker 当前批次完成后退出
- **无效请求**: 400 响应，返回错误描述

## 测试策略

- **单元测试**: `internal/db/` 和 `internal/worker/` 使用 SQLite `:memory:` 模式
- **Handler 测试**: `internal/api/` 使用 `httptest.NewRecorder`
- **集成测试**: 启动完整服务，验证端到端提交通知 → 投递成功流程
