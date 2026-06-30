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
	// Agent 在线状态（仅对 agent 类型成员有效，user 类型为空）
	// 由 agents 表的 status 字段决定，反映 x-client 的心跳状态
	AgentStatus string `json:"agent_status,omitempty" db:"agent_status"`
	// 用户是否建立了 WebSocket 连接（通过 user_room_sessions 表判断）
	WsEstablished bool `json:"ws_established,omitempty" db:"ws_established"`
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
	ID           int64     `json:"-" db:"id"`
	MsgID        string    `json:"msg_id" db:"msg_id"`
	RoomID       string    `json:"room_id" db:"room_id"`
	SenderID     string    `json:"sender_id" db:"sender_id"`
	SenderType   string    `json:"sender_type" db:"sender_type"`     // agent / user / system
	TargetID     string    `json:"target_id" db:"target_id"`         // ALL / specific_id
	TargetType   string    `json:"target_type" db:"target_type"`     // BROADCAST / DIRECT
	MentionUsers string    `json:"mention_users" db:"mention_users"` // JSON array
	Content      string    `json:"content" db:"content"`
	Intent       string    `json:"intent" db:"intent"` // INFORM / REQUEST / RESPONSE / SYSTEM
	Status       string    `json:"status" db:"status"` // PENDING / DELIVERED / READ
	ReplyToMsgID string    `json:"reply_to_msg_id,omitempty" db:"reply_to_msg_id"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	DeliveredAt  time.Time `json:"delivered_at,omitempty" db:"delivered_at"`
	ReadAt       time.Time `json:"read_at,omitempty" db:"read_at"`
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

// ============ Task 模型 ============

// Task 任务
type Task struct {
	ID           int64  `json:"-" db:"id"`
	TaskID       string `json:"task_id" db:"task_id"`
	Title        string `json:"title" db:"title"`
	Description  string `json:"description" db:"description"`
	Status       string `json:"status" db:"status"` // todo / in_progress / done
	Priority     int    `json:"priority" db:"priority"`
	CreatedBy    string `json:"created_by" db:"created_by"`
	AssignedTo   string `json:"assigned_to" db:"assigned_to"`
	RoomID       string `json:"room_id" db:"room_id"`
	ParentTaskID string `json:"parent_task_id,omitempty" db:"parent_task_id"`
	CreatedAt    int64  `json:"created_at" db:"created_at"`
	UpdatedAt    int64  `json:"updated_at" db:"updated_at"`
	CompletedAt  int64  `json:"completed_at,omitempty" db:"completed_at"`
}

// FocusItem 任务关注点
type FocusItem struct {
	ID        int64  `json:"-" db:"id"`
	ItemID    string `json:"item_id" db:"item_id"`
	TaskID    string `json:"task_id" db:"task_id"`
	Content   string `json:"content" db:"content"`
	Status    string `json:"status" db:"status"` // [ ] / [/] / [x]
	AgentID   string `json:"agent_id" db:"agent_id"`
	RoomID    string `json:"room_id" db:"room_id"`
	ItemOrder int    `json:"item_order" db:"item_order"`
	CreatedAt int64  `json:"created_at" db:"created_at"`
	UpdatedAt int64  `json:"updated_at" db:"updated_at"`
}

// AgentPermission Agent 权限
type AgentPermission struct {
	ID                  int64  `json:"-" db:"id"`
	AgentID             string `json:"agent_id" db:"agent_id"`
	Level               string `json:"level" db:"level"`                 // l1 / l2 / l3
	AllowedTools        string `json:"allowed_tools" db:"allowed_tools"` // JSON array
	DeniedTools         string `json:"denied_tools" db:"denied_tools"`   // JSON array
	DailyTokenLimit     int64  `json:"daily_token_limit" db:"daily_token_limit"`
	MonthlyTokenLimit   int64  `json:"monthly_token_limit" db:"monthly_token_limit"`
	FileSizeLimitMB     int    `json:"file_size_limit_mb" db:"file_size_limit_mb"`
	MessageLimitPerHour int    `json:"message_limit_per_hour" db:"message_limit_per_hour"`
	CreatedAt           int64  `json:"created_at" db:"created_at"`
	UpdatedAt           int64  `json:"updated_at" db:"updated_at"`
}

// FileTransfer 文件传输记录
type FileTransfer struct {
	ID          int64  `json:"-" db:"id"`
	TransferID  string `json:"transfer_id" db:"transfer_id"`
	FileName    string `json:"file_name" db:"file_name"`
	FileSize    int64  `json:"file_size" db:"file_size"`
	MimeType    string `json:"mime_type" db:"mime_type"`
	FromAgent   string `json:"from_agent" db:"from_agent"`
	ToAgent     string `json:"to_agent" db:"to_agent"`
	RoomID      string `json:"room_id" db:"room_id"`
	TaskID      string `json:"task_id,omitempty" db:"task_id"`
	S3Key       string `json:"s3_key" db:"s3_key"`
	Status      string `json:"status" db:"status"` // pending / uploading / completed / failed
	CreatedAt   int64  `json:"created_at" db:"created_at"`
	CompletedAt int64  `json:"completed_at,omitempty" db:"completed_at"`
}

// ============ Task API 请求/响应模型 ============

// CreateTaskRequest 创建任务请求
type CreateTaskRequest struct {
	Title        string `json:"title" binding:"required"`
	Description  string `json:"description"`
	Priority     int    `json:"priority"`
	AssignedTo   string `json:"assigned_to" binding:"required"`
	RoomID       string `json:"room_id" binding:"required"`
	ParentTaskID string `json:"parent_task_id"`
	CreatedBy    string `json:"created_by"`
}

// UpdateTaskRequest 更新任务请求
type UpdateTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"` // todo / in_progress / done
	Priority    int    `json:"priority"`
}

