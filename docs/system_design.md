# 消息投递服务系统设计

## 概述

企业内部多个业务系统在关键事件发生时，需要调用外部系统供应商提供的 HTTP(S) API 进行通知。本服务负责接收业务系统提交的外部 HTTP 通知请求，并尽可能可靠地投递到目标地址。

- 不同供应商的 API 请求地址、Header、Body 格式不同
- 业务系统不需要关心外部 API 的返回值
- 业务系统只需确保通知请求能够被稳定、可靠地送达

---

## 1. 系统边界

### 1.1 解决的问题

- **稳定可靠的外部通知触达** — 持久化存储通知请求，异步可靠投递，提供重试和死信机制
- **消息送达策略配置** — 每个供应商可独立配置重试次数、退避间隔、超时等策略
- **供应商接口管理** — 通过配置文件和运行时 API 管理供应商的 URL、Headers、Method 等接入信息
- **幂等性投递** — 支持 `idempotency_key` 防止业务系统重复提交导致重复投递
- **投递记录可追溯** — 每次投递尝试写入 `delivery_attempts` 表，支持查询通知状态和投递历史
- **死信管理** — 超过最大重试次数的通知进入死信状态，支持查询和重新投递
- **回调通知** — 提交通知时可传入回调地址，投递终态时（delivered / dead）系统主动回调通知上游系统，回调失败有独立重试机制
- **自定义 Status Code 接受范围** — 每个供应商可指定哪些 HTTP status code 视为投递成功，替代硬编码的 2xx 判断

### 1.2 明确不解决的问题

| 不解决的问题 | 原因 |
|---|---|
| **外部 API 业务错误处理** — 外部 API 返回的 4xx 业务错误（参数错误、业务校验失败等）由业务系统自行处理 | 业务错误的含义因供应商而异，本服务无法理解或处理这些语义；本服务仅关注 HTTP 层面的送达成功与否（2xx 为成功，其他为失败） |
| **通知内容生成与模板渲染** — 不提供模板引擎、变量替换或内容转换能力 | 不同供应商的 body 格式差异巨大，引入模板引擎会大幅增加复杂性（模板管理、版本控制、变量校验等），且每次新增供应商都需要修改模板逻辑。业务系统最了解目标格式，应由其自行构造 |
| **多请求流程编排** — 不涉及跨供应商的请求编排、条件分支或聚合 | 这是工作流引擎的职责，不是通知投递服务的范围。保持服务职责单一，避免变成"万能管道" |

---

## 2. 架构

### 2.1 架构选型：单体 Outbox 模式

选择 **单体 Outbox 模式** 而非 API+Worker 分离或事件驱动架构。

| 方案 | 优势 | 劣势 |
|---|---|---|
| 单体 Outbox | 单二进制部署，运维简单，事务一致性强 | 吞吐受单进程限制，API 和 Worker 不能独立扩缩容 |
| API+Worker 分离 | API 和 Worker 可独立扩缩 | 部署运维更复杂，需进程间协调 |
| 事件驱动 (消息队列) | 松耦合，高可扩展性 | 引入额外中间件，运维复杂度大增 |

**取舍**：MVP 验证阶段，部署简便性优先于可扩展性。若未来吞吐需求超出单进程能力，参见第 5 章演进路径。

### 2.2 架构图

**单体 Outbox 模式** — 单进程运行 HTTP API Server + Background Worker，使用 SQLite 持久化存储。

```
┌──────────────┐   POST /api/v1/notifications
│  业务系统 A   │ ──────────────► ┌──────────────────────────────────────────┐
│  业务系统 B   │                 │          Delivery Service                  │
│  业务系统 C   │                 │    单进程 (HTTP Server + Workers)          │
└──────────────┘                 │  ┌─────────┐  ┌──────────────────────┐   │
      ▲                          │  │ Gin API │  │   Background Worker  │   │
      │                          │  └────┬────┘  │  (轮询 + 投递)        │   │
      │ callback                  │       │        └────────┬─────────────┘   │
      │ HTTP POST                 │       ▼                  ▼               │
      │ "notif_id + status"       │  ┌───────────────────────────────────┐   │
      │                          │  │          SQLite                  │   │
      │                          │  │  notifications / suppliers      │   │
      │                          │  │  delivery_attempts / callbacks   │   │
      │                          │  │  callback_attempts               │   │
      │                          │  └────────────┬──────────────────────┘   │
      │                          │               │                          │
      │                          │               ▼                          │
      │                          │  ┌──────────────────────┐                │
      │                          │  │  CallbackWorker      │               │
      │                          │  │  (轮询 callbacks 表)  │               │
      │                          │  └──────────────────────┘                │
      │                          └──────────────────────────────────────────┘
      │                                         │
      │                                         ▼
      │                                ┌────────────────────┐
      │  ──────────────────────────────│  外部供应商 API      │
      │                                └────────────────────┘
```

