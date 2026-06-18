package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type A2AMessage struct {
	MsgId        string   `json:"msgId"`
	ChannelId    string   `json:"channelId"`
	Sender       string   `json:"sender"`
	Target       string   `json:"target"`
	MentionUsers []string `json:"mentionUsers,omitempty"`
	Intent       string   `json:"intent"`
	ContentText  string   `json:"contentText"`
	Timestamp    int64    `json:"timestamp"`
	ReplyToMsgId string   `json:"replyToMsgId,omitempty"`
	// 流式响应支持
	Status      string `json:"status,omitempty"`      // "thinking", "streaming", "completed"
	ParentMsgId string `json:"parentMsgId,omitempty"` // 父消息ID，用于流式响应的关联
}

type ClientMessage struct {
	Action string      `json:"action"`
	Data   interface{} `json:"data"`
}

type ServerMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type JoinData struct {
	ChannelId string `json:"channelId"`
	AgentId   string `json:"agentId"`
}

type XClient struct {
	agentID           string
	coordinatorURL    string
	agentCoreURL      string
	listenAddr        string
	endpoint          string // Agent 的公网访问地址（用于 coordinator 调用）
	heartbeatInterval int
	reconnectInterval int

	memoryWins     map[string]*MemoryWindow
	sessionMgr     *SessionManager
	wsConn         *websocket.Conn
	wsMu           sync.Mutex
	httpServer     *http.Server
	messageIDs     map[string]bool
	msgIDsMu       sync.Mutex
	maxMemorySize  int
	maxMemoryChars int
	joinedChannels []string   // 加入的聊天室列表
	channelsMu     sync.Mutex // 保护 joinedChannels 的互斥锁
}

func NewXClient(cfg *Config) *XClient {
	// 如果没有指定 endpoint，使用默认的 localhost:端口
	endpoint := cfg.Endpoint
	if endpoint == "" {
		// 从 listenAddr 提取端口，构造 localhost URL
		port := cfg.ListenAddr
		if len(port) > 0 && port[0] == ':' {
			port = "localhost" + port
		} else {
			port = "http://" + port
		}
		endpoint = port
	}

	return &XClient{
		agentID:           cfg.AgentID,
		coordinatorURL:    cfg.CoordinatorURL,
		agentCoreURL:      cfg.AgentCoreURL,
		listenAddr:        cfg.ListenAddr,
		endpoint:          endpoint,
		heartbeatInterval: cfg.HeartbeatInterval,
		reconnectInterval: cfg.ReconnectInterval,
		memoryWins:        make(map[string]*MemoryWindow),
		sessionMgr:        NewSessionManager("multi_channel"),
		messageIDs:        make(map[string]bool),
		maxMemorySize:     cfg.MaxMemorySize,
		maxMemoryChars:    cfg.MaxMemoryChars,
	}
}

func (x *XClient) getMemoryWindow(channelId string) *MemoryWindow {
	x.msgIDsMu.Lock()
	defer x.msgIDsMu.Unlock()

	if win, exists := x.memoryWins[channelId]; exists {
		return win
	}

	win := NewMemoryWindow(x.maxMemorySize, x.maxMemoryChars)
	x.memoryWins[channelId] = win
	return win
}

func (x *XClient) connectCoordinator() error {
	// 1. 建立 WebSocket 连接
	wsURL := strings.Replace(x.coordinatorURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL+"/ws/chat", nil)
	if err != nil {
		return err
	}
	x.wsConn = conn

	// 2. 注册 Agent ID
	registerMsg := ClientMessage{
		Action: "register",
		Data: map[string]string{
			"agentId": x.agentID,
		},
	}
	x.sendMessage(registerMsg)

	// 3. 启动接收循环
	go x.receiveLoop()

	return nil
}

