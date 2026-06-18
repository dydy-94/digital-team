# 修复前端 warning 消息不展示问题

## 问题分析

用户发送 `@agent_4` 后：
- WS 返回了 warning 消息：`{"data":{"content":"以下 agent 不存在或不在聊天室中: [agent_4]","roomId":"room_xxx"},"type":"warning"}`
- 但前端界面没有展示这条警告信息

## 问题根源

检查 `ui-test/app.js` 第 817-823 行的 warning 处理逻辑：

```javascript
} else if (data.type === 'warning' || data.type === 'error' || data.type === 'info') {
    const msg = data.data;
    if (msg.roomId === this.currentChannelId || !msg.roomId) {
        this.addMessage('[系统通知]', msg.content, data.type === 'warning');
    }
}
```

代码逻辑看起来正确。可能的问题：
1. **浏览器缓存**：Manager 提供了最新的 `app.js`，但浏览器使用了缓存的旧版本
2. **Manager 未重新加载**：Manager 二进制文件是旧的（02:16），可能没有正确读取最新文件

## 解决方案

### 方案 1：重启 Manager 清除缓存（推荐）

重新启动 Manager 服务，让它加载最新的静态文件：

```bash
pkill -f "manager"
cd /Users/cdy/opensource/x-client/ui-test/manager && ./bin/manager -listen :9000 &
```

### 方案 2：添加缓存控制头

在 Manager 的静态文件服务中添加缓存控制头，强制浏览器每次请求最新文件。

修改 `/Users/cdy/opensource/x-client/ui-test/manager/main.go`：

```go
http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    // 禁用缓存
    w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
    w.Header().Set("Pragma", "no-cache")
    w.Header().Set("Expires", "0")

    if r.URL.Path == "/" {
        http.ServeFile(w, r, filepath.Join(uiTestAbsPath, "index.html"))
    } else {
        fs.ServeHTTP(w, r)
    }
})
```

### 方案 3：在 app.js 文件名添加版本号

修改 `index.html` 中引用 `app.js` 的方式，添加时间戳参数：

```html
<script src="app.js?v=2024061717"></script>
```

## 实施步骤

1. **先尝试方案 1**：重启 Manager，刷新浏览器
2. **如果方案 1 无效**：实施方案 2，修改 Manager 添加缓存控制头，然后重新编译 Manager

## 验证步骤

1. 刷新浏览器页面（强制刷新 Cmd+Shift+R）
2. 打开浏览器开发者工具（F12）→ Network 标签
3. 发送 `@agent_4` 消息
4. 确认：
   - WS 连接收到 warning 消息
   - 前端界面显示黄色警告消息
