package main

import (
	"encoding/json"
	"fmt"
	"log"
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
		log.Printf("[ERROR] 注册 Agent 失败: %v", err)
		h.writeError(w, http.StatusInternalServerError, "注册失败")
		return
	}

	log.Printf("[INFO] Agent 注册成功: %s, endpoint: %s", req.AgentID, req.Endpoint)

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
		log.Printf("[WARN] 更新心跳失败: %s, error: %v", req.AgentID, err)
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
		log.Printf("[ERROR] 轮询消息失败: %v", err)
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
			log.Printf("[WARN] 消息引用了不存在的 agent: %v", invalidAgents)
			// 异步通知发送者
			go func() {
				h.notifySender(req.SenderID, req.RoomID, "warning",
					fmt.Sprintf("以下 agent 不存在或不在聊天室中: %v", invalidAgents))
			}()
		}
	}

	// 生成消息 ID
	msgID := uuid.New().String()

	// 尝试获取发言锁（非 @ 消息且非 agent 发送需要锁）
	// Agent 发送消息不需要发言锁，因为它们是被 @ 后必须回复的
	needsLock := len(req.MentionUsers) == 0 && req.SenderType != "agent"
	if needsLock {
		acquired, err := h.storage.TryAcquireLock(req.RoomID, req.SenderID, req.SenderType)
		if err != nil || !acquired {
			currentSpeaker, _ := h.storage.GetCurrentSpeaker(req.RoomID)
			h.writeError(w, http.StatusConflict, fmt.Sprintf("当前发言者: %s，请稍后重试", currentSpeaker))
			return
		}
		// 延迟释放锁
		go func() {
			time.Sleep(time.Duration(h.cfg.SpeakerLockTimeout) * time.Millisecond)
			h.storage.ReleaseLock(req.RoomID, req.SenderID)
		}()
	}

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
		log.Printf("[ERROR] 保存消息失败: %v", err)
		h.writeError(w, http.StatusInternalServerError, "保存消息失败")
		return
	}

	log.Printf("[INFO] 消息已保存: %s, room: %s, sender: %s", msgID, req.RoomID, req.SenderID)

	// 禁用旧的广播机制，改用 notificationPump 轮询推送
	// go h.broadcastToUsers(req.RoomID, msg)

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

	log.Printf("[INFO] 用户注册成功: %s (nickname: %s)", userID, nickname)

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

	log.Printf("[INFO] 聊天室创建成功: %s, name: %s", roomID, req.Name)

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

	log.Printf("[INFO] 成员加入聊天室: %s -> %s (%s), sessionID=%d, reused=%v", req.MemberID, req.RoomID, req.MemberType, sessionID, isReused)

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

	log.Printf("[INFO] 成员离开聊天室: %s <- %s", roomID, memberID)

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

	log.Printf("[INFO] 成员离开聊天室: %s <- %s", req.RoomID, req.MemberID)

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

	log.Printf("[DEBUG] ChatWSHandler 收到请求: URL=%s, userID=%s, roomID=%s, sessionID=%s", r.URL.String(), userID, roomID, sessionIDStr)

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

	log.Printf("[DEBUG] UserWSHandler 收到请求: URL=%s, userID=%s, roomID=%s, sessionID=%s", r.URL.String(), userID, roomID, sessionIDStr)

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
		log.Printf("[ERROR] WebSocket 升级失败: %v", err)
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
		log.Printf("[INFO] WebSocket 连接已关闭: userID=%s, connectionID=%s", userID, connectionID)
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
		log.Printf("[DEBUG] WS 收到 session_id: userID=%s, roomID=%s, sessionIDStr=%s", userID, roomID, sessionIDStr)
		// 解析 session_id
		var sessionID int64
		if _, err := fmt.Sscanf(sessionIDStr, "%d", &sessionID); err != nil {
			// session_id 无效，拒绝连接
			log.Printf("[WARN] 无效的 session_id: %s", sessionIDStr)
			h.sendWarningAndClose(conn, roomID, "无效的 session_id，请重新加入聊天室")
			return
		}

		// 更新会话状态为 ws_established=true，并设置 connection_id
		if err := h.storage.UpdateUserRoomSessionWsEstablishedWithConnection(sessionID, connectionID, userID, roomID); err != nil {
			// 会话验证失败，拒绝连接
			log.Printf("[WARN] 会话验证失败: sessionID=%d, userID=%s, roomID=%s, err=%v", sessionID, userID, roomID, err)
			h.sendWarningAndClose(conn, roomID, "会话验证失败，请重新加入聊天室")
			return
		}
		userConn.Rooms[roomID] = true
		log.Printf("[INFO] WS 连接验证成功: userID=%s, roomID=%s, sessionID=%d, connectionID=%s", userID, roomID, sessionID, connectionID)
	} else {
		log.Printf("[DEBUG] WS 未携带 session_id: userID=%s, roomID=%s, sessionIDStr=%s", userID, roomID, sessionIDStr)
	}

	// 从数据库加载用户已订阅的房间列表（用于恢复之前的订阅）
	sessions, err := h.storage.GetUserRoomSessions(userID)
	if err == nil && len(sessions) > 0 {
		for _, session := range sessions {
			userConn.Rooms[session.RoomID] = true
		}
		log.Printf("[INFO] 从数据库恢复 %d 个房间订阅: userID=%s", len(sessions), userID)
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

	log.Printf("[INFO] WebSocket 连接已建立: userID=%s, connectionID=%s", userID, connectionID)

	// 启动读写协程和投递轮询协程
	go h.userWritePump(userConn)
	go h.notificationPump(userConn) // 启动消息投递轮询
	h.userReadPump(userConn)
}