func (x *XClient) receiveLoop() {
	for {
		var msg ServerMessage
		if err := x.wsConn.ReadJSON(&msg); err != nil {
			logWarn("x-client", x.agentID, "WebSocket 读取失败，尝试重连")
			x.reconnect()
			return
		}

		// 处理消息
		switch msg.Type {
		case "message":
			var a2aMsg A2AMessage
			jsonData, err := json.Marshal(msg.Data)
			if err != nil {
				logError("x-client", x.agentID, "解析消息失败", "error", err.Error())
				continue
			}
			if err := json.Unmarshal(jsonData, &a2aMsg); err != nil {
				logError("x-client", x.agentID, "解析消息失败", "error", err.Error())
				continue
			}
			x.handleIncomingMessage(&a2aMsg)
		case "join":
			var joinData JoinData
			jsonData, err := json.Marshal(msg.Data)
			if err != nil {
				logError("x-client", x.agentID, "解析加入消息失败", "error", err.Error())
				continue
			}
			if err := json.Unmarshal(jsonData, &joinData); err != nil {
				logError("x-client", x.agentID, "解析加入消息失败", "error", err.Error())
				continue
			}
			logInfo("x-client", x.agentID, "加入聊天室", "channel_id", joinData.ChannelId)
		case "leave":
			var leaveData JoinData
			jsonData, err := json.Marshal(msg.Data)
			if err != nil {
				logError("x-client", x.agentID, "解析离开消息失败", "error", err.Error())
				continue
			}
			if err := json.Unmarshal(jsonData, &leaveData); err != nil {
				logError("x-client", x.agentID, "解析离开消息失败", "error", err.Error())
				continue
			}
			logInfo("x-client", x.agentID, "离开聊天室", "channel_id", leaveData.ChannelId)
		}
	}
}

func (x *XClient) Start() error {
	if err := x.connectCoordinator(); err != nil {
		return err
	}

	if err := x.registerToCoordinator(); err != nil {
		return err
	}

	go x.startHTTPServer()
	go x.keepAlive()

	logInfo("x-client", x.agentID, "x-client 启动成功")
	return nil
}

func (x *XClient) registerToCoordinator() error {
	// 发送HTTP注册请求，包含 agent_id 和 endpoint
	regData := map[string]interface{}{
		"agent_id": x.agentID,
		"endpoint": x.endpoint,
	}
	jsonData, _ := json.Marshal(regData)

	resp, err := http.Post(x.coordinatorURL+"/api/agent/register", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("注册失败，状态码: %d", resp.StatusCode)
	}

	logInfo("x-client", x.agentID, "已注册到协调器", "coordinator_url", x.coordinatorURL)
	return nil
}

func (x *XClient) keepAlive() {
	ticker := time.NewTicker(time.Duration(x.heartbeatInterval) * time.Second)
	for range ticker.C {
		// 发送HTTP心跳请求
		heartbeatData := map[string]string{
			"agent_id": x.agentID,
		}
		jsonData, _ := json.Marshal(heartbeatData)

		resp, err := http.Post(x.coordinatorURL+"/api/agent/heartbeat", "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			logWarn("x-client", x.agentID, "心跳发送失败", "error", err.Error())
			continue
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			logWarn("x-client", x.agentID, "心跳响应失败", "status_code", resp.StatusCode)
		} else {
			logDebug("x-client", x.agentID, "心跳发送成功")
		}
	}
}

func (x *XClient) handleIncomingMessage(msg *A2AMessage) {
	if x.isMessageProcessed(msg.MsgId) {
		logInfo("x-client", x.agentID, "[重复] 消息已处理", "msg_id", msg.MsgId)
		return
	}

	// 自动将收到的消息所在聊天室添加到心跳列表
	x.addJoinedChannel(msg.ChannelId)

	memoryWin := x.getMemoryWindow(msg.ChannelId)

	isMentioned := false
	for _, user := range msg.MentionUsers {
		if user == x.agentID {
			isMentioned = true
			break
		}
	}

	if !isMentioned {
		memoryWin.Push(msg.Sender, msg.ContentText)
		logInfo("x-client", x.agentID, "[旁听] 收到消息", "sender", msg.Sender, "content", msg.ContentText, "channel_id", msg.ChannelId)
		return
	}

	logInfo("x-client", x.agentID, "[唤醒] 收到 @ 消息", "sender", msg.Sender, "channel_id", msg.ChannelId)

	contextPrompt := memoryWin.BuildContext(msg.Sender, msg.ContentText)
	sessionID := x.sessionMgr.GenerateGroupSessionID()

	// 使用流式响应
	go x.wakeupAgentCoreStream(contextPrompt, sessionID, msg)
}

