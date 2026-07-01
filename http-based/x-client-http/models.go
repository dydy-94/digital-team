package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ============ 数据模型 ============

// PollMessage 从 Coordinator 轮询到的消息
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
	TaskID       string   `json:"task_id,omitempty"` // 关联的任务 ID
	CreatedAt    int64    `json:"created_at"`
	Type         string   `json:"type,omitempty"` // 消息类型: text / file / delegate / notify
}

// PollResponse 轮询响应
type PollResponse struct {
	Messages  []*PollMessage `json:"messages"`
	NextSince int64          `json:"next_since"`
}

// SendMessageRequest 发送消息请求
type SendMessageRequest struct {
	RoomID       string   `json:"room_id"`
	SenderID     string   `json:"sender_id"`
	SenderType   string   `json:"sender_type"`
	Content      string   `json:"content"`
	TargetID     string   `json:"target_id"`
	MentionUsers []string `json:"mention_users"`
	Intent       string   `json:"intent"`
	ReplyToMsgID string   `json:"reply_to_msg_id"`
	TaskID       string   `json:"task_id,omitempty"` // 关联的任务 ID
}

// SendMessageResponse 发送消息响应
type SendMessageResponse struct {
	Success bool   `json:"success"`
	MsgID   string `json:"msg_id,omitempty"`
	Error   string `json:"error,omitempty"`
}

// RegisterRequest 注册请求
type RegisterRequest struct {
	AgentID     string `json:"agent_id"`
	Endpoint    string `json:"endpoint"`
	CallbackURL string `json:"callback_url,omitempty"` // A2A 回调 URL
}

// AgentSendMessageRequest Agent 主动发送消息请求（通过 x-client 代理）
type AgentSendMessageRequest struct {
	RoomID       string   `json:"room_id" binding:"required"`
	Content      string   `json:"content" binding:"required"`
	TargetID     string   `json:"target_id"` // 默认 ALL
	MentionUsers []string `json:"mention_users"`
	Intent       string   `json:"intent"` // 默认 INFORM
	ReplyToMsgID string   `json:"reply_to_msg_id"`
}

