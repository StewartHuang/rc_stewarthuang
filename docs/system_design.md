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

| 不解决的问题                                                         | 原因                                                                                       |
|----------------------------------------------------------------|------------------------------------------------------------------------------------------|
| **外部 API 业务错误处理** — 外部 API 返回的 4xx 业务错误（参数错误、业务校验失败等）由业务系统自行处理 | 业务错误的含义因供应商而异，本服务无法理解或处理这些语义；本服务仅关注 HTTP 层面的送达成功与否（配置中可指定成功状态码，其他为失败）                    |
| **通知内容生成与模板渲染** — 不提供模板引擎、变量替换或内容转换能力                          | 不同供应商的 body 格式差异巨大，引入模板引擎会大幅增加复杂性（模板管理、版本控制、变量校验等），且每次新增供应商都需要修改模板逻辑。业务系统最了解目标格式，应由其自行构造 |
| **多请求流程编排** — 不涉及跨供应商的请求编排、条件分支或聚合                             | 这是工作流引擎的职责，不是通知投递服务的范围。保持服务职责单一，避免变成"万能管道"                                               |

---

## 2. 架构

### 2.1 架构选型：单体 Outbox 模式

选择 **单体 Outbox 模式** 而非 API+Worker 分离或事件驱动架构。

| 方案            | 优势                 | 劣势                            |
|---------------|--------------------|-------------------------------|
| 单体 Outbox     | 单二进制部署，运维简单，事务一致性强 | 吞吐受单进程限制，API 和 Worker 不能独立扩缩容 |
| API+Worker 分离 | API 和 Worker 可独立扩缩 | 部署运维更复杂，需进程间协调                |
| 事件驱动 (消息队列)   | 松耦合，高可扩展性          | 引入额外中间件，涉及基建运维                |

**取舍**：MVP 验证阶段，部署简便性优先于可扩展性。若未来吞吐需求超出单进程能力，参见第 7.3 演进路径。

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
      ------------------------------│  (轮询 callbacks 表)  │               │
                                 │  └──────────────────────┘                │
                                 └──────────────────────────────────────────┘
                                                │
                                                ▼
                                       ┌────────────────────┐
                                       │  外部供应商 API      │
                                       └────────────────────┘
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

| 失败场景                                           | 处理方式                                                                                                                                           |
|------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------|
| **网络超时 / DNS 解析失败 / 连接拒绝**                     | 记录到 `delivery_attempts`，按退避策略重试                                                                                                                |
| **HTTP 非接受状态码**（不匹配供应商 `accepted_statuses` 列表） | 统一视为投递失败，记录响应状态码和 body，按退避策略重试                                                                                                                 |
| **超过最大重试次数**（默认 15 次）                          | 标记 `status = 'dead'`，写入 `dead_reason` 字段（通常在业务上会使用类似 Prometheus 打点+ Grafana 监控来配置告警，打点可以区分具体的错误类型，如果是基建层导致的死信需要告警人工介入，如果是业务状态码导致的告警，需要通知业务方关注） |
| **请求超时**                                       | 使用 `context.WithTimeout`（默认 30s）控制，超时返回失败                                                                                                      |
| **数据库写入失败**                                    | 创建通知时返回 500 错误给业务系统；投递过程中的写入失败仅记录日志，不影响重试                                                                                                      |
| **进程崩溃**                                       | SQLite 持久化保证消息不丢失，重启后 Worker 从数据库中恢复待投递消息                                                                                                      |
| **优雅关闭**                                       | 捕获 SIGTERM/SIGINT，等待当前批次投递和回调批次完成后退出                                                                                                           |
| **回调投递失败**                                     | 独立 CallbackWorker 轮询 callbacks 表，固定间隔 10s 重试，最多 3 次，超过后标记 failed 并记录错误（通常会接入告警平台，这种情况下会通过告警寻求人工介入）                                             |


