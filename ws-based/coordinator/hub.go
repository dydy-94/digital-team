package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/digital-team/x-client/storage"
)

type Hub struct {
	rooms           map[string]*ChannelRoom
	clients         map[*Client]bool
	Register        chan *Client
	Unregister      chan *Client
	JoinChannel     chan *JoinEvent
	Speak           chan *SpeakEvent
	StreamMessage   chan *StreamEvent    // 流式消息更新
	StreamComplete  chan *StreamEvent    // 流式消息完成
	UpdateHeartbeat chan *HeartbeatEvent // 心跳更新
	mu              sync.RWMutex
	storage         storage.Storage
	redisStream     *storage.RedisStreamStorage // Redis Stream 用于分布式
	instanceID      string                      // 实例ID
}

type RoomInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Created int64  `json:"created"`
}

type JoinEvent struct {
	Client    *Client
	ChannelId string
	AgentId   string
}

type SpeakEvent struct {
	Client  *Client
	Message *A2AMessage
}

// StreamEvent 流式消息事件
type StreamEvent struct {
	Client  *Client
	Message *A2AMessage
}

// HeartbeatEvent 心跳事件
type HeartbeatEvent struct {
	Client    *Client
	ChannelId string
	AgentId   string
}

func NewHub(store storage.Storage, redisStream *storage.RedisStreamStorage, instanceID string) *Hub {
	hub := &Hub{
		rooms:           make(map[string]*ChannelRoom),
		clients:         make(map[*Client]bool),
		Register:        make(chan *Client),
		Unregister:      make(chan *Client),
		JoinChannel:     make(chan *JoinEvent),
		Speak:           make(chan *SpeakEvent),
		StreamMessage:   make(chan *StreamEvent, 256),
		StreamComplete:  make(chan *StreamEvent, 256),
		UpdateHeartbeat: make(chan *HeartbeatEvent, 100),
		storage:         store,
		redisStream:     redisStream,
		instanceID:      instanceID,
	}

	// 从数据库加载聊天室
	hub.loadRoomsFromStorage()

	// 启动 Redis Stream 订阅（如果启用）
	if redisStream != nil {
		go hub.runRedisSubscriber()
	}

	return hub
}

