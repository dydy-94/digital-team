package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ============ 数据模型 ============

// PollMessage 从 Coordinator 轮询到的消息
type PollMessage struct {
	MsgID         string   `json:"msg_id"`
	RoomID        string   `json:"room_id"`
	SenderID      string   `json:"sender_id"`
	SenderType    string   `json:"sender_type"`
	Content       string   `json:"content"`
	MentionUsers  []string `json:"mention_users"`
	Intent        string   `json:"intent"`
	ReplyToMsgID  string   `json:"reply_to_msg_id,omitempty"`
	TargetID      string   `json:"target_id"`
	CreatedAt     int64    `json:"created_at"`
}

// PollResponse 轮询响应
type PollResponse struct {
	Messages  []*PollMessage `json:"messages"`
	NextSince int64          `json:"next_since"`
}

// SendMessageRequest 发送消息请求
type SendMessageRequest struct {
	RoomID        string   `json:"room_id"`
	SenderID      string   `json:"sender_id"`
	SenderType    string   `json:"sender_type"`
	Content       string   `json:"content"`
	TargetID      string   `json:"target_id"`
	MentionUsers  []string `json:"mention_users"`
	Intent        string   `json:"intent"`
	ReplyToMsgID  string   `json:"reply_to_msg_id"`
}

// SendMessageResponse 发送消息响应
type SendMessageResponse struct {
	Success bool   `json:"success"`
	MsgID   string `json:"msg_id,omitempty"`
	Error   string `json:"error,omitempty"`
}

// RegisterRequest 注册请求
type RegisterRequest struct {
	AgentID  string `json:"agent_id"`
	Endpoint string `json:"endpoint"`
}

// ============ Memory Window ============

// MemoryWindow 记忆窗口，管理聊天上下文
type MemoryWindow struct {
	maxSize  int
	maxChars int
	messages []string
	totalLen int
}

func NewMemoryWindow(maxSize, maxChars int) *MemoryWindow {
	return &MemoryWindow{
		maxSize:  maxSize,
		maxChars: maxChars,
		messages: make([]string, 0, maxSize),
	}
}

func (m *MemoryWindow) Push(sender, content string) {
	msg := fmt.Sprintf("[%s]: %s", sender, content)
	msgLen := len(msg)

	// 如果单条消息超长，截断
	if msgLen > m.maxChars {
		content = content[:m.maxChars-len(sender)-5] + "..."
		msg = fmt.Sprintf("[%s]: %s", sender, content)
		msgLen = len(msg)
	}

	m.messages = append(m.messages, msg)
	m.totalLen += msgLen

	// 裁剪超长的消息
	m.trim()
}

func (m *MemoryWindow) trim() {
	// 超过数量限制
	for len(m.messages) > m.maxSize {
		if len(m.messages) > 0 {
			m.totalLen -= len(m.messages[0])
			m.messages = m.messages[1:]
		}
	}

	// 超过字符限制
	for m.totalLen > m.maxChars && len(m.messages) > 0 {
		m.totalLen -= len(m.messages[0])
		m.messages = m.messages[1:]
	}
}

func (m *MemoryWindow) BuildContext(sender, currentMsg string) string {
	var sb strings.Builder

	for _, msg := range m.messages {
		sb.WriteString(msg)
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("[%s]: %s\n", sender, currentMsg))
	sb.WriteString(fmt.Sprintf("[%s]: ", ""))

	return sb.String()
}

func (m *MemoryWindow) Len() int {
	return len(m.messages)
}

// ============ Session Manager ============

// SessionManager 会话管理器
type SessionManager struct {
	mu         sync.Mutex
	sessionSeq int64
}

func NewSessionManager() *SessionManager {
	return &SessionManager{}
}

func (s *SessionManager) GenerateSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionSeq++
	return fmt.Sprintf("sess_%d_%d", time.Now().Unix(), s.sessionSeq)
}
