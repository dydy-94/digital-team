package main

import (
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/digital-team/x-client/storage"
)

type ChannelRoom struct {
	ID             string
	name           string
	createdAt      int64
	members        map[*Client]string
	messageHistory []*A2AMessage
	maxHistory     int

	// 创建聊天室时选择的初始 agents（仅 agent，不包含 user）
	initialAgents []string

	speakerLock    sync.Mutex
	currentSpeaker string
	lastSpeakTime  time.Time

	mu      sync.RWMutex
	storage storage.Storage
	loaded  bool
}

func NewChannelRoom(id string) *ChannelRoom {
	return &ChannelRoom{
		ID:             id,
		name:           id,
		createdAt:      time.Now().Unix(),
		members:        make(map[*Client]string),
		messageHistory: make([]*A2AMessage, 0),
		maxHistory:     50,
	}
}

// SetStorage 设置存储实例
func (r *ChannelRoom) SetStorage(store storage.Storage) {
	r.storage = store
}

// LoadMessagesFromStorage 从数据库加载历史消息
func (r *ChannelRoom) LoadMessagesFromStorage() []*A2AMessage {
	if r.loaded || r.storage == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.loaded {
		return nil
	}

	msgs, err := r.storage.GetRoomMessages(r.ID, r.maxHistory)
	if err != nil {
		log.Printf("Failed to load messages from storage: %v", err)
		r.loaded = true
		return nil
	}

	for _, msg := range msgs {
		mentionUsers := []string{}
		if msg.MentionUsers != "" {
			mentionUsers = strings.Split(msg.MentionUsers, ",")
		}
		a2aMsg := &A2AMessage{
			MsgId:        msg.MsgID,
			ChannelId:    msg.RoomID,
			Sender:       msg.Sender,
			Target:       msg.Target,
			MentionUsers: mentionUsers,
			Intent:       msg.Intent,
			ContentText:  msg.ContentText,
			Timestamp:    msg.Timestamp,
		}
		r.messageHistory = append(r.messageHistory, a2aMsg)
	}

	r.loaded = true
	logInfo("room", "Loaded messages from storage", "room_id", r.ID, "count", len(msgs))
	return nil
}

func (r *ChannelRoom) GetMemberIds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.members))
	for _, agentId := range r.members {
		ids = append(ids, agentId)
	}
	return ids
}

// GetInitialAgents 获取创建聊天室时选择的初始 agents
func (r *ChannelRoom) GetInitialAgents() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]string, len(r.initialAgents))
	copy(result, r.initialAgents)
	return result
}

// SetInitialAgents 设置创建聊天室时选择的初始 agents
func (r *ChannelRoom) SetInitialAgents(agents []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.initialAgents = make([]string, len(agents))
	copy(r.initialAgents, agents)
}

func (r *ChannelRoom) AddMember(client *Client, agentId string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.members[client] = agentId

	// 更新数据库中的在线状态（支持分布式）
	if r.storage != nil {
		r.storage.UpdateMemberOnline(r.ID, agentId, true)
	}
}

func (r *ChannelRoom) RemoveMember(client *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 获取要删除的 agentId
	agentId := r.members[client]
	delete(r.members, client)

	// 更新数据库中的在线状态（支持分布式）
	if r.storage != nil && agentId != "" {
		r.storage.UpdateMemberOnline(r.ID, agentId, false)
	}
}

func (r *ChannelRoom) GetMemberCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.members)
}

func (r *ChannelRoom) IsEmpty() bool {
	return r.GetMemberCount() == 0
}

func (r *ChannelRoom) Broadcast(message *ServerMessage, excludeClient *Client) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for client := range r.members {
		if client != excludeClient {
			client.Send(message)
		}
	}
}

func (r *ChannelRoom) AddToHistory(msg *A2AMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.messageHistory = append(r.messageHistory, msg)
	if len(r.messageHistory) > r.maxHistory {
		r.messageHistory = r.messageHistory[1:]
	}
}

func (r *ChannelRoom) GetRecentMessages(count int) []*A2AMessage {
	// 先尝试从数据库加载历史消息
	r.LoadMessagesFromStorage()

	r.mu.RLock()
	defer r.mu.RUnlock()

	if count > len(r.messageHistory) {
		count = len(r.messageHistory)
	}
	start := len(r.messageHistory) - count

	// 过滤掉system类型的消息（如"加入了群聊"）
	result := make([]*A2AMessage, 0)
	for i := start; i < len(r.messageHistory); i++ {
		if r.messageHistory[i].Sender != "system" {
			result = append(result, r.messageHistory[i])
		}
	}
	return result
}

func (r *ChannelRoom) TryAcquireSpeakerLock(agentId string, timeoutMs int64) bool {
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)

	for {
		r.speakerLock.Lock()
		if r.currentSpeaker == "" || r.currentSpeaker == agentId {
			r.currentSpeaker = agentId
			r.lastSpeakTime = time.Now()
			r.speakerLock.Unlock()
			return true
		}
		r.speakerLock.Unlock()

		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (r *ChannelRoom) ReleaseSpeakerLock() {
	r.speakerLock.Lock()
	defer r.speakerLock.Unlock()
	r.currentSpeaker = ""
}

func (r *ChannelRoom) GetCurrentSpeaker() string {
	r.speakerLock.Lock()
	defer r.speakerLock.Unlock()
	return r.currentSpeaker
}

func (r *ChannelRoom) ScheduleReleaseLock(delayMs int64) {
	go func() {
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
		r.ReleaseSpeakerLock()
	}()
}

// BroadcastStream 广播流式消息（不排除任何人）
func (r *ChannelRoom) BroadcastStream(message *ServerMessage, excludeClient *Client) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for client := range r.members {
		if excludeClient == nil || client != excludeClient {
			client.Send(message)
		}
	}
}

