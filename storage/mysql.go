package storage

import (
	"fmt"
	"log"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// MySQLConfig MySQL配置
type MySQLConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

// MySQLStorage MySQL存储实现
type MySQLStorage struct {
	db     *gorm.DB
	config *MySQLConfig
}

// NewMySQLStorage 创建MySQL存储
func NewMySQLStorage(config *MySQLConfig) *MySQLStorage {
	return &MySQLStorage{
		config: config,
	}
}

// Initialize 初始化MySQL数据库连接
func (s *MySQLStorage) Initialize() error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		s.config.User, s.config.Password, s.config.Host, s.config.Port, s.config.Database)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return fmt.Errorf("failed to connect to MySQL: %w", err)
	}

	// 自动迁移表结构（包括新增的 agents 表）
	err = db.AutoMigrate(&Room{}, &Member{}, &Message{}, &Memory{}, &User{}, &Agent{})
	if err != nil {
		return fmt.Errorf("failed to migrate MySQL database: %w", err)
	}

	s.db = db
	log.Printf("MySQL database initialized: %s@%s:%d/%s", s.config.User, s.config.Host, s.config.Port, s.config.Database)
	return nil
}

// Close 关闭数据库连接
func (s *MySQLStorage) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// 以下方法与SQLite实现相同...

func (s *MySQLStorage) CreateRoom(room *Room) error {
	return s.db.Create(room).Error
}

func (s *MySQLStorage) GetRoom(id string) (*Room, error) {
	var room Room
	err := s.db.Where("id = ?", id).First(&room).Error
	if err != nil {
		return nil, err
	}
	return &room, nil
}

func (s *MySQLStorage) GetAllRooms() ([]*Room, error) {
	var rooms []*Room
	err := s.db.Order("created_at desc").Find(&rooms).Error
	return rooms, err
}

func (s *MySQLStorage) UpdateRoom(room *Room) error {
	return s.db.Save(room).Error
}

func (s *MySQLStorage) DeleteRoom(id string) error {
	s.db.Where("room_id = ?", id).Delete(&Member{})
	s.db.Where("room_id = ?", id).Delete(&Message{})
	return s.db.Where("id = ?", id).Delete(&Room{}).Error
}

func (s *MySQLStorage) AddMember(member *Member) error {
	var existing Member
	err := s.db.Where("room_id = ? AND agent_id = ? AND left_at IS NULL", member.RoomID, member.AgentID).First(&existing).Error
	if err == nil {
		existing.LeftAt = nil
		return s.db.Save(&existing).Error
	}
	return s.db.Create(member).Error
}

func (s *MySQLStorage) RemoveMember(roomID, agentID string) error {
	return s.db.Model(&Member{}).
		Where("room_id = ? AND agent_id = ? AND left_at IS NULL", roomID, agentID).
		Update("left_at", nil).Error
}

func (s *MySQLStorage) GetRoomMembers(roomID string) ([]*Member, error) {
	var members []*Member
	err := s.db.Where("room_id = ? AND left_at IS NULL", roomID).Find(&members).Error
	return members, err
}

func (s *MySQLStorage) GetMemberByAgentID(agentID string) (*Member, error) {
	var member Member
	err := s.db.Where("agent_id = ? AND left_at IS NULL", agentID).First(&member).Error
	if err != nil {
		return nil, err
	}
	return &member, nil
}

// UpdateMemberOnline 更新成员的在线状态
func (s *MySQLStorage) UpdateMemberOnline(roomID, agentID string, online bool) error {
	return s.db.Model(&Member{}).
		Where("room_id = ? AND agent_id = ? AND left_at IS NULL", roomID, agentID).
		Updates(map[string]interface{}{
			"online":         online,
			"last_heartbeat": time.Now(),
		}).Error
}

// UpdateMemberHeartbeat 更新成员的心跳时间（更新该 Agent 在所有聊天室的状态）
func (s *MySQLStorage) UpdateMemberHeartbeat(roomID, agentID string) error {
	return s.db.Model(&Member{}).
		Where("agent_id = ? AND left_at IS NULL", agentID).
		Updates(map[string]interface{}{
			"online":         true,
			"last_heartbeat": time.Now(),
		}).Error
}

// GetOfflineMembers 获取超时的离线成员（心跳超时）
// Agent 相关实现（专门记录连接到 coordinator 的 agent）

func (s *MySQLStorage) RegisterAgent(agentID string, endpoint string) error {
	var existing Agent
	err := s.db.Where("agent_id = ?", agentID).First(&existing).Error
	if err == nil {
		// 已存在，更新状态和端点
		existing.Online = true
		existing.LastHeartbeat = time.Now()
		existing.ConnectedAt = time.Now()
		existing.Endpoint = endpoint
		return s.db.Save(&existing).Error
	}

	// 不存在，创建新记录
	agent := &Agent{
		AgentID:       agentID,
		Endpoint:      endpoint,
		Online:        true,
		LastHeartbeat: time.Now(),
		ConnectedAt:   time.Now(),
	}
	return s.db.Create(agent).Error
}