### 3.3 重试策略：指数退避 + 随机抖动

```
delay = min(base_delay_ms × 2^attempt, max_delay_ms) + random(0, 50%)
```

默认参数（每个供应商可独立覆盖）：

| 参数              | 默认值           | 说明     |
|-----------------|---------------|--------|
| `base_delay_ms` | 1000 (1s)     | 首次重试延迟 |
| `max_delay_ms`  | 240000 (4min) | 最大延迟上限 |
| `max_attempts`  | 15            | 最大重试次数 |

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

- 超过 `max_attempts` 后，通知进入 `dead` 状态并记录失败原因。
- 提供 API 支持查询和重新投递死信（重置为 `pending` 状态，清空 `next_retry_at`）。

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

| 决策                 | 选择                                | 替代方案               | 理由                                      |
|--------------------|-----------------------------------|--------------------|-----------------------------------------|
| **架构**             | 单体 Outbox                         | API+Worker 分离、事件驱动 | MVP 部署简便性优先，如果要真实大规模放量还是要做 API + Worker |
| **存储**             | SQLite                            | PostgreSQL、BoltDB  | 零依赖，单二进制部署                              |
| **可靠性模型**          | 持久化 + 至少一次投递                      | 基础重试、内存队列          | 写入延迟换取投递确定性                             |
| **供应商配置**          | 配置文件 + 运行时 API                    | 纯配置文件、纯 API        | 兼顾启动可用性和运行时灵活性                          |
| **内容生成**           | 不提供                               | 模板引擎               | 避免模板管理复杂度                               |
| **业务错误处理**         | 不处理                               | 解析业务错误码            | HTTP 层面职责清晰                             |
| **数据访问层**          | GORM ORM                          | 原生 SQL             | 参数化查询防 SQL 注入，AutoMigrate 省去手动管理 DDL    |
| **数据约束**           | 应用层校验                             | 数据库外键 / CHECK      | 修改规则无需变更表定义                             |
| **回调机制**           | 独立 CallbackWorker + callbacks 队列表 | Worker 内联执行        | 回调失败重试不阻塞主投递流程，进程重启不丢重试                 |
| **Status Code 判断** | 供应商可配置 `accepted_statuses` 列表     | 硬编码 2xx            | 不同供应商对成功响应有不同定义（如某些仅接受 200），可配置更灵活      |

### 7.2 被拒绝的"过度设计"

| 方案                    | 拒绝理由                                            |
|-----------------------|-------------------------------------------------|
| **事件驱动架构（消息队列）**      | 这里不能算完全的过度设计，如果需要上大规模生产，还是要用消息队列架构，只是现在MVP暂时不考虑 |
| **PostgreSQL**        | MVP 版本追求最快可用，零外部依赖优先                            |
| **API 与 Worker 分离部署** | 拆分需进程间协调和独立部署流水线，单二进制更简单                        |
| **模板渲染引擎**            | 增加模板管理、版本控制、变量校验等复杂度                            |
| **数据库外键与 CHECK 约束**   | 应用层校验更灵活，修改规则无需变更表定义                            |
| **独立 dead_letters 表** | status 字段 + dead_reason 更简单，减少表数量               |
| **纯 API 管理供应商**       | 配置文件确保启动即可工作，无需提前调用 API                         |
| **Worker 内联执行回调**     | 回调重试会阻塞主 Worker 槽位，影响正常投递吞吐                     |

### 7.3 演进路径

#### 第一阶段（当前）：单体 Outbox + SQLite

- 单二进制部署，运维简单
- 零外部依赖（不需要数据库服务器、消息队列）
- 适合日投递量百万级以下的内部场景

#### 第二阶段：API + Worker 分离 + PostgreSQL

> **阶段说明**：该阶段在实际演进中往往直接跳过，进入第三阶段（引入消息队列）。独立的 API + Worker 分离虽然解决了单进程瓶颈，但引入了多实例竞态问题，而该问题最终仍需消息队列来解决。因此除非有明确的渐进式部署约束，否则建议直接从第一阶段演进到第三阶段。

