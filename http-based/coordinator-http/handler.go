package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// ============ HTTP Handler ============

type Handler struct {
	storage *Storage
	cfg     *Config

	// User WebSocket 连接管理（按 connectionID 索引，支持多 Tab）
	userConns   map[string]*UserConn // connection_id -> connection
	userConnsMu sync.RWMutex

	// User 订阅的聊天室（按 userID 索引，一个用户可以在多个房间）
	userRooms   map[string]map[string]bool // user_id -> room_id -> true
	userRoomsMu sync.RWMutex

	upgrader websocket.Upgrader
}

type UserConn struct {
	UserID       string // 用户ID（同一用户可能有多个连接）
	ConnectionID string // 唯一连接ID
	Conn         *websocket.Conn
	Send         chan []byte
	Rooms        map[string]bool
	RoomsMu      sync.RWMutex
	CloseChan    chan struct{} // 连接关闭信号
}

func NewHandler(storage *Storage, cfg *Config) *Handler {
	h := &Handler{
		storage:   storage,
		cfg:       cfg,
		userConns: make(map[string]*UserConn),
		userRooms: make(map[string]map[string]bool),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // 生产环境应限制来源
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}

	// 启动用户连接清理
	go h.cleanupUserConnections()

	return h
}

// ============ Agent HTTP API ============

// RegisterHandler Agent 注册
func (h *Handler) RegisterHandler(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的请求体")
		return
	}

	if req.AgentID == "" || req.Endpoint == "" {
		h.writeError(w, http.StatusBadRequest, "agent_id 和 endpoint 不能为空")
		return
	}

	if err := h.storage.RegisterAgent(req.AgentID, req.Endpoint); err != nil {
		slog.Error("注册 Agent 失败", "error", err)
		h.writeError(w, http.StatusInternalServerError, "注册失败")
		return
	}

	slog.Info("Agent 注册成功", "agent_id", req.AgentID, "endpoint", req.Endpoint)

	h.writeJSON(w, http.StatusOK, RegisterResponse{
		Success: true,
		Message: "注册成功",
	})
}

// HeartbeatHandler 心跳
func (h *Handler) HeartbeatHandler(w http.ResponseWriter, r *http.Request) {
	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的请求体")
		return
	}

	if err := h.storage.UpdateHeartbeat(req.AgentID); err != nil {
		slog.Warn("更新心跳失败", "agent_id", req.AgentID, "error", err)
	}

	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// PollHandler Agent 轮询消息
func (h *Handler) PollHandler(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		h.writeError(w, http.StatusBadRequest, "agent_id 不能为空")
		return
	}

	// 解析参数
	var since int64
	fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)

	roomID := r.URL.Query().Get("room_id")

	var limit int
	fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
	if limit <= 0 {
		limit = h.cfg.PollBatchSize
	}

	// 轮询消息
	messages, nextSince, err := h.storage.PollMessages(agentID, since, roomID, limit)
	if err != nil {
		slog.Error("轮询消息失败", "error", err)
		h.writeError(w, http.StatusInternalServerError, "轮询失败")
		return
	}

	// 标记消息已投递
	if len(messages) > 0 {
		msgIDs := make([]string, len(messages))
		for i, m := range messages {
			msgIDs[i] = m.MsgID
		}
		h.storage.MarkMessagesDelivered(msgIDs, agentID)
	}

	// 更新心跳
	h.storage.UpdateHeartbeat(agentID)

	h.writeJSON(w, http.StatusOK, PollResponse{
		Messages:  messages,
		NextSince: nextSince,
	})
}

// SendMessageHandler Agent/User 发送消息
func (h *Handler) SendMessageHandler(w http.ResponseWriter, r *http.Request) {
	var req SendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的请求体")
		return
	}

	// 验证聊天室存在
	room, err := h.storage.GetRoom(req.RoomID)
	if err != nil || room == nil {
		h.writeError(w, http.StatusNotFound, "聊天室不存在")
		return
	}

	// 验证发送者在聊天室中
	isMember, err := h.storage.IsMemberInRoom(req.RoomID, req.SenderID)
	if err != nil || !isMember {
		h.writeError(w, http.StatusForbidden, "你不是该聊天室的成员")
		return
	}

	// 检查 mention_users 中引用的 agent 是否存在且在聊天室中
	if len(req.MentionUsers) > 0 {
		var invalidAgents []string
		for _, agentID := range req.MentionUsers {
			exists, _ := h.storage.GetAgent(agentID)
			inRoom, _ := h.storage.IsMemberInRoom(req.RoomID, agentID)
			if exists == nil || !inRoom {
				invalidAgents = append(invalidAgents, agentID)
			}
		}
		if len(invalidAgents) > 0 {
			// 返回警告但不阻止消息发送
			slog.Warn("消息引用了不存在的 agent", "invalid_agents", invalidAgents)
			// 异步通知发送者
			go func() {
				h.notifySender(req.SenderID, req.RoomID, "warning",
					fmt.Sprintf("以下 agent 不存在或不在聊天室中: %v", invalidAgents))
			}()
		}
	}

	// 生成消息 ID
	msgID := uuid.New().String()

	// 构建消息
	intent := req.Intent
	if intent == "" {
		intent = "INFORM"
	}
	msg := &Message{
		MsgID:        msgID,
		RoomID:       req.RoomID,
		SenderID:     req.SenderID,
		SenderType:   req.SenderType,
		TargetID:     req.TargetID,
		TargetType:   "BROADCAST",
		MentionUsers: toJSONArray(req.MentionUsers), // 统一存储为 JSON 数组格式
		Content:      req.Content,
		Intent:       intent,
		ReplyToMsgID: req.ReplyToMsgID,
	}

	// 保存消息
	if err := h.storage.SaveMessage(msg); err != nil {
		slog.Error("保存消息失败", "error", err)
		h.writeError(w, http.StatusInternalServerError, "保存消息失败")
		return
	}

	slog.Info("消息已保存", "msg_id", msgID, "room", req.RoomID, "sender", req.SenderID)

	h.writeJSON(w, http.StatusOK, SendMessageResponse{
		Success: true,
		MsgID:   msgID,
	})
}