func (s *MySQLStorage) UnregisterAgent(agentID string) error {
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

func (s *MySQLStorage) UpdateAgentHeartbeat(agentID string) error {
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

func (s *MySQLStorage) GetAgentStatus(agentID string) (*Agent, error) {
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

func (s *MySQLStorage) GetAllOnlineAgents() ([]*Agent, error) {
	var agents []*Agent
	cutoff := time.Now().Add(-30 * time.Second)
	err := s.db.Where("online = ? AND last_heartbeat > ?", true, cutoff).Find(&agents).Error
	return agents, err
}

func (s *MySQLStorage) GetOfflineAgents(timeout time.Duration) ([]*Agent, error) {
	var agents []*Agent
	cutoff := time.Now().Add(-timeout)
	err := s.db.Where("online = ? AND last_heartbeat < ?", true, cutoff).Find(&agents).Error
	return agents, err
}

func (s *MySQLStorage) GetOfflineMembers(timeout time.Duration) ([]*Member, error) {
	cutoff := time.Now().Add(-timeout)
	var members []*Member
	err := s.db.Where("online = ? AND last_heartbeat < ? AND left_at IS NULL", true, cutoff).Find(&members).Error
	return members, err
}

// IsAgentOnline 检查 Agent 是否在指定聊天室在线
func (s *MySQLStorage) IsAgentOnline(roomID, agentID string) (bool, error) {
	var member Member
	err := s.db.Where("room_id = ? AND agent_id = ? AND left_at IS NULL", roomID, agentID).First(&member).Error
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
func (s *MySQLStorage) IsAgentOnlineAnywhere(agentID string) (bool, error) {
	var member Member
	err := s.db.Where("agent_id = ? AND left_at IS NULL", agentID).First(&member).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, err
	}
	return member.Online && time.Since(member.LastHeartbeat) < 30*time.Second, nil
}

// GetRoomMember 获取指定聊天室指定成员
func (s *MySQLStorage) GetRoomMember(roomID, agentID string) (*Member, error) {
	var member Member
	err := s.db.Where("room_id = ? AND agent_id = ? AND left_at IS NULL", roomID, agentID).First(&member).Error
	if err != nil {
		return nil, err
	}
	return &member, nil
}

func (s *MySQLStorage) SaveMessage(msg *Message) error {
	return s.db.Create(msg).Error
}

func (s *MySQLStorage) GetRoomMessages(roomID string, limit int) ([]*Message, error) {
	var messages []*Message
	err := s.db.Where("room_id = ?", roomID).
		Order("timestamp desc").
		Limit(limit).
		Find(&messages).Error

	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, err
}

func (s *MySQLStorage) GetRoomMessagesAfter(roomID string, afterTime time.Time, limit int) ([]*Message, error) {
	var messages []*Message
	err := s.db.Where("room_id = ? AND created_at > ?", roomID, afterTime).
		Order("timestamp asc").
		Limit(limit).
		Find(&messages).Error
	return messages, err
}

func (s *MySQLStorage) SaveMemory(agentID, channelID string, memory *Memory) error {
	s.db.Where("agent_id = ? AND channel_id = ?", agentID, channelID).Delete(&Memory{})
	memory.AgentID = agentID
	memory.ChannelID = channelID
	return s.db.Create(memory).Error
}

func (s *MySQLStorage) GetMemory(agentID, channelID string) (*Memory, error) {
	var memory Memory
	err := s.db.Where("agent_id = ? AND channel_id = ?", agentID, channelID).First(&memory).Error
	if err != nil {
		return nil, err
	}
	return &memory, nil
}

func (s *MySQLStorage) DeleteMemory(agentID, channelID string) error {
	return s.db.Where("agent_id = ? AND channel_id = ?", agentID, channelID).Delete(&Memory{}).Error
}

// User 相关实现

func (s *MySQLStorage) CreateUser(user *User) error {
	user.CreatedAt = time.Now()
	user.UpdatedAt = time.Now()
	return s.db.Create(user).Error
}

func (s *MySQLStorage) GetUserByUsername(username string) (*User, error) {
	var user User
	err := s.db.Where("username = ?", username).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *MySQLStorage) GetUserByID(id uint) (*User, error) {
	var user User
	err := s.db.Where("id = ?", id).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *MySQLStorage) UpdateUser(user *User) error {
	user.UpdatedAt = time.Now()
	return s.db.Save(user).Error
}