触发信号：

- Worker 轮询对 API 写入造成性能影响
- 需要独立扩缩容 API 和 Worker
- 需要多实例部署提高可用性

演进内容：

- 存储切换为 PostgreSQL，解决 SQLite 写入并发上限
- 服务拆分为 API Server 和 Worker 两个独立部署单元
- API 负责接收请求写入 DB，Worker 负责轮询投递

##### 多实例竞态问题

部署多 Worker 实例后，单进程 `wg.Wait()` 不再能保护同一记录不被重复消费：

```
时间线（两个 Worker 进程）:

Worker A (tick 1):  SELECT status='pending' → 返回 n1      [仍在投递中]
Worker B (tick 1):  SELECT status='pending' → 返回 n1 (!!) [重复消费]
```
```
同一进程内部（无 wg.Wait 串联时）:

Ticker A (t=0ms):   SELECT LIMIT 5 → [n1...n5]
Ticker B (t=500ms):  SELECT LIMIT 5 → [n1...n5] (尚未更新, 重复)
```

竞态根本原因：SELECT 和 UPDATE 之间存在时间窗口，多个进程/协程都能读到同一未锁定记录。

| 部署方式 | 是否出现 | 原因 |
|---------|---------|------|
| 单进程（当前） | 否 | `processBatch()` 用 `wg.Wait()` 阻塞，下一 tick 不会与上一批并发 |
| 多 Worker 进程 | 是 | 各进程独立轮询，无协调机制 |
| 单进程去掉 wg.Wait | 是 | 连续 tick 的 SELECT 可能返回同一批未处理完的记录 |

##### 解决方案（选择其一）

**方案 A：Atomic Status Claim**（推荐）

用单条 UPDATE 原子抢锁，避免 SELECT → UPDATE 窗口：

```sql
UPDATE notifications
SET status = 'claimed'
WHERE id = (
  SELECT id FROM notifications
  WHERE status IN ('pending', 'failed')
    AND (next_retry_at IS NULL OR next_retry_at <= NOW())
  ORDER BY created_at ASC
  LIMIT 1
)
RETURNING *;
```

`RowsAffected == 0` 表示已被其他实例抢走，跳过此轮。PostgreSQL 的 `RETURNING` 子句可同时返回抢到的记录内容。

**方案 B：乐观锁**

表增加 `version` 字段，UPDATE 时检查版本号：

```sql
UPDATE notifications SET status = 'delivered', version = version + 1
WHERE id = 'n1' AND version = 3;
```

受影响行数为 0 则重试。

**方案 C：咨询锁 / 分布式锁**

使用 PostgreSQL 的 `pg_advisory_lock()` 或 Redis Redlock 在进程间协调。

**三选一建议**：方案 A 最简洁，方案 B 适用于更多 ORM 兼容场景，方案 C 引入额外依赖，仅在需要更细粒度锁控制时考虑。

##### 说明

对于大多数内部场景，第一阶段直接演进到第三阶段（消息队列）更合理。消息队列天然解决了消费竞争问题（MQ 的 ack 机制保证单次消费），同时消除了 DB 轮询瓶颈。第二阶段仅在下述情况下考虑：

- 团队对消息队列运维经验不足，需要一个过渡期
- 组织流程要求分阶段演进，不允许一次跨度过大

#### 第三阶段：引入消息队列

触发信号：

- 流量大幅增长，DB 轮询模式成为瓶颈
- 需要严格的消息顺序保证或更多消息语义
- 需要跨可用区部署

##### 为什么选择 RabbitMQ 而非 Kafka

RabbitMQ 更适合本服务的消息投递场景，具体原因如下（针对本服务的消息模型）：