// ============ User API ============

// RegisterUserRequest 注册用户请求（匹配前端格式）
type RegisterUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
}

// RegisterUserHandler 注册用户
func (h *Handler) RegisterUserHandler(w http.ResponseWriter, r *http.Request) {
	var req RegisterUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的请求体")
		return
	}

	if req.Username == "" {
		h.writeError(w, http.StatusBadRequest, "用户名不能为空")
		return
	}

	// 使用 username 作为 user_id
	userID := req.Username
	nickname := req.Nickname
	if nickname == "" {
		nickname = req.Username
	}

	if err := h.storage.RegisterUser(userID, nickname, ""); err != nil {
		h.writeError(w, http.StatusInternalServerError, fmt.Sprintf("注册用户失败: %v", err))
		return
	}

	slog.Info("用户注册成功", "user_id", userID, "nickname", nickname)

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"user": map[string]interface{}{
			"id":       userID,
			"username": userID,
			"nickname": nickname,
		},
	})
}

// GetUserHandler 获取用户信息
func (h *Handler) GetUserHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		h.writeError(w, http.StatusBadRequest, "user_id 不能为空")
		return
	}

	user, err := h.storage.GetUser(userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "获取用户失败")
		return
	}
	if user == nil {
		h.writeError(w, http.StatusNotFound, "用户不存在")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"user":    user,
	})
}

// LoginRequest 登录请求（匹配前端格式）
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginHandler 用户登录
func (h *Handler) LoginHandler(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的请求体")
		return
	}

	// 支持 username
	loginID := req.Username
	if loginID == "" {
		h.writeError(w, http.StatusBadRequest, "用户名不能为空")
		return
	}

	user, err := h.storage.GetUser(loginID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "登录失败")
		return
	}
	if user == nil {
		// 用户不存在，自动注册，使用登录ID作为 user_id 和 username
		if err := h.storage.RegisterUser(loginID, loginID, ""); err != nil {
			h.writeError(w, http.StatusInternalServerError, "自动注册失败")
			return
		}
		user, _ = h.storage.GetUser(loginID)
	}

	// 更新在线状态
	h.storage.UpdateUserStatus(loginID, "ONLINE")

	// 返回匹配前端期望的格式
	nickname := loginID
	if user != nil && user.Username != "" {
		nickname = user.Username
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"user": map[string]interface{}{
			"id":       loginID,
			"username": loginID,
			"nickname": nickname,
		},
	})
}

// ============ Room API ============

// CreateRoomHandler 创建聊天室
func (h *Handler) CreateRoomHandler(w http.ResponseWriter, r *http.Request) {
	var req CreateRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的请求体")
		return
	}

	// 生成 room_id
	roomID := fmt.Sprintf("room_%d", time.Now().UnixNano())

	// 创建聊天室
	if err := h.storage.CreateRoom(roomID, req.Name, req.Description, req.CreatedBy); err != nil {
		h.writeError(w, http.StatusInternalServerError, fmt.Sprintf("创建聊天室失败: %v", err))
		return
	}

	// 添加初始成员
	for _, memberID := range req.Members {
		memberType := "agent"
		if strings.HasPrefix(memberID, "user_") {
			memberType = "user"
		}
		h.storage.AddMember(roomID, memberID, memberType)
	}

	slog.Info("聊天室创建成功", "room_id", roomID, "name", req.Name)

	h.writeJSON(w, http.StatusOK, CreateRoomResponse{
		Success: true,
		RoomID:  roomID,
	})
}

// GetRoomsHandler 获取聊天室列表
func (h *Handler) GetRoomsHandler(w http.ResponseWriter, r *http.Request) {
	rooms, err := h.storage.GetAllRooms()
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "获取聊天室列表失败")
		return
	}

	h.writeJSON(w, http.StatusOK, GetRoomsResponse{
		Success: true,
		Rooms:   rooms,
	})
}

