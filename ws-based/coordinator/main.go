package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/digital-team/x-client/storage"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logError("coordinator", "WebSocket upgrade failed: "+err.Error())
		return
	}

	client := NewClient(hub, conn)
	hub.Register <- client

	go client.writePump()
	go client.readPump()
}

func main() {
	config, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 初始化指标收集器
	InitializeMetrics()

	// 初始化存储层（暂时只使用SQLite）
	var store storage.Storage
	os.MkdirAll("data", 0755)
	store = storage.NewSQLiteStorage(config.StoragePath)

	if err := store.Initialize(); err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	// 生成实例ID（用于Redis消费者组）
	if config.InstanceID == "" {
		config.InstanceID = uuid.New().String()[:8]
	}
	log.Printf("Coordinator instance ID: %s", config.InstanceID)

	// 初始化 Redis Stream
	var redisStream *storage.RedisStreamStorage
	if config.RedisEnabled {
		redisAddr := fmt.Sprintf("%s:%d", config.RedisHost, config.RedisPort)
		redisStream = storage.NewRedisStreamStorage(redisAddr, config.RedisPass, config.RedisDB)
		if err := redisStream.Ping(); err != nil {
			log.Printf("Warning: Redis connection failed: %v, running without Redis", err)
			redisStream = nil
		} else {
			log.Printf("Redis Stream enabled: %s", redisAddr)
		}
	}

	hub := NewHub(store, redisStream, config.InstanceID)
	go hub.Run()

	// 启动定时任务来检查并更新 agents 表中心跳超时的 agent 状态
	go func() {
		ticker := time.NewTicker(60 * time.Second) // 每隔60秒检查一次
		defer ticker.Stop()

		for range ticker.C {
			log.Println("Checking for heartbeat timeout agents...")

			// 获取所有 agent 的状态
			// 注意：我们需要获取所有 agent，包括在线和离线的，以便检查心跳超时
			// 但是 GetAllOnlineAgents() 只返回在线的，所以我们需要一个新的方法来获取所有 agent

			// 临时解决方案：使用 GetAllOnlineAgents() 然后检查心跳时间
			// 更好的方案是添加一个 GetAllAgents() 方法
			allAgents, err := store.GetAllOnlineAgents() // 目前只能获取在线的
			if err != nil {
				log.Printf("Warning: failed to get agents: %v", err)
				continue
			}

			// 检查每个 agent 的心跳超时
			timeoutCount := 0
			for _, agent := range allAgents {
				// 检查心跳是否超过 2 分钟
				if time.Since(agent.LastHeartbeat) > 2*time.Minute {
					log.Printf("Agent %s heartbeat timeout, marking as offline", agent.AgentID)
					// 标记为离线
					if err := store.UnregisterAgent(agent.AgentID); err != nil {
						log.Printf("Warning: failed to unregister agent %s: %v", agent.AgentID, err)
					}
					timeoutCount++
				}
			}

			log.Printf("Heartbeat check completed, %d agents timed out", timeoutCount)
		}
	}()

	// 启动定时任务来同步 agents 表和 members 表的状态
	go func() {
		ticker := time.NewTicker(30 * time.Second) // 每隔30秒检查一次
		defer ticker.Stop()

		for range ticker.C {
			log.Println("Syncing agents and members status...")

			// 获取 agents 表中所有在线的 agent
			onlineAgents, err := store.GetAllOnlineAgents()
			if err != nil {
				log.Printf("Warning: failed to get online agents: %v", err)
				continue
			}

			// 建立在线 agent 的 map
			onlineAgentMap := make(map[string]bool)
			for _, agent := range onlineAgents {
				onlineAgentMap[agent.AgentID] = true
			}

			// 获取所有聊天室
			rooms, err := store.GetAllRooms()
			if err != nil {
				log.Printf("Warning: failed to get rooms: %v", err)
				continue
			}

			// 遍历每个聊天室的成员，同步状态
			for _, room := range rooms {
				members, err := store.GetRoomMembers(room.ID)
				if err != nil {
					continue
				}

				for _, member := range members {
					// 检查 agents 表中该 agent 是否在线
					isAgentOnline := onlineAgentMap[member.AgentID]

					// 对于 agent 类型，同步在线状态
					if member.MemberType == "agent" {
						// 如果 agents 表离线但 members 表在线，标记为离线
						if !isAgentOnline && member.Online {
							store.UpdateMemberOnline(room.ID, member.AgentID, false)
							log.Printf("Agent %s marked as offline in room %s", member.AgentID, room.ID)
						}

						// 如果 agents 表在线但 members 表离线，标记为在线
						if isAgentOnline && !member.Online {
							store.UpdateMemberOnline(room.ID, member.AgentID, true)
							log.Printf("Agent %s marked as online in room %s", member.AgentID, room.ID)
						}
					}

					// 对于 user 类型，保持在线状态不变（用户主动离开才算离线）
					// user 的在线状态由 WebSocket 连接决定
				}
			}

			log.Printf("Sync completed, %d agents online", len(onlineAgents))
		}
	}()

	go StartMetricsResetTicker()

	// sendError 辅助函数
	sendErrorResp := func(w http.ResponseWriter, message string, status int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   message,
		})
	}

	// ========== 用户认证 API ==========

	// 用户注册
	http.HandleFunc("/api/user/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Nickname string `json:"nickname"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Username == "" || req.Password == "" {
			sendErrorResp(w, "username and password are required", http.StatusBadRequest)
			return
		}

		// 检查用户名是否已存在
		if _, err := store.GetUserByUsername(req.Username); err == nil {
			sendErrorResp(w, "username already exists", http.StatusConflict)
			return
		}

		// 简单密码哈希（实际应用中应使用更安全的方式）
		hashedPassword := req.Password // 简化处理

		user := &storage.User{
			Username: req.Username,
			Password: hashedPassword,
			Nickname: req.Nickname,
		}
		if user.Nickname == "" {
			user.Nickname = req.Username
		}

		if err := store.CreateUser(user); err != nil {
			sendErrorResp(w, "failed to create user: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"user": map[string]interface{}{
				"id":       user.ID,
				"username": user.Username,
				"nickname": user.Nickname,
			},
		})
	})

	// 用户登录
	http.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		user, err := store.GetUserByUsername(req.Username)
		if err != nil {
			sendErrorResp(w, "invalid username or password", http.StatusUnauthorized)
			return
		}

		// 简单密码验证（实际应用中应使用更安全的方式）
		if user.Password != req.Password {
			sendErrorResp(w, "invalid username or password", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"user": map[string]interface{}{
				"id":       user.ID,
				"username": user.Username,
				"nickname": user.Nickname,
			},
		})
	})

	// 获取当前用户信息
	http.HandleFunc("/api/user/current", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			sendErrorResp(w, "user_id is required", http.StatusBadRequest)
			return
		}

		var id uint
		if _, err := fmt.Sscanf(userID, "%d", &id); err != nil {
			sendErrorResp(w, "invalid user_id", http.StatusBadRequest)
			return
		}

		user, err := store.GetUserByID(id)
		if err != nil {
			sendErrorResp(w, "user not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"user": map[string]interface{}{
				"id":       user.ID,
				"username": user.Username,
				"nickname": user.Nickname,
			},
		})
	})

	// ========== Agent 管理 API ==========

	// 注册 Agent（x-client 启动时调用）
	http.HandleFunc("/api/agent/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			AgentID  string `json:"agent_id"`
			Endpoint string `json:"endpoint"` // Agent 的 HTTP 端点（完整 URL）
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.AgentID == "" {
			sendErrorResp(w, "agent_id is required", http.StatusBadRequest)
			return
		}

		log.Printf("Registering agent: %s (endpoint: %s)", req.AgentID, req.Endpoint)

		// 保存到数据库（包含端点信息）
		if err := store.RegisterAgent(req.AgentID, req.Endpoint); err != nil {
			sendErrorResp(w, "failed to register agent: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 获取包含该 agent 的所有聊天室
		rooms, err := store.GetRoomsByInitialAgent(req.AgentID)
		if err != nil {
			log.Printf("Warning: failed to get rooms for agent %s: %v", req.AgentID, err)
		} else {
			// 为每个包含该 agent 的聊天室添加成员记录
			for _, room := range rooms {
				member := &storage.Member{
					RoomID:     room.ID,
					AgentID:    req.AgentID,
					MemberType: "agent",
					Online:     true,
				}

				// 检查该 agent 是否已经是该聊天室的成员
				existingMember, errCheck := store.GetRoomMember(room.ID, req.AgentID)
				if errCheck != nil {
					// 如果不是成员，添加到聊天室
					if errAdd := store.AddMember(member); errAdd != nil {
						log.Printf("Warning: failed to add agent %s to room %s: %v", req.AgentID, room.ID, errAdd)
					}
				} else {
					// 如果是成员但已离开，更新状态
					if existingMember.LeftAt != nil {
						existingMember.LeftAt = nil
						existingMember.Online = true
						existingMember.LastHeartbeat = time.Now()
						if errUpdate := store.UpdateMember(existingMember); errUpdate != nil {
							log.Printf("Warning: failed to update agent %s in room %s: %v", req.AgentID, room.ID, errUpdate)
						}
					}
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Agent registered successfully",
		})
	})

	// 心跳报告接口（x-client 定期调用）
	http.HandleFunc("/api/agent/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.AgentID == "" {
			sendErrorResp(w, "agent_id is required", http.StatusBadRequest)
			return
		}

		log.Printf("Heartbeat from agent: %s", req.AgentID)

		// 获取agent当前状态
		agentStatus, err := store.GetAgentStatus(req.AgentID)
		if err != nil {
			log.Printf("Warning: failed to get agent status for %s: %v", req.AgentID, err)
		}

		// 更新心跳时间
		if err := store.UpdateAgentHeartbeat(req.AgentID); err != nil {
			sendErrorResp(w, "failed to update heartbeat: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 如果agent之前不是在线状态，现在需要更新为在线状态并处理聊天室成员关系
		if agentStatus == nil || !agentStatus.Online {
			log.Printf("Agent %s is now online, updating room memberships", req.AgentID)

			// 获取包含该agent的所有聊天室
			rooms, err := store.GetRoomsByInitialAgent(req.AgentID)
			if err != nil {
				log.Printf("Warning: failed to get rooms for agent %s: %v", req.AgentID, err)
			} else {
				// 为每个包含该agent的聊天室添加成员记录
				for _, room := range rooms {
					member := &storage.Member{
						RoomID:     room.ID,
						AgentID:    req.AgentID,
						MemberType: "agent",
						Online:     true,
					}

					// 检查该agent是否已经是该聊天室的成员
					existingMember, errCheck := store.GetRoomMember(room.ID, req.AgentID)
					if errCheck != nil {
						// 如果不是成员，添加到聊天室
						if errAdd := store.AddMember(member); errAdd != nil {
							log.Printf("Warning: failed to add agent %s to room %s: %v", req.AgentID, room.ID, errAdd)
						}
					} else {
						// 如果是成员但已离开，更新状态
						if existingMember.LeftAt != nil {
							existingMember.LeftAt = nil
							existingMember.Online = true
							existingMember.LastHeartbeat = time.Now()
							if errUpdate := store.UpdateMember(existingMember); errUpdate != nil {
								log.Printf("Warning: failed to update agent %s in room %s: %v", req.AgentID, room.ID, errUpdate)
							}
						} else if !existingMember.Online {
							// 如果是成员但状态为离线，更新为在线
							existingMember.Online = true
							existingMember.LastHeartbeat = time.Now()
							if errUpdate := store.UpdateMember(existingMember); errUpdate != nil {
								log.Printf("Warning: failed to update agent %s in room %s: %v", req.AgentID, room.ID, errUpdate)
							}
						}
					}
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Heartbeat received",
		})
	})

	// 查询可用的 Agent 列表
	http.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 获取所有在线 Agent（最近 60 秒内有心跳的）
		agents, err := store.GetAllOnlineAgents()
		if err != nil {
			sendErrorResp(w, "failed to get agents: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 转换为前端需要的格式
		agentList := make([]map[string]interface{}, 0, len(agents))
		for _, agent := range agents {
			agentList = append(agentList, map[string]interface{}{
				"agent_id":       agent.AgentID,
				"online":         agent.Online,
				"last_heartbeat": agent.LastHeartbeat,
				"connected_at":   agent.ConnectedAt,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"agents":  agentList,
		})
	})

	// ========== WebSocket 和聊天室 API ==========

	// 查询聊天室详情（包含历史消息、agent状态和在线用户）
	http.HandleFunc("/api/room/details", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		channelId := r.URL.Query().Get("channel_id")
		if channelId == "" {
			sendErrorResp(w, "channel_id is required", http.StatusBadRequest)
			return
		}

		// 获取聊天室信息
		room, err := store.GetRoom(channelId)
		if err != nil {
			sendErrorResp(w, "room not found", http.StatusNotFound)
			return
		}

		// 获取历史消息
		const maxMessages = 100
		var messages []*A2AMessage
		dbMessages, err := store.GetRoomMessages(channelId, maxMessages)
		if err == nil && len(dbMessages) > 0 {
			messages = make([]*A2AMessage, 0, len(dbMessages))
			for _, m := range dbMessages {
				messages = append(messages, &A2AMessage{
					MsgId:        m.MsgID,
					ChannelId:    m.RoomID,
					Sender:       m.Sender,
					Target:       m.Target,
					MentionUsers: splitMentionUsers(m.MentionUsers),
					Intent:       m.Intent,
					ContentText:  m.ContentText,
					Timestamp:    m.Timestamp,
				})
			}
		}

		// 获取聊天室成员
		members, err := store.GetRoomMembers(channelId)
		if err != nil {
			log.Printf("Warning: failed to get room members: %v", err)
			members = []*storage.Member{}
		}

		// 分离 agent 和 user 成员
		var agents []map[string]interface{}
		var onlineUsers []map[string]interface{}

		for _, member := range members {
			if member.MemberType == "agent" {
				// 获取 agent 状态
				agentStatus, err := store.GetAgentStatus(member.AgentID)
				if err != nil {
					log.Printf("Warning: failed to get agent status for %s: %v", member.AgentID, err)
					continue
				}

				agents = append(agents, map[string]interface{}{
					"agent_id":       member.AgentID,
					"online":         agentStatus.Online,
					"last_heartbeat": agentStatus.LastHeartbeat,
					"joined_at":      member.JoinedAt,
				})
			} else if member.MemberType == "user" {
				// 获取用户信息（根据 user_<id> 格式解析）
				var userID uint
				if strings.HasPrefix(member.AgentID, "user_") {
					if _, err := fmt.Sscanf(member.AgentID[len("user_"):], "%d", &userID); err == nil {
						user, err := store.GetUserByID(userID)
						if err == nil {
							onlineUsers = append(onlineUsers, map[string]interface{}{
								"user_id":   userID,
								"username":  user.Username,
								"nickname":  user.Nickname,
								"joined_at": member.JoinedAt,
								"online":    member.Online,
							})
						}
					}
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"room": map[string]interface{}{
				"id":         room.ID,
				"name":       room.Name,
				"created_at": room.CreatedAt,
				"updated_at": room.UpdatedAt,
			},
			"messages":    messages,
			"agents":      agents,
			"onlineUsers": onlineUsers,
		})
	})

	http.HandleFunc("/ws/chat", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// 获取聊天室成员在线状态（从数据库读取，支持 x-client HTTP 心跳模式）
	http.HandleFunc("/api/room/members", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		channelId := r.URL.Query().Get("channel_id")
		if channelId == "" {
			sendErrorResp(w, "channel_id is required", http.StatusBadRequest)
			return
		}

		// 获取聊天室信息（包括初始 agents）
		room, err := store.GetRoom(channelId)
		if err != nil {
			sendErrorResp(w, "room not found", http.StatusNotFound)
			return
		}

		// 从数据库获取现有成员
		members, err := store.GetRoomMembers(channelId)
		if err != nil {
			sendErrorResp(w, "failed to get members: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 建立成员 map，方便查找
		memberMap := make(map[string]*storage.Member)
		for _, m := range members {
			if m.LeftAt == nil {
				memberMap[m.AgentID] = m
			}
		}

		// 补偿逻辑：以 agents 表为准，同步 members 表
		// 1. 检查 initial_agents 中是否有成员缺失，需要补充
		for _, agentID := range room.InitialAgents {
			if _, exists := memberMap[agentID]; !exists {
				// 检查该 agent 是否存在且在线
				agent, _ := store.GetAgentStatus(agentID)
				if agent != nil && agent.Online && time.Since(agent.LastHeartbeat) < 60*time.Second {
					// 补充缺失的成员
					store.AddMember(&storage.Member{
						RoomID:     channelId,
						AgentID:    agentID,
						MemberType: "agent",
						Online:     true,
					})
					log.Printf("Compensated: added missing member %s to room %s", agentID, channelId)
				}
			}
		}

		// 2. 检查现有成员，补偿在线状态或清理离线成员
		for agentID, member := range memberMap {
			if member.MemberType != "agent" {
				continue
			}

			agent, err := store.GetAgentStatus(agentID)
			isAgentOnline := err == nil && agent != nil && agent.Online && time.Since(agent.LastHeartbeat) < 60*time.Second

			if isAgentOnline && !member.Online {
				// agents 表在线但 members 表离线 -> 同步为在线
				store.UpdateMemberOnline(channelId, agentID, true)
				log.Printf("Compensated: updated member %s to online in room %s", agentID, channelId)
			} else if !isAgentOnline && member.Online {
				// agents 表离线但 members 表在线 -> 标记为离线
				store.UpdateMemberOnline(channelId, agentID, false)
				log.Printf("Compensated: updated member %s to offline in room %s", agentID, channelId)
			}
		}

		// 重新获取成员列表（包含补偿后的数据）
		members, _ = store.GetRoomMembers(channelId)

		// 构建返回结果，agent 的在线状态从 agents 表读取
		var result []map[string]interface{}
		for _, m := range members {
			if m.LeftAt != nil {
				continue
			}

			item := map[string]interface{}{
				"agent_id":    m.AgentID,
				"member_type": m.MemberType,
				"joined_at":   m.JoinedAt.Unix(),
			}

			// agent 的在线状态从 agents 表读取
			if m.MemberType == "agent" {
				agent, err := store.GetAgentStatus(m.AgentID)
				if err == nil && agent != nil {
					item["online"] = agent.Online && time.Since(agent.LastHeartbeat) < 60*time.Second
				} else {
					item["online"] = false
				}
			} else {
				item["online"] = m.Online
			}

			result = append(result, item)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"channel_id": channelId,
			"members":    result,
		})
	})

	// 获取聊天室历史消息
	http.HandleFunc("/api/room/history", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		channelId := r.URL.Query().Get("channel_id")
		if channelId == "" {
			sendErrorResp(w, "channel_id is required", http.StatusBadRequest)
			return
		}

		count := 100 // 默认返回最近100条消息
		if countStr := r.URL.Query().Get("count"); countStr != "" {
			if c, err := strconv.Atoi(countStr); err == nil && c > 0 {
				count = c
			}
		}

		// 从数据库获取历史消息（不依赖内存中的聊天室是否存在）
		var messages []*A2AMessage

		if hub.storage != nil {
			dbMessages, err := hub.storage.GetRoomMessages(channelId, count)
			if err == nil && len(dbMessages) > 0 {
				messages = make([]*A2AMessage, 0, len(dbMessages))
				for _, m := range dbMessages {
					messages = append(messages, &A2AMessage{
						MsgId:        m.MsgID,
						ChannelId:    m.RoomID,
						Sender:       m.Sender,
						Target:       m.Target,
						MentionUsers: splitMentionUsers(m.MentionUsers),
						Intent:       m.Intent,
						ContentText:  m.ContentText,
						Timestamp:    m.Timestamp,
					})
				}
			}
		}

		// 如果数据库没有，返回空数组
		if messages == nil {
			messages = []*A2AMessage{}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"channel_id": channelId,
			"messages":   messages,
		})
	})

	http.HandleFunc("/api/rooms", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 从数据库获取聊天室列表（不依赖内存）
		dbRooms, err := store.GetAllRooms()
		if err != nil {
			sendErrorResp(w, "failed to get rooms: "+err.Error(), http.StatusInternalServerError)
			return
		}

		rooms := make([]RoomInfo, 0, len(dbRooms))
		for _, room := range dbRooms {
			roomInfo := RoomInfo{
				ID:      room.ID,
				Name:    room.Name,
				Created: room.CreatedAt.Unix(),
			}
			rooms = append(rooms, roomInfo)
		}

		log.Printf("[API] /api/rooms: found %d rooms from database", len(rooms))
		for _, room := range rooms {
			log.Printf("[API]   Room: %s (%s)", room.Name, room.ID)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"rooms":   rooms,
		})
	})

	http.HandleFunc("/api/room/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Name   string   `json:"name"`
			Agents []string `json:"agents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Name == "" {
			http.Error(w, "Room name is required", http.StatusBadRequest)
			return
		}

		// 生成唯一的聊天室 ID（使用纯数字后缀，避免用户输入的名字影响 ID 格式）
		timestamp := time.Now().UnixNano()
		roomId := fmt.Sprintf("room_%d", timestamp)

		// 创建聊天室
		room := hub.GetOrCreateRoom(roomId)
		if room != nil {
			room.name = req.Name
			// 保存创建时选择的初始 agents
			room.SetInitialAgents(req.Agents)
			// 更新数据库中的房间信息，包括名称和初始agents
			store.UpdateRoom(&storage.Room{
				ID:            roomId,
				Name:          req.Name,
				InitialAgents: req.Agents,
				CreatedAt:     time.Now(), // 设置创建时间
			})
		}

		// 让选中的agent加入该频道
		hub.JoinAgentsToChannel(roomId, req.Agents)

		// 同时检查已注册的 agent，直接添加到 members 表（因为 x-client 没有 WebSocket 连接）
		for _, agentID := range req.Agents {
			// 检查 agent 是否已注册
			agent, err := store.GetAgentStatus(agentID)
			if err != nil || agent == nil {
				log.Printf("Agent %s not registered, skipping member addition", agentID)
				continue
			}

			// 添加到 members 表
			member := &storage.Member{
				RoomID:     roomId,
				AgentID:    agentID,
				MemberType: "agent",
				Online:     agent.Online, // 使用 agents 表中的在线状态
			}
			if err := store.AddMember(member); err != nil {
				log.Printf("Warning: failed to add agent %s to room %s: %v", agentID, roomId, err)
			} else {
				log.Printf("Agent %s added to room %s (via room creation)", agentID, roomId)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"room_id": roomId,
		})
	})

	http.HandleFunc("/api/room/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			RoomID string `json:"room_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.RoomID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Room ID is required",
			})
			return
		}

		// 只从数据库删除（不操作内存）
		if err := hub.storage.DeleteRoom(req.RoomID); err != nil {
			logError("coordinator", "Failed to delete room from storage: "+err.Error())
			sendErrorResp(w, "failed to delete room: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Room deleted successfully",
		})
	})

	// 加入聊天室
	http.HandleFunc("/api/room/join", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			ChannelID string `json:"channel_id"`
			UserID    uint   `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.ChannelID == "" || req.UserID == 0 {
			sendErrorResp(w, "channel_id and user_id are required", http.StatusBadRequest)
			return
		}

		// 获取用户信息
		user, err := store.GetUserByID(req.UserID)
		if err != nil {
			sendErrorResp(w, "user not found", http.StatusNotFound)
			return
		}

		// 添加用户为聊天室成员
		member := &storage.Member{
			RoomID:     req.ChannelID,
			AgentID:    user.Username, // 直接使用用户名作为成员ID
			MemberType: "user",
			Online:     true, // 用户加入时标记为在线
		}

		// 简化逻辑：直接更新用户的成员状态，不检查是否已存在
		// 使用 GetRoomMembers 获取所有成员，避免使用有问题的 GetRoomMember
		members, _ := store.GetRoomMembers(req.ChannelID)
		isAlreadyMember := false
		for _, m := range members {
			if m.AgentID == member.AgentID {
				isAlreadyMember = true
				// 更新现有成员
				m.Online = true
				m.LastHeartbeat = time.Now()
				store.UpdateMember(m)
				break
			}
		}

		if !isAlreadyMember {
			// 添加新成员
			if err := store.AddMember(member); err != nil {
				sendErrorResp(w, "failed to add member: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("User %s joined room %s", user.Username, req.ChannelID),
		})
	})

	// 退出聊天室
	http.HandleFunc("/api/room/leave", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			ChannelID string `json:"channel_id"`
			UserID    uint   `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.ChannelID == "" || req.UserID == 0 {
			sendErrorResp(w, "channel_id and user_id are required", http.StatusBadRequest)
			return
		}

		// 从聊天室移除用户
		agentID := fmt.Sprintf("user_%d", req.UserID)
		if err := store.RemoveMember(req.ChannelID, agentID); err != nil {
			sendErrorResp(w, "failed to remove member: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 更新在线状态为 false
		store.UpdateMemberOnline(req.ChannelID, agentID, false)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("User %s left room %s", agentID, req.ChannelID),
		})
	})

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"websocket_connections": ` + string(rune(globalMetrics.GetWebSocketConnections()+'0')) + `,
			"speak_conflicts": ` + string(rune(globalMetrics.GetSpeakConflicts()+'0')) + `
		}`))
	})

	srv := &http.Server{
		Addr:              config.ListenAddr,
		ReadHeaderTimeout: 3 * time.Second,
		Handler:           http.DefaultServeMux, // 添加默认的 ServeMux
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		logInfo("coordinator", "Shutting down server...")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			logError("coordinator", "Server forced to shutdown: "+err.Error())
		}
	}()

	logInfo("coordinator", "Coordinator started on "+config.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logError("coordinator", "ListenAndServe failed: "+err.Error())
	}

	logInfo("coordinator", "Server exited")
}