| 维度                        | RabbitMQ                                        | Apache Kafka                           |
|---------------------------|-------------------------------------------------|----------------------------------------|
| **死信队列（DLX）**             | 原生支持，Exchange 级配置，队列绑定即可                        | 需通过 topic 重定向或 Stream 的 compact 策略模拟   |
| **消息存活时间（TTL）**           | 队列/消息级别原生支持                                     | 无原生 TTL，需应用层或配置 retention.ms           |
| **延迟投递（Delayed Message）** | 通过延迟交换机（rabbitmq_delayed_message_exchange）支持    | 需依赖时间戳轮询处理                             |
| **消费确认（ACK）**             | 支持推模式消费且单条 ACK，天然匹配 Worker"取一条→投递→ACK"模式        | 需维护 offset，更适用于批量流式消费                  |
| **重试机制**                  | 死信后 re-route 回原队列（DLX + TTL）实现重试，完全在 broker 侧完成 | 需重投到原 topic，配合 consumer seek 实现，应用负担更重 |
| **消息语义**                  | 至少一次投递（ACK+重投）                                  | 支持精确一次（需配合幂等 Producer 和 Transaction）   |
| **运维复杂度**                 | 单节点可部署（erlang 一致 Hash），配置简单                     | 需要 Zookeeper/Kraft，最少 3 节点才能算生产集群      |
| **适合场景**                  | 任务队列、异步 RPC、事件通知、死信重试                           | 日志聚合、流计算、事件溯源、大数据管道                    |

总结：RabbitMQ 的 **Exchange → Queue → Binding** 模型天然匹配"提交通知 → 投递队列 → 死信重试"的消息流转路径；原生死信交换机（DLX）和 TTL 可以在 broker 层面完成重试延迟，Worker 只需要单条 ACK 即可控制消费进度。

##### 架构图

```
┌──────────────┐
│  业务系统 A   │         API 层 (无状态, 可多实例)
│  业务系统 B   │───┐ ┌──────────────────────────────────────────────┐
│  业务系统 C   │   │ │ POST /api/v1/notifications                  │
└──────────────┘   │ │  → 校验 supplier / body / callback_url      │
                   │ │  → DB 写入 notification + delivery_history  │
                   │ │  → 投递进度写入 RabbitMQ                    │
                   │ └──────────────────────────────────────────────┘
                   │                     │
                   │                     ▼
                   │         ┌─────────────────────┐
                   │         │   RabbitMQ Broker    │
                   │         │  ┌─────────────────┐ │
                   │         │  │  delivery.exchange─────────[direct]
                   │         │  └────────┬────────┘ │
                   │         │           │          │
                   │         │  ┌────────▼────────┐ │
                   │         │  │ delivery.queue   │──┐
                   │         │  │ (可多 consumer)  │  │  Worker 从队列消费,
                   │         │  └─────────────────┘  │  无需轮询 DB
                   │         │           │           │
                   │         │  ┌────────▼────────┐ │
                   │         │  │ callback.exchange─────[direct]
                   │         │  └────────┬────────┘ │
                   │         │           │          │
                   │         │  ┌────────▼────────┐ │
                   │         │  │ callback.queue   │─┘
                   │         │  │ (回调通知)       │
                   │         │  └─────────────────┘ │
                   │         │           │           │
                   │         │  ┌────────▼────────┐ │
                   │         │  │ dlx.exchange    │ │ 死信 Exchange
                   │         │  │  ┌───────────┐  │ │
                   │         │  │  │ retry.queue│──┘── TTL 到期投回 delivery.queue
                   │         │  │  └───────────┘  │
                   │         │  │  ┌───────────┐  │
                   │         │  │  │ dead.queue │   超过最大重试 → 人工处理 / 告警
                   │         │  │  └───────────┘  │
                   │         │  └─────────────────┘ │
                   │         └─────────────────────┘
                   │                     │
                   │                     ▼
                   │          ┌──────────────────────┐
                   │          │  Worker 层             │
                   │          │  (无状态, 可多实例)      │
                   │          │  ┌──────────────────┐ │
                   │          │  │  Delivery Worker  │ │ 从 delivery.queue 消费
                   │          │  │  → HTTP 投递       │ │ Basic.Ack 在投递完成后
                   │          │  │  → 成功: Ack      │ │
                   │          │  │  → 失败: Nack +   │ │
                   │          │  │    Reject → DLX   │ │
                   │          │  │  → 终态: 插入      │ │
                   │          │  │    callback.queue │ │
                   │          │  └──────────────────┘ │
                   │          │  ┌──────────────────┐ │
                   │          │  │ Callback Worker   │ │ 从 callback.queue 消费
                   │          │  │ → HTTP 回调       │ │
                   │          │  │ → 成功: Ack      │ │
                   │          │  │ → 失败: Nack → DLX│ │
                   │          │  └──────────────────┘ │
                   │          └──────────────────────┘
                   │                     │
                   │                     ▼
                   │         ┌──────────────────────┐
                   │         │     PostgreSQL        │
                   │         │  (查询 / 记录存储)      │
                   │         │  notifications        │
                   │         │  delivery_attempts    │
                   │         │  callbacks            │
                   │         │  callback_attempts    │
                   │         └──────────────────────┘
                   │
                   └─────────────────────────────────── 外部供应商 API
                                                         (广吿/CRM/库存等)
```