func (h *Handler) userReadPump(conn *UserConn) {
	defer func() {
		// 只关闭 WebSocket 连接，channel 关闭操作在 handleWebSocket 函数中统一处理
		conn.Conn.Close()
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

				log.Printf("[INFO] WebSocket 用户加入聊天室: %s -> %s", conn.UserID, roomID)
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

				log.Printf("[INFO] WebSocket 用户离开聊天室: %s <- %s", roomID, conn.UserID)
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
		log.Printf("[WARN] speak 消息缺少必要参数: roomID=%s, content=%s", roomID, content)
		return
	}

	// 验证聊天室存在
	room, err := h.storage.GetRoom(roomID)
	if err != nil || room == nil {
		log.Printf("[WARN] speak 消息聊天室不存在: %s", roomID)
		return
	}

	// 验证发送者是聊天室成员
	isMember, err := h.storage.IsMemberInRoom(roomID, sender)
	if err != nil || !isMember {
		// 如果 sender 不在成员列表中，尝试使用 conn.UserID
		isMember, _ = h.storage.IsMemberInRoom(roomID, conn.UserID)
		if !isMember {
			log.Printf("[WARN] 发送者不是聊天室成员: %s, room: %s", sender, roomID)
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

	// 尝试获取发言锁（非 @ 消息需要锁）
	if len(mentionUsers) == 0 {
		acquired, err := h.storage.TryAcquireLock(roomID, sender, "user")
		if err != nil || !acquired {
			currentSpeaker, _ := h.storage.GetCurrentSpeaker(roomID)
			log.Printf("[WARN] 当前发言者: %s，请稍后重试", currentSpeaker)
			return
		}
		// 延迟释放锁
		go func() {
			time.Sleep(time.Duration(h.cfg.SpeakerLockTimeout) * time.Millisecond)
			h.storage.ReleaseLock(roomID, sender)
		}()
	}

	// 构建消息
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
		log.Printf("[ERROR] 通过 WebSocket 保存消息失败: %v", err)
		return
	}

	log.Printf("[INFO] WebSocket 消息已保存: %s, room: %s, sender: %s", msgID, roomID, sender)

	// 禁用旧的广播机制，改用 notificationPump 轮询推送
	// go h.broadcastToUsers(roomID, wsMsg)
}

func (h *Handler) userWritePump(conn *UserConn) {
	defer func() {
		log.Printf("[DEBUG] userWritePump: 协程结束: userID=%s", conn.UserID)
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
				log.Printf("[DEBUG] userWritePump: 发送通道已关闭: userID=%s", conn.UserID)
				return
			}

			log.Printf("[DEBUG] userWritePump: 准备发送消息到 WebSocket: userID=%s, 消息长度=%d", conn.UserID, len(message))
			if err := conn.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				log.Printf("[ERROR] userWritePump: 发送消息到 WebSocket 失败: userID=%s, err=%v", conn.UserID, err)

				// 发送失败，不立即返回，继续尝试发送下一条消息
				continue
			}
			log.Printf("[DEBUG] userWritePump: 消息发送成功: userID=%s", conn.UserID)

		case <-conn.CloseChan:
			log.Printf("[DEBUG] userWritePump: 收到关闭信号: userID=%s", conn.UserID)
			return
		}
	}
}

// broadcastToUsers 广播消息给聊天室的所有在线用户
func (h *Handler) broadcastToUsers(roomID string, msg *Message) {
	wsMsg := WSMessage{
		Type: "message",
		Data: h.messageToWSData(msg),
	}
	data, _ := json.Marshal(wsMsg)

	// log.Printf("[DEBUG] 开始广播消息: room=%s, sender=%s, content=%.30s", roomID, msg.SenderID, msg.Content)

	h.userConnsMu.RLock()
	defer h.userConnsMu.RUnlock()

	// log.Printf("[DEBUG] 当前在线用户数: %d", len(h.userConns))

	sentCount := 0
	for userID, conn := range h.userConns {
		conn.RoomsMu.RLock()
		isInRoom := conn.Rooms[roomID]
		conn.RoomsMu.RUnlock()

		// log.Printf("[DEBUG] 检查用户 %s: 在房间中=%v, 订阅房间数=%d", userID, isInRoom, roomCount)

		if isInRoom {
			select {
			case conn.Send <- data:
				sentCount++
				// log.Printf("[DEBUG] 已发送消息给用户: %s", userID)
			default:
				// 连接缓冲区满，跳过
				log.Printf("[WARN] User %s 消息缓冲区满，跳过", userID)
			}
		}
	}

	// log.Printf("[DEBUG] 广播完成: 发送了 %d 个用户", sentCount)
}

