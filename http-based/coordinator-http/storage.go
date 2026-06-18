package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// ============ Storage 数据库操作层 ============

type Storage struct {
	db  *sql.DB
	cfg *Config

	// 发言锁（内存 + 数据库双重保证）
	lockMu   sync.RWMutex
	speeches map[string]*SpeakerLock // room_id -> lock

	// 聊天室成员缓存（内存加速查询）
	memberMu sync.RWMutex
	members  map[string]map[string]*Member // room_id -> member_id -> Member
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
		db:       db,
		cfg:      cfg,
		speeches: make(map[string]*SpeakerLock),
		members:  make(map[string]map[string]*Member),
	}

	// 创建用户会话表
	if err := s.createUserRoomSessionsTable(); err != nil {
		log.Printf("创建 user_room_sessions 表失败: %v", err)
	}

	// 初始化加载聊天室成员到内存
	if err := s.loadMembersToMemory(); err != nil {
		log.Printf("加载聊天室成员到内存失败: %v", err)
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
func (s *Storage) RegisterAgent(agentID, endpoint string) error {
	query := `
		INSERT INTO agents (agent_id, endpoint, status, last_heartbeat)
		VALUES (?, ?, 'ONLINE', NOW())
		ON DUPLICATE KEY UPDATE
			endpoint = VALUES(endpoint),
			status = 'ONLINE',
			last_heartbeat = NOW()
	`
	_, err := s.db.Exec(query, agentID, endpoint)
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
	query := `SELECT id, agent_id, endpoint, status, last_heartbeat, created_at FROM agents WHERE agent_id = ?`
	row := s.db.QueryRow(query, agentID)

	var a Agent
	err := row.Scan(&a.ID, &a.AgentID, &a.Endpoint, &a.Status, &a.LastHeartbeat, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
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
func (s *Storage) GetRoomMembers(roomID string) ([]*Member, error) {
	query := `
		SELECT id, room_id, member_id, member_type, joined_at, left_at, is_active
		FROM members
		WHERE room_id = ? AND is_active = TRUE
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
		if err := rows.Scan(&m.ID, &m.RoomID, &m.MemberID, &m.MemberType, &m.JoinedAt, &leftAt, &m.IsActive); err != nil {
			return nil, err
		}
		if leftAt.Valid {
			m.LeftAt = &leftAt.Time
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
		log.Printf("[ERROR] GetMemberRooms failed: %v", err)
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
		log.Printf("[ERROR] PollMessages query failed: %v", err)
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
			log.Printf("[WARN] Scan error: %v", err)
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
		log.Printf("[ERROR] GetPendingNotifications 开始事务失败: %v", err)
		return nil, err
	}

	rows, err := tx.Query(query, args...)
	if err != nil {
		log.Printf("[ERROR] GetPendingNotifications 查询失败: %v", err)
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
			log.Printf("[WARN] Scan error: %v", err)
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
			log.Printf("[WARN] 标记消息已通知失败: msgID=%s, userID=%s, err=%v", msgID, userID, err)
		}
	}

	// 提交事务
	if err := tx.Commit(); err != nil {
		log.Printf("[ERROR] GetPendingNotifications 提交事务失败: %v", err)
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
			log.Printf("[WARN] MarkNotificationsSent 失败: msgID=%s, userID=%s, err=%v", msgID, userID, err)
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

// ============ 发言锁操作 ============

// TryAcquireLock 尝试获取发言锁
func (s *Storage) TryAcquireLock(roomID, holderID, holderType string) (bool, error) {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()

	// 检查当前锁状态
	if lock, exists := s.speeches[roomID]; exists {
		// 锁未过期，不能获取
		if time.Now().Before(lock.ExpiresAt) {
			return false, nil
		}
	}

	// 尝试更新数据库中的锁
	query := `
		INSERT INTO speaker_locks (room_id, holder_id, holder_type, expires_at)
		VALUES (?, ?, ?, DATE_ADD(NOW(), INTERVAL ? MILLISECOND))
		ON DUPLICATE KEY UPDATE
			holder_id = VALUES(holder_id),
			holder_type = VALUES(holder_type),
			expires_at = VALUES(expires_at)
	`

	timeout := s.cfg.SpeakerLockTimeout
	if timeout <= 0 {
		timeout = 2000
	}

	result, err := s.db.Exec(query, roomID, holderID, holderType, timeout)
	if err != nil {
		return false, err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		// 锁已存在且未过期
		return false, nil
	}

	// 更新内存中的锁
	s.speeches[roomID] = &SpeakerLock{
		RoomID:     roomID,
		HolderID:   holderID,
		HolderType: holderType,
		ExpiresAt:  time.Now().Add(time.Duration(timeout) * time.Millisecond),
	}

	return true, nil
}

// ReleaseLock 释放发言锁
func (s *Storage) ReleaseLock(roomID, holderID string) error {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()

	query := `DELETE FROM speaker_locks WHERE room_id = ? AND holder_id = ?`
	_, err := s.db.Exec(query, roomID, holderID)

	if err == nil {
		delete(s.speeches, roomID)
	}

	return err
}

// GetCurrentSpeaker 获取当前发言者
func (s *Storage) GetCurrentSpeaker(roomID string) (string, error) {
	s.lockMu.RLock()
	if lock, exists := s.speeches[roomID]; exists {
		if time.Now().Before(lock.ExpiresAt) {
			s.lockMu.RUnlock()
			return lock.HolderID, nil
		}
	}
	s.lockMu.RUnlock()

	// 查数据库
	query := `SELECT holder_id FROM speaker_locks WHERE room_id = ? AND expires_at > NOW()`
	var holderID string
	err := s.db.QueryRow(query, roomID).Scan(&holderID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return holderID, err
}

// ============ 清理任务 ============

// startCleanup 启动定期清理
func (s *Storage) startCleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		// 清理过期的发言锁
		s.cleanupExpiredLocks()

		// 检查离线 Agent
		s.CheckOfflineAgents()

		// 清理旧消息
		s.cleanupOldMessages()
	}
}

func (s *Storage) cleanupExpiredLocks() {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()

	for roomID, lock := range s.speeches {
		if time.Now().After(lock.ExpiresAt) {
			delete(s.speeches, roomID)
		}
	}

	query := `DELETE FROM speaker_locks WHERE expires_at < NOW()`
	s.db.Exec(query)
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
func (s *Storage) DeleteUserRoomSessionByConnection(connectionID string) error {
	query := `DELETE FROM user_room_sessions WHERE connection_id = ?`
	_, err := s.db.Exec(query, connectionID)
	return err
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
func (s *Storage) CleanupStaleSessions(maxAge time.Duration) error {
	query := `DELETE FROM user_room_sessions WHERE last_active_at < ?`
	_, err := s.db.Exec(query, time.Now().Add(-maxAge))
	return err
}