##### 消息流转路径

完整的消息生命周期：

```
① 提交 → ② 持久化 → ③ 入队 → ④ 消费 → ⑤ 投递 → ⑥ 终态
```

**投递消息流：**

1. API 收到请求后，写入 `notifications` 表（ID、supplier、body、headers 等），同时写入 `delivery_history` 记录初始状态
2. API 将 `{notification_id, supplier, url, method, headers, body, `callback_url}` 发布到 `delivery.exchange`（routing_key = supplier.name）
3. `delivery.queue` 通过 binding_key 接收消息，多个 Worker 实例消费（竞争消费模式，RabbitMQ 保证一条消息仅被一个 consumer 接收）
4. Worker 消费消息后执行 HTTP 投递：
   - **成功**（匹配 `accepted_statuses`）：`Basic.Ack`，更新 DB 为 `delivered`，若含 `callback_url` 则发布消息到 `callback.exchange`
   - **可重试失败**（网络超时、服务器错误 5xx、非接受状态码）：`Basic.Nack(requeue=false)`，消息被投递到 `dlx.exchange` → `retry.queue`（设置 TTL 实现重试延迟）
   - **超过最大重试次数**：消息路由到 `dead.queue`，触发告警通知
5. `retry.queue` 的 TTL 到期后，消息自动重新投递到 `delivery.queue`（通过 DLX 的 routing_key 回路由），Worker 再次消费

**回调消息流（独立于投递流程）：**

- 投递到达终态（delivered/dead）且通知含 `callback_url` 时，API/Worker 发布消息到 `callback.exchange`
- `callback.queue` 由 CallbackWorker 消费，回调业务系统，成功则 Ack
- 回调失败的消息也经 DLX → `retry.queue` → TTL 到期回调 `callback.queue`，超过最大重试进入 `dead.queue`

##### 死信与重试机制

RabbitMQ 的死信交换机（DLX）天然适合本服务的重试场景：

```
第一次投递失败:
┌──────────────┐   Basic.Nack   ┌───────────────┐   TTL 5s   ┌──────────────┐
│ delivery.queue│──────────────►│ dlx.exchange  │──────────►│ retry.queue  │
│              │ (requeue=false) │               │            │              │
└──────────────┘                └───────────────┘            └──────┬───────┘
                                                                    │
                                                     自动重新路由    │
                                                                    ▼
                                                            ┌──────────────┐
                                                            │ delivery.queue│
                                                            │ (再次消费)    │
                                                            └──────────────┘