// sendThinkingMessage 发送"正在思考"消息
func (x *XClient) sendThinkingMessage(channelId, parentMsgId string) string {
	// 生成一个新的消息ID用于标识这个响应
	responseMsgId := "resp_" + uuid.New().String()

	msg := &A2AMessage{
		MsgId:        responseMsgId,
		ChannelId:    channelId,
		Sender:       x.agentID,
		Target:       "ALL",
		Intent:       "INFORM",
		ContentText:  "正在思考...",
		Timestamp:    time.Now().Unix(),
		ReplyToMsgId: parentMsgId,
		Status:       "thinking",
	}

	clientMsg := ClientMessage{
		Action: "speak",
		Data:   msg,
	}
	x.sendMessage(clientMsg)
	logInfo("x-client", x.agentID, "发送思考状态", "msg_id", responseMsgId)
	return responseMsgId
}

// wakeupAgentCoreStream 使用 SSE 流式调用 AgentCore
func (x *XClient) wakeupAgentCoreStream(prompt, sessionID string, originalMsg *A2AMessage) {
	// 1. 先发送"正在思考"消息
	responseMsgId := x.sendThinkingMessage(originalMsg.ChannelId, originalMsg.MsgId)

	// 2. 发送流式请求
	reqBody := map[string]string{
		"message":    prompt,
		"session_id": sessionID,
		"sender":     originalMsg.Sender,
	}
	jsonData, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", x.agentCoreURL+"/chat/stream", bytes.NewBuffer(jsonData))
	if err != nil {
		logError("x-client", x.agentID, "创建请求失败", "error", err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	// 无超时设置
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		logError("x-client", x.agentID, "调用 agentcore 失败", "error", err.Error())
		// 发送错误状态
		x.sendStreamUpdate(originalMsg.ChannelId, responseMsgId, "AgentCore 调用失败", "error")
		return
	}
	defer resp.Body.Close()

	// 3. 逐步读取 SSE 数据并发送更新
	var fullContent strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	// SSE 数据可能很大，增加缓冲区
	scanner.Buffer(make([]byte, 100), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			data = strings.TrimSpace(data)

			// 跳过空的或特殊的 SSE 行
			if data == "" || data == "[DONE]" {
				continue
			}

			fullContent.WriteString(data)

			// 发送流式更新
			x.sendStreamUpdate(originalMsg.ChannelId, responseMsgId, fullContent.String(), "streaming")
		}
	}

	if err := scanner.Err(); err != nil {
		logError("x-client", x.agentID, "读取流失败", "error", err.Error())
	}

	logInfo("x-client", x.agentID, "Agent 响应完成", "reply_len", len(fullContent.String()))

	// 4. 发送完成状态（移除 thinking 标记，变为普通消息）
	x.sendStreamComplete(originalMsg.ChannelId, responseMsgId, fullContent.String(), originalMsg.MsgId)
}

// sendStreamUpdate 发送流式更新消息
func (x *XClient) sendStreamUpdate(channelId, msgId, content, status string) {
	msg := &A2AMessage{
		MsgId:       msgId,
		ChannelId:   channelId,
		Sender:      x.agentID,
		Target:      "ALL",
		Intent:      "INFORM",
		ContentText: content,
		Timestamp:   time.Now().Unix(),
		Status:      status,
	}

	clientMsg := ClientMessage{
		Action: "stream",
		Data:   msg,
	}
	x.sendMessage(clientMsg)
}

// sendStreamComplete 发送流式完成消息（转为普通消息）
func (x *XClient) sendStreamComplete(channelId, msgId, content, replyToMsgId string) {
	msg := &A2AMessage{
		MsgId:        msgId,
		ChannelId:    channelId,
		Sender:       x.agentID,
		Target:       "ALL",
		Intent:       "INFORM",
		ContentText:  content,
		Timestamp:    time.Now().Unix(),
		ReplyToMsgId: replyToMsgId,
		Status:       "completed",
	}

	clientMsg := ClientMessage{
		Action: "stream_complete",
		Data:   msg,
	}
	x.sendMessage(clientMsg)
}

func (x *XClient) isMessageProcessed(msgId string) bool {
	x.msgIDsMu.Lock()
	defer x.msgIDsMu.Unlock()

	if x.messageIDs[msgId] {
		return true
	}
	x.messageIDs[msgId] = true

	if len(x.messageIDs) > 100 {
		clearCount := len(x.messageIDs) - 50
		count := 0
		for k := range x.messageIDs {
			delete(x.messageIDs, k)
			count++
			if count >= clearCount {
				break
			}
		}
	}
	return false
}

