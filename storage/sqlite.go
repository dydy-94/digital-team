package storage

import (
	"fmt"
	"log"
	"os"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// SQLiteConfig SQLite配置
type SQLiteConfig struct {
	Path string // 数据库文件路径
}

// SQLiteStorage SQLite存储实现
type SQLiteStorage struct {
	db   *gorm.DB
	path string
}

// NewSQLiteStorage 创建SQLite存储
func NewSQLiteStorage(path string) *SQLiteStorage {
	return &SQLiteStorage{
		path: path,
	}
}

// Initialize 初始化数据库连接
func (s *SQLiteStorage) Initialize() error {
	// 检查数据库文件是否存在
	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		// 文件不存在，尝试创建
		file, err := os.Create(s.path)
		if err != nil {
			return fmt.Errorf("failed to create database file: %w", err)
		}
		file.Close()
		log.Printf("Created new database file at: %s", s.path)
	}

	// 尝试设置文件权限
	if err := os.Chmod(s.path, 0777); err != nil {
		log.Printf("Warning: failed to change database file permissions: %v", err)
	}

	db, err := gorm.Open(sqlite.Open(s.path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// 自动迁移表结构（包括新增的 agents 表）
	err = db.AutoMigrate(&Room{}, &Member{}, &Message{}, &Memory{}, &User{}, &Agent{})
	if err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	s.db = db
	log.Printf("SQLite database initialized at: %s", s.path)
	return nil
}

// Close 关闭数据库连接
func (s *SQLiteStorage) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Room 相关实现

func (s *SQLiteStorage) CreateRoom(room *Room) error {
	room.CreatedAt = time.Now()
	room.UpdatedAt = time.Now()
	return s.db.Create(room).Error
}

func (s *SQLiteStorage) GetRoom(id string) (*Room, error) {
	var room Room
	err := s.db.Where("id = ?", id).First(&room).Error
	if err != nil {
		return nil, err
	}
	return &room, nil
}

func (s *SQLiteStorage) GetAllRooms() ([]*Room, error) {
	var rooms []*Room
	err := s.db.Order("created_at desc").Find(&rooms).Error
	return rooms, err
}

func (s *SQLiteStorage) GetRoomsByInitialAgent(agentID string) ([]*Room, error) {
	var rooms []*Room
	err := s.db.Where("initial_agents LIKE ?", "%"+agentID+"%").Find(&rooms).Error
	if err != nil {
		return nil, err
	}
	return rooms, nil
}

func (s *SQLiteStorage) UpdateRoom(room *Room) error {
	room.UpdatedAt = time.Now()
	return s.db.Save(room).Error
}

func (s *SQLiteStorage) DeleteRoom(id string) error {
	// 先删除关联的成员和消息
	s.db.Where("room_id = ?", id).Delete(&Member{})
	s.db.Where("room_id = ?", id).Delete(&Message{})
	return s.db.Where("id = ?", id).Delete(&Room{}).Error
}

// Agent 相关实现（专门记录连接到 coordinator 的 agent）

func (s *SQLiteStorage) RegisterAgent(agentID string, endpoint string) error {
	var existing Agent
	err := s.db.Where("agent_id = ?", agentID).First(&existing).Error
	if err == nil {
		// 已存在，更新状态和端点
		existing.Online = true
		existing.LastHeartbeat = time.Now()
		existing.ConnectedAt = time.Now()
		existing.Endpoint = endpoint
		result := s.db.Save(&existing)
		if result.Error != nil {
			log.Printf("Failed to save existing agent: %v, Error: %v", agentID, result.Error)
		}
		return result.Error
	}

	// 不存在，创建新记录
	agent := &Agent{
		AgentID:       agentID,
		Endpoint:      endpoint,
		Online:        true,
		LastHeartbeat: time.Now(),
		ConnectedAt:   time.Now(),
	}
	result := s.db.Create(agent)
	if result.Error != nil {
		log.Printf("Failed to create new agent: %v, Error: %v", agentID, result.Error)
	}
	return result.Error
}

func (s *SQLiteStorage) UnregisterAgent(agentID string) error {
	var agent Agent
	err := s.db.Where("agent_id = ?", agentID).First(&agent).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}

	agent.Online = false
	return s.db.Save(&agent).Error
}

func (s *SQLiteStorage) UpdateAgentHeartbeat(agentID string) error {
	var agent Agent
	err := s.db.Where("agent_id = ?", agentID).First(&agent).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// 如果 Agent 不存在，注册它（心跳时无法获取端口，设为空）
			return s.RegisterAgent(agentID, "")
		}
		return err
	}

	agent.Online = true
	agent.LastHeartbeat = time.Now()
	return s.db.Save(&agent).Error
}