### 2.3 核心组件

- **Gin HTTP Server** — 接收业务系统的通知提交请求和管理 API 请求
- **Background Worker** — 定期轮询待投递消息，执行 HTTP 投递，处理重试和死信
- **CallbackWorker** — 独立的后台轮询协程，负责执行回调通知投递（投递完成/进入死信时回调业务系统），自包含重试逻辑（固定间隔，最多 3 次）
- **GORM + SQLite 存储** — 使用 GORM 作为 ORM 层，`modernc.org/sqlite`（纯 Go，无需 CGO）作为底层驱动。GORM 提供参数化查询，从框架层面避免 SQL 注入风险，同时省去手动拼接 SQL 的重复工作

---

## 3. 可靠性与失败处理

### 3.1 投递语义：至少一次投递（At-Least-Once Delivery）

选择 **持久化 + 至少一次投递 + 死信队列** 而非基础重试或内存队列。

**取舍**：写入延迟换取投递确定性。每条通知至少产生 1 次 SQLite 写入（创建）+ N 次更新（投递尝试/状态变更），写路径比内存队列更慢，但系统崩溃或重启后不丢消息。

### 3.2 失败处理策略

| 失败场景 | 处理方式 |
|---|---|
| **网络超时 / DNS 解析失败 / 连接拒绝** | 记录到 `delivery_attempts`，按退避策略重试 |
| **HTTP 非接受状态码**（不匹配供应商 `accepted_statuses` 列表） | 统一视为投递失败，记录响应状态码和 body，按退避策略重试 |
| **超过最大重试次数**（默认 15 次） | 标记 `status = 'dead'`，写入 `dead_reason` 字段 |
| **请求超时** | 使用 `context.WithTimeout`（默认 30s）控制，超时返回失败 |
| **数据库写入失败** | 创建通知时返回 500 错误给业务系统；投递过程中的写入失败仅记录日志，不影响重试 |
| **进程崩溃** | SQLite 持久化保证消息不丢失，重启后 Worker 从数据库中恢复待投递消息 |
| **优雅关闭** | 捕获 SIGTERM/SIGINT，等待当前批次投递和回调批次完成后退出 |
| **回调投递失败** | 独立 CallbackWorker 轮询 callbacks 表，固定间隔 10s 重试，最多 3 次，超过后标记 failed 并记录错误 |

### 3.3 重试策略：指数退避 + 随机抖动

```
delay = min(base_delay_ms × 2^attempt, max_delay_ms) + random(0, 50%)
```

默认参数（每个供应商可独立覆盖）：

| 参数 | 默认值 | 说明 |
|---|---|---|
| `base_delay_ms` | 1000 (1s) | 首次重试延迟 |
| `max_delay_ms` | 240000 (4min) | 最大延迟上限 |
| `max_attempts` | 15 | 最大重试次数 |

15 次重试的理论总等待时间约 8 分钟（4min × 2 次达到上限 + 之前的指数增长阶段）。

### 3.4 Worker 投递流程

1. 每 500ms 轮询 `notifications` 表
2. 查询条件: `status IN ('pending', 'failed') AND (next_retry_at IS NULL OR next_retry_at <= datetime('now'))`
3. 批次获取，最多 `max_concurrency` 条
4. 对每条消息启动独立 goroutine 执行 HTTP 投递
5. 成功 (供应商 `accepted_statuses` 列表匹配): 更新 `status = 'delivered'`
6. 失败: `attempt_count++`；计算下次重试时间；若超过 `max_attempts` 则 `status = 'dead'` + 写 `dead_reason`
7. 每次投递记录写入 `delivery_attempts` 表
8. 到达终态时（delivered / dead），若通知有 `callback_url`，插入 `callbacks` 表由 CallbackWorker 异步处理

### 3.5 死信处理

超过 `max_attempts` 后，通知进入 `dead` 状态并记录失败原因。提供 API 支持查询和重新投递死信（重置为 `pending` 状态，清空 `next_retry_at`）。

### 3.6 回调投递流程