// JoinRoomHandler 加入聊天室
// 逻辑：
// 1. 检查用户是否已有 ws_established=true 的会话 -> 返回错误
// 2. 如果有 ws_established=false 的会话 -> 复用，更新 connection_id
// 3. 如果没有 -> 创建新会话
// 4. 返回 session_id，前端用此建立 WS 连接
func (h *Handler) JoinRoomHandler(w http.ResponseWriter, r *http.Request) {
	var req JoinRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的请求体")
		return
	}

	// 验证聊天室存在
	room, err := h.storage.GetRoom(req.RoomID)
	if err != nil || room == nil {
		h.writeError(w, http.StatusNotFound, "聊天室不存在")
		return
	}

	var sessionID int64
	var isReused bool

	// 只对 user 类型创建 user_room_sessions 记录
	if req.MemberType == "user" {
		// 生成临时 connection_id（WS 连接建立时会替换）
		tempConnectionID := fmt.Sprintf("pending_%s_%d", req.MemberID, time.Now().UnixNano())

		// 检查并创建会话（支持 ws 未建立时的复用）
		sessionID, isReused, err = h.storage.CheckAndCreateUserRoomSession(req.MemberID, req.RoomID, tempConnectionID)
		if err != nil {
			h.writeError(w, http.StatusConflict, err.Error())
			return
		}
	}

	// 添加成员
	if err := h.storage.AddMember(req.RoomID, req.MemberID, req.MemberType); err != nil {
		// 只对 user 类型清理会话记录
		if req.MemberType == "user" {
			h.storage.DeleteUserRoomSession(req.MemberID, req.RoomID)
		}
		h.writeError(w, http.StatusInternalServerError, fmt.Sprintf("加入聊天室失败: %v", err))
		return
	}

	// 获取历史消息
	history, _ := h.storage.GetRecentMessages(req.RoomID, 50)

	slog.Info("成员加入聊天室", "member_id", req.MemberID, "room_id", req.RoomID, "member_type", req.MemberType, "session_id", sessionID, "reused", isReused)

	h.writeJSON(w, http.StatusOK, JoinRoomResponse{
		Success:   true,
		Room:      room,
		History:   history,
		SessionID: sessionID,
	})
}

// LeaveRoomHandler 离开聊天室 (DELETE 方法)
func (h *Handler) LeaveRoomHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	roomID := vars["room_id"]
	memberID := vars["member_id"]

	if err := h.storage.RemoveMember(roomID, memberID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "离开聊天室失败")
		return
	}

	slog.Info("成员离开聊天室", "room_id", roomID, "member_id", memberID)

	h.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// LeaveRoomRequest 离开聊天室请求 (POST 方法)
type LeaveRoomRequest struct {
	RoomID   string `json:"room_id"`
	MemberID string `json:"member_id"`
}

// LeaveRoomPOSTHandler 离开聊天室 (POST 方法)
func (h *Handler) LeaveRoomPOSTHandler(w http.ResponseWriter, r *http.Request) {
	var req LeaveRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的请求体")
		return
	}

	if err := h.storage.RemoveMember(req.RoomID, req.MemberID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "离开聊天室失败")
		return
	}

	slog.Info("成员离开聊天室", "room_id", req.RoomID, "member_id", req.MemberID)

	h.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// HistoryRequest 获取历史消息请求
type HistoryRequest struct {
	RoomID string `form:"room_id"`
	Count  int    `form:"count"`
}

// GetHistoryHandler 获取聊天室历史消息
func (h *Handler) GetHistoryHandler(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("room_id")
	if roomID == "" {
		h.writeError(w, http.StatusBadRequest, "room_id 不能为空")
		return
	}

	var count int = 50
	if c := r.URL.Query().Get("count"); c != "" {
		fmt.Sscanf(c, "%d", &count)
	}

	messages, err := h.storage.GetRecentMessages(roomID, count)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "获取历史消息失败")
		return
	}

	// 转换消息格式
	type HistoryMessage struct {
		MsgID     string `json:"msg_id"`
		Sender    string `json:"sender"`
		Content   string `json:"content"`
		Intent    string `json:"intent"`
		CreatedAt int64  `json:"created_at"`
	}

	history := make([]HistoryMessage, len(messages))
	for i, msg := range messages {
		history[i] = HistoryMessage{
			MsgID:     msg.MsgID,
			Sender:    msg.SenderID,
			Content:   msg.Content,
			Intent:    msg.Intent,
			CreatedAt: msg.CreatedAt.Unix(),
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"messages": history,
	})
}

// GetRoomMembersByQueryHandler 获取聊天室成员（查询参数版本）
func (h *Handler) GetRoomMembersByQueryHandler(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("room_id")
	if roomID == "" {
		h.writeError(w, http.StatusBadRequest, "room_id 不能为空")
		return
	}

	members, err := h.storage.GetRoomMembers(roomID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "获取成员列表失败")
		return
	}

	h.writeJSON(w, http.StatusOK, GetRoomMembersResponse{
		Success: true,
		Members: members,
	})
}

