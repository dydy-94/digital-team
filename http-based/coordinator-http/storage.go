package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// ============ Storage 数据库操作层 ============

type Storage struct {
	db  *sql.DB
	cfg *Config

	// 聊天室成员缓存（内存加速查询）
	memberMu sync.RWMutex
	members  map[string]map[string]*Member // room_id -> member_id -> Member

	// Agent 模板缓存
	templateMu sync.RWMutex
	templates  map[string]*AgentTemplate // agent_id -> AgentTemplate
}

func NewStorage(cfg *Config) (*Storage, error) {
	db, err := sql.Open("mysql", cfg.GetDSN())
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	// 连接池配置
	db.SetMaxOpenConns(100)
	db.SetMaxIdleConns(50)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("数据库 ping 失败: %w", err)
	}

	s := &Storage{
		db:        db,
		cfg:       cfg,
		members:   make(map[string]map[string]*Member),
		templates: make(map[string]*AgentTemplate),
	}

	// 创建用户会话表
	if err := s.createUserRoomSessionsTable(); err != nil {
		slog.Error("创建 user_room_sessions 表失败", "error", err)
	}

	// 创建 Agent 模板表
	if err := s.createAgentTemplatesTable(); err != nil {
		slog.Error("创建 agent_templates 表失败", "error", err)
	}

	// 初始化加载聊天室成员到内存
	if err := s.loadMembersToMemory(); err != nil {
		slog.Error("加载聊天室成员到内存失败", "error", err)
	}

	// 初始化加载模板到内存
	if err := s.loadTemplatesToMemory(); err != nil {
		slog.Error("加载模板到内存失败", "error", err)
	}

	// 启动定期清理任务
	go s.startCleanup()

	return s, nil
}

func (s *Storage) Close() error {
	return s.db.Close()
}

// ============ Agent 操作 ============

// RegisterAgent 注册或更新 Agent
func (s *Storage) RegisterAgent(agentID, endpoint, callbackURL string) error {
	query := `
		INSERT INTO agents (agent_id, endpoint, callback_url, status, last_heartbeat)
		VALUES (?, ?, ?, 'ONLINE', NOW())
		ON DUPLICATE KEY UPDATE
			endpoint = VALUES(endpoint),
			callback_url = VALUES(callback_url),
			status = 'ONLINE',
			last_heartbeat = NOW()
	`
	_, err := s.db.Exec(query, agentID, endpoint, callbackURL)
	return err
}

// UpdateHeartbeat 更新心跳
func (s *Storage) UpdateHeartbeat(agentID string) error {
	query := `UPDATE agents SET status = 'ONLINE', last_heartbeat = NOW() WHERE agent_id = ?`
	_, err := s.db.Exec(query, agentID)
	return err
}