1. CallbackWorker 独立于主 Worker，每 2s 轮询 `callbacks` 表
2. 查询条件: `status IN ('pending', 'failed') AND (next_retry_at IS NULL OR next_retry_at <= datetime('now'))`
3. 对每条回调执行 HTTP POST（body: `{"notification_id":"...","status":"..."}`）
4. 成功 (2xx): 标记 `status = 'completed'`
5. 失败: `attempt_count++`；固定间隔 10s 后重试；超过最大次数（3 次）标记 `status = 'failed'` 并记录 `last_error`
6. 每次回调尝试写入 `callback_attempts` 表

---

## 4. API 设计

### 4.1 提交通知

```
POST /api/v1/notifications
Content-Type: application/json

{
  "supplier": "ad-system",
  "url": "https://override-url.com",    // 可选，覆盖供应商默认 URL
  "method": "POST",                      // 可选，覆盖供应商默认方法
  "headers": {"X-Extra": "value"},      // 可选，与供应商默认 headers 合并
  "body": {"user_id": 123, "action": "register"},  // 原样透传给外部 API
  "idempotency_key": "evt_abc123",                  // 可选，幂等键
  "callback_url": "https://biz.company.com/callback/notif"  // 可选，回调地址
}

→ 202 Accepted
{
  "id": "notif_uuid",
  "status": "accepted"
}
```

- `idempotency_key` 用于去重，相同 key 的重复请求返回已有记录

### 4.2 查询通知状态

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

### 4.3 死信管理

```
GET /api/v1/notifications?status=dead      → 获取所有死信
POST /api/v1/notifications/:id/replay       → 重新投递死信（重置为 pending）
```

### 4.4 供应商管理

```
GET    /api/v1/suppliers              → 列出所有供应商
GET    /api/v1/suppliers/:name        → 查看单个供应商
POST   /api/v1/suppliers              → 创建供应商
PUT    /api/v1/suppliers/:name        → 更新供应商
DELETE /api/v1/suppliers/:name        → 删除供应商
```

创建/更新供应商时支持 `accepted_statuses` 字段（JSON 数组，如 `[200, 201]`），不传时默认 `[200]`。

---

## 5. 数据库设计

### 5.1 ORM 选型：GORM

选择 **GORM** 作为数据访问层，而非直接编写原生 SQL。

| 方案 | 优势 | 劣势 |
|---|---|---|
| **GORM**（选择） | 参数化查询防 SQL 注入，AutoMigrate 自动管理表结构，省去重复的 SQL 编写工作 | 引入 ORM 抽象，复杂查询性能略低于手写 SQL |
| 原生 SQL | 最大性能控制，无额外抽象层开销 | 手动拼接 SQL 存在注入风险，每个查询需重复编写，表结构变更需手动维护迁移脚本 |

**取舍**：开发效率和安全性优先于极致性能。GORM 的参数化查询机制从框架层面杜绝 SQL 注入；AutoMigrate 根据 Model 结构体自动创建/更新表，消除手动维护 DDL 的工作量。Worker 的查询模式（按 status + next_retry_at 轮询）简单规整，ORM 不会成为性能瓶颈。

### 5.2 Model 定义

GORM 通过 Model 结构体定义表结构，启动时由 `AutoMigrate` 自动同步到数据库。

#### Supplier

```go
type Supplier struct {
    ID               uint   `gorm:"primaryKey;autoIncrement"`
    Name             string `gorm:"uniqueIndex;size:255;not null"`
    URL              string `gorm:"size:2048;not null"`
    Method           string `gorm:"size:10;not null;default:POST"`
    Headers          string `gorm:"type:text;not null;default:{}"`
    RetryMaxAttempts int    `gorm:"not null;default:15"`
    RetryBaseDelayMs int    `gorm:"not null;default:1000"`
    RetryMaxDelayMs  int    `gorm:"not null;default:240000"`
    AcceptedStatuses string `gorm:"type:text;not null;default:'[200]'"`
    Enabled          bool   `gorm:"not null;default:true"`
    CreatedAt        time.Time
    UpdatedAt        time.Time
}
```

#### Notification