// AgentSendMessageResponse Agent 主动发送消息响应
type AgentSendMessageResponse struct {
	Success bool   `json:"success"`
	MsgID   string `json:"msg_id,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ============ Task 相关模型 ============

// CreateTaskRequest 创建任务请求
type CreateTaskRequest struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	Priority     int    `json:"priority"`
	AssignedTo   string `json:"assigned_to"`
	RoomID       string `json:"room_id"`
	ParentTaskID string `json:"parent_task_id,omitempty"`
	CreatedBy    string `json:"created_by"`
}

// Task 任务
type Task struct {
	TaskID      string `json:"task_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    int    `json:"priority"`
	CreatedBy   string `json:"created_by"`
	AssignedTo  string `json:"assigned_to"`
	RoomID      string `json:"room_id"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// DelegateCommand /delegate 命令解析结果
type DelegateCommand struct {
	TaskID       string   // 任务 ID（如果是子任务委托）
	Title        string   // 任务标题
	Description  string   // 任务描述
	AssignedTo   string   // 被分配的 agent
	FocusItems   []string // 关注点列表
	ParentTaskID string   // 父任务 ID（可选）
	IsValid      bool     // 是否有效命令
	RawContent   string   // 原始内容
}

// ParseDelegateCommand 解析 /delegate 命令
// 格式: /delegate <title> to <agent_id> [with focus <item1>, <item2>]
// 示例: /delegate 设计登录页面 to agent-001 with focus [ ] 设计 UI, [ ] 实现后端
func ParseDelegateCommand(content string) *DelegateCommand {
	cmd := &DelegateCommand{
		IsValid:    false,
		RawContent: content,
	}

	// 检查是否以 /delegate 开头
	if !strings.HasPrefix(content, "/delegate") {
		return cmd
	}

	// 去掉 /delegate 前缀
	rest := strings.TrimSpace(strings.TrimPrefix(content, "/delegate"))
	if rest == "" {
		return cmd
	}

	// 检查是否有 "to" 关键字
	toParts := strings.SplitN(rest, " to ", 2)
	if len(toParts) != 2 {
		return cmd
	}

	titlePart := strings.TrimSpace(toParts[0])
	toPart := strings.TrimSpace(toParts[1])

	// 解析被分配的 agent
	// 可能格式: "agent-001" 或 "agent-001 with focus ..."
	assignedTo := ""
	focusPart := ""

	withIdx := strings.Index(toPart, " with focus ")
	if withIdx >= 0 {
		assignedTo = strings.TrimSpace(toPart[:withIdx])
		focusPart = strings.TrimSpace(toPart[withIdx+len(" with focus "):])
	} else {
		assignedTo = toPart
	}

	if assignedTo == "" {
		return cmd
	}

	cmd.Title = titlePart
	cmd.AssignedTo = assignedTo
	cmd.IsValid = true

	// 解析关注点
	if focusPart != "" {
		// 按逗号分隔关注点
		items := strings.Split(focusPart, ",")
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item != "" {
				// 确保关注点格式正确
				if !strings.HasPrefix(item, "[") {
					item = "[ ] " + item
				}
				cmd.FocusItems = append(cmd.FocusItems, item)
			}
		}
	}

	return cmd
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

// ============ Agent 关系模型 ============

// AgentRelation Agent 关系
type AgentRelation struct {
	ID             int64  `json:"id"`
	AgentID        string `json:"agent_id"`
	RelationType   string `json:"relation_type"` // colleague / superior / subordinate
	RelatedAgentID string `json:"related_agent_id"`
	RoomID         string `json:"room_id,omitempty"`
	Description    string `json:"description,omitempty"`
}

// Relations Agent 关系汇总
type Relations struct {
	Colleagues   []string `json:"colleagues"`
	Superiors    []string `json:"superiors"`
	Subordinates []string `json:"subordinates"`
}

// AgentInfo Agent 信息
type AgentInfo struct {
	AgentID     string     `json:"agent_id"`
	Role        string     `json:"role,omitempty"`
	Description string     `json:"description,omitempty"`
	Online      bool       `json:"online,omitempty"`
	Relations   *Relations `json:"relations,omitempty"`
	Endpoint    string     `json:"endpoint,omitempty"`
}

// RoomConfig 聊天室配置
type RoomConfig struct {
	RoomID           string `json:"room_id"`
	Name             string `json:"name"`
	HierarchyEnabled bool   `json:"hierarchy_enabled"`
	AutoWelcome      bool   `json:"auto_welcome"`
	WelcomeMessage   string `json:"welcome_message"`
}

// AgentContext Agent 完整上下文
type AgentContext struct {
	CurrentAgent *AgentInfo   `json:"current_agent"`
	RoomMembers  []*AgentInfo `json:"room_members"`
	Relations    *Relations   `json:"relations"`
	RoomConfig   *RoomConfig  `json:"room_config,omitempty"`
}

// CreateRelationRequest 创建关系请求
type CreateRelationRequest struct {
	AgentID        string `json:"agent_id"`
	RelationType   string `json:"relation_type"` // colleague / superior / subordinate
	RelatedAgentID string `json:"related_agent_id"`
	RoomID         string `json:"room_id,omitempty"`
	Description    string `json:"description,omitempty"`
}

// UpsertRoomConfigRequest 创建/更新聊天室配置请求
type UpsertRoomConfigRequest struct {
	Name             string `json:"name"`
	HierarchyEnabled bool   `json:"hierarchy_enabled"`
	AutoWelcome      bool   `json:"auto_welcome"`
	WelcomeMessage   string `json:"welcome_message"`
}

// ============ Agent 模板模型 ============

// Soul Agent 人格定义
type Soul struct {
	Identity struct {
		Role      string `json:"role"`
		Expertise string `json:"expertise,omitempty"`
		Creator   string `json:"creator,omitempty"`
	} `json:"identity"`
	Personality []string `json:"personality"` // 性格特点列表
	WorkStyle   []string `json:"work_style"`  // 工作方式列表
	Boundaries  []string `json:"boundaries"`  // 行为边界列表
}

// Bootstrap 对话流程模板
type Bootstrap struct {
	GreetingTemplate    string   `json:"greeting_template"`    // 首次见面模板
	DeliverableTemplate string   `json:"deliverable_template"` // 交付物模板
	Capabilities        []string `json:"capabilities"`         // 能力介绍列表
	ExitPrompt          string   `json:"exit_prompt"`          // 结束引导
}

// BootstrapContext bootstrap 渲染上下文
type BootstrapContext struct {
	AgentName string `json:"name"`
	UserName  string `json:"user_name"`
	UserTurns int    `json:"user_turns"` // 用户消息数
	RoomID    string `json:"room_id"`
	AgentID   string `json:"agent_id"`
}

// AgentTemplate Agent 模板
type AgentTemplate struct {
	Soul      *Soul         `json:"soul,omitempty"`
	Bootstrap *Bootstrap    `json:"bootstrap,omitempty"`
	Meta      *TemplateMeta `json:"meta,omitempty"`
}

// TemplateMeta 模板元数据
type TemplateMeta struct {
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Icon           string            `json:"icon"`     // 2-letter icon
	Category       string            `json:"category"` // software-development / productivity / analysis
	Capabilities   []string          `json:"capabilities"`
	AutonomyPolicy map[string]string `json:"autonomy_policy"` // L1/L2/L3 权限
}

// TemplateRenderContext 模板渲染上下文
type TemplateRenderContext struct {
	AgentID   string
	AgentName string
	UserID    string
	UserName  string
	RoomID    string
	Turns     int // user_turns
}

// TemplateLoader 模板加载器接口
type TemplateLoader interface {
	LoadSoul() (*Soul, error)
	LoadBootstrap() (*Bootstrap, error)
	LoadMeta() (*TemplateMeta, error)
	LoadTemplate() (*AgentTemplate, error)
}

// ============ Trigger 触发器模型 ============

// Trigger 触发器
type Trigger struct {
	ID              string          `json:"id"`
	XClientID       string          `json:"xclient_id"`
	Name            string          `json:"name"`
	Type            string          `json:"type"` // cron|once|interval|poll|webhook|on_message
	Config          json.RawMessage `json:"config"`
	Reason          string          `json:"reason"`
	RoomID          string          `json:"room_id"`
	RoomValid       bool            `json:"room_valid"`
	Status          string          `json:"status"` // enabled|disabled|invalid|expired
	InvalidReason   string          `json:"invalid_reason,omitempty"`
	LastFiredAt     int64           `json:"last_fired_at,omitempty"`
	FireCount       int             `json:"fire_count"`
	MaxFires        *int            `json:"max_fires,omitempty"`
	CooldownSeconds int             `json:"cooldown_seconds"`
	ExpiresAt       *int64          `json:"expires_at,omitempty"`
	CreatedAt       int64           `json:"created_at"`
	UpdatedAt       int64           `json:"updated_at"`
}

// CreateTriggerRequest 创建触发器请求
type CreateTriggerRequest struct {
	Name            string          `json:"name" binding:"required"`
	Type            string          `json:"type" binding:"required"`
	Config          json.RawMessage `json:"config" binding:"required"`
	Reason          string          `json:"reason"`
	RoomID          string          `json:"room_id" binding:"required"`
	MaxFires        *int            `json:"max_fires"`
	CooldownSeconds int             `json:"cooldown_seconds"`
}

// UpdateTriggerRequest 更新触发器请求
type UpdateTriggerRequest struct {
	Name            *string          `json:"name,omitempty"`
	Config          *json.RawMessage `json:"config,omitempty"`
	Reason          *string          `json:"reason,omitempty"`
	RoomID          *string          `json:"room_id,omitempty"`
	IsEnabled       *bool            `json:"is_enabled,omitempty"`
	MaxFires        *int             `json:"max_fires,omitempty"`
	CooldownSeconds *int             `json:"cooldown_seconds,omitempty"`
}

// TriggerNotifyRequest 触发器触发通知请求
type TriggerNotifyRequest struct {
	TriggerID   string `json:"trigger_id" binding:"required"`
	XClientID   string `json:"xclient_id" binding:"required"`
	TriggerType string `json:"trigger_type" binding:"required"`
	Reason      string `json:"reason"`
}

// TriggerResponse 触发器响应
type TriggerResponse struct {
	Success    bool   `json:"success"`
	TriggerID  string `json:"trigger_id,omitempty"`
	NextFireAt int64  `json:"next_fire_at,omitempty"`
	Error      string `json:"error,omitempty"`
}

// TriggerListResponse 触发器列表响应
type TriggerListResponse struct {
	Success  bool       `json:"success"`
	Triggers []*Trigger `json:"triggers"`
}

// RoomDeletedRequest 聊天室删除通知请求
type RoomDeletedRequest struct {
	RoomID string `json:"room_id" binding:"required"`
}
