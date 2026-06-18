package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ============ XClient HTTP 版本 ============

type XClient struct {
	agentID      string
	coordinatorURL string
	agentCoreURL string
	listenAddr   string
	endpoint     string
	pollInterval int

	memoryWins    map[string]*MemoryWindow
	sessionMgr    *SessionManager
	messageIDs    map[string]bool
	msgIDsMu      sync.Mutex
	maxMemorySize int
	maxMemoryChars int

	httpClient *http.Client
	httpServer *http.Server

	// 已加入的聊天室
	joinedRooms map[string]bool
	roomsMu     sync.RWMutex
}

func NewXClient(cfg *Config) *XClient {
	// 构造 endpoint
	endpoint := cfg.Endpoint
	if endpoint == "" {
		// 从 listenAddr 提取端口，构造 localhost URL
		port := cfg.ListenAddr
		if len(port) > 0 && port[0] == ':' {
			endpoint = "http://localhost" + port
		} else {
			endpoint = "http://" + port
		}
	}

	return &XClient{
		agentID:       cfg.AgentID,
		coordinatorURL: cfg.CoordinatorURL,
		agentCoreURL:  cfg.AgentCoreURL,
		listenAddr:    cfg.ListenAddr,
		endpoint:      endpoint,
		pollInterval:  cfg.PollInterval,
		memoryWins:    make(map[string]*MemoryWindow),
		sessionMgr:    NewSessionManager(),
		messageIDs:    make(map[string]bool),
		maxMemorySize: cfg.MaxMemorySize,
		maxMemoryChars: cfg.MaxMemoryChars,
		joinedRooms:   make(map[string]bool),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (x *XClient) getMemoryWindow(roomID string) *MemoryWindow {
	x.msgIDsMu.Lock()
	defer x.msgIDsMu.Unlock()

	if win, exists := x.memoryWins[roomID]; exists {
		return win
	}

	win := NewMemoryWindow(x.maxMemorySize, x.maxMemoryChars)
	x.memoryWins[roomID] = win
	return win
}

func (x *XClient) isMessageProcessed(msgId string) bool {
	x.msgIDsMu.Lock()
	defer x.msgIDsMu.Unlock()

	if x.messageIDs[msgId] {
		return true
	}
	x.messageIDs[msgId] = true

	// 清理过期的消息 ID
	if len(x.messageIDs) > 1000 {
		clearCount := len(x.messageIDs) - 500
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

// ============ 注册和心跳 ============

func (x *XClient) register() error {
	req := RegisterRequest{
		AgentID:  x.agentID,
		Endpoint: x.endpoint,
	}
	jsonData, _ := json.Marshal(req)

	resp, err := x.httpClient.Post(
		x.coordinatorURL+"/api/agent/register",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return fmt.Errorf("注册失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("注册失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	log.Printf("[INFO] [%s] 注册成功，endpoint: %s", x.agentID, x.endpoint)
	return nil
}

func (x *XClient) heartbeat() error {
	req := map[string]string{"agent_id": x.agentID}
	jsonData, _ := json.Marshal(req)

	resp, err := x.httpClient.Post(
		x.coordinatorURL+"/api/agent/heartbeat",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return fmt.Errorf("心跳失败: %w", err)
	}
	defer resp.Body.Close()

	return nil
}

// ============ 轮询消息 ============

func (x *XClient) pollMessages(since int64) (*PollResponse, error) {
	url := fmt.Sprintf("%s/api/poll?agent_id=%s&since=%d&limit=%d",
		x.coordinatorURL, x.agentID, since, 50)

	resp, err := x.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("轮询失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("轮询失败，状态码: %d", resp.StatusCode)
	}

	var pollResp PollResponse
	if err := json.NewDecoder(resp.Body).Decode(&pollResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return &pollResp, nil
}

func (x *XClient) pollLoop() {
	since := int64(0) // 查询所有历史消息

	ticker := time.NewTicker(time.Duration(x.pollInterval) * time.Second)
	defer ticker.Stop()

	log.Printf("[INFO] [%s] 轮询循环启动，间隔: %ds", x.agentID, x.pollInterval)

	// 立即执行一次轮询
	// log.Printf("[DEBUG] [%s] 立即执行首次轮询", x.agentID)
	x.doPoll(&since)

	for {
		select {
		case <-ticker.C:
			x.doPoll(&since)
		}
	}
}

func (x *XClient) doPoll(since *int64) {
	// log.Printf("[DEBUG] [%s] 开始轮询 since=%d", x.agentID, *since)
	resp, err := x.pollMessages(*since)
	if err != nil {
		log.Printf("[WARN] [%s] 轮询失败: %v", x.agentID, err)
		return
	}

	// log.Printf("[DEBUG] [%s] 轮询成功，收到 %d 条消息", x.agentID, len(resp.Messages))

	// 处理消息
	for _, msg := range resp.Messages {
		// log.Printf("[DEBUG] [%s] 处理消息: %s", x.agentID, msg.Content)
		x.handleMessage(msg)
	}

	// 更新 since
	if resp.NextSince > *since {
		*since = resp.NextSince
	}
}

func (x *XClient) handleMessage(msg *PollMessage) {
	// 去重
	if x.isMessageProcessed(msg.MsgID) {
		return
	}

	log.Printf("[INFO] [%s] 收到消息 [%s]: %s (from %s)",
		x.agentID, msg.RoomID, truncate(msg.Content, 50), msg.SenderID)

	// 添加到记忆窗口（旁听模式）
	memoryWin := x.getMemoryWindow(msg.RoomID)
	memoryWin.Push(msg.SenderID, msg.Content)

	// 检查是否 @ 了自己
	isMentioned := false
	for _, user := range msg.MentionUsers {
		if user == x.agentID {
			isMentioned = true
			break
		}
	}

	if !isMentioned {
		log.Printf("[INFO] [%s] [旁听] 消息已存储", x.agentID)
		return
	}

	// 被 @ 了，需要处理
	log.Printf("[INFO] [%s] [唤醒] 被 @ 消息，需要处理", x.agentID)

	// 异步调用 AgentCore
	go x.wakeupAgentCore(msg, memoryWin)
}

// ============ AgentCore 调用 ============

func (x *XClient) wakeupAgentCore(msg *PollMessage, memoryWin *MemoryWindow) {
	contextPrompt := memoryWin.BuildContext(msg.SenderID, msg.Content)
	sessionID := x.sessionMgr.GenerateSessionID()

	// 构建请求
	reqBody := map[string]string{
		"message":    contextPrompt,
		"session_id": sessionID,
		"sender":     msg.SenderID,
	}
	jsonData, _ := json.Marshal(reqBody)

	// 调用流式接口
	req, err := http.NewRequest("POST", x.agentCoreURL+"/chat/stream", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("[ERROR] [%s] 创建请求失败: %v", x.agentID, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0} // 无超时
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[ERROR] [%s] 调用 AgentCore 失败: %v", x.agentID, err)
		return
	}
	defer resp.Body.Close()

	// 读取流式响应
	var fullContent strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 100), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			data = strings.TrimSpace(data)
			if data == "" || data == "[DONE]" {
				continue
			}
			fullContent.WriteString(data)
		}
	}

	responseContent := fullContent.String()
	log.Printf("[INFO] [%s] AgentCore 响应完成，长度: %d", x.agentID, len(responseContent))

	// 发送回复消息
	x.sendReply(msg, responseContent)
}

func (x *XClient) sendReply(originalMsg *PollMessage, content string) {
	req := SendMessageRequest{
		RoomID:        originalMsg.RoomID,
		SenderID:      x.agentID,
		SenderType:    "agent",
		Content:       content,
		TargetID:      "ALL",
		MentionUsers:  []string{},
		Intent:        "RESPONSE",
		ReplyToMsgID:  originalMsg.MsgID,
	}
	jsonData, _ := json.Marshal(req)

	log.Printf("[DEBUG] [%s] 发送回复请求: %s, URL: %s", x.agentID, string(jsonData), x.coordinatorURL+"/api/message")
	
	resp, err := x.httpClient.Post(
		x.coordinatorURL+"/api/message",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		log.Printf("[ERROR] [%s] 发送回复失败: %v", x.agentID, err)
		return
	}
	defer resp.Body.Close()

	// 打印响应信息
	bodyBytes, _ := io.ReadAll(resp.Body)
	log.Printf("[DEBUG] [%s] 收到响应: 状态码=%d, 响应内容=%s", x.agentID, resp.StatusCode, string(bodyBytes))

	// 重置响应体，以便再次读取
	resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	var sendResp SendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&sendResp); err != nil {
		log.Printf("[ERROR] [%s] 解析发送响应失败: %v", x.agentID, err)
		return
	}

	if sendResp.Success {
		log.Printf("[INFO] [%s] 回复已发送，msg_id: %s", x.agentID, sendResp.MsgID)
	} else {
		log.Printf("[ERROR] [%s] 发送回复失败: %s", x.agentID, sendResp.Error)
	}
}

// ============ HTTP 服务 ============

func (x *XClient) startHTTPServer() {
	mux := http.NewServeMux()

	// 健康检查
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// 技能回调（供外部调用）
	mux.HandleFunc("/skill/callback", x.handleSkillCallback)

	// 获取状态
	mux.HandleFunc("/status", x.handleStatus)

	x.httpServer = &http.Server{
		Addr:    x.listenAddr,
		Handler: mux,
	}

	log.Printf("[INFO] [%s] HTTP 服务启动，监听: %s", x.agentID, x.listenAddr)
	if err := x.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("[ERROR] [%s] HTTP 服务异常: %v", x.agentID, err)
	}
}

func (x *XClient) handleSkillCallback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RoomID  string `json:"room_id"`
		Sender  string `json:"sender"`
		Content string `json:"content"`
		MsgID   string `json:"msg_id"`
		Intent  string `json:"intent"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	// 检查是否已处理
	if x.isMessageProcessed(req.MsgID) {
		json.NewEncoder(w).Encode(map[string]string{"status": "duplicate"})
		return
	}

	log.Printf("[INFO] [%s] 收到 Skill 回调: %s", x.agentID, req.Content)

	// 处理回调
	memoryWin := x.getMemoryWindow(req.RoomID)
	go x.wakeupAgentCore(&PollMessage{
		MsgID:      req.MsgID,
		RoomID:     req.RoomID,
		SenderID:   req.Sender,
		Content:    req.Content,
		Intent:     req.Intent,
	}, memoryWin)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "received"})
}

func (x *XClient) handleStatus(w http.ResponseWriter, r *http.Request) {
	x.msgIDsMu.Lock()
	msgCount := len(x.messageIDs)
	x.msgIDsMu.Unlock()

	x.roomsMu.RLock()
	roomCount := len(x.joinedRooms)
	x.roomsMu.RUnlock()

	status := map[string]interface{}{
		"agent_id":      x.agentID,
		"status":        "running",
		"poll_interval": x.pollInterval,
		"message_count": msgCount,
		"room_count":    roomCount,
		"memory_windows": len(x.memoryWins),
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}

// ============ 启动和关闭 ============

func (x *XClient) Start() error {
	// 注册
	if err := x.register(); err != nil {
		return err
	}

	// 启动 HTTP 服务
	go x.startHTTPServer()

	// 启动轮询循环
	go x.pollLoop()

	// 启动心跳
	go x.heartbeatLoop()

	log.Printf("[INFO] [%s] x-client 启动成功", x.agentID)
	return nil
}

func (x *XClient) heartbeatLoop() {
	ticker := time.NewTicker(time.Duration(30) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if err := x.heartbeat(); err != nil {
			log.Printf("[WARN] [%s] 心跳失败: %v", x.agentID, err)
		}
	}
}

func (x *XClient) Shutdown() {
	log.Printf("[INFO] [%s] 开始关闭...", x.agentID)

	if x.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		x.httpServer.Shutdown(ctx)
	}

	log.Printf("[INFO] [%s] 已关闭", x.agentID)
}

// ============ 辅助函数 ============

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func main() {
	flag.Parse()

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	client := NewXClient(cfg)
	if err := client.Start(); err != nil {
		log.Fatalf("启动失败: %v", err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	client.Shutdown()
}
