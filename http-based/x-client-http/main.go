package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
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
	agentID        string
	coordinatorURL string
	agentCoreURL   string
	listenAddr     string
	endpoint       string
	pollInterval   int

	memoryWins     map[string]*MemoryWindow
	sessionMgr     *SessionManager
	messageIDs     map[string]bool
	msgIDsMu       sync.Mutex
	maxMemorySize  int
	maxMemoryChars int

	httpClient       *http.Client
	httpServer       *http.Server
	permissionCache  *PermissionCache
	workspaceManager *WorkspaceManager

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
		agentID:        cfg.AgentID,
		coordinatorURL: cfg.CoordinatorURL,
		agentCoreURL:   cfg.AgentCoreURL,
		listenAddr:     cfg.ListenAddr,
		endpoint:       endpoint,
		pollInterval:   cfg.PollInterval,
		memoryWins:     make(map[string]*MemoryWindow),
		sessionMgr:     NewSessionManager(),
		messageIDs:     make(map[string]bool),
		maxMemorySize:  cfg.MaxMemorySize,
		maxMemoryChars: cfg.MaxMemoryChars,
		joinedRooms:    make(map[string]bool),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		permissionCache:  NewPermissionCache(cfg.CoordinatorURL),
		workspaceManager: NewWorkspaceManager(cfg.CoordinatorURL, cfg.AgentID, ""),
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

	// 特殊命令检测（在 Intent 路由之前）
	content := strings.TrimSpace(msg.Content)
	if strings.HasPrefix(content, "/file") || msg.Type == "file" {
		go x.handleFileMessage(msg, memoryWin)
		return
	}

	// 根据 Intent 类型路由
	switch msg.Intent {
	case "DELEGATE":
		// DELEGATE Intent：创建任务并分配
		go x.handleDelegateCommand(msg, memoryWin)
	case "QUERY":
		// QUERY Intent：查询任务状态
		go x.handleQueryCommand(msg, memoryWin)
	case "RESPONSE":
		// RESPONSE Intent：直接回复（无需特殊处理）
		go x.wakeupAgentCore(msg, memoryWin)
	case "FILE":
		// FILE Intent：文件下载到工作区
		go x.handleFileMessage(msg, memoryWin)
	default:
		// 默认：调用 AgentCore 处理
		go x.wakeupAgentCore(msg, memoryWin)
	}
}

// ============ Delegate 命令处理 ============

func (x *XClient) handleDelegateCommand(msg *PollMessage, memoryWin *MemoryWindow) {
	// 解析 delegate 命令
	cmd := ParseDelegateCommand(msg.Content)
	if !cmd.IsValid {
		// 命令格式错误，发送错误提示
		x.sendReplyWithTaskID(msg, "无效的 /delegate 命令格式。\n正确格式: /delegate <任务标题> to <agent_id> [with focus [ ] <关注点1>, [ ] <关注点2>]", "")
		return
	}

	log.Printf("[INFO] [%s] [Delegate] 解析命令: title=%s, assigned_to=%s, focus_count=%d",
		x.agentID, cmd.Title, cmd.AssignedTo, len(cmd.FocusItems))

	// 调用 Coordinator API 创建任务
	taskID, err := x.createTask(cmd, msg)
	if err != nil {
		log.Printf("[ERROR] [%s] [Delegate] 创建任务失败: %v", x.agentID, err)
		x.sendReplyWithTaskID(msg, fmt.Sprintf("创建任务失败: %v", err), "")
		return
	}

	log.Printf("[INFO] [%s] [Delegate] 任务已创建: task_id=%s", x.agentID, taskID)

	// 发送确认消息，附带任务 ID
	confirmMsg := fmt.Sprintf("任务已创建 ✅\n标题: %s\n分配给: %s\n任务ID: %s", cmd.Title, cmd.AssignedTo, taskID)
	if len(cmd.FocusItems) > 0 {
		confirmMsg += "\n关注点:"
		for _, item := range cmd.FocusItems {
			confirmMsg += fmt.Sprintf("\n  %s", item)
		}
	}
	x.sendReplyWithTaskID(msg, confirmMsg, taskID)
}