// loadRoomsFromStorage 从数据库加载聊天室到内存
func (h *Hub) loadRoomsFromStorage() {
	rooms, err := h.storage.GetAllRooms()
	if err != nil {
		log.Printf("Failed to load rooms from storage: %v", err)
		return
	}

	for _, room := range rooms {
		cr := &ChannelRoom{
			ID:         room.ID,
			name:       room.Name,
			createdAt:  room.CreatedAt.Unix(),
			members:    make(map[*Client]string),
			maxHistory: 50,
		}
		cr.SetStorage(h.storage)
		cr.SetInitialAgents(room.InitialAgents) // 加载初始 agents
		h.rooms[room.ID] = cr
		logInfo("hub", "Loaded room from storage", "channel_id", room.ID, "name", room.Name, "initial_agents", room.InitialAgents)

		// 从数据库加载成员（不包括已离开的）
		if members, err := h.storage.GetRoomMembers(room.ID); err == nil {
			logInfo("hub", "Room has members from storage", "channel_id", room.ID, "count", len(members))
			for _, m := range members {
				logInfo("hub", "  Member in storage", "agent_id", m.AgentID)
			}
		}
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			globalMetrics.IncWebSocketConnections()
			logInfo("hub", "New client registered")

		case client := <-h.Unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			globalMetrics.DecWebSocketConnections()
			logInfo("hub", "Client unregistered", "agent_id", client.agentId, "channel_id", client.channelId)

			// 标记 agent 为离线（使用新的 agents 表）
			if client.agentId != "" {
				h.storage.UnregisterAgent(client.agentId)
			}

			if client.channelId != "" {
				if room := h.GetRoom(client.channelId); room != nil {
					room.RemoveMember(client)
					// 注意：只从内存移除，不设置数据库的 left_at
					// 这样 Coordinator 重启后，数据库中的成员状态仍然有效
					logInfo("hub", "Member removed from room (memory only)", "agent_id", client.agentId, "channel_id", client.channelId)
					// 注意：不再因为聊天室变空而删除聊天室
					// 聊天室由协调器管理，客户端断开不影响其存在
				}
			}

		case event := <-h.JoinChannel:
			// 跳过空的 channelId
			if event.ChannelId == "" {
				logWarn("hub", "Skipping join with empty channelId", "agent_id", event.AgentId)
				continue
			}
			room := h.GetOrCreateRoom(event.ChannelId)
			room.AddMember(event.Client, event.AgentId)
			event.Client.channelId = event.ChannelId
			event.Client.agentId = event.AgentId

			// 从存储加载历史消息
			history := room.GetRecentMessages(10)
			event.Client.Send(&ServerMessage{
				Type: "history",
				Data: history,
			})

			joinMsg := NewSystemMessage(event.ChannelId, event.AgentId+" 加入了群聊")
			room.AddToHistory(joinMsg)
			room.Broadcast(&ServerMessage{
				Type: "message",
				Data: joinMsg,
			}, event.Client)

		case event := <-h.UpdateHeartbeat:
			h.handleHeartbeat(event)

			// 持久化成员加入
			h.storage.AddMember(&storage.Member{
				RoomID:  event.ChannelId,
				AgentID: event.AgentId,
			})

			logInfo("hub", "Agent joined channel", "agent_id", event.AgentId, "channel_id", event.ChannelId)

		case event := <-h.Speak:
			msg := event.Message
			msg.Timestamp = time.Now().Unix()

			room := h.GetOrCreateRoom(msg.ChannelId)

			// 如果是"正在思考"状态消息，不获取锁，直接广播
			if msg.Status == "thinking" {
				room.AddToHistory(msg)
				room.Broadcast(&ServerMessage{
					Type: "message",
					Data: msg,
				}, event.Client)
				logInfo("hub", "Thinking message broadcasted", "msg_id", msg.MsgId, "agent_id", msg.Sender)
				continue
			}

			// 正常消息需要获取发言锁
			if !room.TryAcquireSpeakerLock(msg.Sender, 2000) {
				globalMetrics.IncSpeakConflicts()
				event.Client.Send(NewRejectMessage(msg.MsgId,
					"Agent "+room.GetCurrentSpeaker()+" 正在发言，请稍后"))
				logWarn("hub", "Speak rejected - speaker lock acquired", "agent_id", msg.Sender, "channel_id", msg.ChannelId)
				return
			}

			room.AddToHistory(msg)

			// 检查 @ 提到的 Agent 是否在当前聊天室中且在线
			if len(msg.MentionUsers) > 0 {
				var invalidAgents []string
				for _, agentID := range msg.MentionUsers {
					// 检查 Agent 是否在当前聊天室中
					isInRoom := false
					members, err := h.storage.GetRoomMembers(msg.ChannelId)
					if err == nil {
						for _, member := range members {
							if member.AgentID == agentID && member.LeftAt == nil {
								isInRoom = true
								break
							}
						}
					}
					if !isInRoom {
						invalidAgents = append(invalidAgents, agentID)
						continue
					}
					
					// 检查 Agent 是否在线
					online, err := h.IsAgentOnline(msg.ChannelId, agentID)
					if err != nil || !online {
						invalidAgents = append(invalidAgents, agentID)
					}
				}

				if len(invalidAgents) > 0 {
					// 有无效的 Agent，拒绝消息
					event.Client.Send(NewRejectMessage(msg.MsgId,
						"以下 Agent 不在聊天室内或不在线，无法对话: "+strings.Join(invalidAgents, ", ")))
					logWarn("hub", "Message rejected - invalid agents", "agent_ids", strings.Join(invalidAgents, ", "), "channel_id", msg.ChannelId)
					room.ReleaseSpeakerLock()
					continue
				}

				// 调用被 @ 的 Agent 的 HTTP API 来委派任务
				for _, agentID := range msg.MentionUsers {
					h.delegateToAgent(agentID, msg)
				}
			}

			// 持久化消息
			mentionUsers := ""
			for i, u := range msg.MentionUsers {
				if i > 0 {
					mentionUsers += ","
				}
				mentionUsers += u
			}
			h.storage.SaveMessage(&storage.Message{
				RoomID:       msg.ChannelId,
				MsgID:        msg.MsgId,
				Sender:       msg.Sender,
				Target:       msg.Target,
				MentionUsers: mentionUsers,
				Intent:       msg.Intent,
				ContentText:  msg.ContentText,
				Timestamp:    msg.Timestamp,
			})

			// 根据是否启用Redis来决定如何广播消息
			if h.redisStream != nil {
				// 启用Redis的情况：发布到Redis Stream，由订阅者广播
				h.publishToRedis(msg)
			} else {
				// 未启用Redis的情况：直接在协调器中广播
				room.Broadcast(&ServerMessage{
					Type: "message",
					Data: msg,
				}, event.Client)
			}

			logInfo("hub", "Message processed", "msg_id", msg.MsgId, "agent_id", msg.Sender, "channel_id", msg.ChannelId)

			if len(msg.MentionUsers) > 0 {
				room.ReleaseSpeakerLock()
			} else {
				room.ScheduleReleaseLock(2000)
			}

		case event := <-h.StreamMessage:
			// 处理流式消息更新（不获取锁，直接广播）
			msg := event.Message
			msg.Timestamp = time.Now().Unix()

			room := h.GetRoom(msg.ChannelId)
			if room == nil {
				logWarn("hub", "Stream message: room not found", "channel_id", msg.ChannelId)
				continue
			}

			// 更新房间中的消息或添加新消息
			room.UpdateOrAddStreamMessage(msg)

			// 广播到聊天室所有成员（包括发送者，用于确认）
			room.BroadcastStream(&ServerMessage{
				Type: "stream",
				Data: msg,
			}, nil) // nil 表示不排除任何人

			logInfo("hub", "Stream message broadcasted", "msg_id", msg.MsgId, "status", msg.Status)

		case event := <-h.StreamComplete:
			// 处理流式消息完成
			msg := event.Message
			msg.Timestamp = time.Now().Unix()
			msg.Status = "completed"

			room := h.GetRoom(msg.ChannelId)
			if room == nil {
				logWarn("hub", "Stream complete: room not found", "channel_id", msg.ChannelId)
				continue
			}

			// 更新消息状态为完成
			room.UpdateStreamMessageComplete(msg)

			// 持久化完整消息
			h.storage.SaveMessage(&storage.Message{
				RoomID:       msg.ChannelId,
				MsgID:        msg.MsgId,
				Sender:       msg.Sender,
				Target:       msg.Target,
				MentionUsers: "",
				Intent:       msg.Intent,
				ContentText:  msg.ContentText,
				Timestamp:    msg.Timestamp,
			})

			// 广播完成状态
			room.BroadcastStream(&ServerMessage{
				Type: "stream_complete",
				Data: msg,
			}, nil)

			// 释放发言锁（如果之前获取了）
			room.ReleaseSpeakerLock()

			logInfo("hub", "Stream complete broadcasted", "msg_id", msg.MsgId, "sender", msg.Sender)
		}
	}
}