func (h *Handler) messageToWSData(msg *Message) map[string]interface{} {
	mentionUsers := []string{}
	if msg.MentionUsers != "" {
		json.Unmarshal([]byte(msg.MentionUsers), &mentionUsers)
	}

	return map[string]interface{}{
		"msg_id":          msg.MsgID,
		"room_id":         msg.RoomID,
		"channelId":       msg.RoomID, // 兼容前端
		"sender_id":       msg.SenderID,
		"sender":          msg.SenderID, // 兼容前端
		"sender_type":     msg.SenderType,
		"content":         msg.Content,
		"contentText":     msg.Content, // 兼容前端
		"mention_users":   mentionUsers,
		"mentionUsers":    mentionUsers, // 兼容前端
		"intent":          msg.Intent,
		"reply_to_msg_id": msg.ReplyToMsgID,
		"created_at":      msg.CreatedAt.Unix(),
	}
}

// cleanupUserConnections 定期清理断开的连接
func (h *Handler) cleanupUserConnections() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		h.userConnsMu.Lock()
		var closedConnIDs []string
		for connectionID, conn := range h.userConns {
			// 使用 Ping 方法检查连接是否已关闭，避免阻塞
			err := conn.Conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(1*time.Second))
			if err != nil {
				delete(h.userConns, connectionID)
				closedConnIDs = append(closedConnIDs, connectionID)
				// 安全关闭 channel（如果已经关闭会 panic）
				defer func() {
					recover()
				}()
				close(conn.Send)
				close(conn.CloseChan)
				log.Printf("[INFO] 清理断开用户连接: connectionID=%s, userID=%s", connectionID, conn.UserID)
			}
		}
		h.userConnsMu.Unlock()

		// 从数据库清理断开的会话
		for _, connectionID := range closedConnIDs {
			h.storage.DeleteUserRoomSessionByConnection(connectionID)
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

	pollInterval := 500 * time.Millisecond // 轮询间隔 500ms
	lastPolled := time.Now()

	for {
		select {
		case <-conn.CloseChan:
			// 连接关闭信号
			// log.Printf("[DEBUG] 停止消息投递轮询: userID=%s", conn.UserID)
			return

		case <-time.After(pollInterval):
			// 轮询待通知的消息
			notifications, err := h.storage.GetPendingNotifications(conn.UserID, lastPolled)
			if err != nil {
				log.Printf("[WARN] GetPendingNotifications 失败: userID=%s, err=%v", conn.UserID, err)
				continue
			}

			if len(notifications) > 0 {
				log.Printf("[INFO] 发现 %d 条待通知消息，推送给用户 %s", len(notifications), conn.UserID)

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
						log.Printf("[DEBUG] 已发送通知消息: userID=%s, msg_id=%s, sender=%s", conn.UserID, n.MsgID, n.SenderID)
						notifiedMsgIDs = append(notifiedMsgIDs, n.MsgID)
					case <-time.After(2 * time.Second):
						log.Printf("[WARN] 发送消息超时: userID=%s, msg_id=%s", conn.UserID, n.MsgID)
					default:
						log.Printf("[WARN] 消息缓冲区满，跳过: userID=%s", conn.UserID)
					}
				}

				// 标记消息已通知
				if len(notifiedMsgIDs) > 0 {
					if err := h.storage.MarkNotificationsSent(notifiedMsgIDs, conn.UserID); err != nil {
						log.Printf("[WARN] MarkNotificationsSent 失败: userID=%s, err=%v", conn.UserID, err)
					}
				}

				lastPolled = time.Now()
			}
		}
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
		log.Printf("[WARN] 无法通知用户 %s: 没有活跃连接", userID)
		return
	}

	// 发送给该用户的所有连接
	for _, conn := range userConns {
		select {
		case conn.Send <- data:
			log.Printf("[INFO] 已通知用户 %s (connectionID=%s): %s", userID, conn.ConnectionID, content)
		default:
			log.Printf("[WARN] 无法通知用户 %s (connectionID=%s): 缓冲区满", userID, conn.ConnectionID)
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

	log.Printf("[INFO] 已发送警告并关闭连接: connectionID=%s, userID=%s, content=%s",
		userConn.ConnectionID, userConn.UserID, content)
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
	log.Printf("[INFO] 已发送警告并关闭连接: %s", content)
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