func (s *SQLiteStorage) GetAgentStatus(agentID string) (*Agent, error) {
	var agent Agent
	err := s.db.Where("agent_id = ?", agentID).First(&agent).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &agent, nil
}

func (s *SQLiteStorage) GetAllOnlineAgents() ([]*Agent, error) {
	var agents []*Agent
	cutoff := time.Now().Add(-30 * time.Second)
	err := s.db.Where("online = ? AND last_heartbeat > ?", true, cutoff).Find(&agents).Error
	return agents, err
}

func (s *SQLiteStorage) GetOfflineAgents(timeout time.Duration) ([]*Agent, error) {
	var agents []*Agent
	cutoff := time.Now().Add(-timeout)
	err := s.db.Where("online = ? AND last_heartbeat < ?", true, cutoff).Find(&agents).Error
	return agents, err
}

// Member 相关实现

func (s *SQLiteStorage) AddMember(member *Member) error {
	// 检查是否已存在
	var existing Member
	err := s.db.Where("room_id = ? AND agent_id = ? AND left_at IS NULL", member.RoomID, member.AgentID).First(&existing).Error
	if err == nil {
		// 已存在，更新joined_at
		existing.JoinedAt = member.JoinedAt
		existing.LeftAt = nil
		return s.db.Save(&existing).Error
	}

	// 不存在，创建新记录
	member.JoinedAt = time.Now()
	return s.db.Create(member).Error
}

func (s *SQLiteStorage) RemoveMember(roomID, agentID string) error {
	now := time.Now()
	return s.db.Model(&Member{}).
		Where("room_id = ? AND agent_id = ? AND left_at IS NULL", roomID, agentID).
		Update("left_at", &now).Error
}

func (s *SQLiteStorage) GetRoomMembers(roomID string) ([]*Member, error) {
	var members []*Member
	err := s.db.Where("room_id = ? AND left_at IS NULL", roomID).Find(&members).Error
	return members, err
}

func (s *SQLiteStorage) GetMemberByAgentID(agentID string) (*Member, error) {
	var member Member
	err := s.db.Where("agent_id = ? AND left_at IS NULL", agentID).First(&member).Error
	if err != nil {
		return nil, err
	}
	return &member, nil
}

// UpdateMemberOnline 更新成员的在线状态
func (s *SQLiteStorage) UpdateMemberOnline(roomID, agentID string, online bool) error {
	return s.db.Model(&Member{}).
		Where("room_id = ? AND agent_id = ? AND left_at IS NULL", roomID, agentID).
		Updates(map[string]interface{}{
			"online":         online,
			"last_heartbeat": time.Now(),
		}).Error
}

// UpdateMemberHeartbeat 更新成员的心跳时间（更新该 Agent 在所有聊天室的状态）
func (s *SQLiteStorage) UpdateMemberHeartbeat(roomID, agentID string) error {
	return s.db.Model(&Member{}).
		Where("agent_id = ? AND left_at IS NULL", agentID).
		Updates(map[string]interface{}{
			"online":         true,
			"last_heartbeat": time.Now(),
		}).Error
}

// GetOfflineMembers 获取超时的离线成员（心跳超时）
func (s *SQLiteStorage) GetOfflineMembers(timeout time.Duration) ([]*Member, error) {
	cutoff := time.Now().Add(-timeout)
	var members []*Member
	err := s.db.Where("online = ? AND last_heartbeat < ? AND left_at IS NULL", true, cutoff).Find(&members).Error
	return members, err
}