// BatchGetTasksRequest 批量获取任务请求
type BatchGetTasksRequest struct {
	TaskIDs []string `json:"task_ids" binding:"required"`
}

// BatchGetTasksResponse 批量获取任务响应
type BatchGetTasksResponse struct {
	Tasks []Task `json:"tasks"`
}

// CreateFocusItemRequest 创建关注点请求
type CreateFocusItemRequest struct {
	Content   string `json:"content" binding:"required"`
	AgentID   string `json:"agent_id" binding:"required"`
	ItemOrder int    `json:"item_order"`
}

// UpdateFocusItemRequest 更新关注点请求
type UpdateFocusItemRequest struct {
	Content string `json:"content"`
	Status  string `json:"status"` // [ ] / [/] / [x]
}

// UpsertPermissionRequest 创建/更新权限请求
type UpsertPermissionRequest struct {
	Level               string   `json:"level"` // l1 / l2 / l3
	AllowedTools        []string `json:"allowed_tools"`
	DeniedTools         []string `json:"denied_tools"`
	DailyTokenLimit     int64    `json:"daily_token_limit"`
	MonthlyTokenLimit   int64    `json:"monthly_token_limit"`
	FileSizeLimitMB     int      `json:"file_size_limit_mb"`
	MessageLimitPerHour int      `json:"message_limit_per_hour"`
}

// FileTransferResponse 文件传输响应
type FileTransferResponse struct {
	TransferID   string `json:"transfer_id"`
	PresignedURL string `json:"presigned_url,omitempty"`
	S3Key        string `json:"s3_key"`
}

// ============ Agent 关系模型 ============

// RelationType 关系类型常量
const (
	RelationColleague   = "colleague"
	RelationSuperior    = "superior"
	RelationSubordinate = "subordinate"
)

// AgentRelation Agent 关系
type AgentRelation struct {
	ID             int64     `json:"id" db:"id"`
	AgentID        string    `json:"agent_id" db:"agent_id"`
	RelationType   string    `json:"relation_type" db:"relation_type"` // colleague / superior / subordinate
	RelatedAgentID string    `json:"related_agent_id" db:"related_agent_id"`
	RoomID         string    `json:"room_id,omitempty" db:"room_id"`
	Description    string    `json:"description,omitempty" db:"description"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// RoomConfig 聊天室配置
type RoomConfig struct {
	RoomID           string `json:"room_id" db:"room_id"`
	Name             string `json:"name"`
	HierarchyEnabled bool   `json:"hierarchy_enabled"`
	AutoWelcome      bool   `json:"auto_welcome"`
	WelcomeMessage   string `json:"welcome_message"`
}

// Relations Agent 关系汇总
type Relations struct {
	Colleagues   []string `json:"colleagues"`
	Superiors    []string `json:"superiors"`
	Subordinates []string `json:"subordinates"`
}

// AgentContext Agent 完整上下文
type AgentContext struct {
	CurrentAgent *AgentInfo  `json:"current_agent"`
	RoomMembers  []AgentInfo `json:"room_members"`
	Relations    *Relations  `json:"relations"`
	RoomConfig   *RoomConfig `json:"room_config,omitempty"`
}

// AgentInfo Agent 信息（用于 API 响应）
type AgentInfo struct {
	AgentID     string     `json:"agent_id"`
	Role        string     `json:"role,omitempty"`
	Description string     `json:"description,omitempty"`
	Online      bool       `json:"online,omitempty"`
	Relations   *Relations `json:"relations,omitempty"`
	Endpoint    string     `json:"endpoint,omitempty"`
}

// ============ Agent 关系 API 请求/响应模型 ============

// CreateRelationRequest 创建关系请求
type CreateRelationRequest struct {
	AgentID        string `json:"agent_id" binding:"required"`
	RelationType   string `json:"relation_type" binding:"required"` // colleague / superior / subordinate
	RelatedAgentID string `json:"related_agent_id" binding:"required"`
	RoomID         string `json:"room_id"`
	Description    string `json:"description"`
}

// UpsertRoomConfigRequest 创建/更新聊天室配置请求
type UpsertRoomConfigRequest struct {
	Name             string `json:"name"`
	HierarchyEnabled bool   `json:"hierarchy_enabled"`
	AutoWelcome      bool   `json:"auto_welcome"`
	WelcomeMessage   string `json:"welcome_message"`
}