// GetRoomMembersHandler 获取聊天室成员（路径参数版本）
func (h *Handler) GetRoomMembersHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	roomID := vars["room_id"]

	members, err := h.storage.GetRoomMembers(roomID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "获取成员列表失败")
		return
	}

	h.writeJSON(w, http.StatusOK, GetRoomMembersResponse{
		Success: true,
		Members: members,
	})
}

// ============ User WebSocket ============

// ChatWSHandler 聊天室 WebSocket 连接（支持 /ws/chat）
func (h *Handler) ChatWSHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	roomID := r.URL.Query().Get("room_id")
	sessionIDStr := r.URL.Query().Get("session_id")

	if userID == "" {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}

	h.handleWebSocket(w, r, userID, roomID, sessionIDStr)
}

// UserWSHandler 用户 WebSocket 连接
func (h *Handler) UserWSHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	roomID := r.URL.Query().Get("room_id")
	sessionIDStr := r.URL.Query().Get("session_id")

	slog.Debug("ChatWSHandler 收到请求", "url", r.URL.String(), "user_id", userID, "room_id", roomID, "session_id", sessionIDStr)

	if userID == "" {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}

	h.handleWebSocket(w, r, userID, roomID, sessionIDStr)
}

// handleWebSocket 通用 WebSocket 处理
// sessionID 用于验证 WS 连接是否合法
func (h *Handler) handleWebSocket(w http.ResponseWriter, r *http.Request, userID, roomID, sessionIDStr string) {
	// 升级为 WebSocket
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WebSocket 升级失败", "error", err)
		return
	}

	// 生成唯一的连接ID
	connectionID := uuid.New().String()

	userConn := &UserConn{
		UserID:       userID,
		ConnectionID: connectionID,
		Conn:         conn,
		Send:         make(chan []byte, 256),
		Rooms:        make(map[string]bool),
		CloseChan:    make(chan struct{}),
	}

	// 确保资源正确清理的 defer 函数
	defer func() {
		slog.Info("WebSocket 连接已关闭", "user_id", userID, "connection_id", connectionID)
		// 关闭通道，通知协程停止
		close(userConn.CloseChan)
		// 等待协程结束（简单的等待方式，防止资源泄漏）
		time.Sleep(100 * time.Millisecond)
		// 清理连接
		userConn.Conn.Close()
		// 从内存中删除连接
		h.userConnsMu.Lock()
		delete(h.userConns, connectionID)
		h.userConnsMu.Unlock()
		// 从数据库删除连接对应的会话
		h.storage.DeleteUserRoomSessionByConnection(connectionID)
	}()

	// 如果指定了 room_id 和 session_id，验证并更新会话状态
	if roomID != "" && sessionIDStr != "" {
		slog.Debug("WS 收到 session_id", "user_id", userID, "room_id", roomID, "session_id", sessionIDStr)
		// 解析 session_id
		var sessionID int64
		if _, err := fmt.Sscanf(sessionIDStr, "%d", &sessionID); err != nil {
			// session_id 无效，拒绝连接
			slog.Warn("无效的 session_id", "session_id", sessionIDStr)
			h.sendWarningAndClose(conn, roomID, "无效的 session_id，请重新加入聊天室")
			return
		}

		// 更新会话状态为 ws_established=true，并设置 connection_id
		if err := h.storage.UpdateUserRoomSessionWsEstablishedWithConnection(sessionID, connectionID, userID, roomID); err != nil {
			// 会话验证失败，拒绝连接
			slog.Warn("会话验证失败", "session_id", sessionID, "user_id", userID, "room_id", roomID, "error", err)
			h.sendWarningAndClose(conn, roomID, "会话验证失败，请重新加入聊天室")
			return
		}
		userConn.Rooms[roomID] = true
		slog.Info("WS 连接验证成功", "user_id", userID, "room_id", roomID, "session_id", sessionID, "connection_id", connectionID)
	} else {
		slog.Debug("WS 未携带 session_id", "user_id", userID, "room_id", roomID, "session_id", sessionIDStr)
	}

	// 从数据库加载用户已订阅的房间列表（仅恢复 ws_established=TRUE 的订阅）
	// 未携带 session_id 时，需要检查 ws_established 状态
	sessions, err := h.storage.GetUserRoomSessions(userID)
	if err == nil && len(sessions) > 0 {
		validCount := 0
		for _, session := range sessions {
			// 只有 ws_established=TRUE 的会话才能恢复订阅
			if session.WsEstablished {
				userConn.Rooms[session.RoomID] = true
				validCount++
			}
		}
		if validCount > 0 {
			slog.Info("从数据库恢复有效房间订阅", "count", validCount, "user_id", userID)
		}
	}

	// 注册连接（按 connectionID 索引，支持多 Tab）
	h.userConnsMu.Lock()
	h.userConns[connectionID] = userConn
	if h.userRooms[userID] == nil {
		h.userRooms[userID] = make(map[string]bool)
	}
	for rmID := range userConn.Rooms {
		h.userRooms[userID][rmID] = true
	}
	h.userConnsMu.Unlock()

	slog.Info("WebSocket 连接已建立", "user_id", userID, "connection_id", connectionID)

	// 启动读写协程和投递轮询协程
	go h.userWritePump(userConn)
	go h.notificationPump(userConn) // 启动消息投递轮询
	h.userReadPump(userConn)
}