// UpdateOrAddStreamMessage 更新或添加流式消息到历史记录
func (r *ChannelRoom) UpdateOrAddStreamMessage(msg *A2AMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 查找是否已存在该消息
	for i, existing := range r.messageHistory {
		if existing.MsgId == msg.MsgId {
			// 更新现有消息
			r.messageHistory[i] = msg
			return
		}
	}

	// 添加新消息
	r.messageHistory = append(r.messageHistory, msg)
	// 如果超过最大历史，移除最老的非流式消息
	if len(r.messageHistory) > r.maxHistory {
		r.messageHistory = r.messageHistory[1:]
	}
}

// UpdateStreamMessageComplete 更新流式消息为完成状态
func (r *ChannelRoom) UpdateStreamMessageComplete(msg *A2AMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, existing := range r.messageHistory {
		if existing.MsgId == msg.MsgId {
			// 更新为完成状态
			msg.Status = "completed"
			r.messageHistory[i] = msg
			return
		}
	}
}

// MemberStatus 成员在线状态
type MemberStatus struct {
	AgentID    string `json:"agentId"`
	MemberType string `json:"memberType"` // "agent" or "user"
	Online     bool   `json:"online"`
	JoinedAt   int64  `json:"joinedAt,omitempty"`
	Username   string `json:"username,omitempty"` // 用户名（如果是用户类型）
	Nickname   string `json:"nickname,omitempty"` // 用户昵称（如果是用户类型）
}

// GetMembersOnlineStatus 获取聊天室成员在线状态
// 在线状态从数据库读取，支持分布式部署
func (r *ChannelRoom) GetMembersOnlineStatus() []MemberStatus {
	// 判断成员类型
	getMemberType := func(agentId string) string {
		// 用户ID可以是以下格式：
		// 1. 直接使用用户名（如cdy）
		// 2. user_${id}格式（为了兼容旧代码）
		if strings.HasPrefix(agentId, "user_") {
			return "user"
		}
		// 判断是否是注册用户（检查用户名是否在用户表中）
		if _, err := r.storage.GetUserByUsername(agentId); err == nil {
			return "user"
		}
		// 默认是agent
		return "agent"
	}

	// 从数据库获取成员及在线状态
	var result []MemberStatus
	if r.storage != nil {
		if members, err := r.storage.GetRoomMembers(r.ID); err == nil {
			for _, m := range members {
				if m.LeftAt == nil { // 只显示当前在聊天室的成员
					ms := MemberStatus{
						AgentID:    m.AgentID,
						MemberType: getMemberType(m.AgentID),
						Online:     m.Online, // 从数据库的 Online 字段读取
						JoinedAt:   m.JoinedAt.Unix(),
					}

					// 如果是用户类型，获取用户名和昵称信息
					if ms.MemberType == "user" {
						if strings.HasPrefix(m.AgentID, "user_") {
							// 兼容旧格式 user_${id}
							userIDStr := strings.TrimPrefix(m.AgentID, "user_")
							userID, err := strconv.Atoi(userIDStr)
							if err == nil {
								if user, err := r.storage.GetUserByID(uint(userID)); err == nil {
									ms.Username = user.Username
									ms.Nickname = user.Nickname
								}
							}
						} else {
							// 直接使用用户名格式
							ms.Username = m.AgentID
							if user, err := r.storage.GetUserByUsername(m.AgentID); err == nil {
								ms.Nickname = user.Nickname
							}
						}
					}

					result = append(result, ms)
				}
			}
		}
	}

	// 添加聊天室创建时的初始agents（如果它们还没有在成员列表中）
	initialAgents := r.GetInitialAgents()
	for _, agentId := range initialAgents {
		// 检查这个agent是否已经在成员列表中
		found := false
		for _, existingMember := range result {
			if existingMember.AgentID == agentId {
				found = true
				break
			}
		}

		// 如果不在成员列表中，添加到结果中
		if !found {
			result = append(result, MemberStatus{
				AgentID:    agentId,
				MemberType: "agent", // 初始agents默认是agent类型
				Online:     false,   // 初始状态为离线
			})
		}
	}

	// 如果数据库没有成员，回退到使用内存中的成员（仅用于旧数据兼容）
	if len(result) == 0 {
		r.mu.RLock()
		defer r.mu.RUnlock()
		for _, agentId := range r.members {
			ms := MemberStatus{
				AgentID:    agentId,
				MemberType: getMemberType(agentId),
				Online:     true,
			}

			// 如果是用户类型，获取用户名和昵称信息
			if ms.MemberType == "user" {
				if strings.HasPrefix(agentId, "user_") {
					// 兼容旧格式 user_${id}
					userIDStr := strings.TrimPrefix(agentId, "user_")
					userID, err := strconv.Atoi(userIDStr)
					if err == nil {
						if user, err := r.storage.GetUserByID(uint(userID)); err == nil {
							ms.Username = user.Username
							ms.Nickname = user.Nickname
						}
					}
				} else {
					// 直接使用用户名格式
					ms.Username = agentId
					if user, err := r.storage.GetUserByUsername(agentId); err == nil {
						ms.Nickname = user.Nickname
					}
				}
			}

			result = append(result, ms)
		}
	}

	return result
}
