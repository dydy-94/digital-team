package main

import "time"

// ============ 数据库模型 ============

// Agent Agent 注册信息
type Agent struct {
	ID            int64     `json:"-" db:"id"`
	AgentID       string    `json:"agent_id" db:"agent_id"`
	Endpoint      string    `json:"endpoint" db:"endpoint"`
	Status        string    `json:"status" db:"status"`
	LastHeartbeat time.Time `json:"last_heartbeat" db:"last_heartbeat"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

// Room 聊天室
type Room struct {
	ID          int64     `json:"-" db:"id"`
	RoomID      string    `json:"room_id" db:"room_id"`
	Name        string    `json:"name" db:"name"`
	Description string    `json:"description" db:"description"`
	CreatedBy   string    `json:"created_by" db:"created_by"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// Member 聊天室成员
type Member struct {
	ID         int64      `json:"-" db:"id"`
	RoomID     string     `json:"room_id" db:"room_id"`
	MemberID   string     `json:"member_id" db:"member_id"`
	MemberType string     `json:"member_type" db:"member_type"` // agent / user
	JoinedAt   time.Time  `json:"joined_at" db:"joined_at"`
	LeftAt     *time.Time `json:"left_at,omitempty" db:"left_at"`
	IsActive   bool       `json:"is_active" db:"is_active"`
}

// User 平台用户
type User struct {
	ID           int64     `json:"-" db:"id"`
	UserID       string    `json:"user_id" db:"user_id"`
	Username     string    `json:"username" db:"username"`
	PasswordHash string    `json:"-" db:"password_hash"`
	Email        string    `json:"email" db:"email"`
	AvatarURL    string    `json:"avatar_url" db:"avatar_url"`
	Status       string    `json:"status" db:"status"`
	LastLogin    time.Time `json:"last_login" db:"last_login"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// Message 消息
type Message struct {
	ID            int64     `json:"-" db:"id"`
	MsgID         string    `json:"msg_id" db:"msg_id"`
	RoomID        string    `json:"room_id" db:"room_id"`
	SenderID      string    `json:"sender_id" db:"sender_id"`
	SenderType    string    `json:"sender_type" db:"sender_type"`     // agent / user / system
	TargetID      string    `json:"target_id" db:"target_id"`         // ALL / specific_id
	TargetType    string    `json:"target_type" db:"target_type"`     // BROADCAST / DIRECT
	MentionUsers  string    `json:"mention_users" db:"mention_users"` // JSON array
	Content       string    `json:"content" db:"content"`
	Intent        string    `json:"intent" db:"intent"` // INFORM / REQUEST / RESPONSE / SYSTEM
	Status        string    `json:"status" db:"status"` // PENDING / DELIVERED / READ
	ReplyToMsgID  string    `json:"reply_to_msg_id,omitempty" db:"reply_to_msg_id"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	DeliveredAt   time.Time `json:"delivered_at,omitempty" db:"delivered_at"`
	ReadAt        time.Time `json:"read_at,omitempty" db:"read_at"`
	SpeakerLock   string    `json:"-" db:"speaker_lock"`
	LockExpiresAt time.Time `json:"-" db:"lock_expires_at"`
}

// SpeakerLock 发言锁
type SpeakerLock struct {
	ID         int64     `json:"-" db:"id"`
	RoomID     string    `json:"room_id" db:"room_id"`
	HolderID   string    `json:"holder_id" db:"holder_id"`
	HolderType string    `json:"holder_type" db:"holder_type"`
	AcquiredAt time.Time `json:"acquired_at" db:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at" db:"expires_at"`
}

// MessageDelivery 消息投递记录
type MessageDelivery struct {
	ID          int64     `json:"-" db:"id"`
	MsgID       string    `json:"msg_id" db:"msg_id"`
	RecipientID string    `json:"recipient_id" db:"recipient_id"`
	DeliveredAt time.Time `json:"delivered_at" db:"delivered_at"`
}

// ============ API 请求/响应模型 ============

// RegisterRequest Agent 注册请求
type RegisterRequest struct {
	AgentID  string `json:"agent_id" binding:"required"`
	Endpoint string `json:"endpoint" binding:"required"`
}

// RegisterResponse 注册响应
type RegisterResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// HeartbeatRequest 心跳请求
type HeartbeatRequest struct {
	AgentID string `json:"agent_id" binding:"required"`
}

// PollRequest 轮询请求（Query 参数）
type PollRequest struct {
	AgentID string `form:"agent_id" binding:"required"`
	Since   int64  `form:"since"`   // Unix 时间戳，查询此时间之后的消息
	RoomID  string `form:"room_id"` // 可选，限定聊天室
	Limit   int    `form:"limit"`   // 返回条数限制
}

// PollResponse 轮询响应
type PollResponse struct {
	Messages  []*PollMessage `json:"messages"`
	NextSince int64          `json:"next_since"` // 下次 poll 的 since 参数
}

// PollMessage 轮询返回的消息
type PollMessage struct {
	MsgID        string   `json:"msg_id"`
	RoomID       string   `json:"room_id"`
	SenderID     string   `json:"sender_id"`
	SenderType   string   `json:"sender_type"`
	Content      string   `json:"content"`
	MentionUsers []string `json:"mention_users"`
	Intent       string   `json:"intent"`
	ReplyToMsgID string   `json:"reply_to_msg_id,omitempty"`
	TargetID     string   `json:"target_id"`
	CreatedAt    int64    `json:"created_at"`
}

// SendMessageRequest 发送消息请求
type SendMessageRequest struct {
	RoomID       string   `json:"room_id" binding:"required"`
	SenderID     string   `json:"sender_id" binding:"required"`
	SenderType   string   `json:"sender_type" binding:"required"` // agent / user
	Content      string   `json:"content" binding:"required"`
	TargetID     string   `json:"target_id"` // 默认 ALL
	MentionUsers []string `json:"mention_users"`
	Intent       string   `json:"intent"` // 默认 INFORM
	ReplyToMsgID string   `json:"reply_to_msg_id"`
}

// SendMessageResponse 发送消息响应
type SendMessageResponse struct {
	Success bool   `json:"success"`
	MsgID   string `json:"msg_id,omitempty"`
	Error   string `json:"error,omitempty"`
}

// BroadcastRequest 广播消息请求
type BroadcastRequest struct {
	RoomID     string `json:"room_id" binding:"required"`
	SenderID   string `json:"sender_id" binding:"required"`
	SenderType string `json:"sender_type" binding:"required"`
	Content    string `json:"content" binding:"required"`
	Intent     string `json:"intent"`
}

// CreateRoomRequest 创建聊天室请求
type CreateRoomRequest struct {
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description"`
	Members     []string `json:"members"` // 初始成员列表
	CreatedBy   string   `json:"created_by"`
}

// CreateRoomResponse 创建聊天室响应
type CreateRoomResponse struct {
	Success bool   `json:"success"`
	RoomID  string `json:"room_id,omitempty"`
	Error   string `json:"error,omitempty"`
}

// JoinRoomRequest 加入聊天室请求
type JoinRoomRequest struct {
	RoomID     string `json:"room_id" binding:"required"`
	MemberID   string `json:"member_id" binding:"required"`
	MemberType string `json:"member_type" binding:"required"` // agent / user
}

// JoinRoomResponse 加入聊天室响应
type JoinRoomResponse struct {
	Success   bool       `json:"success"`
	Room      *Room      `json:"room,omitempty"`
	History   []*Message `json:"history,omitempty"`
	SessionID int64      `json:"session_id,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// GetRoomsResponse 获取聊天室列表响应
type GetRoomsResponse struct {
	Success bool    `json:"success"`
	Rooms   []*Room `json:"rooms"`
}

// GetRoomMembersResponse 获取聊天室成员响应
type GetRoomMembersResponse struct {
	Success bool      `json:"success"`
	Members []*Member `json:"members"`
}

// ErrorResponse 错误响应
type ErrorResponse struct {
	Error string `json:"error"`
	Code  int    `json:"code,omitempty"`
}

// ============ WebSocket 消息模型（User 使用）============

// WSMessage WebSocket 消息
type WSMessage struct {
	Type string      `json:"type"` // message / history / error / system
	Data interface{} `json:"data"`
}