func (h *Handler) userReadPump(conn *UserConn) {
	defer func() {
		// 关闭 WebSocket 连接
		conn.Conn.Close()

		// 立即更新数据库：将 ws_established 设为 FALSE
		if err := h.storage.UpdateUserRoomSessionWsEstablished(conn.ConnectionID, false); err != nil {
			slog.Warn("更新 ws_established 失败", "connection_id", conn.ConnectionID, "error", err)
		} else {
			slog.Info("WebSocket 断开，已更新 ws_established=FALSE", "connection_id", conn.ConnectionID, "user_id", conn.UserID)
		}
	}()

	for {
		_, message, err := conn.Conn.ReadMessage()
		if err != nil {
			break
		}

		// 解析消息
		var rawMsg struct {
			Action string                 `json:"action"`
			Type   string                 `json:"type"`
			Data   map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(message, &rawMsg); err != nil {
			continue
		}

		// 提取字段的辅助函数
		getString := func(key string) string {
			if v, ok := rawMsg.Data[key].(string); ok {
				return v
			}
			return ""
		}

		// 获取聊天室 ID（支持多种格式）
		roomID := getString("channelId")
		if roomID == "" {
			roomID = getString("room_id")
		}

		// 确定消息类型（支持 action 和 type 两种格式）
		msgType := rawMsg.Type
		if msgType == "" {
			msgType = rawMsg.Action
		}

		switch msgType {
		case "join":
			// 加入聊天室（WS 连接已通过 session_id 验证）
			// 这里只需要更新内存状态
			if roomID != "" {
				conn.RoomsMu.Lock()
				conn.Rooms[roomID] = true
				conn.RoomsMu.Unlock()

				h.userRoomsMu.Lock()
				if h.userRooms[conn.UserID] == nil {
					h.userRooms[conn.UserID] = make(map[string]bool)
				}
				h.userRooms[conn.UserID][roomID] = true
				h.userRoomsMu.Unlock()

				slog.Info("WebSocket 用户加入聊天室", "user_id", conn.UserID, "room_id", roomID)
			}

		case "leave":
			// 离开聊天室
			if roomID != "" {
				conn.RoomsMu.Lock()
				delete(conn.Rooms, roomID)
				conn.RoomsMu.Unlock()

				h.userRoomsMu.Lock()
				delete(h.userRooms[conn.UserID], roomID)
				h.userRoomsMu.Unlock()

				// 从数据库删除会话
				h.storage.DeleteUserRoomSession(conn.UserID, roomID)

				slog.Info("WebSocket 用户离开聊天室", "room_id", roomID, "user_id", conn.UserID)
			}

		case "message":
			// 用户发送消息（通过 WebSocket）
			// 实际上用户消息应该通过 HTTP API 发送，这里可以忽略或转发
		}

		// 处理 speak 动作（前端发送消息）
		if rawMsg.Action == "speak" {
			h.handleSpeakMessage(conn, rawMsg.Data, conn.UserID)
		}
	}
}

// handleSpeakMessage 处理用户通过 WebSocket 发送的消息
func (h *Handler) handleSpeakMessage(conn *UserConn, data map[string]interface{}, defaultUserID string) {
	// 提取字段的辅助函数
	getString := func(key string) string {
		if v, ok := data[key].(string); ok {
			return v
		}
		return ""
	}
	getStringArray := func(key string) []string {
		if v, ok := data[key].([]interface{}); ok {
			result := make([]string, len(v))
			for i, item := range v {
				if s, ok := item.(string); ok {
					result[i] = s
				}
			}
			return result
		}
		return nil
	}

	// 获取聊天室 ID
	roomID := getString("channelId")
	if roomID == "" {
		roomID = getString("room_id")
	}

	// 获取消息内容
	content := getString("contentText")
	if content == "" {
		content = getString("content")
	}

	sender := getString("sender")
	if sender == "" {
		sender = defaultUserID
	}

	target := getString("target")
	if target == "" {
		target = "ALL"
	}

	intent := getString("intent")
	if intent == "" {
		intent = "INFORM"
	}

	mentionUsers := getStringArray("mentionUsers")

	if roomID == "" || content == "" {
		slog.Warn("speak 消息缺少必要参数", "room_id", roomID, "content", content)
		return
	}

	// 验证聊天室存在
	room, err := h.storage.GetRoom(roomID)
	if err != nil || room == nil {
		slog.Warn("speak 消息聊天室不存在", "room_id", roomID)
		return
	}

	// 验证发送者是聊天室成员
	isMember, err := h.storage.IsMemberInRoom(roomID, sender)
	if err != nil || !isMember {
		// 如果 sender 不在成员列表中，尝试使用 conn.UserID
		isMember, _ = h.storage.IsMemberInRoom(roomID, conn.UserID)
		if !isMember {
			slog.Warn("发送者不是聊天室成员", "sender", sender, "room_id", roomID)
			return
		}
		sender = conn.UserID
	}

	// 检查 mention_users 中引用的 agent 是否存在且在聊天室中
	if len(mentionUsers) > 0 {
		var invalidAgents []string
		for _, agentID := range mentionUsers {
			exists, _ := h.storage.GetAgent(agentID)
			inRoom, _ := h.storage.IsMemberInRoom(roomID, agentID)
			if exists == nil || !inRoom {
				invalidAgents = append(invalidAgents, agentID)
			}
		}
		if len(invalidAgents) > 0 {
			// 异步通知发送者
			go func() {
				h.notifySender(conn.UserID, roomID, "warning",
					fmt.Sprintf("以下 agent 不存在或不在聊天室中: %v", invalidAgents))
			}()
		}
	}

	// 生成消息 ID
	msgID := getString("msgId")
	if msgID == "" {
		msgID = uuid.New().String()
	}

	wsMsg := &Message{
		MsgID:        msgID,
		RoomID:       roomID,
		SenderID:     sender,
		SenderType:   "user",
		TargetID:     target,
		TargetType:   "BROADCAST",
		MentionUsers: toJSONArray(mentionUsers), // 统一存储为 JSON 数组格式
		Content:      content,
		Intent:       intent,
	}

	// 保存消息
	if err := h.storage.SaveMessage(wsMsg); err != nil {
		slog.Error("通过 WebSocket 保存消息失败", "error", err)
		return
	}

	slog.Info("WebSocket 消息已保存", "msg_id", msgID, "room", roomID, "sender", sender)
}

func (h *Handler) userWritePump(conn *UserConn) {
	defer func() {
		slog.Debug("userWritePump: 协程结束", "user_id", conn.UserID)
		conn.Conn.Close()
		// 确保通道只关闭一次，避免 panic
		defer func() {
			recover()
		}()
		close(conn.CloseChan) // 通知 notificationPump 协程停止
	}()

	for {
		select {
		case message, ok := <-conn.Send:
			if !ok {
				slog.Debug("userWritePump: 发送通道已关闭", "user_id", conn.UserID)
				return
			}

			slog.Debug("userWritePump: 准备发送消息到 WebSocket", "user_id", conn.UserID, "len", len(message))
			if err := conn.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				slog.Error("userWritePump: 发送消息到 WebSocket 失败", "user_id", conn.UserID, "error", err)

				// 发送失败，不立即返回，继续尝试发送下一条消息
				continue
			}
			slog.Debug("userWritePump: 消息发送成功", "user_id", conn.UserID)

		case <-conn.CloseChan:
			slog.Debug("userWritePump: 收到关闭信号", "user_id", conn.UserID)
			return
		}
	}
}