// GetAgent 获取 Agent 信息
func (s *Storage) GetAgent(agentID string) (*Agent, error) {
	query := `SELECT id, agent_id, endpoint, COALESCE(callback_url, ''), status, last_heartbeat, created_at FROM agents WHERE agent_id = ?`
	row := s.db.QueryRow(query, agentID)

	var a Agent
	err := row.Scan(&a.ID, &a.AgentID, &a.Endpoint, &a.CallbackURL, &a.Status, &a.LastHeartbeat, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAgents 获取所有 Agent 列表
func (s *Storage) ListAgents() ([]Agent, error) {
	query := `SELECT id, agent_id, endpoint, COALESCE(callback_url, ''), status, last_heartbeat, created_at FROM agents ORDER BY created_at DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.AgentID, &a.Endpoint, &a.CallbackURL, &a.Status, &a.LastHeartbeat, &a.CreatedAt); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, nil
}

// CheckOfflineAgents 检查离线 Agent 并更新状态
func (s *Storage) CheckOfflineAgents() error {
	query := `
		UPDATE agents
		SET status = 'OFFLINE'
		WHERE status = 'ONLINE'
		  AND last_heartbeat < DATE_SUB(NOW(), INTERVAL ? SECOND)
	`
	timeout := s.cfg.HeartbeatTimeout
	if timeout < 60 {
		timeout = 60
	}
	_, err := s.db.Exec(query, timeout)
	return err
}

// ============ Room 操作 ============

// CreateRoom 创建聊天室
func (s *Storage) CreateRoom(roomID, name, description, createdBy string) error {
	query := `
		INSERT INTO rooms (room_id, name, description, created_by)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE name = VALUES(name), description = VALUES(description)
	`
	_, err := s.db.Exec(query, roomID, name, description, createdBy)
	return err
}

// GetRoom 获取聊天室
func (s *Storage) GetRoom(roomID string) (*Room, error) {
	query := `SELECT id, room_id, name, description, created_by, created_at FROM rooms WHERE room_id = ?`
	row := s.db.QueryRow(query, roomID)

	var r Room
	var desc sql.NullString
	err := row.Scan(&r.ID, &r.RoomID, &r.Name, &desc, &r.CreatedBy, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if desc.Valid {
		r.Description = desc.String
	}
	return &r, nil
}

// GetAllRooms 获取所有聊天室
func (s *Storage) GetAllRooms() ([]*Room, error) {
	query := `SELECT id, room_id, name, description, created_by, created_at FROM rooms ORDER BY created_at DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []*Room
	for rows.Next() {
		var r Room
		var desc sql.NullString
		if err := rows.Scan(&r.ID, &r.RoomID, &r.Name, &desc, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		if desc.Valid {
			r.Description = desc.String
		}
		rooms = append(rooms, &r)
	}
	return rooms, rows.Err()
}

// DeleteRoom 删除聊天室（级联删除 members 表记录）
func (s *Storage) DeleteRoom(roomID string) error {
	// 删除聊天室（members 表通过外键级联删除，或手动删除）
	query := `DELETE FROM rooms WHERE room_id = ?`
	_, err := s.db.Exec(query, roomID)
	if err != nil {
		return err
	}

	// 从内存缓存中移除成员
	s.memberMu.Lock()
	delete(s.members, roomID)
	s.memberMu.Unlock()

	return nil
}

// ============ User 操作 ============

// RegisterUser 注册用户
func (s *Storage) RegisterUser(userID, username, email string) error {
	query := `
		INSERT INTO users (user_id, username, email, status, created_at)
		VALUES (?, ?, ?, 'OFFLINE', NOW())
		ON DUPLICATE KEY UPDATE
			username = VALUES(username),
			email = VALUES(email)
	`
	_, err := s.db.Exec(query, userID, username, email)
	return err
}

// GetUser 获取用户
func (s *Storage) GetUser(userID string) (*User, error) {
	query := `SELECT id, user_id, username, email, status, last_login, created_at FROM users WHERE user_id = ?`
	row := s.db.QueryRow(query, userID)

	var u User
	var email sql.NullString
	var lastLogin sql.NullTime
	err := row.Scan(&u.ID, &u.UserID, &u.Username, &email, &u.Status, &lastLogin, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if email.Valid {
		u.Email = email.String
	}
	if lastLogin.Valid {
		u.LastLogin = lastLogin.Time
	}
	return &u, nil
}

// UpdateUserStatus 更新用户状态
func (s *Storage) UpdateUserStatus(userID, status string) error {
	query := `UPDATE users SET status = ?, last_login = NOW() WHERE user_id = ?`
	_, err := s.db.Exec(query, status, userID)
	return err
}

// ============ Member 操作 ============

// AddMember 添加聊天室成员
func (s *Storage) AddMember(roomID, memberID, memberType string) error {
	query := `
		INSERT INTO members (room_id, member_id, member_type, is_active)
		VALUES (?, ?, ?, TRUE)
		ON DUPLICATE KEY UPDATE
			member_type = VALUES(member_type),
			left_at = NULL,
			is_active = TRUE
	`
	_, err := s.db.Exec(query, roomID, memberID, memberType)

	// 更新内存缓存
	if err == nil {
		s.memberMu.Lock()
		if s.members[roomID] == nil {
			s.members[roomID] = make(map[string]*Member)
		}
		s.members[roomID][memberID] = &Member{
			RoomID:     roomID,
			MemberID:   memberID,
			MemberType: memberType,
			IsActive:   true,
		}
		s.memberMu.Unlock()
	}

	return err
}

// RemoveMember 移除聊天室成员
func (s *Storage) RemoveMember(roomID, memberID string) error {
	query := `UPDATE members SET left_at = NOW(), is_active = FALSE WHERE room_id = ? AND member_id = ?`
	_, err := s.db.Exec(query, roomID, memberID)

	if err == nil {
		s.memberMu.Lock()
		if s.members[roomID] != nil {
			delete(s.members[roomID], memberID)
		}
		s.memberMu.Unlock()
	}

	return err
}

// GetRoomMembers 获取聊天室成员
// 对于 agent 类型的成员，同时返回 agents 表的在线状态
// 对于 user 类型的成员，通过 user_room_sessions 表判断 ws_established
func (s *Storage) GetRoomMembers(roomID string) ([]*Member, error) {
	query := `
		SELECT m.id, m.room_id, m.member_id, m.member_type, m.joined_at, m.left_at, m.is_active,
		       a.status AS agent_status,
		       COALESCE(urs.ws_established, FALSE) AS ws_established
		FROM members m
		LEFT JOIN agents a ON m.member_id = a.agent_id AND m.member_type = 'agent'
		LEFT JOIN user_room_sessions urs ON m.member_id = urs.user_id AND m.room_id = urs.room_id AND urs.ws_established = TRUE
		WHERE m.room_id = ? AND m.is_active = TRUE
	`
	rows, err := s.db.Query(query, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*Member
	for rows.Next() {
		var m Member
		var leftAt sql.NullTime
		var agentStatus sql.NullString
		if err := rows.Scan(&m.ID, &m.RoomID, &m.MemberID, &m.MemberType, &m.JoinedAt, &leftAt, &m.IsActive, &agentStatus, &m.WsEstablished); err != nil {
			return nil, err
		}
		if leftAt.Valid {
			m.LeftAt = &leftAt.Time
		}
		if agentStatus.Valid {
			m.AgentStatus = agentStatus.String
		}
		members = append(members, &m)
	}
	return members, rows.Err()
}

// GetMemberRooms 获取成员所在的所有聊天室
func (s *Storage) GetMemberRooms(memberID string) ([]string, error) {
	query := `SELECT room_id FROM members WHERE member_id = ? AND is_active = TRUE`
	rows, err := s.db.Query(query, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roomIDs []string
	for rows.Next() {
		var roomID string
		if err := rows.Scan(&roomID); err != nil {
			return nil, err
		}
		roomIDs = append(roomIDs, roomID)
	}
	return roomIDs, rows.Err()
}

// IsMemberInRoom 检查成员是否在聊天室中
func (s *Storage) IsMemberInRoom(roomID, memberID string) (bool, error) {
	// 先查内存缓存
	s.memberMu.RLock()
	if room, ok := s.members[roomID]; ok {
		if member, ok := room[memberID]; ok && member.IsActive {
			s.memberMu.RUnlock()
			return true, nil
		}
	}
	s.memberMu.RUnlock()

	// 查数据库
	query := `SELECT COUNT(*) FROM members WHERE room_id = ? AND member_id = ? AND is_active = TRUE`
	var count int
	err := s.db.QueryRow(query, roomID, memberID).Scan(&count)
	return count > 0, err
}

// loadMembersToMemory 加载成员到内存
func (s *Storage) loadMembersToMemory() error {
	query := `SELECT room_id, member_id, member_type FROM members WHERE is_active = TRUE`
	rows, err := s.db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.RoomID, &m.MemberID, &m.MemberType); err != nil {
			continue
		}
		m.IsActive = true

		if s.members[m.RoomID] == nil {
			s.members[m.RoomID] = make(map[string]*Member)
		}
		s.members[m.RoomID][m.MemberID] = &m
	}
	return rows.Err()
}

// ============ Message 操作 ============

// SaveMessage 保存消息
func (s *Storage) SaveMessage(msg *Message) error {
	query := `
		INSERT INTO messages (
			msg_id, room_id, sender_id, sender_type,
			target_id, target_type, mention_users, content, intent,
			status, reply_to_msg_id, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'PENDING', ?, NOW())
		ON DUPLICATE KEY UPDATE content = VALUES(content)
	`

	// mention_users 已经是 JSON 字符串格式
	mentionUsersJSON := msg.MentionUsers
	if mentionUsersJSON == "" {
		mentionUsersJSON = "[]"
	}

	targetID := msg.TargetID
	if targetID == "" {
		targetID = "ALL"
	}
	targetType := msg.TargetType
	if targetType == "" {
		targetType = "BROADCAST"
	}
	intent := msg.Intent
	if intent == "" {
		intent = "INFORM"
	}

	_, err := s.db.Exec(query,
		msg.MsgID, msg.RoomID, msg.SenderID, msg.SenderType,
		targetID, targetType, mentionUsersJSON, msg.Content, intent,
		msg.ReplyToMsgID,
	)
	return err
}

// PollMessages Agent 轮询消息
func (s *Storage) PollMessages(agentID string, since int64, roomID string, limit int) ([]*PollMessage, int64, error) {
	if limit <= 0 {
		limit = s.cfg.PollBatchSize
	}

	// log.Printf("[DEBUG] PollMessages: agentID=%s, since=%d, limit=%d", agentID, since, limit)

	// 查询条件：
	// 1. 消息发送给该 agent (target_id = agentID)
	// 2. 或 广播消息 (target_id = ALL)
	// 3. 该 agent 所在聊天室的消息
	// 4. 且该 agent 尚未接收过这条消息（通过 message_delivery 表判断）
	// 5. created_at > since

	// 先获取 agent 所在的聊天室
	rooms, err := s.GetMemberRooms(agentID)
	if err != nil {
		slog.Error("GetMemberRooms failed", "error", err)
		return nil, 0, err
	}

	// log.Printf("[DEBUG] GetMemberRooms returned %d rooms: %v", len(rooms), rooms)

	if len(rooms) == 0 {
		// log.Printf("[DEBUG] No rooms found for agent %s", agentID)
		return []*PollMessage{}, since, nil
	}

	// 构建查询
	var placeholders []string
	var args []interface{}

	for _, r := range rooms {
		placeholders = append(placeholders, "?")
		args = append(args, r)
	}

	// 参数顺序：rooms, agentID (target_id), since, agentID (sender_id), agentID (delivery), limit
	args = append(args, agentID, since, agentID, agentID, limit)

	// log.Printf("[DEBUG] Query args count: %d, args: %v", len(args), args)

	roomCondition := strings.Join(placeholders, ",")

	query := fmt.Sprintf(`
		SELECT m.msg_id, m.room_id, m.sender_id, m.sender_type,
		       m.content, m.mention_users, m.intent, m.reply_to_msg_id,
		       m.target_id, m.created_at
		FROM messages m
		WHERE m.room_id IN (%s)
		  AND (m.target_id = 'ALL' OR m.target_id = ?)
		  AND m.created_at > FROM_UNIXTIME(?)
		  AND m.sender_id != ?
		  AND m.msg_id NOT IN (
		      SELECT msg_id FROM message_delivery WHERE recipient_id = ?
		  )
		ORDER BY m.created_at ASC
		LIMIT ?
	`, roomCondition)

	// log.Printf("[DEBUG] Query: %s", query)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		slog.Error("PollMessages query failed", "error", err)
		return nil, 0, err
	}
	defer rows.Close()

	var messages []*PollMessage
	var maxCreatedAt int64 = since

	for rows.Next() {
		var m PollMessage
		var mentionUsersJSON string
		var replyTo sql.NullString
		var createdAt time.Time

		if err := rows.Scan(&m.MsgID, &m.RoomID, &m.SenderID, &m.SenderType,
			&m.Content, &mentionUsersJSON, &m.Intent, &replyTo,
			&m.TargetID, &createdAt); err != nil {
			slog.Warn("Scan error", "error", err)
			continue
		}

		// 转换 created_at 为 Unix 时间戳
		m.CreatedAt = createdAt.Unix()

		// log.Printf("[DEBUG] 扫描到消息: msg_id=%s, sender=%s, content=%s", m.MsgID, m.SenderID, m.Content)

		// 解析 mention_users（支持 JSON 数组和逗号分隔的字符串）
		m.MentionUsers = parseMentionUsers(mentionUsersJSON)

		if replyTo.Valid {
			m.ReplyToMsgID = replyTo.String
		}

		// 转换时间戳
		maxCreatedAt = m.CreatedAt

		messages = append(messages, &m)
	}

	// log.Printf("[DEBUG] PollMessages 完成，共 %d 条消息", len(messages))

	return messages, maxCreatedAt, rows.Err()
}

// MarkMessagesDelivered 标记消息已投递
func (s *Storage) MarkMessagesDelivered(msgIDs []string, recipientID string) error {
	if len(msgIDs) == 0 {
		return nil
	}

	query := `INSERT IGNORE INTO message_delivery (msg_id, recipient_id, delivered_at) VALUES (?, ?, NOW())`
	stmt, err := s.db.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, msgID := range msgIDs {
		stmt.Exec(msgID, recipientID)
	}
	return nil
}

// GetPendingNotifications 获取待通知的消息（用户 WebSocket 轮询）
// 查询某个用户所在聊天室的新消息，且尚未通过 WebSocket 通知该用户的
func (s *Storage) GetPendingNotifications(userID string, since time.Time) ([]*NotificationMessage, error) {
	// 1. 获取用户所在的聊天室
	rooms, err := s.GetMemberRooms(userID)
	if err != nil || len(rooms) == 0 {
		return nil, err
	}

	// 2. 构建聊天室查询条件
	placeholders := make([]string, len(rooms))
	args := make([]interface{}, 0, len(rooms)+3)
	for i, r := range rooms {
		placeholders[i] = "?"
		args = append(args, r)
	}
	roomCondition := strings.Join(placeholders, ",")

	// 3. 查询待通知的消息
	// 条件：
	//   - 消息在用户所在的聊天室
	//   - 消息不是用户自己发送的
	//   - 消息尚未通知给该用户 (notified_at IS NULL)
	//   - 消息在指定时间之后
	query := fmt.Sprintf(`
		SELECT m.msg_id, m.room_id, m.sender_id, m.sender_type,
		       m.content, m.mention_users, m.intent, m.reply_to_msg_id,
		       m.target_id, m.created_at
		FROM messages m
		WHERE m.room_id IN (%s)
		  AND m.sender_id != ?
		  AND NOT EXISTS (
		      SELECT 1 FROM message_delivery 
		      WHERE msg_id = m.msg_id 
		      AND recipient_id = ? 
		      AND notified_at IS NOT NULL
		  )
		  AND m.created_at > ?
		ORDER BY m.created_at ASC
		LIMIT 50
	`, roomCondition)

	args = append(args, userID, userID, since)

	// 使用事务确保查询和更新操作的原子性
	tx, err := s.db.Begin()
	if err != nil {
		slog.Error("GetPendingNotifications 开始事务失败", "error", err)
		return nil, err
	}

	rows, err := tx.Query(query, args...)
	if err != nil {
		slog.Error("GetPendingNotifications 查询失败", "error", err)
		tx.Rollback()
		return nil, err
	}
	defer rows.Close()

	var notifications []*NotificationMessage
	var msgIDs []string
	for rows.Next() {
		var n NotificationMessage
		var mentionUsersJSON string
		var replyTo sql.NullString
		var createdAt time.Time

		if err := rows.Scan(&n.MsgID, &n.RoomID, &n.SenderID, &n.SenderType,
			&n.Content, &mentionUsersJSON, &n.Intent, &replyTo,
			&n.TargetID, &createdAt); err != nil {
			slog.Warn("Scan error", "error", err)
			continue
		}

		// 解析 mention_users
		n.MentionUsers = parseMentionUsers(mentionUsersJSON)

		if replyTo.Valid {
			n.ReplyToMsgID = replyTo.String
		}

		// 检查用户是否在消息中被 @ 了
		n.IsMentioned = false
		for _, u := range n.MentionUsers {
			if u == userID {
				n.IsMentioned = true
				break
			}
		}

		notifications = append(notifications, &n)
		msgIDs = append(msgIDs, n.MsgID)
	}

	// 立即更新 notified_at 字段，防止重复查询
	for _, msgID := range msgIDs {
		_, err = tx.Exec(`INSERT INTO message_delivery (msg_id, recipient_id, notified_at) VALUES (?, ?, NOW()) ON DUPLICATE KEY UPDATE notified_at = NOW()`, msgID, userID)
		if err != nil {
			slog.Warn("标记消息已通知失败", "msg_id", msgID, "user_id", userID, "error", err)
		}
	}

	// 提交事务
	if err := tx.Commit(); err != nil {
		slog.Error("GetPendingNotifications 提交事务失败", "error", err)
		return nil, err
	}

	return notifications, rows.Err()
}

// MarkNotificationsSent 标记消息已通知
// 使用 INSERT ... ON DUPLICATE KEY UPDATE 确保即使消息不在 message_delivery 表中也能插入
func (s *Storage) MarkNotificationsSent(msgIDs []string, userID string) error {
	if len(msgIDs) == 0 {
		return nil
	}

	// 使用 INSERT ... ON DUPLICATE KEY UPDATE
	// 如果记录不存在则插入 notified_at，如果已存在则更新 notified_at
	query := `INSERT INTO message_delivery (msg_id, recipient_id, notified_at) VALUES (?, ?, NOW())
	          ON DUPLICATE KEY UPDATE notified_at = NOW()`
	stmt, err := s.db.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, msgID := range msgIDs {
		if _, err := stmt.Exec(msgID, userID); err != nil {
			slog.Warn("MarkNotificationsSent 失败", "msg_id", msgID, "user_id", userID, "error", err)
		}
	}
	return nil
}

// NotificationMessage 通知消息结构
type NotificationMessage struct {
	MsgID        string    `json:"msg_id"`
	RoomID       string    `json:"room_id"`
	SenderID     string    `json:"sender_id"`
	SenderType   string    `json:"sender_type"`
	Content      string    `json:"content"`
	MentionUsers []string  `json:"mention_users"`
	Intent       string    `json:"intent"`
	ReplyToMsgID string    `json:"reply_to_msg_id,omitempty"`
	TargetID     string    `json:"target_id"`
	CreatedAt    time.Time `json:"created_at"`
	IsMentioned  bool      `json:"is_mentioned"` // 是否被 @ 了
}

// GetRecentMessages 获取最近消息
func (s *Storage) GetRecentMessages(roomID string, limit int) ([]*Message, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT msg_id, room_id, sender_id, sender_type, target_id, target_type,
		       mention_users, content, intent, status, reply_to_msg_id, created_at
		FROM messages
		WHERE room_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`

	rows, err := s.db.Query(query, roomID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*Message
	for rows.Next() {
		var m Message
		var mentionUsers sql.NullString
		var replyTo sql.NullString

		if err := rows.Scan(&m.MsgID, &m.RoomID, &m.SenderID, &m.SenderType,
			&m.TargetID, &m.TargetType, &mentionUsers, &m.Content,
			&m.Intent, &m.Status, &replyTo, &m.CreatedAt); err != nil {
			continue
		}

		if mentionUsers.Valid {
			m.MentionUsers = mentionUsers.String
		}
		if replyTo.Valid {
			m.ReplyToMsgID = replyTo.String
		}

		messages = append(messages, &m)
	}

	// 反转数组，按时间正序返回
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

// ============ 清理任务 ============

// startCleanup 启动定期清理
func (s *Storage) startCleanup() {
	ticker := time.NewTicker(s.cfg.GetCleanupInterval())
	defer ticker.Stop()

	for range ticker.C {
		// 检查离线 Agent
		s.CheckOfflineAgents()

		// 清理旧消息
		s.cleanupOldMessages()

		// 清理超时会话（ws_established=FALSE 且 last_active_at 超过5分钟）
		s.CleanupStaleSessions(5 * time.Minute)
	}
}

func (s *Storage) cleanupOldMessages() {
	retentionDays := s.cfg.MessageRetentionDays
	if retentionDays <= 0 {
		retentionDays = 7
	}

	// 清理已投递的旧消息
	query := fmt.Sprintf(`
		DELETE FROM messages
		WHERE status IN ('DELIVERED', 'READ')
		  AND created_at < DATE_SUB(NOW(), INTERVAL %d DAY)
	`, retentionDays)
	s.db.Exec(query)

	// 清理消息投递记录
	query = fmt.Sprintf(`
		DELETE md FROM message_delivery md
		JOIN messages m ON md.msg_id = m.msg_id
		WHERE m.created_at < DATE_SUB(NOW(), INTERVAL %d DAY)
	`, retentionDays)
	s.db.Exec(query)
}

// parseMentionUsers 解析 mention_users 字段
// 支持 JSON 数组格式 ["user1","user2"] 和逗号分隔格式 "user1,user2"
func parseMentionUsers(data string) []string {
	if data == "" || data == "[]" {
		return []string{} // 返回空数组而不是 nil
	}

	// 尝试 JSON 数组解析
	var result []string
	if err := json.Unmarshal([]byte(data), &result); err == nil {
		if result == nil {
			return []string{}
		}
		return result
	}

	// 如果解析失败，尝试逗号分隔
	if strings.Contains(data, ",") {
		parts := strings.Split(data, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}

	// 单个用户
	return []string{data}
}

// ============ User Room Session 操作（支持多 Tab 登录）============

// UserRoomSession 用户房间会话
type UserRoomSession struct {
	ID            int64     `json:"id"`
	UserID        string    `json:"user_id"`
	RoomID        string    `json:"room_id"`
	ConnectionID  string    `json:"connection_id"`
	WsEstablished bool      `json:"ws_established"`
	ConnectedAt   time.Time `json:"connected_at"`
	LastActiveAt  time.Time `json:"last_active_at"`
}

// createUserRoomSessionsTable 创建用户会话表
func (s *Storage) createUserRoomSessionsTable() error {
	query := `
		CREATE TABLE IF NOT EXISTS user_room_sessions (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL,
			room_id VARCHAR(255) NOT NULL,
			connection_id VARCHAR(255) NOT NULL,
			ws_established BOOLEAN DEFAULT FALSE,
			connected_at DATETIME NOT NULL,
			last_active_at DATETIME NOT NULL,
			UNIQUE KEY uk_user_room (user_id, room_id),
			KEY idx_connection (connection_id),
			KEY idx_user (user_id),
			KEY idx_room (room_id)
		)
	`
	_, err := s.db.Exec(query)
	return err
}

// createAgentTemplatesTable 创建 Agent 模板表
func (s *Storage) createAgentTemplatesTable() error {
	query := `
		CREATE TABLE IF NOT EXISTS agent_templates (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			agent_id VARCHAR(64) NOT NULL UNIQUE,
			soul_json TEXT,
			bootstrap_json TEXT,
			meta_json TEXT,
			updated_at BIGINT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at_datetime DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			KEY idx_agent_id (agent_id)
		)
	`
	_, err := s.db.Exec(query)
	return err
}

// loadTemplatesToMemory 从数据库加载模板到内存
func (s *Storage) loadTemplatesToMemory() error {
	query := `SELECT agent_id, soul_json, bootstrap_json, meta_json, updated_at FROM agent_templates`
	rows, err := s.db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	s.templateMu.Lock()
	defer s.templateMu.Unlock()

	for rows.Next() {
		var agentID string
		var soulJSON, bootstrapJSON, metaJSON sql.NullString
		var updatedAt int64

		if err := rows.Scan(&agentID, &soulJSON, &bootstrapJSON, &metaJSON, &updatedAt); err != nil {
			slog.Error("扫描模板行失败", "error", err)
			continue
		}

		template := &AgentTemplate{
			AgentID:   agentID,
			UpdatedAt: updatedAt,
		}

		if soulJSON.Valid && soulJSON.String != "" {
			var soul Soul
			if err := json.Unmarshal([]byte(soulJSON.String), &soul); err == nil {
				template.Soul = &soul
			}
		}

		if bootstrapJSON.Valid && bootstrapJSON.String != "" {
			var bootstrap Bootstrap
			if err := json.Unmarshal([]byte(bootstrapJSON.String), &bootstrap); err == nil {
				template.Bootstrap = &bootstrap
			}
		}

		if metaJSON.Valid && metaJSON.String != "" {
			var meta TemplateMeta
			if err := json.Unmarshal([]byte(metaJSON.String), &meta); err == nil {
				template.Meta = &meta
			}
		}

		s.templates[agentID] = template
	}

	slog.Info("[Template] 已加载模板到内存", "count", len(s.templates))
	return nil
}

// ============ Task 操作 ============

// CreateTask 创建任务
func (s *Storage) CreateTask(task *Task) error {
	query := `
		INSERT INTO tasks (task_id, title, description, status, priority,
		                   created_by, assigned_to, room_id, parent_task_id,
		                   created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query,
		task.TaskID, task.Title, task.Description, task.Status, task.Priority,
		task.CreatedBy, task.AssignedTo, task.RoomID, task.ParentTaskID,
		task.CreatedAt, task.UpdatedAt)
	return err
}

// GetTask 获取任务
func (s *Storage) GetTask(taskID string) (*Task, error) {
	query := `SELECT id, task_id, title, description, status, priority,
	                 created_by, assigned_to, room_id, parent_task_id,
	                 created_at, updated_at, completed_at
	          FROM tasks WHERE task_id = ?`
	row := s.db.QueryRow(query, taskID)

	var t Task
	var parentTaskID sql.NullString
	var completedAt sql.NullInt64

	err := row.Scan(&t.ID, &t.TaskID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&t.CreatedBy, &t.AssignedTo, &t.RoomID, &parentTaskID,
		&t.CreatedAt, &t.UpdatedAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if parentTaskID.Valid {
		t.ParentTaskID = parentTaskID.String
	}
	if completedAt.Valid {
		t.CompletedAt = completedAt.Int64
	}
	return &t, nil
}

// GetTasksByIDs 批量获取任务
func (s *Storage) GetTasksByIDs(taskIDs []string) ([]Task, error) {
	if len(taskIDs) == 0 {
		return []Task{}, nil
	}

	// 构建 IN 子句
	query := `SELECT id, task_id, title, description, status, priority,
	                 created_by, assigned_to, room_id, parent_task_id,
	                 created_at, updated_at, completed_at
	          FROM tasks WHERE task_id IN (?`
	args := make([]interface{}, len(taskIDs))
	args[0] = taskIDs[0]
	for i := 1; i < len(taskIDs); i++ {
		query += ",?"
		args[i] = taskIDs[i]
	}
	query += ")"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		var parentTaskID sql.NullString
		var completedAt sql.NullInt64

		err := rows.Scan(&t.ID, &t.TaskID, &t.Title, &t.Description, &t.Status, &t.Priority,
			&t.CreatedBy, &t.AssignedTo, &t.RoomID, &parentTaskID,
			&t.CreatedAt, &t.UpdatedAt, &completedAt)
		if err != nil {
			return nil, err
		}
		if parentTaskID.Valid {
			t.ParentTaskID = parentTaskID.String
		}
		if completedAt.Valid {
			t.CompletedAt = completedAt.Int64
		}
		tasks = append(tasks, t)
	}

	return tasks, rows.Err()
}

// UpdateTask 更新任务
func (s *Storage) UpdateTask(taskID string, req *UpdateTaskRequest) error {
	query := `UPDATE tasks SET title = COALESCE(NULLIF(?, ''), title),
	                           description = COALESCE(NULLIF(?, ''), description),
	                           status = COALESCE(NULLIF(?, ''), status),
	                           priority = CASE WHEN ? > 0 THEN ? ELSE priority END,
	                           updated_at = ?
	          WHERE task_id = ?`
	now := time.Now().Unix()
	_, err := s.db.Exec(query, req.Title, req.Description, req.Status, req.Priority, req.Priority, now, taskID)
	return err
}

// DeleteTask 删除任务
func (s *Storage) DeleteTask(taskID string) error {
	query := `DELETE FROM tasks WHERE task_id = ?`
	_, err := s.db.Exec(query, taskID)
	return err
}

// GetTasksByRoom 获取聊天室的任务
func (s *Storage) GetTasksByRoom(roomID string) ([]*Task, error) {
	query := `SELECT id, task_id, title, description, status, priority,
	                 created_by, assigned_to, room_id, parent_task_id,
	                 created_at, updated_at, completed_at
	          FROM tasks WHERE room_id = ? ORDER BY created_at DESC`
	rows, err := s.db.Query(query, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var t Task
		var parentTaskID sql.NullString
		var completedAt sql.NullInt64

		if err := rows.Scan(&t.ID, &t.TaskID, &t.Title, &t.Description, &t.Status, &t.Priority,
			&t.CreatedBy, &t.AssignedTo, &t.RoomID, &parentTaskID,
			&t.CreatedAt, &t.UpdatedAt, &completedAt); err != nil {
			continue
		}
		if parentTaskID.Valid {
			t.ParentTaskID = parentTaskID.String
		}
		if completedAt.Valid {
			t.CompletedAt = completedAt.Int64
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

// GetTasksByAgent 获取 Agent 被分配的任务
func (s *Storage) GetTasksByAgent(agentID string) ([]*Task, error) {
	query := `SELECT id, task_id, title, description, status, priority,
	                 created_by, assigned_to, room_id, parent_task_id,
	                 created_at, updated_at, completed_at
	          FROM tasks WHERE assigned_to = ? ORDER BY created_at DESC`
	rows, err := s.db.Query(query, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var t Task
		var parentTaskID sql.NullString
		var completedAt sql.NullInt64

		if err := rows.Scan(&t.ID, &t.TaskID, &t.Title, &t.Description, &t.Status, &t.Priority,
			&t.CreatedBy, &t.AssignedTo, &t.RoomID, &parentTaskID,
			&t.CreatedAt, &t.UpdatedAt, &completedAt); err != nil {
			continue
		}
		if parentTaskID.Valid {
			t.ParentTaskID = parentTaskID.String
		}
		if completedAt.Valid {
			t.CompletedAt = completedAt.Int64
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

// ============ Focus Item 操作 ============

// CreateFocusItem 创建关注点
func (s *Storage) CreateFocusItem(item *FocusItem) error {
	query := `
		INSERT INTO focus_items (item_id, task_id, content, status, agent_id, room_id, item_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query,
		item.ItemID, item.TaskID, item.Content, item.Status, item.AgentID, item.RoomID, item.ItemOrder,
		item.CreatedAt, item.UpdatedAt)
	return err
}

// GetFocusItemsByTask 获取任务的所有关注点
func (s *Storage) GetFocusItemsByTask(taskID string) ([]*FocusItem, error) {
	query := `SELECT id, item_id, task_id, content, status, agent_id, room_id, item_order, created_at, updated_at
	          FROM focus_items WHERE task_id = ? ORDER BY item_order ASC`
	rows, err := s.db.Query(query, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*FocusItem
	for rows.Next() {
		var it FocusItem
		if err := rows.Scan(&it.ID, &it.ItemID, &it.TaskID, &it.Content, &it.Status,
			&it.AgentID, &it.RoomID, &it.ItemOrder, &it.CreatedAt, &it.UpdatedAt); err != nil {
			continue
		}
		items = append(items, &it)
	}
	return items, rows.Err()
}

// UpdateFocusItem 更新关注点
func (s *Storage) UpdateFocusItem(itemID string, req *UpdateFocusItemRequest) error {
	query := `UPDATE focus_items SET content = COALESCE(NULLIF(?, ''), content),
	                                status = COALESCE(NULLIF(?, ''), status),
	                                updated_at = ?
	          WHERE item_id = ?`
	_, err := s.db.Exec(query, req.Content, req.Status, time.Now().Unix(), itemID)
	return err
}

// DeleteFocusItem 删除关注点
func (s *Storage) DeleteFocusItem(itemID string) error {
	query := `DELETE FROM focus_items WHERE item_id = ?`
	_, err := s.db.Exec(query, itemID)
	return err
}

// ============ Permission 操作 ============

// GetPermission 获取 Agent 权限
func (s *Storage) GetPermission(agentID string) (*AgentPermission, error) {
	query := `SELECT id, agent_id, level, allowed_tools, denied_tools,
	                 daily_token_limit, monthly_token_limit, file_size_limit_mb, message_limit_per_hour,
	                 created_at, updated_at
	          FROM agent_permissions WHERE agent_id = ?`
	row := s.db.QueryRow(query, agentID)

	var p AgentPermission
	var allowedTools, deniedTools sql.NullString

	err := row.Scan(&p.ID, &p.AgentID, &p.Level, &allowedTools, &deniedTools,
		&p.DailyTokenLimit, &p.MonthlyTokenLimit, &p.FileSizeLimitMB, &p.MessageLimitPerHour,
		&p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if allowedTools.Valid {
		p.AllowedTools = allowedTools.String
	}
	if deniedTools.Valid {
		p.DeniedTools = deniedTools.String
	}
	return &p, nil
}

// UpsertPermission 创建或更新权限
func (s *Storage) UpsertPermission(agentID string, req *UpsertPermissionRequest) error {
	allowedToolsJSON := "[]"
	if len(req.AllowedTools) > 0 {
		data, _ := json.Marshal(req.AllowedTools)
		allowedToolsJSON = string(data)
	}
	deniedToolsJSON := "[]"
	if len(req.DeniedTools) > 0 {
		data, _ := json.Marshal(req.DeniedTools)
		deniedToolsJSON = string(data)
	}

	now := time.Now().Unix()
	query := `
		INSERT INTO agent_permissions (agent_id, level, allowed_tools, denied_tools,
		                              daily_token_limit, monthly_token_limit, file_size_limit_mb, message_limit_per_hour,
		                              created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			level = VALUES(level),
			allowed_tools = VALUES(allowed_tools),
			denied_tools = VALUES(denied_tools),
			daily_token_limit = VALUES(daily_token_limit),
			monthly_token_limit = VALUES(monthly_token_limit),
			file_size_limit_mb = VALUES(file_size_limit_mb),
			message_limit_per_hour = VALUES(message_limit_per_hour),
			updated_at = VALUES(updated_at)
	`
	_, err := s.db.Exec(query, agentID, req.Level, allowedToolsJSON, deniedToolsJSON,
		req.DailyTokenLimit, req.MonthlyTokenLimit, req.FileSizeLimitMB, req.MessageLimitPerHour,
		now, now)
	return err
}

// DeletePermission 删除权限
func (s *Storage) DeletePermission(agentID string) error {
	query := `DELETE FROM agent_permissions WHERE agent_id = ?`
	_, err := s.db.Exec(query, agentID)
	return err
}

// ============ FileTransfer 操作 ============

// CreateFileTransfer 创建文件传输记录
func (s *Storage) CreateFileTransfer(ft *FileTransfer) error {
	query := `
		INSERT INTO file_transfers (transfer_id, file_name, file_size, mime_type,
		                            from_agent, to_agent, room_id, task_id, s3_key, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query,
		ft.TransferID, ft.FileName, ft.FileSize, ft.MimeType,
		ft.FromAgent, ft.ToAgent, ft.RoomID, ft.TaskID, ft.S3Key, ft.Status, ft.CreatedAt)
	return err
}

// GetFileTransfer 获取文件传输记录
func (s *Storage) GetFileTransfer(transferID string) (*FileTransfer, error) {
	query := `SELECT id, transfer_id, file_name, file_size, mime_type,
	                 from_agent, to_agent, room_id, task_id, s3_key, status, created_at, completed_at
	          FROM file_transfers WHERE transfer_id = ?`
	row := s.db.QueryRow(query, transferID)

	var ft FileTransfer
	var toAgent, taskID sql.NullString
	var completedAt sql.NullInt64

	err := row.Scan(&ft.ID, &ft.TransferID, &ft.FileName, &ft.FileSize, &ft.MimeType,
		&ft.FromAgent, &toAgent, &ft.RoomID, &taskID, &ft.S3Key, &ft.Status, &ft.CreatedAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if toAgent.Valid {
		ft.ToAgent = toAgent.String
	}
	if taskID.Valid {
		ft.TaskID = taskID.String
	}
	if completedAt.Valid {
		ft.CompletedAt = completedAt.Int64
	}
	return &ft, nil
}

// UpdateFileTransferStatus 更新文件传输状态
func (s *Storage) UpdateFileTransferStatus(transferID, status string) error {
	query := `UPDATE file_transfers SET status = ?, completed_at = ? WHERE transfer_id = ?`
	var completedAt interface{}
	if status == "completed" || status == "failed" {
		completedAt = time.Now().Unix()
	}
	_, err := s.db.Exec(query, status, completedAt, transferID)
	return err
}

// GetFileTransfersByRoom 获取聊天室的文件传输记录
func (s *Storage) GetFileTransfersByRoom(roomID string) ([]*FileTransfer, error) {
	query := `SELECT id, transfer_id, file_name, file_size, mime_type,
	                 from_agent, to_agent, room_id, task_id, s3_key, status, created_at, completed_at
	          FROM file_transfers WHERE room_id = ? ORDER BY created_at DESC`
	rows, err := s.db.Query(query, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transfers []*FileTransfer
	for rows.Next() {
		var ft FileTransfer
		var toAgent, taskID sql.NullString
		var completedAt sql.NullInt64

		if err := rows.Scan(&ft.ID, &ft.TransferID, &ft.FileName, &ft.FileSize, &ft.MimeType,
			&ft.FromAgent, &toAgent, &ft.RoomID, &taskID, &ft.S3Key, &ft.Status, &ft.CreatedAt, &completedAt); err != nil {
			continue
		}
		if toAgent.Valid {
			ft.ToAgent = toAgent.String
		}
		if taskID.Valid {
			ft.TaskID = taskID.String
		}
		if completedAt.Valid {
			ft.CompletedAt = completedAt.Int64
		}
		transfers = append(transfers, &ft)
	}
	return transfers, rows.Err()
}

// CreateUserRoomSession 创建用户房间会话（ws_established 初始为 false）
// 返回会话 ID
func (s *Storage) CreateUserRoomSession(userID, roomID, connectionID string) (int64, error) {
	// 创建新会话
	insertQuery := `
		INSERT INTO user_room_sessions (user_id, room_id, connection_id, ws_established, connected_at, last_active_at)
		VALUES (?, ?, ?, FALSE, NOW(), NOW())
	`
	result, err := s.db.Exec(insertQuery, userID, roomID, connectionID)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// CheckAndCreateUserRoomSession 检查并创建用户房间会话
// 如果存在 ws_established=true 的会话，返回错误
// 如果存在 ws_established=false 的会话，返回该会话 ID
// 如果不存在，创建新会话并返回 ID
func (s *Storage) CheckAndCreateUserRoomSession(userID, roomID, connectionID string) (int64, bool, error) {
	// 先检查是否已存在 ws_established=true 的会话
	query := `SELECT id, ws_established FROM user_room_sessions WHERE user_id = ? AND room_id = ?`
	var sessionID int64
	var wsEstablished bool
	err := s.db.QueryRow(query, userID, roomID).Scan(&sessionID, &wsEstablished)
	if err == sql.ErrNoRows {
		// 不存在，创建新会话
		id, err := s.CreateUserRoomSession(userID, roomID, connectionID)
		return id, false, err
	} else if err != nil {
		return 0, false, err
	}

	// 存在，检查 ws_established 状态
	if wsEstablished {
		// 已有活跃 WS 连接
		return 0, false, fmt.Errorf("用户已在该聊天室有活跃连接")
	}

	// 存在但 ws 未建立，更新 connection_id
	updateQuery := `UPDATE user_room_sessions SET connection_id = ?, last_active_at = NOW() WHERE id = ?`
	_, err = s.db.Exec(updateQuery, connectionID, sessionID)
	return sessionID, true, err
}

// UpdateUserRoomSessionWsEstablished 更新会话的 ws_established 状态
func (s *Storage) UpdateUserRoomSessionWsEstablished(connectionID string, established bool) error {
	query := `UPDATE user_room_sessions SET ws_established = ? WHERE connection_id = ?`
	_, err := s.db.Exec(query, established, connectionID)
	return err
}

// UpdateUserRoomSessionWsEstablishedWithConnection 验证并更新会话的 ws_established 状态
// 确保会话存在且属于指定的用户和房间，然后更新 connection_id 和 ws_established
func (s *Storage) UpdateUserRoomSessionWsEstablishedWithConnection(sessionID int64, connectionID, userID, roomID string) error {
	query := `UPDATE user_room_sessions SET connection_id = ?, ws_established = TRUE WHERE id = ? AND user_id = ? AND room_id = ?`
	result, err := s.db.Exec(query, connectionID, sessionID, userID, roomID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("会话不存在或不属于该用户")
	}
	return nil
}

// DeleteUserRoomSession 删除用户房间会话
func (s *Storage) DeleteUserRoomSession(userID, roomID string) error {
	query := `DELETE FROM user_room_sessions WHERE user_id = ? AND room_id = ?`
	_, err := s.db.Exec(query, userID, roomID)
	return err
}

// DeleteUserRoomSessionByConnection 删除用户房间会话（按连接ID）
// 同时更新 members 表的 is_active 状态
func (s *Storage) DeleteUserRoomSessionByConnection(connectionID string) error {
	// 先获取该连接对应的 user_id 和 room_id，用于更新 members 表
	var userID, roomID string
	query := `SELECT user_id, room_id FROM user_room_sessions WHERE connection_id = ?`
	err := s.db.QueryRow(query, connectionID).Scan(&userID, &roomID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	// 删除会话
	delQuery := `DELETE FROM user_room_sessions WHERE connection_id = ?`
	_, err = s.db.Exec(delQuery, connectionID)
	if err != nil {
		return err
	}

	// 如果成功获取到 userID 和 roomID，更新 members 表的 is_active
	if userID != "" && roomID != "" {
		// 检查是否还有其他活跃的会话
		var count int
		checkQuery := `SELECT COUNT(*) FROM user_room_sessions WHERE user_id = ? AND room_id = ? AND ws_established = TRUE`
		s.db.QueryRow(checkQuery, userID, roomID).Scan(&count)
		if count == 0 {
			// 没有其他活跃会话，更新 members 表
			updateQuery := `UPDATE members SET is_active = FALSE, left_at = NOW() WHERE member_id = ? AND room_id = ? AND is_active = TRUE`
			s.db.Exec(updateQuery, userID, roomID)
		}
	}
	return nil
}

// GetUserRoomSessions 获取用户的所有房间会话
func (s *Storage) GetUserRoomSessions(userID string) ([]*UserRoomSession, error) {
	query := `SELECT id, user_id, room_id, connection_id, ws_established, connected_at, last_active_at FROM user_room_sessions WHERE user_id = ?`
	rows, err := s.db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*UserRoomSession
	for rows.Next() {
		var s UserRoomSession
		if err := rows.Scan(&s.ID, &s.UserID, &s.RoomID, &s.ConnectionID, &s.WsEstablished, &s.ConnectedAt, &s.LastActiveAt); err != nil {
			continue
		}
		sessions = append(sessions, &s)
	}
	return sessions, rows.Err()
}

// GetOnlineUsersInRoom 获取聊天室中的在线用户连接
func (s *Storage) GetOnlineUsersInRoom(roomID string) ([]*UserRoomSession, error) {
	query := `SELECT id, user_id, room_id, connection_id, ws_established, connected_at, last_active_at FROM user_room_sessions WHERE room_id = ?`
	rows, err := s.db.Query(query, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*UserRoomSession
	for rows.Next() {
		var s UserRoomSession
		if err := rows.Scan(&s.ID, &s.UserID, &s.RoomID, &s.ConnectionID, &s.WsEstablished, &s.ConnectedAt, &s.LastActiveAt); err != nil {
			continue
		}
		sessions = append(sessions, &s)
	}
	return sessions, rows.Err()
}

// UpdateUserRoomSessionActivity 更新会话活跃时间
func (s *Storage) UpdateUserRoomSessionActivity(connectionID string) error {
	query := `UPDATE user_room_sessions SET last_active_at = NOW() WHERE connection_id = ?`
	_, err := s.db.Exec(query, connectionID)
	return err
}

// CleanupStaleSessions 清理超时会话
// 清理 ws_established=FALSE 且 last_active_at 超过 maxAge 的会话
// 同时更新对应 members 表的 is_active 状态
func (s *Storage) CleanupStaleSessions(maxAge time.Duration) error {
	// 查询需要清理的会话
	query := `SELECT user_id, room_id FROM user_room_sessions WHERE last_active_at < ? AND ws_established = FALSE`
	rows, err := s.db.Query(query, time.Now().Add(-maxAge))
	if err != nil {
		return err
	}
	defer rows.Close()

	// 收集需要更新 is_active 的 member
	type memberKey struct {
		userID string
		roomID string
	}
	var membersToUpdate []memberKey

	for rows.Next() {
		var userID, roomID string
		if err := rows.Scan(&userID, &roomID); err != nil {
			continue
		}
		membersToUpdate = append(membersToUpdate, memberKey{userID: userID, roomID: roomID})
	}

	// 删除超时会话
	delQuery := `DELETE FROM user_room_sessions WHERE last_active_at < ? AND ws_established = FALSE`
	_, err = s.db.Exec(delQuery, time.Now().Add(-maxAge))
	if err != nil {
		return err
	}

	// 更新对应成员的 is_active 状态
	for _, mk := range membersToUpdate {
		// 检查是否还有其他活跃会话
		var count int
		checkQuery := `SELECT COUNT(*) FROM user_room_sessions WHERE user_id = ? AND room_id = ? AND ws_established = TRUE`
		s.db.QueryRow(checkQuery, mk.userID, mk.roomID).Scan(&count)
		if count == 0 {
			updateQuery := `UPDATE members SET is_active = FALSE, left_at = NOW() WHERE member_id = ? AND room_id = ? AND is_active = TRUE`
			s.db.Exec(updateQuery, mk.userID, mk.roomID)
		}
	}

	return nil
}

// ============ Agent 关系操作 ============

// CreateRelation 创建关系
func (s *Storage) CreateRelation(rel *AgentRelation) (int64, error) {
	query := `
		INSERT INTO agent_relations (agent_id, relation_type, related_agent_id, room_id, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, NOW(), NOW())
		ON DUPLICATE KEY UPDATE description = VALUES(description), updated_at = NOW()
	`
	result, err := s.db.Exec(query, rel.AgentID, rel.RelationType, rel.RelatedAgentID, rel.RoomID, rel.Description)
	if err != nil {
		return 0, err
	}
	id, _ := result.LastInsertId()
	return id, nil
}

// GetAgentRelations 获取 Agent 的所有关系
func (s *Storage) GetAgentRelations(agentID string, roomID string) ([]AgentRelation, error) {
	query := `SELECT id, agent_id, relation_type, related_agent_id, room_id, description, created_at, updated_at
	          FROM agent_relations WHERE agent_id = ?`
	args := []interface{}{agentID}

	if roomID != "" {
		query += ` AND (room_id = ? OR room_id IS NULL OR room_id = '')`
		args = append(args, roomID)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		slog.Error("GetAgentRelations query failed", "error", err, "agent_id", agentID)
		return nil, err
	}
	defer rows.Close()

	relations := make([]AgentRelation, 0)
	for rows.Next() {
		var rel AgentRelation
		var roomIDVal, descriptionVal sql.NullString
		if err := rows.Scan(&rel.ID, &rel.AgentID, &rel.RelationType, &rel.RelatedAgentID, &roomIDVal, &descriptionVal, &rel.CreatedAt, &rel.UpdatedAt); err != nil {
			slog.Warn("Scan error", "error", err)
			continue
		}
		if roomIDVal.Valid {
			rel.RoomID = roomIDVal.String
		}
		if descriptionVal.Valid {
			rel.Description = descriptionVal.String
		}
		relations = append(relations, rel)
		slog.Info("GetAgentRelations: found relation", "agent_id", rel.AgentID, "type", rel.RelationType)
	}
	if err := rows.Err(); err != nil {
		slog.Error("GetAgentRelations rows error", "error", err)
	}
	return relations, rows.Err()
}

// DeleteRelation 删除关系
func (s *Storage) DeleteRelation(relationID int64) error {
	query := `DELETE FROM agent_relations WHERE id = ?`
	_, err := s.db.Exec(query, relationID)
	return err
}

// GetRelationsSummary 获取 Agent 关系汇总
func (s *Storage) GetRelationsSummary(agentID string, roomID string) (*Relations, error) {
	relations, err := s.GetAgentRelations(agentID, roomID)
	if err != nil {
		return nil, err
	}

	summary := &Relations{
		Colleagues:   []string{},
		Superiors:    []string{},
		Subordinates: []string{},
	}

	for _, rel := range relations {
		switch rel.RelationType {
		case RelationColleague:
			summary.Colleagues = append(summary.Colleagues, rel.RelatedAgentID)
		case RelationSuperior:
			summary.Superiors = append(summary.Superiors, rel.RelatedAgentID)
		case RelationSubordinate:
			summary.Subordinates = append(summary.Subordinates, rel.RelatedAgentID)
		}
	}

	return summary, nil
}

// GetRoomConfig 获取聊天室配置
func (s *Storage) GetRoomConfig(roomID string) (*RoomConfig, error) {
	query := `SELECT room_id, config FROM room_configs WHERE room_id = ?`
	row := s.db.QueryRow(query, roomID)

	var configRoomID string
	var configJSON sql.NullString

	err := row.Scan(&configRoomID, &configJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if !configJSON.Valid || configJSON.String == "" {
		return nil, nil
	}

	var config RoomConfig
	if err := json.Unmarshal([]byte(configJSON.String), &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// UpsertRoomConfig 创建或更新聊天室配置
func (s *Storage) UpsertRoomConfig(config *RoomConfig) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO room_configs (room_id, config) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE config = VALUES(config)
	`
	_, err = s.db.Exec(query, config.RoomID, string(configJSON))
	return err
}

// GetAgentInfo 获取 Agent 信息
func (s *Storage) GetAgentInfo(agentID string) (*AgentInfo, error) {
	agent, err := s.GetAgent(agentID)
	if err != nil || agent == nil {
		return nil, err
	}

	info := &AgentInfo{
		AgentID:  agent.AgentID,
		Online:   agent.Status == "ONLINE",
		Endpoint: agent.Endpoint,
	}

	// 获取 Agent 的 role 和 description（如果 agents 表有这些字段）
	// 这里假设通过单独的查询或扩展 agents 表来获取

	return info, nil
}

// GetRoomAgents 获取聊天室中的所有 Agent 成员及其关系
func (s *Storage) GetRoomAgents(roomID string) ([]AgentInfo, error) {
	members, err := s.GetRoomMembers(roomID)
	if err != nil {
		return nil, err
	}

	var agents []AgentInfo
	for _, member := range members {
		if member.MemberType != "agent" {
			continue
		}

		agent, err := s.GetAgent(member.MemberID)
		if err != nil || agent == nil {
			continue
		}

		info := &AgentInfo{
			AgentID:  agent.AgentID,
			Online:   agent.Status == "ONLINE",
			Endpoint: agent.Endpoint,
		}

		// 获取关系
		relations, err := s.GetRelationsSummary(agent.AgentID, roomID)
		if err == nil {
			info.Relations = relations
		}

		agents = append(agents, *info)
	}

	return agents, nil
}

// ============ Agent 模板操作 ============

// GetTemplate 获取 Agent 模板
func (s *Storage) GetTemplate(agentID string) (*AgentTemplate, error) {
	s.templateMu.RLock()
	defer s.templateMu.RUnlock()

	if template, ok := s.templates[agentID]; ok {
		return template, nil
	}
	return nil, nil // 不存在返回 nil
}

// SaveTemplate 保存 Agent 模板（持久化到MySQL + 更新内存）
func (s *Storage) SaveTemplate(agentID string, template *AgentTemplate) error {
	template.AgentID = agentID
	template.UpdatedAt = time.Now().Unix()

	// 序列化为 JSON
	soulJSON, _ := json.Marshal(template.Soul)
	bootstrapJSON, _ := json.Marshal(template.Bootstrap)
	metaJSON, _ := json.Marshal(template.Meta)

	// 写入 MySQL
	query := `
		INSERT INTO agent_templates (agent_id, soul_json, bootstrap_json, meta_json, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			soul_json = VALUES(soul_json),
			bootstrap_json = VALUES(bootstrap_json),
			meta_json = VALUES(meta_json),
			updated_at = VALUES(updated_at)
	`
	_, err := s.db.Exec(query, agentID, string(soulJSON), string(bootstrapJSON), string(metaJSON), template.UpdatedAt)
	if err != nil {
		return fmt.Errorf("保存模板到数据库失败: %w", err)
	}

	// 更新内存缓存
	s.templateMu.Lock()
	s.templates[agentID] = template
	s.templateMu.Unlock()

	return nil
}

// DeleteTemplate 删除 Agent 模板（从MySQL删除 + 从内存移除）
func (s *Storage) DeleteTemplate(agentID string) error {
	// 从 MySQL 删除
	query := `DELETE FROM agent_templates WHERE agent_id = ?`
	_, err := s.db.Exec(query, agentID)
	if err != nil {
		return fmt.Errorf("从数据库删除模板失败: %w", err)
	}

	// 从内存移除
	s.templateMu.Lock()
	delete(s.templates, agentID)
	s.templateMu.Unlock()

	return nil
}

// ============ Trigger 操作 ============

// CreateTrigger 创建触发器
func (s *Storage) CreateTrigger(t *Trigger) error {
	query := `
		INSERT INTO triggers (id, xclient_id, name, type, config, reason, room_id, room_valid, status, fire_count, cooldown_seconds, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query, t.ID, t.XClientID, t.Name, t.Type, string(t.Config), t.Reason, t.RoomID, t.RoomValid, t.Status, t.FireCount, t.CooldownSeconds, t.CreatedAt, t.UpdatedAt)
	return err
}

// GetTrigger 获取单个触发器
func (s *Storage) GetTrigger(id string) (*Trigger, error) {
	query := `SELECT id, xclient_id, name, type, config, reason, room_id, room_valid, status, invalid_reason, last_fired_at, fire_count, max_fires, cooldown_seconds, expires_at, created_at, updated_at FROM triggers WHERE id = ?`
	row := s.db.QueryRow(query, id)
	return s.scanTrigger(row)
}

// GetTriggers 获取触发器列表
func (s *Storage) GetTriggers(roomID, status string) ([]*Trigger, error) {
	query := `SELECT id, xclient_id, name, type, config, reason, room_id, room_valid, status, invalid_reason, last_fired_at, fire_count, max_fires, cooldown_seconds, expires_at, created_at, updated_at FROM triggers WHERE 1=1`
	args := []interface{}{}

	if roomID != "" {
		query += ` AND room_id = ?`
		args = append(args, roomID)
	}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}

	query += ` ORDER BY created_at DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var triggers []*Trigger
	for rows.Next() {
		t, err := s.scanTriggerRows(rows)
		if err != nil {
			continue
		}
		triggers = append(triggers, t)
	}
	return triggers, rows.Err()
}

// GetTriggersByXClient 获取 X-Client 的所有触发器
func (s *Storage) GetTriggersByXClient(xclientID string) ([]*Trigger, error) {
	query := `SELECT id, xclient_id, name, type, config, reason, room_id, room_valid, status, invalid_reason, last_fired_at, fire_count, max_fires, cooldown_seconds, expires_at, created_at, updated_at FROM triggers WHERE xclient_id = ? ORDER BY created_at DESC`
	rows, err := s.db.Query(query, xclientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var triggers []*Trigger
	for rows.Next() {
		t, err := s.scanTriggerRows(rows)
		if err != nil {
			continue
		}
		triggers = append(triggers, t)
	}
	return triggers, rows.Err()
}

// UpdateTrigger 更新触发器
func (s *Storage) UpdateTrigger(t *Trigger) error {
	query := `
		UPDATE triggers SET
			name = ?, type = ?, config = ?, reason = ?, room_id = ?, room_valid = ?,
			status = ?, invalid_reason = ?, max_fires = ?, cooldown_seconds = ?, expires_at = ?, updated_at = ?
		WHERE id = ?
	`
	_, err := s.db.Exec(query, t.Name, t.Type, string(t.Config), t.Reason, t.RoomID, t.RoomValid,
		t.Status, t.InvalidReason, t.MaxFires, t.CooldownSeconds, t.ExpiresAt, t.UpdatedAt, t.ID)
	return err
}

// UpdateTriggerFired 更新触发器触发状态
func (s *Storage) UpdateTriggerFired(id string, firedAt int64) error {
	query := `UPDATE triggers SET last_fired_at = ?, fire_count = fire_count + 1, updated_at = ? WHERE id = ?`
	_, err := s.db.Exec(query, firedAt, time.Now().UnixMilli(), id)
	return err
}

// InvalidateTrigger 使触发器失效
func (s *Storage) InvalidateTrigger(id, reason string) error {
	query := `UPDATE triggers SET status = 'invalid', room_valid = FALSE, invalid_reason = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.Exec(query, reason, time.Now().UnixMilli(), id)
	return err
}

// InvalidateTriggersByRoom 使聊天室关联的所有触发器失效
func (s *Storage) InvalidateTriggersByRoom(roomID, reason string) error {
	query := `UPDATE triggers SET status = 'invalid', room_valid = FALSE, invalid_reason = ?, updated_at = ? WHERE room_id = ?`
	_, err := s.db.Exec(query, reason, time.Now().UnixMilli(), roomID)
	return err
}

// DeleteTrigger 删除触发器
func (s *Storage) DeleteTrigger(id string) error {
	query := `DELETE FROM triggers WHERE id = ?`
	_, err := s.db.Exec(query, id)
	return err
}

// scanTrigger 扫描单行触发器
func (s *Storage) scanTrigger(row *sql.Row) (*Trigger, error) {
	var t Trigger
	var config, reason, invalidReason sql.NullString
	var lastFiredAt, expiresAt sql.NullInt64
	var maxFires, cooldownSeconds sql.NullInt64

	err := row.Scan(&t.ID, &t.XClientID, &t.Name, &t.Type, &config, &reason, &t.RoomID, &t.RoomValid,
		&t.Status, &invalidReason, &lastFiredAt, &t.FireCount, &maxFires, &cooldownSeconds, &expiresAt,
		&t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if config.Valid {
		t.Config = json.RawMessage(config.String)
	}
	if reason.Valid {
		t.Reason = reason.String
	}
	if invalidReason.Valid {
		t.InvalidReason = invalidReason.String
	}
	if lastFiredAt.Valid {
		t.LastFiredAt = lastFiredAt.Int64
	}
	if maxFires.Valid {
		maxFiresInt := int(maxFires.Int64)
		t.MaxFires = &maxFiresInt
	}
	if cooldownSeconds.Valid {
		t.CooldownSeconds = int(cooldownSeconds.Int64)
	}
	if expiresAt.Valid {
		t.ExpiresAt = &expiresAt.Int64
	}

	return &t, nil
}

// scanTriggerRows 扫描多行触发器
func (s *Storage) scanTriggerRows(rows *sql.Rows) (*Trigger, error) {
	var t Trigger
	var config, reason, invalidReason sql.NullString
	var lastFiredAt, expiresAt sql.NullInt64
	var maxFires, cooldownSeconds sql.NullInt64

	err := rows.Scan(&t.ID, &t.XClientID, &t.Name, &t.Type, &config, &reason, &t.RoomID, &t.RoomValid,
		&t.Status, &invalidReason, &lastFiredAt, &t.FireCount, &maxFires, &cooldownSeconds, &expiresAt,
		&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}

	if config.Valid {
		t.Config = json.RawMessage(config.String)
	}
	if reason.Valid {
		t.Reason = reason.String
	}
	if invalidReason.Valid {
		t.InvalidReason = invalidReason.String
	}
	if lastFiredAt.Valid {
		t.LastFiredAt = lastFiredAt.Int64
	}
	if maxFires.Valid {
		maxFiresInt := int(maxFires.Int64)
		t.MaxFires = &maxFiresInt
	}
	if cooldownSeconds.Valid {
		t.CooldownSeconds = int(cooldownSeconds.Int64)
	}
	if expiresAt.Valid {
		t.ExpiresAt = &expiresAt.Int64
	}

	return &t, nil
}

// CreateTriggerExecution 创建触发器执行记录
func (s *Storage) CreateTriggerExecution(ex *TriggerExecution) error {
	query := `
		INSERT INTO trigger_executions (id, trigger_id, fired_at, status, error_message, execution_time_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query, ex.ID, ex.TriggerID, ex.FiredAt, ex.Status, ex.ErrorMessage, ex.ExecutionTimeMs, ex.CreatedAt)
	return err
}

// UpdateTriggerExecution 更新触发器执行记录
func (s *Storage) UpdateTriggerExecution(id, status, errorMessage string, executionTimeMs int) error {
	query := `UPDATE trigger_executions SET status = ?, error_message = ?, execution_time_ms = ? WHERE id = ?`
	_, err := s.db.Exec(query, status, errorMessage, executionTimeMs, id)
	return err
}

// GetTriggerExecutions 获取触发器的执行记录
func (s *Storage) GetTriggerExecutions(triggerID string, limit int) ([]*TriggerExecution, error) {
	query := `SELECT id, trigger_id, fired_at, status, error_message, execution_time_ms, created_at FROM trigger_executions WHERE trigger_id = ? ORDER BY fired_at DESC LIMIT ?`
	rows, err := s.db.Query(query, triggerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []*TriggerExecution
	for rows.Next() {
		var ex TriggerExecution
		var errorMsg sql.NullString
		var execTimeMs sql.NullInt64
		if err := rows.Scan(&ex.ID, &ex.TriggerID, &ex.FiredAt, &ex.Status, &errorMsg, &execTimeMs, &ex.CreatedAt); err != nil {
			continue
		}
		if errorMsg.Valid {
			ex.ErrorMessage = errorMsg.String
		}
		if execTimeMs.Valid {
			ex.ExecutionTimeMs = int(execTimeMs.Int64)
		}
		execs = append(execs, &ex)
	}
	return execs, rows.Err()
}

// ============ Poll State 操作 ============

// GetPollState 获取轮询状态
func (s *Storage) GetPollState(triggerID string) (*PollState, error) {
	query := `SELECT trigger_id, last_value, last_checked_at FROM poll_states WHERE trigger_id = ?`
	row := s.db.QueryRow(query, triggerID)

	var ps PollState
	var lastValue sql.NullString
	err := row.Scan(&ps.TriggerID, &lastValue, &ps.LastCheckedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastValue.Valid {
		ps.LastValue = lastValue.String
	}
	return &ps, nil
}

// UpsertPollState 更新或创建轮询状态
func (s *Storage) UpsertPollState(ps *PollState) error {
	query := `
		INSERT INTO poll_states (trigger_id, last_value, last_checked_at)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE last_value = VALUES(last_value), last_checked_at = VALUES(last_checked_at)
	`
	_, err := s.db.Exec(query, ps.TriggerID, ps.LastValue, ps.LastCheckedAt)
	return err
}

// DeletePollState 删除轮询状态
func (s *Storage) DeletePollState(triggerID string) error {
	query := `DELETE FROM poll_states WHERE trigger_id = ?`
	_, err := s.db.Exec(query, triggerID)
	return err
}