// createTask 调用 Coordinator API 创建任务
func (x *XClient) createTask(cmd *DelegateCommand, msg *PollMessage) (string, error) {
	req := CreateTaskRequest{
		Title:        cmd.Title,
		Description:  cmd.Description,
		Priority:     3, // 默认优先级
		AssignedTo:   cmd.AssignedTo,
		RoomID:       msg.RoomID,
		ParentTaskID: cmd.ParentTaskID,
		CreatedBy:    x.agentID,
	}
	jsonData, _ := json.Marshal(req)

	resp, err := x.httpClient.Post(
		x.coordinatorURL+"/api/task/create",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("创建任务失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	var task Task
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return "", err
	}

	// 如果有关注点，创建关注点
	if len(cmd.FocusItems) > 0 {
		for i, content := range cmd.FocusItems {
			if err := x.createFocusItem(task.TaskID, content, cmd.AssignedTo, msg.RoomID, i); err != nil {
				log.Printf("[WARN] [%s] [Delegate] 创建关注点失败: %v", x.agentID, err)
			}
		}
	}

	return task.TaskID, nil
}

// createFocusItem 创建关注点
func (x *XClient) createFocusItem(taskID, content, agentID, roomID string, order int) error {
	reqBody := map[string]interface{}{
		"content":    content,
		"agent_id":   agentID,
		"item_order": order,
	}
	jsonData, _ := json.Marshal(reqBody)

	resp, err := x.httpClient.Post(
		fmt.Sprintf("%s/api/task/%s/focus", x.coordinatorURL, taskID),
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("创建关注点失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	return nil
}

// ============ Query 命令处理 ============

func (x *XClient) handleQueryCommand(msg *PollMessage, memoryWin *MemoryWindow) {
	// 解析消息内容，提取任务 ID 或任务关键词
	content := strings.TrimSpace(msg.Content)

	// 支持多种查询格式：
	// /query <task_id> - 查询指定任务
	// /query room - 查询当前聊天室的所有任务
	// /query my tasks - 查询分配给我的任务
	// /query <关键词> - 搜索任务

	var queryType string
	var queryParam string

	if strings.HasPrefix(content, "/query") {
		parts := strings.SplitN(content, " ", 2)
		if len(parts) > 1 {
			params := strings.TrimSpace(parts[1])
			// 解析参数
			if strings.HasPrefix(params, "room") {
				queryType = "room"
			} else if strings.HasPrefix(params, "my") {
				queryType = "my"
			} else {
				// 假设是 task_id
				queryType = "task"
				queryParam = params
			}
		} else {
			queryType = "my"
		}
	} else {
		// 直接是 QUERY Intent，内容就是查询参数
		queryType = "my"
	}

	var responseContent string

	switch queryType {
	case "task":
		// 查询指定任务
		task, err := x.getTask(queryParam)
		if err != nil {
			responseContent = fmt.Sprintf("查询任务失败: %v", err)
		} else if task == nil {
			responseContent = fmt.Sprintf("任务不存在: %s", queryParam)
		} else {
			responseContent = x.formatTaskResponse(task)
		}
	case "room":
		// 查询当前聊天室的任务
		tasks, err := x.getTasksByRoom(msg.RoomID)
		if err != nil {
			responseContent = fmt.Sprintf("查询任务失败: %v", err)
		} else if len(tasks) == 0 {
			responseContent = "当前聊天室暂无任务"
		} else {
			responseContent = "当前聊天室的任务：\n"
			for _, task := range tasks {
				responseContent += x.formatTaskSummary(task)
			}
		}
	case "my":
		// 查询分配给自己的任务
		tasks, err := x.getTasksByAgent(x.agentID)
		if err != nil {
			responseContent = fmt.Sprintf("查询任务失败: %v", err)
		} else if len(tasks) == 0 {
			responseContent = "暂无分配给你的任务"
		} else {
			responseContent = "分配给你的任务：\n"
			for _, task := range tasks {
				responseContent += x.formatTaskSummary(task)
			}
		}
	default:
		responseContent = "未知的查询类型"
	}

	x.sendReplyWithTaskID(msg, responseContent, "")
}

// handleFileMessage 处理文件消息，下载文件到工作区
// 消息格式: /file <transfer_id> 或 FILE:<transfer_id>
func (x *XClient) handleFileMessage(msg *PollMessage, memoryWin *MemoryWindow) {
	content := strings.TrimSpace(msg.Content)

	// 提取 transfer_id
	var transferID string
	if strings.HasPrefix(content, "/file") {
		transferID = strings.TrimSpace(strings.TrimPrefix(content, "/file"))
	} else if strings.HasPrefix(content, "FILE:") {
		transferID = strings.TrimSpace(strings.TrimPrefix(content, "FILE:"))
	} else {
		// 假设内容就是 transfer_id
		transferID = content
	}

	if transferID == "" {
		x.sendReply(msg, "文件传输ID不能为空")
		return
	}

	log.Printf("[INFO] [%s] [文件] 处理文件下载请求: transfer_id=%s", x.agentID, transferID)

	// 检查缓存
	if localPath, found := x.workspaceManager.GetCachedFile(transferID); found {
		log.Printf("[INFO] [%s] [文件] 文件已缓存: %s", x.agentID, localPath)
		x.sendReply(msg, fmt.Sprintf("文件已存在: %s", localPath))
		return
	}

	// 获取传输记录
	transfer, err := x.workspaceManager.GetTransfer(transferID)
	if err != nil {
		log.Printf("[ERROR] [%s] [文件] 获取传输记录失败: %v", x.agentID, err)
		x.sendReply(msg, fmt.Sprintf("获取文件信息失败: %v", err))
		return
	}

	if transfer == nil {
		x.sendReply(msg, fmt.Sprintf("传输记录不存在: %s", transferID))
		return
	}

	// 获取下载 URL
	downloadResp, err := x.workspaceManager.DownloadFile(transferID)
	if err != nil {
		log.Printf("[ERROR] [%s] [文件] 获取下载URL失败: %v", x.agentID, err)
		x.sendReply(msg, fmt.Sprintf("获取下载链接失败: %v", err))
		return
	}

	// 从 S3 下载文件
	// 使用 room 级别的下载目录
	roomID := msg.RoomID
	downloadDir := x.workspaceManager.GetDownloadsPath(roomID)
	if err := x.workspaceManager.DownloadFileFromS3ToPath(transferID, downloadResp.PresignedURL, transfer.FileName, downloadDir); err != nil {
		log.Printf("[ERROR] [%s] [文件] 下载文件失败: %v", x.agentID, err)
		x.sendReply(msg, fmt.Sprintf("下载文件失败: %v", err))
		return
	}

	localPath, _ := x.workspaceManager.GetCachedFile(transferID)
	log.Printf("[INFO] [%s] [文件] 文件已保存: %s", x.agentID, localPath)
	x.sendReply(msg, fmt.Sprintf("文件已保存到工作区: %s\n文件大小: %d bytes", localPath, transfer.FileSize))
}

// getTask 获取任务详情
func (x *XClient) getTask(taskID string) (*Task, error) {
	resp, err := x.httpClient.Get(fmt.Sprintf("%s/api/task/%s", x.coordinatorURL, taskID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("获取任务失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	var task Task
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, err
	}
	return &task, nil
}

// getTasksByRoom 获取聊天室的任务列表
func (x *XClient) getTasksByRoom(roomID string) ([]*Task, error) {
	resp, err := x.httpClient.Get(fmt.Sprintf("%s/api/room/%s/tasks", x.coordinatorURL, roomID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("获取任务列表失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool    `json:"success"`
		Tasks   []*Task `json:"tasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Tasks, nil
}

// getTasksByAgent 获取 Agent 被分配的任务列表
func (x *XClient) getTasksByAgent(agentID string) ([]*Task, error) {
	resp, err := x.httpClient.Get(fmt.Sprintf("%s/api/agent/%s/tasks", x.coordinatorURL, agentID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("获取任务列表失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool    `json:"success"`
		Tasks   []*Task `json:"tasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Tasks, nil
}

// formatTaskResponse 格式化任务详情
func (x *XClient) formatTaskResponse(task *Task) string {
	statusIcon := map[string]string{
		"todo":        "📋",
		"in_progress": "🔄",
		"done":        "✅",
	}
	icon := statusIcon[task.Status]
	if icon == "" {
		icon = "📋"
	}

	return fmt.Sprintf("%s 任务详情\n"+
		"标题: %s\n"+
		"状态: %s %s\n"+
		"优先级: %d\n"+
		"分配给: %s\n"+
		"创建者: %s\n"+
		"任务ID: %s",
		icon, task.Title, icon, task.Status, task.Priority, task.AssignedTo, task.CreatedBy, task.TaskID)
}

// formatTaskSummary 格式化任务摘要
func (x *XClient) formatTaskSummary(task *Task) string {
	statusIcon := map[string]string{
		"todo":        "📋",
		"in_progress": "🔄",
		"done":        "✅",
	}
	icon := statusIcon[task.Status]
	if icon == "" {
		icon = "📋"
	}
	return fmt.Sprintf("%s [%s] %s (优先级: %d)\n", icon, task.Status, task.Title, task.Priority)
}

// sendReplyWithTaskID 发送回复并附带任务 ID
func (x *XClient) sendReplyWithTaskID(originalMsg *PollMessage, content, taskID string) {
	req := SendMessageRequest{
		RoomID:       originalMsg.RoomID,
		SenderID:     x.agentID,
		SenderType:   "agent",
		Content:      content,
		TargetID:     "ALL",
		MentionUsers: []string{},
		Intent:       "RESPONSE",
		ReplyToMsgID: originalMsg.MsgID,
		TaskID:       taskID,
	}
	jsonData, _ := json.Marshal(req)

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

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("[ERROR] [%s] 发送回复失败，状态码: %d, 响应: %s", x.agentID, resp.StatusCode, string(bodyBytes))
	}
}

// ============ AgentCore 调用 ============

func (x *XClient) wakeupAgentCore(msg *PollMessage, memoryWin *MemoryWindow) {
	contextPrompt := memoryWin.BuildContext(msg.SenderID, msg.Content)
	sessionID := x.sessionMgr.GenerateSessionID()
	workspaceDir := x.workspaceManager.GetWorkspaceDirForRoom(msg.RoomID)

	// 确保工作目录存在
	x.workspaceManager.EnsureWorkspaceDirForRoom(msg.RoomID)

	// 构建请求
	reqBody := map[string]string{
		"message":    contextPrompt,
		"session_id": sessionID,
		"sender":     msg.SenderID,
		"workspace":  workspaceDir,
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
		RoomID:       originalMsg.RoomID,
		SenderID:     x.agentID,
		SenderType:   "agent",
		Content:      content,
		TargetID:     "ALL",
		MentionUsers: []string{},
		Intent:       "RESPONSE",
		ReplyToMsgID: originalMsg.MsgID,
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

	// Agent 主动发送消息接口
	mux.HandleFunc("/api/send", x.handleAgentSendMessage)

	// Agent 自主委托接口（供 Coordinator 调用）
	mux.HandleFunc("/skill/delegate", x.handleSkillDelegate)
	mux.HandleFunc("/skill/send", x.handleSkillSend)

	// 文件上传接口
	mux.HandleFunc("/api/file/upload", x.handleFileUpload)
	mux.HandleFunc("/api/file/download", x.handleFileDownload)
	mux.HandleFunc("/api/test/static", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[DEBUG] test static route matched")
		w.Write([]byte("test static ok"))
	})

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
		MsgID:    req.MsgID,
		RoomID:   req.RoomID,
		SenderID: req.Sender,
		Content:  req.Content,
		Intent:   req.Intent,
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
		"agent_id":       x.agentID,
		"status":         "running",
		"poll_interval":  x.pollInterval,
		"message_count":  msgCount,
		"room_count":     roomCount,
		"memory_windows": len(x.memoryWins),
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}

// handleAgentSendMessage 处理 Agent 主动发送消息的请求
// Agent 调用此接口将消息通过 x-client 代理发送到 Coordinator
func (x *XClient) handleAgentSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST 方法", http.StatusMethodNotAllowed)
		return
	}

	var req AgentSendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "无效的请求体: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 设置默认值
	if req.TargetID == "" {
		req.TargetID = "ALL"
	}
	if req.Intent == "" {
		req.Intent = "INFORM"
	}

	// 权限检查：检查是否有发送消息的权限
	allowed, reason := x.permissionCache.CheckPermission(x.agentID, "message:send", "send")
	if !allowed {
		log.Printf("[WARN] [%s] [权限检查] 发送消息被拒绝: agent=%s, reason=%s",
			x.agentID, x.agentID, reason)
		json.NewEncoder(w).Encode(AgentSendMessageResponse{
			Success: false,
			Error:   "权限不足: " + reason,
		})
		return
	}

	// 构建发送到 Coordinator 的请求
	coordinatorReq := SendMessageRequest{
		RoomID:       req.RoomID,
		SenderID:     x.agentID,
		SenderType:   "agent",
		Content:      req.Content,
		TargetID:     req.TargetID,
		MentionUsers: req.MentionUsers,
		Intent:       req.Intent,
		ReplyToMsgID: req.ReplyToMsgID,
	}
	jsonData, _ := json.Marshal(coordinatorReq)

	log.Printf("[INFO] [%s] [代理发送] 收到 Agent 发送请求，转发到 Coordinator: room=%s, content=%s",
		x.agentID, req.RoomID, truncate(req.Content, 50))

	// 调用 Coordinator 的 /api/message 接口
	resp, err := x.httpClient.Post(
		x.coordinatorURL+"/api/message",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		log.Printf("[ERROR] [%s] [代理发送] 调用 Coordinator 失败: %v", x.agentID, err)
		json.NewEncoder(w).Encode(AgentSendMessageResponse{
			Success: false,
			Error:   "发送失败: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	// 解析 Coordinator 的响应
	var sendResp SendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&sendResp); err != nil {
		log.Printf("[ERROR] [%s] [代理发送] 解析 Coordinator 响应失败: %v", x.agentID, err)
		json.NewEncoder(w).Encode(AgentSendMessageResponse{
			Success: false,
			Error:   "解析响应失败: " + err.Error(),
		})
		return
	}

	if resp.StatusCode != http.StatusOK || !sendResp.Success {
		log.Printf("[ERROR] [%s] [代理发送] Coordinator 返回错误: %s", x.agentID, sendResp.Error)
		json.NewEncoder(w).Encode(AgentSendMessageResponse{
			Success: false,
			Error:   sendResp.Error,
		})
		return
	}

	log.Printf("[INFO] [%s] [代理发送] 消息发送成功: msg_id=%s", x.agentID, sendResp.MsgID)
	json.NewEncoder(w).Encode(AgentSendMessageResponse{
		Success: true,
		MsgID:   sendResp.MsgID,
	})
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

// ============ Agent 自主委托接口（供 Coordinator 调用）============

// SkillDelegateRequest Coordinator 委托任务请求
type SkillDelegateRequest struct {
	RoomID    string `json:"room_id"`
	Sender    string `json:"sender"`
	Content   string `json:"content"`
	MsgID     string `json:"msg_id"`
	Intent    string `json:"intent"`
	TaskID    string `json:"task_id,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// SkillSendRequest Agent 发送消息请求
type SkillSendRequest struct {
	RoomID       string   `json:"room_id"`
	Content      string   `json:"content"`
	MentionUsers []string `json:"mention_users"`
	ReplyToMsgID string   `json:"reply_to_msg_id"`
	Intent       string   `json:"intent"`
}

// handleSkillDelegate 处理 Coordinator 委派的任务
// AgentCore 可以通过这个接口接收来自 Coordinator 的任务委托
func (x *XClient) handleSkillDelegate(w http.ResponseWriter, r *http.Request) {
	var req SkillDelegateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[ERROR] [%s] [委托] 解析请求失败: %v", x.agentID, err)
		http.Error(w, err.Error(), 400)
		return
	}

	// 检查是否已经处理过该消息（去重）
	if x.isMessageProcessed(req.MsgID) {
		log.Printf("[INFO] [%s] [委托] 消息已处理，跳过: msg_id=%s", x.agentID, req.MsgID)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "duplicate"})
		return
	}

	log.Printf("[INFO] [%s] [委托] 收到任务委派: sender=%s, room_id=%s, content=%s",
		x.agentID, req.Sender, req.RoomID, truncate(req.Content, 50))

	// 异步处理任务
	// 构建上下文并处理任务
	memoryWin := x.getMemoryWindow(req.RoomID)

	// 创建消息记录
	msg := &PollMessage{
		MsgID:     req.MsgID,
		RoomID:    req.RoomID,
		SenderID:  req.Sender,
		Content:   req.Content,
		Intent:    req.Intent,
		TaskID:    req.TaskID,
		CreatedAt: req.Timestamp,
	}

	// 调用 wakeupAgentCore 处理任务
	go x.wakeupAgentCore(msg, memoryWin)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "delegated"})
}

// handleSkillSend 处理 Agent 主动发送消息
// AgentCore 可以通过这个接口发送消息到聊天室
func (x *XClient) handleSkillSend(w http.ResponseWriter, r *http.Request) {
	var req SkillSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[ERROR] [%s] [发送] 解析请求失败: %v", x.agentID, err)
		http.Error(w, err.Error(), 400)
		return
	}

	log.Printf("[INFO] [%s] [发送] 收到发送请求: room_id=%s, content=%s, intent=%s",
		x.agentID, req.RoomID, truncate(req.Content, 50), req.Intent)

	// 构建 Intent
	intent := req.Intent
	if intent == "" {
		intent = "INFORM"
	}

	// 构建发送到 Coordinator 的请求
	coordinatorReq := SendMessageRequest{
		RoomID:       req.RoomID,
		SenderID:     x.agentID,
		SenderType:   "agent",
		Content:      req.Content,
		TargetID:     "ALL",
		MentionUsers: req.MentionUsers,
		Intent:       intent,
		ReplyToMsgID: req.ReplyToMsgID,
	}
	jsonData, _ := json.Marshal(coordinatorReq)

	// 调用 Coordinator 的 /api/message 接口发送消息
	resp, err := x.httpClient.Post(
		x.coordinatorURL+"/api/message",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		log.Printf("[ERROR] [%s] [发送] 发送消息失败: %v", x.agentID, err)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "error",
			"error":  err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	var sendResp SendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&sendResp); err != nil {
		log.Printf("[ERROR] [%s] [发送] 解析响应失败: %v", x.agentID, err)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "error",
			"error":  err.Error(),
		})
		return
	}

	if resp.StatusCode != http.StatusOK || !sendResp.Success {
		log.Printf("[ERROR] [%s] [发送] Coordinator 返回错误: %s", x.agentID, sendResp.Error)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "error",
			"error":  sendResp.Error,
		})
		return
	}

	log.Printf("[INFO] [%s] [发送] 消息发送成功: msg_id=%s", x.agentID, sendResp.MsgID)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
}

// ============ Agent 关系 API ============

// GetAgentContext 获取 Agent 完整上下文
func (x *XClient) GetAgentContext(roomID string) (*AgentContext, error) {
	url := fmt.Sprintf("%s/api/agent/context?agent_id=%s", x.coordinatorURL, x.agentID)
	if roomID != "" {
		url += "&room_id=" + roomID
	}

	resp, err := x.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Success bool          `json:"success"`
		Context *AgentContext `json:"context"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Context, nil
}

// GetRoomAgents 获取聊天室成员
func (x *XClient) GetRoomAgents(roomID string) ([]*AgentInfo, error) {
	url := fmt.Sprintf("%s/api/room/%s/agents", x.coordinatorURL, roomID)

	resp, err := x.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Success bool         `json:"success"`
		Agents  []*AgentInfo `json:"agents"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Agents, nil
}

// GetAgentRelations 获取 Agent 关系
func (x *XClient) GetAgentRelations(roomID string) (*Relations, error) {
	url := fmt.Sprintf("%s/api/agent/relations?agent_id=%s", x.coordinatorURL, x.agentID)
	if roomID != "" {
		url += "&room_id=" + roomID
	}

	resp, err := x.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Success   bool            `json:"success"`
		Relations []AgentRelation `json:"relations"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	// 转换为 Relations 汇总格式
	rel := &Relations{
		Colleagues:   []string{},
		Superiors:    []string{},
		Subordinates: []string{},
	}
	for _, r := range result.Relations {
		switch r.RelationType {
		case "colleague":
			rel.Colleagues = append(rel.Colleagues, r.RelatedAgentID)
		case "superior":
			rel.Superiors = append(rel.Superiors, r.RelatedAgentID)
		case "subordinate":
			rel.Subordinates = append(rel.Subordinates, r.RelatedAgentID)
		}
	}

	return rel, nil
}

// BuildCollaborationPrompt 构建协作提示
func (x *XClient) BuildCollaborationPrompt(ctx *AgentContext) string {
	var sb strings.Builder

	sb.WriteString("【团队协作上下文】\n\n")

	// 聊天室成员
	if len(ctx.RoomMembers) > 0 {
		sb.WriteString("📋 当前聊天室成员:\n")
		for _, agent := range ctx.RoomMembers {
			role := ""
			if agent.Role != "" {
				role = fmt.Sprintf(" (%s)", agent.Role)
			}
			status := "离线"
			if agent.Online {
				status = "在线"
			}
			sb.WriteString(fmt.Sprintf("  • %s%s [%s]\n", agent.AgentID, role, status))
		}
		sb.WriteString("\n")
	}

	// 同事关系
	if ctx.Relations != nil && len(ctx.Relations.Colleagues) > 0 {
		sb.WriteString("👥 同事:\n")
		for _, id := range ctx.Relations.Colleagues {
			sb.WriteString(fmt.Sprintf("  • %s\n", id))
		}
		sb.WriteString("\n")
	}

	// 上下级关系
	if ctx.Relations != nil {
		if len(ctx.Relations.Superiors) > 0 {
			sb.WriteString("⬆️ 上级:\n")
			for _, id := range ctx.Relations.Superiors {
				sb.WriteString(fmt.Sprintf("  • %s\n", id))
			}
			sb.WriteString("\n")
		}
		if len(ctx.Relations.Subordinates) > 0 {
			sb.WriteString("⬇️ 下级:\n")
			for _, id := range ctx.Relations.Subordinates {
				sb.WriteString(fmt.Sprintf("  • %s\n", id))
			}
			sb.WriteString("\n")
		}
	}

	// 可用的协作动作
	sb.WriteString("💡 可用协作命令:\n")
	sb.WriteString("  • @agent-id - 向指定 Agent 发送消息\n")
	sb.WriteString("  • /delegate 任务 to agent-id - 分配任务\n")
	sb.WriteString("  • /query task-id - 查询任务状态\n\n")

	return sb.String()
}

// ============ 文件传输 API ============

// handleFileUpload 处理文件上传
// Agent 可以调用此接口将文件上传到 S3，并通过聊天室发送给其他 Agent
// 使用 multipart/form-data 格式
func (x *XClient) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST 方法", http.StatusMethodNotAllowed)
		return
	}

	// 解析 multipart form
	if err := r.ParseMultipartForm(100 << 20); err != nil { // 100MB limit
		// 如果不是 multipart，尝试普通解析
		if err := r.ParseForm(); err != nil {
			http.Error(w, "解析请求失败: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	fileName := r.FormValue("file_name")
	roomID := r.FormValue("room_id")
	toAgent := r.FormValue("to_agent")

	if fileName == "" || roomID == "" {
		http.Error(w, "缺少必要参数: file_name, room_id", http.StatusBadRequest)
		return
	}

	log.Printf("[INFO] [%s] [上传] 收到文件上传请求: file_name=%s, room_id=%s, to_agent=%s",
		x.agentID, fileName, roomID, toAgent)

	var fileData []byte
	var mimeType string

	// 尝试从 multipart form 获取文件
	if f, header, err := r.FormFile("file"); err == nil {
		defer f.Close()
		fileData, err = io.ReadAll(f)
		if err != nil {
			log.Printf("[ERROR] [%s] [上传] 读取文件失败: %v", x.agentID, err)
			http.Error(w, "读取文件失败", http.StatusInternalServerError)
			return
		}
		mimeType = header.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		if fileName == "" {
			fileName = header.Filename
		}
	} else {
		// 如果没有文件，检查是否有 base64 编码的文件数据
		fileBase64 := r.FormValue("file_data")
		if fileBase64 == "" {
			http.Error(w, "缺少文件数据", http.StatusBadRequest)
			return
		}
		var err error
		fileData, err = base64.StdEncoding.DecodeString(fileBase64)
		if err != nil {
			http.Error(w, "文件数据 base64 解码失败", http.StatusBadRequest)
			return
		}
		mimeType = r.FormValue("mime_type")
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}

	// 获取上传 Presigned URL
	uploadResp, err := x.workspaceManager.UploadFile(fileName, int64(len(fileData)), mimeType, roomID, "")
	if err != nil {
		log.Printf("[ERROR] [%s] [上传] 获取上传 URL 失败: %v", x.agentID, err)
		http.Error(w, "获取上传 URL 失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 上传到 S3
	if err := x.workspaceManager.UploadFileToS3Data(uploadResp.PresignedURL, fileData, mimeType); err != nil {
		log.Printf("[ERROR] [%s] [上传] 上传文件到 S3 失败: %v", x.agentID, err)
		http.Error(w, "上传文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 确认上传
	if err := x.workspaceManager.ConfirmUpload(uploadResp.TransferID, x.agentID, toAgent); err != nil {
		log.Printf("[WARN] [%s] [上传] 确认上传失败: %v", x.agentID, err)
	}

	log.Printf("[INFO] [%s] [上传] 文件上传成功: transfer_id=%s", x.agentID, uploadResp.TransferID)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "success",
		"transfer_id": uploadResp.TransferID,
		"s3_key":      uploadResp.S3Key,
		"file_name":   fileName,
	})
}

// handleFileDownload 处理文件下载
// 根据 transfer_id 下载文件到工作区
func (x *XClient) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	transferID := r.URL.Query().Get("transfer_id")
	if transferID == "" {
		http.Error(w, "缺少 transfer_id", http.StatusBadRequest)
		return
	}

	log.Printf("[INFO] [%s] [下载] 收到文件下载请求: transfer_id=%s", x.agentID, transferID)

	// 获取传输记录
	transfer, err := x.workspaceManager.GetTransfer(transferID)
	if err != nil || transfer == nil {
		log.Printf("[ERROR] [%s] [下载] 获取传输记录失败: %v", x.agentID, err)
		http.Error(w, "传输记录不存在", http.StatusNotFound)
		return
	}

	// 获取下载 URL
	downloadResp, err := x.workspaceManager.DownloadFile(transferID)
	if err != nil {
		log.Printf("[ERROR] [%s] [下载] 获取下载 URL 失败: %v", x.agentID, err)
		http.Error(w, "获取下载链接失败", http.StatusInternalServerError)
		return
	}

	// 从 S3 下载文件
	data, err := x.workspaceManager.DownloadFileFromS3Data(downloadResp.PresignedURL)
	if err != nil {
		log.Printf("[ERROR] [%s] [下载] 从 S3 下载文件失败: %v", x.agentID, err)
		http.Error(w, "下载文件失败", http.StatusInternalServerError)
		return
	}

	log.Printf("[INFO] [%s] [下载] 文件下载成功: transfer_id=%s, size=%d", x.agentID, transferID, len(data))

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", transfer.FileName))
	w.Write(data)
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