func (h *Handler) messageToWSData(msg *Message) map[string]interface{} {
	mentionUsers := []string{}
	if msg.MentionUsers != "" {
		json.Unmarshal([]byte(msg.MentionUsers), &mentionUsers)
	}

	return map[string]interface{}{
		"msgId":        msg.MsgID,
		"roomId":       msg.RoomID,
		"channelId":    msg.RoomID,
		"senderId":     msg.SenderID,
		"sender":       msg.SenderID,
		"senderType":   msg.SenderType,
		"content":      msg.Content,
		"contentText":  msg.Content,
		"mentionUsers": mentionUsers,
		"intent":       msg.Intent,
		"replyToMsgId": msg.ReplyToMsgID,
		"createdAt":    msg.CreatedAt.Unix(),
	}
}

// cleanupUserConnections 定期清理断开的连接
// 使用非阻塞 Ping 检测，避免阻塞主循环
func (h *Handler) cleanupUserConnections() {
	ticker := time.NewTicker(h.cfg.GetCleanupInterval())
	defer ticker.Stop()

	for range ticker.C {
		// 先收集所有连接信息，不加锁
		h.userConnsMu.RLock()
		connInfos := make([]struct {
			connectionID string
			userID       string
			conn         *websocket.Conn
		}, 0, len(h.userConns))
		for connectionID, conn := range h.userConns {
			connInfos = append(connInfos, struct {
				connectionID string
				userID       string
				conn         *websocket.Conn
			}{connectionID: connectionID, userID: conn.UserID, conn: conn.Conn})
		}
		h.userConnsMu.RUnlock()

		// 使用 SetWriteDeadline 进行非阻塞 Ping 检测
		pingTimeout := h.cfg.GetPingTimeout()
		var closedConnIDs []string
		for _, info := range connInfos {
			// 设置 Ping 超时，避免阻塞
			info.conn.SetWriteDeadline(time.Now().Add(pingTimeout))
			err := info.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(pingTimeout))
			if err != nil {
				// 连接已断开，收集ID
				closedConnIDs = append(closedConnIDs, info.connectionID)
			} else {
				// 重置为永久截止时间
				info.conn.SetWriteDeadline(time.Time{})
			}
		}

		if len(closedConnIDs) > 0 {
			// 从内存中删除断开的连接
			h.userConnsMu.Lock()
			for _, connectionID := range closedConnIDs {
				if conn, ok := h.userConns[connectionID]; ok {
					delete(h.userConns, connectionID)
					// 安全关闭 channel（如果已经关闭会 panic）
					defer func() {
						recover()
					}()
					close(conn.Send)
					close(conn.CloseChan)
					slog.Info("清理断开用户连接", "connection_id", connectionID, "user_id", conn.UserID)
				}
			}
			h.userConnsMu.Unlock()

			// 从数据库清理断开的会话
			for _, connectionID := range closedConnIDs {
				h.storage.DeleteUserRoomSessionByConnection(connectionID)
			}
		}
	}
}