func (h *Hub) GetRoom(channelId string) *ChannelRoom {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.rooms[channelId]
}

func (h *Hub) GetOrCreateRoom(channelId string) *ChannelRoom {
	h.mu.Lock()
	defer h.mu.Unlock()

	if room, exists := h.rooms[channelId]; exists {
		return room
	}

	// 尝试从数据库获取
	dbRoom, err := h.storage.GetRoom(channelId)
	if err == nil && dbRoom != nil {
		cr := &ChannelRoom{
			ID:         dbRoom.ID,
			name:       dbRoom.Name,
			createdAt:  dbRoom.CreatedAt.Unix(),
			members:    make(map[*Client]string),
			maxHistory: 50,
		}
		h.rooms[channelId] = cr
		logInfo("hub", "Loaded existing room from storage", "channel_id", channelId)
		return cr
	}

	room := NewChannelRoom(channelId)
	h.rooms[channelId] = room

	// 持久化到数据库
	h.storage.CreateRoom(&storage.Room{
		ID:            channelId,
		Name:          channelId,
		InitialAgents: []string{},
	})
	logInfo("hub", "New channel room created and saved", "channel_id", channelId)
	return room
}

func (h *Hub) RemoveRoom(channelId string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.rooms, channelId)
}

func (h *Hub) FindClientByAgentId(agentId string) *Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		if client.agentId == agentId {
			return client
		}
	}
	return nil
}