```

每次失败时，Worker 执行 `Basic.Nack(requeue=false)`，消息经 DLX 进入 `retry.queue`。

`retry.queue` 的特性：
- `x-message-ttl`: 根据重试次数递增（如 5s, 30s, 2min, 10min, 30min...）
- `x-dead-letter-exchange`: 设置为 `delivery.exchange`
- `x-dead-letter-routing-key`: 设置为原队列的 routing_key

**判断超过最大重试次数**：Worker 在消费时检查消息头部携带的重试计数（`x-death` 头或自定义 header 如 `x-retry-count`），超过阈值后执行 `Basic.Reject(requeue=false)` 将消息路由到 `dead.queue`（无需人工介入，在绑定关系中将 `dead.queue` 绑定到 DLX 的 `dead` routing_key 即可），不再重试。

```
超过最大重试阈值:
┌────────────┐   Basic.Reject   ┌────────────┐
│ retry.queue │──────────────►  │ dead.queue │
│             │ (requeue=false)  │            │
└────────────┘                  └────────────┘
                                    │
                                    ▼
                             告警平台 / 人工介入
```

**Dead Letter 的差异化 routing**：`retry.queue` 和 `dead.queue` 都是 `dlx.exchange` 的绑定队列，但通过不同的 routing_key 区分。Worker 根据重试计数决定使用哪个 routing_key 进行 Reject。

##### RabbitMQ 配置拓扑

三个 Exchange、五个 Queue：

| 组件 | 类型 | 用途 |
|------|------|------|
| `delivery.exchange` | direct | 接收投递消息 |
| `callback.exchange` | direct | 接收回调消息 |
| `dlx.exchange` | direct | 统一死信转发 |
| `delivery.queue` | — | 待投递消息，Worker 消费 |
| `callback.queue` | — | 待回调消息，CallbackWorker 消费 |
| `retry.queue-delivery` | — | 投递重试延迟，TTL 到后回路由到 delivery.exchange |
| `retry.queue-callback` | — | 回调重试延迟 |
| `dead.queue` | — | 超过最大重试次数的终态消息 |

##### 调用已存在的数据库

PostgreSQL 退化为**查询和记录存储**，不再承担轮询职责：

- `notifications` 表：存储通知的完整信息（ID、supplier、body、headers、callback_url 等）
- `delivery_attempts` 表：保留每次投递的完整记录（response_status、response_body、error_message、attempted_at）
- `callbacks` / `callback_attempts` 表：同 MVP 阶段，记录回调记录和历史
- **Worker 不再查询 pending 通知**，消息由 RabbitMQ 队列推送

---

## 8. 存储选型分析

### SQLite vs 其他方案

| 方案             | 优势                  | 劣势                 |
|----------------|---------------------|--------------------|
| **SQLite**（选择） | 零依赖，单二进制部署，足够支持中等规模 | 写并发有限，不支持网络访问      |
| PostgreSQL     | 高并发，支持网络访问，成熟运维生态   | 需要额外部署和维护数据库实例     |

**取舍**：零依赖部署的便利性优先于高并发写入能力。SQLite 通过 WAL 模式可支持并发读写，满足企业内部场景。Worker 轮询 + 指数退避的投递模式天然避免了高并发写入。

---

## 9. 测试策略

| 层级              | 方法                                                                       |
|-----------------|--------------------------------------------------------------------------|
| **单元测试**        | `internal/db/` 和 `internal/worker/` 使用 GORM + SQLite `:memory:` 模式       |
| **Handler 测试**  | `internal/api/` 使用 `httptest.NewRecorder`                                |
| **回调测试**        | 验证 `accepted_statuses` 判断逻辑、callback_url 触发回调记录、CallbackWorker 重试和超过最大次数 |
| **集成测试**        | 启动完整服务，验证端到端提交通知 → 投递成功 → 回调完成流程                                         |