// ============ 辅助方法 ============

func (h *Handler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{
		Error: message,
		Code:  status,
	})
}

// notificationPump 消息投递轮询协程
// 每个 WebSocket 连接都会启动一个独立的协程来轮询该用户的消息
// 这样可以天然支持分布式：每个实例只轮询自己连接的用户
func (h *Handler) notificationPump(conn *UserConn) {
	// log.Printf("[DEBUG] 启动消息投递轮询: userID=%s", conn.UserID)

	pollInterval := h.cfg.GetPollInterval()                 // 消息轮询间隔
	memberStatusInterval := h.cfg.GetMemberStatusInterval() // 成员状态推送间隔
	messageSendTimeout := h.cfg.GetMessageSendTimeout()     // 消息发送超时
	lastPolled := time.Now()
	lastMemberStatusUpdate := time.Now()
	pollCounter := 0

	for {
		select {
		case <-conn.CloseChan:
			// 连接关闭信号
			// log.Printf("停止消息投递轮询: userID=%s", conn.UserID)
			return

		case <-time.After(pollInterval):
			// 轮询待通知的消息
			notifications, err := h.storage.GetPendingNotifications(conn.UserID, lastPolled)
			if err != nil {
				slog.Warn("GetPendingNotifications 失败", "user_id", conn.UserID, "error", err)
				continue
			}

			if len(notifications) > 0 {
				slog.Info("发现待通知消息", "count", len(notifications), "user_id", conn.UserID)

				var notifiedMsgIDs []string
				for _, n := range notifications {
					// 构造 WebSocket 消息（统一使用驼峰命名）
					wsMsg := map[string]interface{}{
						"type": "message",
						"data": map[string]interface{}{
							"msgId":        n.MsgID,
							"roomId":       n.RoomID,
							"channelId":    n.RoomID,
							"senderId":     n.SenderID,
							"sender":       n.SenderID,
							"senderType":   n.SenderType,
							"content":      n.Content,
							"contentText":  n.Content,
							"mentionUsers": n.MentionUsers,
							"intent":       n.Intent,
							"replyToMsgId": n.ReplyToMsgID,
							"isMentioned":  n.IsMentioned,
							"createdAt":    n.CreatedAt.Format("2006-01-02 15:04:05"),
						},
					}

					data, _ := json.Marshal(wsMsg)

					// 发送到 WebSocket，添加超时机制
					select {
					case conn.Send <- data:
						slog.Debug("已发送通知消息", "user_id", conn.UserID, "msg_id", n.MsgID, "sender", n.SenderID)
						notifiedMsgIDs = append(notifiedMsgIDs, n.MsgID)
					case <-time.After(messageSendTimeout):
						slog.Warn("发送消息超时", "user_id", conn.UserID, "msg_id", n.MsgID)
					default:
						slog.Warn("消息缓冲区满，跳过", "user_id", conn.UserID)
					}
				}

				// 标记消息已通知
				if len(notifiedMsgIDs) > 0 {
					if err := h.storage.MarkNotificationsSent(notifiedMsgIDs, conn.UserID); err != nil {
						slog.Warn("MarkNotificationsSent 失败", "user_id", conn.UserID, "error", err)
					}
				}

				lastPolled = time.Now()
			}

			// 定期推送成员在线状态（不存入 messages 表）
			pollCounter++
			if time.Since(lastMemberStatusUpdate) >= memberStatusInterval {
				lastMemberStatusUpdate = time.Now()
				// 获取该连接订阅的所有聊天室
				conn.RoomsMu.RLock()
				rooms := make([]string, 0, len(conn.Rooms))
				for roomID := range conn.Rooms {
					rooms = append(rooms, roomID)
				}
				conn.RoomsMu.RUnlock()

				// 为每个聊天室推送成员状态
				for _, roomID := range rooms {
					h.pushMemberStatus(conn, roomID)
				}
			}
		}
	}
}