func (h *Hub) JoinAgentsToChannel(channelId string, agentIds []string) {
	for _, agentId := range agentIds {
		client := h.FindClientByAgentId(agentId)
		if client != nil {
			h.JoinChannel <- &JoinEvent{
				Client:    client,
				ChannelId: channelId,
				AgentId:   agentId,
			}
		}
	}
}

// runRedisSubscriber 启动 Redis Stream 订阅者
// 使用消费者组模式，Redis 自动跟踪每个消费者的消息位置
func (h *Hub) runRedisSubscriber() {
	if h.redisStream == nil {
		return
	}

	logInfo("hub", "Redis subscriber starting", "instance_id", h.instanceID)

	// 获取所有聊天室ID
	h.mu.RLock()
	roomIDs := make([]string, 0, len(h.rooms))
	for roomID := range h.rooms {
		roomIDs = append(roomIDs, roomID)
	}
	h.mu.RUnlock()

	// 消费者组名固定，实例ID作为消费者名
	groupName := "coordinator-group"

	// 使用消费者组订阅
	msgCh, err := h.redisStream.SubscribeMultipleWithGroups(roomIDs, groupName, h.instanceID)
	if err != nil {
		log.Printf("Failed to start Redis subscription: %v", err)
		return
	}

	logInfo("hub", "Redis subscriber started", "instance_id", h.instanceID, "rooms", len(roomIDs))

	// 处理消息
	for msg := range msgCh {
		// 转换为 A2AMessage 并广播给本地客户端
		a2aMsg := &A2AMessage{
			MsgId:        msg.MsgID,
			ChannelId:    msg.ChannelID,
			Sender:       msg.Sender,
			Target:       msg.Target,
			MentionUsers: splitMentionUsers(msg.MentionUsers),
			Intent:       msg.Intent,
			ContentText:  msg.Content,
			Timestamp:    msg.Timestamp,
		}

		// 广播给本地客户端
		room := h.GetRoom(msg.ChannelID)
		if room != nil {
			room.Broadcast(&ServerMessage{
				Type: "message",
				Data: a2aMsg,
			}, nil)
		}
	}

	logInfo("hub", "Redis subscriber stopped", "instance_id", h.instanceID)
}

// publishToRedis 发布消息到 Redis Stream
func (h *Hub) publishToRedis(msg *A2AMessage) {
	if h.redisStream == nil {
		return
	}

	mentionUsers := ""
	for i, u := range msg.MentionUsers {
		if i > 0 {
			mentionUsers += ","
		}
		mentionUsers += u
	}

	streamMsg := &storage.StreamMessage{
		MsgID:        msg.MsgId,
		ChannelID:    msg.ChannelId,
		Sender:       msg.Sender,
		Target:       msg.Target,
		MentionUsers: mentionUsers,
		Intent:       msg.Intent,
		Content:      msg.ContentText,
		Timestamp:    msg.Timestamp,
	}

	go func() {
		if _, err := h.redisStream.AddMessage(streamMsg); err != nil {
			log.Printf("Failed to publish to Redis: %v", err)
		}
	}()
}

// splitMentionUsers 将逗号分隔的字符串转换为数组
func splitMentionUsers(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := make([]string, 0)
	for _, p := range strings.Split(s, ",") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// checkOfflineMembers 定时检查离线成员（基于 agents 表的状态）
func (h *Hub) checkOfflineMembers() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		<-ticker.C

		// 获取 agents 表中所有在线的 agent
		onlineAgents, err := h.storage.GetAllOnlineAgents()
		if err != nil {
			log.Printf("Failed to get online agents: %v", err)
			continue
		}

		// 建立在线 agent 的 map
		onlineAgentMap := make(map[string]bool)
		for _, agent := range onlineAgents {
			onlineAgentMap[agent.AgentID] = true
		}

		// 获取所有聊天室
		rooms, err := h.storage.GetAllRooms()
		if err != nil {
			log.Printf("Failed to get rooms: %v", err)
			continue
		}

		// 遍历每个聊天室的成员
		for _, room := range rooms {
			members, err := h.storage.GetRoomMembers(room.ID)
			if err != nil {
				continue
			}

			for _, member := range members {
				if member.MemberType != "agent" {
					continue
				}

				// 检查 agents 表中该 agent 是否在线
				isAgentOnline := onlineAgentMap[member.AgentID]

				// 如果 agents 表离线但 members 表在线，标记为离线
				if !isAgentOnline && member.Online {
					h.storage.UpdateMemberOnline(room.ID, member.AgentID, false)
					logInfo("hub", "Agent marked as offline in members", "agent_id", member.AgentID, "room_id", room.ID)
				}

				// 如果 agents 表在线但 members 表离线，标记为在线（可选，保持同步）
				if isAgentOnline && !member.Online {
					h.storage.UpdateMemberOnline(room.ID, member.AgentID, true)
					logInfo("hub", "Agent marked as online in members", "agent_id", member.AgentID, "room_id", room.ID)
				}
			}
		}
	}
}