```go
type Notification struct {
    ID             string     `gorm:"primaryKey;size:36"`
    Supplier       string     `gorm:"size:255;not null"`
    URL            string     `gorm:"size:2048;not null"`
    Method         string     `gorm:"size:10;not null;default:POST"`
    Headers        string     `gorm:"type:text;not null;default:{}"`
    Body           string     `gorm:"type:text;not null;default:{}"`
    IdempotencyKey *string    `gorm:"uniqueIndex;size:255"`
    CallbackURL    *string    `gorm:"size:2048"`
    Status         string     `gorm:"size:20;not null;default:pending;index:idx_status_next_retry"`
    AttemptCount   int        `gorm:"not null;default:0"`
    MaxAttempts    int        `gorm:"not null;default:15"`
    NextRetryAt    *time.Time `gorm:"index:idx_status_next_retry"`
    DeadReason     *string   `gorm:"type:text"`
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

#### DeliveryAttempt

```go
type DeliveryAttempt struct {
    ID               uint      `gorm:"primaryKey;autoIncrement"`
    NotificationID   string    `gorm:"size:36;not null;index"`
    AttemptNumber    int       `gorm:"not null"`
    Status           string    `gorm:"size:20;not null"`
    ResponseStatus   *int
    ResponseBody     *string   `gorm:"type:text"`
    ErrorMessage     *string   `gorm:"type:text"`
    AttemptedAt      time.Time
}
```

#### Callback

```go
type Callback struct {
    ID              uint       `gorm:"primaryKey;autoIncrement"`
    NotificationID  string     `gorm:"size:36;not null"`
    CallbackURL     string     `gorm:"size:2048;not null"`
    Status          string     `gorm:"size:20;not null;default:pending;index:idx_callbacks_status_next_retry"`
    AttemptCount    int        `gorm:"not null;default:0"`
    MaxAttempts     int        `gorm:"not null;default:3"`
    RetryDelayMs    int        `gorm:"not null;default:10000"`
    LastError       *string    `gorm:"type:text"`
    NextRetryAt     *time.Time `gorm:"index:idx_callbacks_status_next_retry"`
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
```

#### CallbackAttempt

```go
type CallbackAttempt struct {
    ID              uint      `gorm:"primaryKey;autoIncrement"`
    CallbackID      uint      `gorm:"not null;index"`
    AttemptNumber   int       `gorm:"not null"`
    ResponseStatus  *int
    ResponseBody    *string   `gorm:"type:text"`
    ErrorMessage    *string   `gorm:"type:text"`
    AttemptedAt     time.Time
}
```

### 5.3 表结构说明

- 死信通过 `Notification.Status = "dead"` + `DeadReason` 字段表达，无独立 dead_letters 表
- 回调通过独立 `callbacks` 表管理，独立 `callback_attempts` 表记录回调投递历史
- GORM 的 `AutoMigrate` 根据 Model 结构体自动创建表和索引，启动时执行一次
- 复合索引 `idx_status_next_retry` / `idx_callbacks_status_next_retry` 通过 GORM 的 `index` 标签定义
- `IdempotencyKey` 的 `uniqueIndex` 标签保证幂等键唯一性
- 所有校验在应用层实现，DB 层不做外键和 CHECK 约束

---

## 6. 配置与部署

### 6.1 配置文件

`config.yaml`，启动时加载。供应商配置同时写入 DB（启动时同步）。

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
    accepted_statuses: [200, 201]
    retry:
      max_attempts: 15
      base_delay: 1s
      max_delay: 240s
```

支持 `${ENV_VAR}` 语法从环境变量注入敏感信息。

### 6.2 供应商配置：混合模式

选择 **配置文件 + 运行时 API** 而非纯配置文件或纯运行时管理。

**取舍**：配置文件确保启动即可工作，运行时 API 支持生产环境动态调整。代价是需要实现配置同步逻辑（启动时从配置文件导入 DB，运行时通过 API 操作 DB）。

### 6.3 项目目录结构

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
│   │   ├── worker.go             // 主 Worker，轮询投递
│   │   └── callback_worker.go    // CallbackWorker，轮询回调
│   ├── db/
│   │   ├── db.go                // GORM 初始化、AutoMigrate、通用查询方法
│   │   ├── notification.go      // 通知 CRUD
│   │   ├── supplier.go          // 供应商 CRUD
│   │   ├── delivery.go          // 投递记录 CRUD
│   │   └── callback.go          // 回调 CRUD
│   ├── model/
│   │   └── types.go             // GORM Model 结构体定义
│   └── config/
│       └── config.go
├── config.yaml
├── go.mod
└── Makefile
```

---

## 7. 取舍与演进

### 7.1 关键工程决策

| 决策 | 选择 | 替代方案 | 理由 |
|---|---|---|---|
| **架构** | 单体 Outbox | API+Worker 分离、事件驱动 | MVP 部署简便性优先 |
| **存储** | SQLite | PostgreSQL、BoltDB | 零依赖，单二进制部署 |
| **可靠性模型** | 持久化 + 至少一次投递 | 基础重试、内存队列 | 写入延迟换取投递确定性 |
| **供应商配置** | 配置文件 + 运行时 API | 纯配置文件、纯 API | 兼顾启动可用性和运行时灵活性 |
| **内容生成** | 不提供 | 模板引擎 | 避免模板管理复杂度 |
| **业务错误处理** | 不处理 | 解析业务错误码 | HTTP 层面职责清晰 |
| **数据访问层** | GORM ORM | 原生 SQL | 参数化查询防 SQL 注入，AutoMigrate 省去手动管理 DDL |
| **数据约束** | 应用层校验 | 数据库外键 / CHECK | 修改规则无需变更表定义 |
| **回调机制** | 独立 CallbackWorker + callbacks 队列表 | Worker 内联执行 | 回调失败重试不阻塞主投递流程，进程重启不丢重试 |
| **Status Code 判断** | 供应商可配置 `accepted_statuses` 列表 | 硬编码 2xx | 不同供应商对成功响应有不同定义（如某些仅接受 200），可配置更灵活 |

### 7.2 被拒绝的"过度设计"

| 方案 | 拒绝理由 |
|---|---|
| **事件驱动架构（消息队列）** | 引入 Kafka/RabbitMQ 增加运维复杂度，单机 Worker 轮询 SQLite 已足够应对企业内部中等规模 |
| **PostgreSQL** | MVP 版本追求最快可用，零外部依赖优先 |
| **API 与 Worker 分离部署** | 拆分需进程间协调和独立部署流水线，单二进制更简单 |
| **模板渲染引擎** | 增加模板管理、版本控制、变量校验等复杂度 |
| **数据库外键与 CHECK 约束** | 应用层校验更灵活，修改规则无需变更表定义 |
| **独立 dead_letters 表** | status 字段 + dead_reason 更简单，减少表数量 |
| **纯 API 管理供应商** | 配置文件确保启动即可工作，无需提前调用 API |
| **Worker 内联执行回调** | 回调重试会阻塞主 Worker 槽位，影响正常投递吞吐 |

### 7.3 演进路径

#### 第一阶段（当前）：单体 Outbox + SQLite

- 单二进制部署，运维简单
- 零外部依赖（不需要数据库服务器、消息队列）
- 适合日投递量百万级以下的内部场景

#### 第二阶段：API + Worker 分离 + PostgreSQL

触发信号：

- Worker 轮询对 API 写入造成性能影响
- 需要独立扩缩容 API 和 Worker
- 需要多实例部署提高可用性

演进内容：

- 存储切换为 PostgreSQL，解决 SQLite 写入并发上限
- 服务拆分为 API Server 和 Worker 两个独立部署单元
- API 负责接收请求写入 DB，Worker 负责轮询投递

#### 第三阶段：引入消息队列

触发信号：

- 流量大幅增长，DB 轮询模式成为瓶颈
- 需要严格的消息顺序保证或更多消息语义
- 需要跨可用区部署

演进内容：

- 引入 Kafka/RabbitMQ 作为消息中间件
- API 将通知写入 MQ 后立即返回
- Worker 从 MQ 消费消息进行投递
- DB 退化为查询和记录存储，不再承担 pending 消息轮询职责

---

## 8. 存储选型分析

### SQLite vs 其他方案

| 方案 | 优势 | 劣势 |
|---|---|---|
| **SQLite**（选择） | 零依赖，单二进制部署，足够支持中等规模 | 写并发有限，不支持网络访问 |
| PostgreSQL | 高并发，支持网络访问，成熟运维生态 | 需要额外部署和维护数据库实例 |
| BoltDB | 嵌入式，纯 Go | 无 SQL 查询能力，不适合复杂查询 |

**取舍**：零依赖部署的便利性优先于高并发写入能力。SQLite 通过 WAL 模式可支持并发读写，满足企业内部场景。Worker 轮询 + 指数退避的投递模式天然避免了高并发写入。

---

## 9. 测试策略

| 层级 | 方法 |
|---|---|
| **单元测试** | `internal/db/` 和 `internal/worker/` 使用 GORM + SQLite `:memory:` 模式 |
| **Handler 测试** | `internal/api/` 使用 `httptest.NewRecorder` |
| **回调测试** | 验证 `accepted_statuses` 判断逻辑、callback_url 触发回调记录、CallbackWorker 重试和超过最大次数 |
| **集成测试** | 启动完整服务，验证端到端提交通知 → 投递成功 → 回调完成流程 |