// pushMemberStatus 推送聊天室成员在线状态（通过 WebSocket，不存入 messages 表）
func (h *Handler) pushMemberStatus(conn *UserConn, roomID string) {
	// 获取聊天室成员列表（包含 agent_status 和 ws_established）
	members, err := h.storage.GetRoomMembers(roomID)
	if err != nil {
		slog.Warn("获取成员列表失败", "room_id", roomID, "error", err)
		return
	}

	// 构造成员状态消息（统一使用驼峰命名）
	memberList := make([]map[string]interface{}, 0, len(members))
	for _, m := range members {
		memberList = append(memberList, map[string]interface{}{
			"memberId":      m.MemberID,
			"memberType":    m.MemberType,
			"agentStatus":   m.AgentStatus,
			"isActive":      m.IsActive,
			"wsEstablished": m.WsEstablished,
			"joinedAt":      m.JoinedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	wsMsg := map[string]interface{}{
		"type": "member_status",
		"data": map[string]interface{}{
			"roomId":  roomID,
			"members": memberList,
		},
	}

	data, _ := json.Marshal(wsMsg)

	// 发送到 WebSocket
	select {
	case conn.Send <- data:
		// log.Printf("[DEBUG] 已推送成员状态: roomID=%s, members=%d", roomID, len(members))
	default:
		// log.Printf("[WARN] 成员状态推送失败，缓冲区满: roomID=%s", roomID)
	}
}

// notifySender 发送通知给特定用户（支持多 Tab）
func (h *Handler) notifySender(userID, roomID, msgType, content string) {
	wsMsg := map[string]interface{}{
		"type": msgType, // "warning", "error", "info" 等
		"data": map[string]interface{}{
			"content": content,
			"roomId":  roomID,
		},
	}

	data, _ := json.Marshal(wsMsg)

	// 查找该用户的所有连接（支持多 Tab）
	h.userConnsMu.RLock()
	var userConns []*UserConn
	for _, conn := range h.userConns {
		if conn.UserID == userID {
			userConns = append(userConns, conn)
		}
	}
	h.userConnsMu.RUnlock()

	if len(userConns) == 0 {
		slog.Warn("无法通知用户：没有活跃连接", "user_id", userID)
		return
	}

	// 发送给该用户的所有连接
	for _, conn := range userConns {
		select {
		case conn.Send <- data:
			slog.Info("已通知用户", "user_id", userID, "connection_id", conn.ConnectionID, "content", content)
		default:
			slog.Warn("无法通知用户：缓冲区满", "user_id", userID, "connection_id", conn.ConnectionID)
		}
	}
}

// sendWarning 发送警告消息给特定连接
func (h *Handler) sendWarning(conn *websocket.Conn, roomID, content string) {
	wsMsg := map[string]interface{}{
		"type": "warning",
		"data": map[string]interface{}{
			"content": content,
			"roomId":  roomID,
		},
	}

	data, _ := json.Marshal(wsMsg)
	conn.WriteMessage(websocket.TextMessage, data)
}

// sendWarningAndCloseConn 发送警告消息后关闭 UserConn 连接
func (h *Handler) sendWarningAndCloseConn(userConn *UserConn, roomID, content string) {
	wsMsg := map[string]interface{}{
		"type": "warning",
		"data": map[string]interface{}{
			"content": content,
			"roomId":  roomID,
		},
	}

	data, _ := json.Marshal(wsMsg)
	userConn.Conn.WriteMessage(websocket.TextMessage, data)

	// 关闭连接
	userConn.Conn.Close()
	close(userConn.Send)
	close(userConn.CloseChan)

	// 从内存中移除连接
	h.userConnsMu.Lock()
	delete(h.userConns, userConn.ConnectionID)
	h.userConnsMu.Unlock()

	// 从数据库删除会话
	h.storage.DeleteUserRoomSessionByConnection(userConn.ConnectionID)

	slog.Info("已发送警告并关闭连接", "connection_id", userConn.ConnectionID, "user_id", userConn.UserID, "content", content)
}

// sendWarningAndClose 发送警告消息后关闭连接
func (h *Handler) sendWarningAndClose(conn *websocket.Conn, roomID, content string) {
	wsMsg := map[string]interface{}{
		"type": "warning",
		"data": map[string]interface{}{
			"content": content,
			"roomId":  roomID,
		},
	}

	data, _ := json.Marshal(wsMsg)
	conn.WriteMessage(websocket.TextMessage, data)

	// 关闭连接
	conn.Close()
	slog.Info("已发送警告并关闭连接", "content", content)
}

// toJSONArray 将字符串切片转换为 JSON 数组格式字符串
// 确保 mention_users 字段在数据库中统一存储为 JSON 数组格式
func toJSONArray(arr []string) string {
	if arr == nil || len(arr) == 0 {
		return "[]"
	}
	data, err := json.Marshal(arr)
	if err != nil {
		return "[]"
	}
	return string(data)
}