// handleHeartbeat 处理心跳
func (h *Hub) handleHeartbeat(event *HeartbeatEvent) {
	if event.ChannelId == "" || event.AgentId == "" {
		return
	}

	// 更新数据库中的心跳时间
	err := h.storage.UpdateMemberHeartbeat(event.ChannelId, event.AgentId)
	if err != nil {
		log.Printf("Failed to update heartbeat: %v", err)
	}
}

// IsAgentOnline 检查 Agent 是否在线（现在使用专门的 agents 表）
func (h *Hub) IsAgentOnline(channelId, agentId string) (bool, error) {
	// 检查 agents 表中该 agent 是否在线
	agent, err := h.storage.GetAgentStatus(agentId)
	if err != nil {
		return false, err
	}
	if agent == nil {
		return false, nil
	}

	// 在线状态为 true 且心跳时间在 30 秒内
	return agent.Online && time.Since(agent.LastHeartbeat) < 30*time.Second, nil
}

// IsAgentOnline 检查 Agent 是否在线（旧方法保留向后兼容）
func (h *Hub) IsAgentOnlineOld(channelId, agentId string) (bool, error) {
	return h.storage.IsAgentOnline(channelId, agentId)
}

// checkMentionedAgentsOnline 检查 @ 提到的 Agent 是否在线（使用新的 agents 表）
func (h *Hub) checkMentionedAgentsOnline(channelId string, mentionUsers []string) (offlineAgents []string) {
	for _, agentId := range mentionUsers {
		online, err := h.IsAgentOnline(channelId, agentId)
		if err != nil || !online {
			offlineAgents = append(offlineAgents, agentId)
		}
	}
	return offlineAgents
}

// delegateToAgent 调用 Agent 的 HTTP API 来委派任务
func (h *Hub) delegateToAgent(agentID string, msg *A2AMessage) {
	// 获取 Agent 的端点
	agent, err := h.storage.GetAgentStatus(agentID)
	if err != nil || agent == nil {
		logWarn("hub", "Agent not found for delegation", "agent_id", agentID)
		return
	}

	if agent.Endpoint == "" {
		logWarn("hub", "Agent has no endpoint registered", "agent_id", agentID)
		return
	}

	// 构建请求 URL，确保包含协议前缀
	var url string
	if strings.HasPrefix(agent.Endpoint, "http://") || strings.HasPrefix(agent.Endpoint, "https://") {
		url = agent.Endpoint + "/skill/delegate"
	} else {
		// 默认使用 http 协议
		url = "http://" + agent.Endpoint + "/skill/delegate"
	}

	// 构建请求体
	reqBody := map[string]interface{}{
		"channel_id": msg.ChannelId,
		"sender":     msg.Sender,
		"content":    msg.ContentText,
		"msg_id":     msg.MsgId,
		"intent":     msg.Intent,
		"timestamp":  msg.Timestamp,
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		logError("hub", "Failed to marshal delegate request", "error", err.Error())
		return
	}

	// 异步发送请求
	go func() {
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			logWarn("hub", "Failed to delegate to agent", "agent_id", agentID, "error", err.Error())
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			logWarn("hub", "Agent delegation failed", "agent_id", agentID, "status", resp.StatusCode)
			return
		}

		logInfo("hub", "Task delegated to agent", "agent_id", agentID, "msg_id", msg.MsgId)
	}()
}
