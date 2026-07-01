# 回调通知 & 自定义 Status Code 接受范围 - 设计文档

## 概述

在现有消息投递服务基础上增加两个功能：

1. **回调通知**: 业务系统提交通知时可传入 HTTP 回调地址，投递完成或进入死信时，系统主动回调通知上游结果
2. **自定义 Status Code 接受范围**: 创建供应商时可指定哪些 HTTP status code 视为投递成功，取代硬编码的 2xx 判断

## 数据模型变更

### Supplier 新增字段

```
accepted_statuses TEXT NOT NULL DEFAULT '[200]'
```

存储 JSON 数组，如 `[200, 201, 202]`。Worker 判断 `resp.StatusCode` 是否在列表中。

### 新增 callbacks 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK AUTOINCREMENT | |
| notification_id | TEXT NOT NULL | 关联的通知 ID |
| callback_url | TEXT NOT NULL | 回调地址 |
| status | TEXT NOT NULL DEFAULT 'pending' | pending / completed / failed |
| attempt_count | INTEGER NOT NULL DEFAULT 0 | 当前重试次数 |
| max_attempts | INTEGER NOT NULL DEFAULT 3 | 最大重试次数 |
| retry_delay_ms | INTEGER NOT NULL DEFAULT 10000 | 固定重试间隔 (10s) |
| last_error | TEXT | 最终失败时记录错误信息 |
| next_retry_at | TEXT | 下次重试时间 |
| created_at | TEXT NOT NULL | |
| updated_at | TEXT NOT NULL | |

索引: `idx_callbacks_status_next_retry` ON (status, next_retry_at)

### 新增 callback_attempts 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK AUTOINCREMENT | |
| callback_id | INTEGER NOT NULL | 关联 callbacks.id |
| attempt_number | INTEGER NOT NULL | 第几次尝试 |
| response_status | INTEGER | HTTP 响应状态码 |
| response_body | TEXT | 响应 body |
| error_message | TEXT | 网络/超时错误 |
| attempted_at | TEXT NOT NULL | 尝试时间 |

索引: `idx_callback_attempts_callback_id` ON (callback_id)

### Notification 新增字段

```
callback_url TEXT   (可选)
```

请求时传入，和 notification 一起持久化，Worker 投递完成后据此插入 callbacks 记录。

## API 变更

### 提交通知 POST /api/v1/notifications

请求 body 新增可选字段:

```json
{
  "supplier": "ad-system",
  "callback_url": "https://biz.company.com/callback/notif",
  "body": {"user_id": 123}
}
```

### 创建/更新供应商 POST/PUT /api/v1/suppliers

请求 body 新增 `accepted_statuses` 字段:

```json
{
  "name": "ad-system",
  "url": "https://api.adsystem.com/notify",
  "accepted_statuses": [200, 201, 202],
  "retry": { ... }
}
```

不传时默认 `[200]`。

## Worker 投递流程变更

### deliver() 状态码判断

当前:
```go
if resp.StatusCode >= 200 && resp.StatusCode < 300 {
```

改为查找 Supplier 的 `accepted_statuses` 列表。Supplier 在 `processBatch` → `deliver` 调用时传入（`processBatch` 已查到 notification，可一并获取 supplier，避免 `deliver` 中重复查询）。

### recordSuccess / recordFailure 末尾（终态时）

回调只应在通知到达**终态**时触发：

- **recordSuccess**: 通知标记为 `delivered` 后，若有 `callback_url` 则插入 callbacks 记录
- **recordFailure 进入死信时**: 当 `attemptCount >= maxAttempts`，通知标记为 `dead` 后，若有 `callback_url` 则插入 callbacks 记录
- **recordFailure 中间重试**: 不插入 callbacks，等待最终结果

## CallbackWorker

独立于主 Worker 的后台轮询协程，负责执行回调。

### 轮询逻辑

- 间隔: 2s
- 查询: `status IN ('pending', 'failed') AND (next_retry_at IS NULL OR next_retry_at <= now)`
- 最大并发: 5
- HTTP POST, 超时 15s

### 回调请求体

```json
{
  "notification_id": "uuid-string",
  "status": "delivered"
}
```

### 回调结果处理

- 2xx 响应: status=completed，写 callback_attempts 记录
- 非 2xx / 网络错误: attempt_count++，计算 next_retry_at（固定 10s 后），写 callback_attempts 记录
- attempt_count >= max_attempts(3): status=failed，写 last_error

### 优雅关闭

CallbackWorker 与主 Worker 并联在 main.go，监听同一个 stopChan，SIGTERM 时等待当前批次完成。

## 向后兼容性

- `accepted_statuses` 新字段默认 `[200]`。这意味着已有供应商在迁移后仅接受 HTTP 200，如果之前依赖 201/202/204 等状态码，需要在迁移后更新供应商配置添加对应状态码
- `callback_url` 在 notification 上为可选字段，不传时行为与之前完全一致，无兼容性问题
- callbacks / callback_attempts 为新表，无迁移冲突

## 配置文件变更

```yaml
suppliers:
  - name: ad-system
    accepted_statuses: [200, 201]
    retry:
      max_attempts: 15
```

`SyncSuppliersFromConfig` 同步时写入 `accepted_statuses`。

## 新增/变更文件

| 文件 | 变更类型 | 说明 |
|------|----------|------|
| internal/model/types.go | 修改 | Supplier 加 AcceptedStatuses；Notification 加 CallbackURL |
| internal/db/db.go | 修改 | AutoMigrate 新增 callbacks / callback_attempts 表 |
| internal/db/callback.go | 新增 | callbacks + callback_attempts CRUD |
| internal/db/supplier.go | 修改 | SyncSuppliersFromConfig 写入 accepted_statuses |
| internal/api/notification.go | 修改 | SubmitNotification 接收 callback_url |
| internal/api/supplier.go | 修改 | CreateSupplier / UpdateSupplier 接受 accepted_statuses |
| internal/worker/worker.go | 修改 | deliver 判断逻辑 + recordSuccess/recordFailure 插入 callbacks |
| internal/worker/callback_worker.go | 新增 | 独立 CallbackWorker |
| cmd/server/main.go | 修改 | 启动 CallbackWorker |
| config.yaml | 修改 | 供应商示例增加 accepted_statuses |

## 测试策略

- DB: callbacks / callback_attempts CRUD 单元测试
- Worker: 验证 `accepted_statuses` 判断逻辑；验证 callback_url 非空时插入 callbacks 记录
- CallbackWorker: 验证回调成功/失败/重试/超过最大次数的完整流程
- API: 验证提交通知带 callback_url；验证创建/更新供应商带 accepted_statuses