// IsAgentOnline 检查 Agent 是否在指定聊天室在线（检查该 Agent 在所有聊天室的心跳）
func (s *SQLiteStorage) IsAgentOnline(roomID, agentId string) (bool, error) {
	var member Member
	// 只检查指定聊天室的记录
	err := s.db.Where("room_id = ? AND agent_id = ? AND left_at IS NULL", roomID, agentId).First(&member).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, err
	}
	// 在线状态为 true 且心跳时间在 30 秒内
	return member.Online && time.Since(member.LastHeartbeat) < 30*time.Second, nil
}

// IsAgentOnlineAnywhere 检查 Agent 是否在任何聊天室在线
func (s *SQLiteStorage) IsAgentOnlineAnywhere(agentId string) (bool, error) {
	var member Member
	err := s.db.Where("agent_id = ? AND left_at IS NULL", agentId).First(&member).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, err
	}
	return member.Online && time.Since(member.LastHeartbeat) < 30*time.Second, nil
}

// GetRoomMember 获取指定聊天室指定成员
func (s *SQLiteStorage) GetRoomMember(roomID, agentID string) (*Member, error) {
	var member Member
	err := s.db.Where("room_id = ? AND agent_id = ?", roomID, agentID).First(&member).Error
	if err != nil {
		return nil, err
	}
	return &member, nil
}

// UpdateMember 更新成员状态
func (s *SQLiteStorage) UpdateMember(member *Member) error {
	return s.db.Save(member).Error
}

// Message 相关实现

func (s *SQLiteStorage) SaveMessage(msg *Message) error {
	msg.CreatedAt = time.Now()
	return s.db.Create(msg).Error
}

func (s *SQLiteStorage) GetRoomMessages(roomID string, limit int) ([]*Message, error) {
	var messages []*Message
	err := s.db.Where("room_id = ?", roomID).
		Order("timestamp desc").
		Limit(limit).
		Find(&messages).Error

	// 反转数组，按时间正序返回
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, err
}

func (s *SQLiteStorage) GetRoomMessagesAfter(roomID string, afterTime time.Time, limit int) ([]*Message, error) {
	var messages []*Message
	err := s.db.Where("room_id = ? AND created_at > ?", roomID, afterTime).
		Order("timestamp asc").
		Limit(limit).
		Find(&messages).Error
	return messages, err
}

// Memory 相关实现

func (s *SQLiteStorage) SaveMemory(agentID, channelID string, memory *Memory) error {
	// 先删除旧记忆
	s.db.Where("agent_id = ? AND channel_id = ?", agentID, channelID).Delete(&Memory{})

	// 保存新记忆
	memory.AgentID = agentID
	memory.ChannelID = channelID
	memory.CreatedAt = time.Now()
	return s.db.Create(memory).Error
}

func (s *SQLiteStorage) GetMemory(agentID, channelID string) (*Memory, error) {
	var memory Memory
	err := s.db.Where("agent_id = ? AND channel_id = ?", agentID, channelID).First(&memory).Error
	if err != nil {
		return nil, err
	}
	return &memory, nil
}

func (s *SQLiteStorage) DeleteMemory(agentID, channelID string) error {
	return s.db.Where("agent_id = ? AND channel_id = ?", agentID, channelID).Delete(&Memory{}).Error
}

// User 相关实现

func (s *SQLiteStorage) CreateUser(user *User) error {
	user.CreatedAt = time.Now()
	user.UpdatedAt = time.Now()
	return s.db.Create(user).Error
}

func (s *SQLiteStorage) GetUserByUsername(username string) (*User, error) {
	var user User
	err := s.db.Where("username = ?", username).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *SQLiteStorage) GetUserByID(id uint) (*User, error) {
	var user User
	err := s.db.Where("id = ?", id).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *SQLiteStorage) UpdateUser(user *User) error {
	user.UpdatedAt = time.Now()
	return s.db.Save(user).Error
}
