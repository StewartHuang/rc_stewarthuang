# Message Deliver System

企业内部多个业务系统在关键事件发生时，通过本服务可靠地投递 HTTP(S) 通知到外部供应商 API。

- **可靠投递**: 持久化存储 + 至少一次投递语义 + 指数退避重试 + 死信队列
- **零依赖存储**: 使用 SQLite
- **单二进制部署**: 无外部中间件依赖
- **供应商管理**: 支持配置文件 + 运行时 API 双模式管理供应商接入信息
- **幂等性**: 支持 idempotency_key，防止重复投递

## 架构

```
┌──────────────┐   POST /api/v1/notifications
│  业务系统 A   │ ──────────────► ┌────────────────────────────────────┐
│  业务系统 B   │                 │        Delivery Service              │
│  业务系统 C   │                 │  单进程 (HTTP Server + Worker)        │
└──────────────┘                 │  ┌─────────┐  ┌───────────────────┐  │
                                 │  │ Gin API │  │ Background Worker │  │
                                 │  └────┬────┘  │ (轮询 + HTTP 投递)  │  │
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
                                      │  (广告/CRM/库存等)   │
                                      └────────────────────┘
```

## 快速开始

### 启动服务

```bash
# 构建
make build

# 运行（使用默认配置 config.yaml）
make run

# 或指定配置文件
go run ./cmd/server /path/to/config.yaml
```

### 配置

配置文件 `config.yaml`:

```yaml
server:
  port: 8080

database:
  path: ./delivery.db

worker:
  poll_interval: 500ms      # 轮询间隔
  max_concurrency: 10        # 最大并发投递数
  http_timeout: 30s          # 投递 HTTP 超时

suppliers:
  - name: ad-system
    url: https://api.adsystem.com/notify
    method: POST
    headers:
      Content-Type: application/json
      Authorization: "Bearer ${AD_SYSTEM_TOKEN}"   # 支持环境变量注入
    retry:
      max_attempts: 15        # 最大重试次数
      base_delay: 1s          # 退避初始间隔
      max_delay: 240s         # 退避最大间隔
```

`${ENV_VAR}` 语法可从环境变量注入敏感信息（如 Token、API Key）。

## API 参考

### 提交通知

```
POST /api/v1/notifications
Content-Type: application/json

{
  "supplier": "ad-system",
  "url": "https://override-url.com",
  "method": "PUT",
  "headers": {"X-Extra": "value"},
  "body": {"user_id": 123, "action": "register"},
  "idempotency_key": "evt_abc123"
}

→ 202 Accepted
{
  "id": "notif_uuid",
  "status": "accepted"
}
```

| 字段 | 必需 | 说明 |
|------|------|------|
| `supplier` | 是 | 供应商名称，引用已配置的供应商 |
| `body` | 否 | 投递内容，原样透传给外部 API |
| `url` | 否 | 覆盖供应商默认 URL |
| `method` | 否 | 覆盖供应商默认 HTTP 方法 |
| `headers` | 否 | 与供应商默认 headers 合并，同名覆盖 |
| `idempotency_key` | 否 | 幂等键，相同 key 的重复请求返回已有记录 |

### 查询通知状态

```
GET /api/v1/notifications/:id

→ 200 OK
{
  "id": "notif_uuid",
  "supplier": "ad-system",
  "status": "pending",
  "attempt_count": 2,
  "max_attempts": 15,
  "next_retry_at": "2026-07-01T12:00:00Z",
  "dead_reason": null,
  "created_at": "2026-07-01T10:00:00Z",
  "updated_at": "2026-07-01T10:00:05Z"
}
```

状态取值: `pending` → `failed` → `delivered` / `dead`

### 通知列表

```
GET /api/v1/notifications?status=pending
GET /api/v1/notifications?status=delivered
GET /api/v1/notifications?status=failed
GET /api/v1/notifications?status=dead
```

### 死信重放

```
POST /api/v1/notifications/:id/replay

→ 202 Accepted
{
  "status": "replayed"
}
```

将死信重置为 pending 状态，重设计数，重新加入投递队列。

### 供应商管理

```
GET    /api/v1/suppliers              # 列出所有供应商
GET    /api/v1/suppliers/:name        # 查看单个供应商
POST   /api/v1/suppliers              # 创建供应商
PUT    /api/v1/suppliers/:name        # 更新供应商
DELETE /api/v1/suppliers/:name        # 删除供应商
```

创建供应商:

```
POST /api/v1/suppliers
Content-Type: application/json

{
  "name": "crm-system",
  "url": "https://crm.company.com/api/contacts/status",
  "method": "PUT",
  "headers": {
    "X-API-Key": "${CRM_API_KEY}",
    "Content-Type": "application/json"
  },
  "retry": {
    "max_attempts": 10,
    "base_delay": "2s",
    "max_delay": "120s"
  }
}

→ 201 Created
```

## Worker 投递机制

Background Worker 每 500ms 轮询一次，处理流程:

1. 查询 `status IN ('pending', 'failed')` 且到达重试时间的通知
2. 并发执行 HTTP 投递（最大并发数由 `max_concurrency` 控制）
3. 成功 (2xx): 标记 `delivered`
4. 失败: 记录投递尝试，计算下次重试时间

### 重试策略

指数退避 + 随机抖动:

```
delay = min(base_delay * 2^attempt, max_delay) * (1 + random(0, 50%))
```

默认参数:
- `base_delay`: 1s
- `max_delay`: 240s (4min)
- `max_attempts`: 15

每个供应商可在配置或 API 中独立覆盖。

### 死信

超过最大重试次数后，通知进入 `dead` 状态，可通过死信重放 API 重新投递。

## 项目结构

```
.
├── cmd/server/main.go           # 入口
├── config.yaml                  # 配置文件
├── Makefile                     # 构建/运行/测试
├── internal/
│   ├── config/config.go         # 配置加载
│   ├── model/types.go           # 数据模型
│   ├── db/
│   │   ├── db.go                # 数据库初始化 & 迁移
│   │   ├── supplier.go          # 供应商 CRUD
│   │   ├── notification.go      # 通知 CRUD + 幂等性
│   │   └── delivery.go          # 投递记录
│   ├── api/
│   │   ├── router.go            # 路由注册
│   │   ├── supplier.go          # 供应商管理 API
│   │   ├── notification.go      # 提交通知 & 状态查询
│   │   └── dead_letter.go       # 死信重放
│   └── worker/worker.go         # 后台投递 Worker
└── docs/
    ├── requirement.md
    ├── AI_usage_explanation.md
    └── superpowers/
        ├── specs/
        └── plans/
```

## 测试

```bash
make test
# 或
go test ./...
```
