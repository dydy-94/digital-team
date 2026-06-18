package storage

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// Storage 是存储层的抽象接口，支持多种数据库实现
type Storage interface {
	// Room 相关
	CreateRoom(room *Room) error
	GetRoom(id string) (*Room, error)
	GetAllRooms() ([]*Room, error)
	GetRoomsByInitialAgent(agentID string) ([]*Room, error) // 获取包含特定agent的所有聊天室
	UpdateRoom(room *Room) error
	DeleteRoom(id string) error

	// Agent 相关（专门记录 agent 在线状态）
	RegisterAgent(agentID string, endpoint string) error      // 注册 Agent 连接（带 endpoint）
	UnregisterAgent(agentID string) error                     // 注销 Agent 连接
	UpdateAgentHeartbeat(agentID string) error                // 更新 Agent 心跳
	GetAgentStatus(agentID string) (*Agent, error)            // 获取 Agent 状态
	GetAllOnlineAgents() ([]*Agent, error)                    // 获取所有在线 Agent
	GetOfflineAgents(timeout time.Duration) ([]*Agent, error) // 获取超时的离线 Agent

	// Member 相关（记录聊天室和成员的关系）
	AddMember(member *Member) error
	RemoveMember(roomID, agentID string) error
	GetRoomMembers(roomID string) ([]*Member, error)
	GetMemberByAgentID(agentID string) (*Member, error)
	UpdateMemberOnline(roomID, agentID string, online bool) error // 更新在线状态
	UpdateMemberHeartbeat(roomID, agentID string) error           // 更新心跳时间
	GetOfflineMembers(timeout time.Duration) ([]*Member, error)   // 获取超时的离线成员
	GetRoomMember(roomID, agentID string) (*Member, error)        // 获取指定成员
	UpdateMember(member *Member) error                            // 更新成员状态
	IsAgentOnline(roomID, agentID string) (bool, error)           // 检查 Agent 是否在指定聊天室在线
	IsAgentOnlineAnywhere(agentID string) (bool, error)           // 检查 Agent 是否在任何聊天室在线

	// Message 相关
	SaveMessage(msg *Message) error

	// Message 相关
	GetRoomMessages(roomID string, limit int) ([]*Message, error)
	GetRoomMessagesAfter(roomID string, afterTime time.Time, limit int) ([]*Message, error)

	// Memory 相关 (x-client用)
	SaveMemory(agentID, channelID string, memory *Memory) error
	GetMemory(agentID, channelID string) (*Memory, error)
	DeleteMemory(agentID, channelID string) error

	// User 相关
	CreateUser(user *User) error
	GetUserByUsername(username string) (*User, error)
	GetUserByID(id uint) (*User, error)
	UpdateUser(user *User) error

	// 初始化
	Initialize() error
	Close() error
}

// Room 聊天室
type Room struct {
	ID                string    `json:"id" gorm:"primaryKey"`
	Name              string    `json:"name"`
	InitialAgents     []string  `json:"initial_agents" gorm:"-"`                  // 不在数据库中直接存储，通过json字符串字段间接存储
	InitialAgentsJSON string    `json:"-" gorm:"column:initial_agents;type:text"` // JSON字符串格式的初始 agents
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// 保存前序列化
func (r *Room) BeforeSave(*gorm.DB) error {
	if r.InitialAgents == nil {
		r.InitialAgents = []string{}
	}
	data, err := json.Marshal(r.InitialAgents)
	if err != nil {
		return err
	}
	r.InitialAgentsJSON = string(data)
	return nil
}

// 加载后反序列化
func (r *Room) AfterFind(*gorm.DB) error {
	if r.InitialAgentsJSON == "" {
		r.InitialAgents = []string{}
		return nil
	}
	return json.Unmarshal([]byte(r.InitialAgentsJSON), &r.InitialAgents)
}

// Member 聊天室成员
type Member struct {
	ID            uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	RoomID        string     `json:"room_id"`
	AgentID       string     `json:"agent_id"`
	MemberType    string     `json:"member_type"` // "agent" or "user"
	JoinedAt      time.Time  `json:"joined_at"`
	LeftAt        *time.Time `json:"left_at"`
	Online        bool       `json:"online"`         // 在线状态，支持分布式
	LastHeartbeat time.Time  `json:"last_heartbeat"` // 最后心跳时间，用于离线检测
}

// Message 消息
type Message struct {
	ID           uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	RoomID       string    `json:"room_id"`
	MsgID        string    `json:"msg_id"`
	Sender       string    `json:"sender"`
	Target       string    `json:"target"`
	MentionUsers string    `json:"mention_users"`
	Intent       string    `json:"intent"`
	ContentText  string    `json:"content_text"`
	Timestamp    int64     `json:"timestamp"`
	CreatedAt    time.Time `json:"created_at"`
}

// Memory x-client的记忆缓存
type Memory struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	AgentID   string    `json:"agent_id"`
	ChannelID string    `json:"channel_id"`
	Sender    string    `json:"sender"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// User 用户
type User struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	Username  string    `json:"username" gorm:"uniqueIndex;size:50"`
	Password  string    `json:"-"` // 不序列化密码
	Nickname  string    `json:"nickname"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Agent 专门记录连接到 coordinator 的 agent（用于心跳和在线状态）
type Agent struct {
	AgentID       string    `json:"agent_id" gorm:"primaryKey"`
	Endpoint      string    `json:"endpoint"`       // Agent 的 HTTP 端点（完整 URL）
	Online        bool      `json:"online"`         // 是否在线
	LastHeartbeat time.Time `json:"last_heartbeat"` // 最后心跳时间
	ConnectedAt   time.Time `json:"connected_at"`   // 连接时间
}

// TableName 自定义表名
func (Room) TableName() string {
	return "rooms"
}

func (Member) TableName() string {
	return "members"
}

func (Message) TableName() string {
	return "messages"
}

func (Memory) TableName() string {
	return "memories"
}

func (User) TableName() string {
	return "users"
}

func (Agent) TableName() string {
	return "agents"
}