func (x *XClient) startHTTPServer() {
	http.HandleFunc("/skill/send", x.handleSkillSend)
	http.HandleFunc("/skill/delegate", x.handleSkillDelegate) // coordinator 委派任务接口
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	x.httpServer = &http.Server{Addr: x.listenAddr, Handler: nil}
	logInfo("x-client", x.agentID, "HTTP 服务启动", "listen_addr", x.listenAddr)
	if err := x.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logError("x-client", x.agentID, "HTTP 服务异常", "error", err.Error())
	}
}

// handleSkillDelegate 处理 coordinator 委派的任务
func (x *XClient) handleSkillDelegate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelID string `json:"channel_id"`
		Sender    string `json:"sender"`
		Content   string `json:"content"`
		MsgID     string `json:"msg_id"`
		Intent    string `json:"intent"`
		Timestamp int64  `json:"timestamp"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	// 检查是否已经处理过该消息（去重）
	if x.isMessageProcessed(req.MsgID) {
		logInfo("x-client", x.agentID, "[重复] 任务已处理", "msg_id", req.MsgID)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "duplicate"})
		return
	}

	logInfo("x-client", x.agentID, "[委派] 收到任务", "sender", req.Sender, "channel_id", req.ChannelID)

	// 构建上下文并处理任务
	memoryWin := x.getMemoryWindow(req.ChannelID)
	contextPrompt := memoryWin.BuildContext(req.Sender, req.Content)
	sessionID := x.sessionMgr.GenerateGroupSessionID()

	// 包装成原始消息格式
	originalMsg := &A2AMessage{
		MsgId:       req.MsgID,
		ChannelId:   req.ChannelID,
		Sender:      req.Sender,
		ContentText: req.Content,
		Intent:      req.Intent,
		Timestamp:   req.Timestamp,
	}

	// 异步处理任务
	go x.wakeupAgentCoreStream(contextPrompt, sessionID, originalMsg)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "delegated"})
}

func (x *XClient) handleSkillSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelID    string   `json:"channel_id"`
		Content      string   `json:"content"`
		MentionUsers []string `json:"mention_users"`
		ReplyToMsgId string   `json:"reply_to_msg_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	x.sendGroupMessage(req.Content, req.ChannelID, req.MentionUsers, req.ReplyToMsgId)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
}

func (x *XClient) sendGroupMessage(content, channelId string, mentionUsers []string, replyToMsgId string) {
	msg := &A2AMessage{
		MsgId:        uuid.New().String(),
		ChannelId:    channelId,
		Sender:       x.agentID,
		Target:       "ALL",
		MentionUsers: mentionUsers,
		Intent:       "INFORM",
		ContentText:  content,
		ReplyToMsgId: replyToMsgId,
		Timestamp:    time.Now().Unix(),
	}

	clientMsg := ClientMessage{
		Action: "speak",
		Data:   msg,
	}
	x.sendMessage(clientMsg)
	logInfo("x-client", x.agentID, "发送群聊消息", "msg_id", msg.MsgId)
}

func (x *XClient) sendMessage(msg interface{}) {
	x.wsMu.Lock()
	defer x.wsMu.Unlock()

	if x.wsConn == nil {
		logError("x-client", x.agentID, "发送消息失败：WebSocket连接未建立")
		return
	}

	if err := x.wsConn.WriteJSON(msg); err != nil {
		logError("x-client", x.agentID, "发送消息失败", "error", err.Error())
	}
}

func (x *XClient) reconnect() {
	for {
		time.Sleep(time.Duration(x.reconnectInterval) * time.Second)
		logInfo("x-client", x.agentID, "尝试重新注册到协调器...")
		if err := x.registerToCoordinator(); err == nil {
			logInfo("x-client", x.agentID, "重新注册成功")
			return
		}
	}
}

// addJoinedChannel 添加加入的聊天室
func (x *XClient) addJoinedChannel(channelId string) {
	x.channelsMu.Lock()
	defer x.channelsMu.Unlock()
	// 检查是否已存在
	for _, ch := range x.joinedChannels {
		if ch == channelId {
			return
		}
	}
	x.joinedChannels = append(x.joinedChannels, channelId)
}

func (x *XClient) Shutdown() {
	logInfo("x-client", x.agentID, "开始优雅关闭...")

	if x.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		x.httpServer.Shutdown(ctx)
	}

	if x.wsConn != nil {
		x.wsConn.Close()
	}

	logInfo("x-client", x.agentID, "优雅关闭完成")
}

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	xclient := NewXClient(cfg)
	if err := xclient.Start(); err != nil {
		log.Fatalf("启动 x-client 失败: %v", err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	<-quit

	xclient.Shutdown()
}
