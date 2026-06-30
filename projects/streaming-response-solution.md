# 流式响应解决方案

## 1. 背景与价值

### 当前模式（同步）

```
Client → 请求 → 等待... → 等待... → 等待... → 完整响应
```

用户看到的是一个完整的回复，需要等待 Agent 完整处理完才能看到任何内容。如果 Agent 处理需要较长时间，用户体验较差。

### 流式响应模式

```
Client → 请求 → chunk1 → chunk2 → chunk3 → ... → 完成
```

用户看到回复**逐步出现**，像 ChatGPT 那样一个字一个字跳出来。

### 用户体验对比

| 场景 | 同步模式 | 流式模式 |
|------|----------|----------|
| 10秒处理时间 | 等待10秒后显示完整回复 | 第1秒开始显示，逐步显示 |
| 长回复 | 等待时间长 | 边处理边显示 |
| 调试 | 只能看到最终结果 | 可以看到生成过程 |

---

## 2. 架构设计

### 消息流程

x-client 是 AgentCore 的代理，Coordinator 是消息路由中枢。正确的消息流程：

```
AgentCore A → x-client A → Coordinator (Room) → x-client B → AgentCore B
```

### 流式响应的正确链路

```
AgentCore A (流式输出)
    ↓ 逐步产生内容
x-client A (转发流式内容)
    ↓ HTTP SSE / WebSocket
Coordinator (/api/stream/{room_id} 或 Room 消息)
    ↓ 路由流式消息
x-client B (接收流式)
    ↓ 逐步转发
AgentCore B (流式输入)
```

### 前提条件

- AgentCore 的接口支持流式输出 ✅
- 目标 Agent 需要支持流式输入

---

## 3. 解决方案

### 3.1 Coordinator 新增

| 功能 | 端点 | 说明 |
|------|------|------|
| 订阅流式消息 | `POST /api/stream/{room_id}/subscribe` | x-client 订阅 Room 的流式消息（SSE） |
| 发布流式消息 | `PUT /api/stream/{room_id}/publish` | x-client 发布流式消息到 Room |
| Room 消息存储 | - | 流式消息片段也需要存储到 messages 表 |
| 消息类型扩展 | `messages.type` | 新增 `streaming` 消息类型 |

#### SSE 端点示例

```go
// POST /api/stream/{room_id}/subscribe
func (h *Handler) StreamSubscribeHandler(w http.ResponseWriter, r *http.Request) {
    roomID := mux.Vars(r)["room_id"]

    // 设置 SSE 响应头
    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")
    w.Header().Set("Access-Control-Allow-Origin", "*")

    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "SSE not supported", http.StatusInternalServerError)
        return
    }

    // 获取 roomID 的订阅者列表中当前 client 的 channel
    clientChan := h.streamingHub.Subscribe(roomID)
    defer h.streamingHub.Unsubscribe(roomID, clientChan)

    // 持续发送流式消息直到连接断开
    for {
        select {
        case msg := <-clientChan:
            fmt.Fprintf(w, "data: %s\n\n", msg)
            flusher.Flush()
        case <-r.Context().Done():
            return
        }
    }
}
```

### 3.2 x-client 新增

| 功能 | 说明 |
|------|------|
| `StreamMessage()` | 订阅 Coordinator 的流式端点，接收 SSE 流式消息 |
| 流式转发逻辑 | 将 AgentCore 的流式输出转发到 Coordinator |
| 流式渲染 | 逐步显示接收到的内容 |

#### 流式转发逻辑示例

```go
// x-client 转发 AgentCore 的流式输出到 Coordinator
func (x *XClient) forwardStreamingResponse(ctx context.Context, roomID string, agentResp io.Reader) {
    sequence := 0
    buf := make([]byte, 1024)
    reader := bufio.NewReader(agentResp)

    for {
        n, err := reader.Read(buf)
        if n > 0 {
            chunk := string(buf[:n])
            sequence++

            // 发布到 Coordinator
            payload := map[string]interface{}{
                "sequence":  sequence,
                "chunk":     chunk,
                "is_final":  err == io.EOF,
            }
            x.publishStreamingChunk(ctx, roomID, payload)
        }

        if err != nil {
            break
        }
    }
}
```

---

## 4. 完整流程示意

```
1. AgentCore A 开始流式输出 "Hello..."
   ↓
2. x-client A 收到 chunk "H"
   ↓
3. x-client A PUT /api/stream/{room_id}/publish {"chunk": "H", "sequence": 1}
   ↓
4. Coordinator 存储 chunk，广播给 Room 订阅者
   ↓
5. x-client B 收到 SSE "H"
   ↓
6. x-client B 转发给 AgentCore B
   ↓
7. AgentCore B 收到 "H"，开始处理
   ... 重复 1-7 直到完成
```

---

## 5. 数据库改动

### messages 表新增字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `type` | VARCHAR(50) | 消息类型，新增 `streaming` |
| `sequence` | INT | 流式消息序号 |
| `is_final` | BOOLEAN | 是否为最终片段 |

---

## 6. 实施步骤

### Phase 1: Coordinator 流式订阅
- [ ] 新增 `POST /api/stream/{room_id}/subscribe` SSE 端点
- [ ] 实现 StreamingHub 管理订阅者
- [ ] 测试 SSE 连接

### Phase 2: Coordinator 流式发布
- [ ] 新增 `PUT /api/stream/{room_id}/publish` 端点
- [ ] 实现消息广播到 Room 订阅者
- [ ] 消息存储（type=streaming）

### Phase 3: x-client 流式转发
- [ ] x-client 新增 `StreamMessage()` 方法
- [ ] 实现 AgentCore 流式输出的监听和转发
- [ ] 实现 SSE 消息接收和渲染

### Phase 4: 端到端测试
- [ ] 两个 Agent 之间的流式消息传递
- [ ] 验证消息完整性
- [ ] 性能测试

---

## 7. 风险与注意事项

| 风险 | 说明 | 缓解措施 |
|------|------|----------|
| 连接断开 | SSE 长连接可能中断 | 心跳保活、重连机制 |
| 消息顺序 | 流式消息可能乱序 | sequence 序号、客户端排序 |
| 消息丢失 | 网络波动可能导致 chunk 丢失 | 最终合并后校验 |
| 存储压力 | 流式消息产生大量小消息 | 定期合并、或仅存最终消息 |

---

## 8. 替代方案

如果 SSE 实现复杂，可以考虑：

### WebSocket 方案
- 双向通信，更适合实时交互
- 需要专门的 WebSocket Hub

### 长轮询方案
- 简单，兼容性好
- 延迟较高，不适合真正的流式

---

## 9. 优先级建议

| 优先级 | 功能 | 理由 |
|--------|------|------|
| ⭐⭐⭐ | Coordinator 流式订阅/发布 | 核心功能 |
| ⭐⭐⭐ | x-client 流式转发 | 核心功能 |
| ⭐⭐ | 数据库消息合并 | 减少存储压力 |
| ⭐ | 心跳保活机制 | 提升稳定性 |
