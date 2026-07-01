package main

import (
	"fmt"
	"strings"
	"sync"
)

// BootstrapEngine 对话流程引擎
type BootstrapEngine struct {
	agentID   string
	agentName string
	bootstrap *Bootstrap
	soul      *Soul
	meta      *TemplateMeta
	soulMode  string // "always" 或 "once"

	// 每个房间的用户消息计数
	roomTurns map[string]map[string]int // roomID -> userID -> turns
	// Soul是否已注入过（用于once模式）
	soulInjected map[string]bool // roomID:userID -> true
	mu           sync.RWMutex
}

// NewBootstrapEngine 创建对话流程引擎
func NewBootstrapEngine(agentID string, template *AgentTemplate, soulMode string) *BootstrapEngine {
	agentName := agentID
	if template != nil && template.Meta != nil && template.Meta.Name != "" {
		agentName = template.Meta.Name
	}

	if soulMode == "" {
		soulMode = "always"
	}

	return &BootstrapEngine{
		agentID:      agentID,
		agentName:    agentName,
		bootstrap:    template.Bootstrap,
		soul:         template.Soul,
		meta:         template.Meta,
		soulMode:     soulMode,
		roomTurns:    make(map[string]map[string]int),
		soulInjected: make(map[string]bool),
	}
}

// ShouldInjectSoul 判断是否应该注入Soul Context
func (e *BootstrapEngine) ShouldInjectSoul(roomID, userID string) bool {
	if e.soul == nil || e.soulMode == "" {
		return false
	}

	key := roomID + ":" + userID

	e.mu.RLock()
	injected := e.soulInjected[key]
	e.mu.RUnlock()

	switch e.soulMode {
	case "always":
		return true
	case "once":
		return !injected
	default:
		return true
	}
}

// MarkSoulInjected 标记Soul已注入（用于once模式）
func (e *BootstrapEngine) MarkSoulInjected(roomID, userID string) {
	if e.soulMode != "once" {
		return
	}

	key := roomID + ":" + userID
	e.mu.Lock()
	e.soulInjected[key] = true
	e.mu.Unlock()
}

// IncrementTurn 增加用户消息计数
func (e *BootstrapEngine) IncrementTurn(roomID, userID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.roomTurns[roomID] == nil {
		e.roomTurns[roomID] = make(map[string]int)
	}

	e.roomTurns[roomID][userID]++
	return e.roomTurns[roomID][userID]
}

// GetTurns 获取用户消息计数
func (e *BootstrapEngine) GetTurns(roomID, userID string) int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.roomTurns[roomID] == nil {
		return 0
	}
	return e.roomTurns[roomID][userID]
}

// IsFirstContact 判断是否首次接触
func (e *BootstrapEngine) IsFirstContact(roomID, userID string) bool {
	return e.GetTurns(roomID, userID) <= 1
}

// BuildContext 构建完整上下文
func (e *BootstrapEngine) BuildContext(roomID, userID, userName string) *BootstrapContext {
	return &BootstrapContext{
		AgentName: e.agentName,
		UserName:  userName,
		UserTurns: e.GetTurns(roomID, userID),
		RoomID:    roomID,
		AgentID:   e.agentID,
	}
}

// RenderBootstrap 渲染 bootstrap 模板
func (e *BootstrapEngine) RenderBootstrap(ctx *BootstrapContext) string {
	if e.bootstrap == nil {
		return ""
	}

	if ctx.UserTurns <= 1 {
		return e.RenderGreeting(ctx)
	}
	return e.RenderDeliverable(ctx)
}

// RenderGreeting 渲染首次见面模板
func (e *BootstrapEngine) RenderGreeting(ctx *BootstrapContext) string {
	if e.bootstrap == nil || e.bootstrap.GreetingTemplate == "" {
		return e.DefaultGreeting(ctx)
	}

	template := e.bootstrap.GreetingTemplate
	return e.renderTemplate(template, ctx)
}

// RenderDeliverable 渲染交付物模板
func (e *BootstrapEngine) RenderDeliverable(ctx *BootstrapContext) string {
	if e.bootstrap == nil || e.bootstrap.DeliverableTemplate == "" {
		return ""
	}

	template := e.bootstrap.DeliverableTemplate
	return e.renderTemplate(template, ctx)
}

// DefaultGreeting 默认首次见面模板
func (e *BootstrapEngine) DefaultGreeting(ctx *BootstrapContext) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("**Hi %s!** I'm **%s**.\n\n", ctx.UserName, ctx.AgentName))

	if e.meta != nil && len(e.meta.Capabilities) > 0 {
		sb.WriteString("Here's what I can do:\n")
		for _, cap := range e.meta.Capabilities {
			sb.WriteString(fmt.Sprintf("- %s\n", cap))
		}
		sb.WriteString("\n")
	} else if e.bootstrap != nil && len(e.bootstrap.Capabilities) > 0 {
		sb.WriteString("Here's what I can do:\n")
		for _, cap := range e.bootstrap.Capabilities {
			sb.WriteString(fmt.Sprintf("- %s\n", cap))
		}
		sb.WriteString("\n")
	}

	if e.soul != nil && e.soul.WorkStyle != nil && len(e.soul.WorkStyle) > 0 {
		sb.WriteString("My work style:\n")
		for _, style := range e.soul.WorkStyle {
			sb.WriteString(fmt.Sprintf("- %s\n", style))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("How can I help you today, %s?", ctx.UserName))

	return sb.String()
}

// renderTemplate 渲染模板字符串
func (e *BootstrapEngine) renderTemplate(template string, ctx *BootstrapContext) string {
	result := template

	// 替换变量
	result = strings.ReplaceAll(result, "{name}", ctx.AgentName)
	result = strings.ReplaceAll(result, "{user_name}", ctx.UserName)
	result = strings.ReplaceAll(result, "{user_turns}", fmt.Sprintf("%d", ctx.UserTurns))
	result = strings.ReplaceAll(result, "{room_id}", ctx.RoomID)
	result = strings.ReplaceAll(result, "{agent_id}", ctx.AgentID)

	return result
}

// BuildSoulContext 构建 soul 上下文
func (e *BootstrapEngine) BuildSoulContext() string {
	return BuildSoulContext(e.soul)
}

// GetCapabilities 获取能力列表
func (e *BootstrapEngine) GetCapabilities() []string {
	if e.meta != nil && len(e.meta.Capabilities) > 0 {
		return e.meta.Capabilities
	}
	if e.bootstrap != nil && len(e.bootstrap.Capabilities) > 0 {
		return e.bootstrap.Capabilities
	}
	return nil
}

// GetExitPrompt 获取退出引导
func (e *BootstrapEngine) GetExitPrompt() string {
	if e.bootstrap != nil && e.bootstrap.ExitPrompt != "" {
		return e.bootstrap.ExitPrompt
	}
	return "Let me know if you need anything else!"
}

// GetAgentName 获取 agent 名称
func (e *BootstrapEngine) GetAgentName() string {
	return e.agentName
}

// HasTemplate 检查是否加载了模板
func (e *BootstrapEngine) HasTemplate() bool {
	return e.bootstrap != nil || e.soul != nil
}

// GetSoul 获取 soul
func (e *BootstrapEngine) GetSoul() *Soul {
	return e.soul
}

// GetMeta 获取 meta
func (e *BootstrapEngine) GetMeta() *TemplateMeta {
	return e.meta
}

// SessionTurnsManager 会话轮次管理器（用于持久化）
type SessionTurnsManager struct {
	turns map[string]int // sessionKey -> turns
	mu    sync.RWMutex
}

// NewSessionTurnsManager 创建会话轮次管理器
func NewSessionTurnsManager() *SessionTurnsManager {
	return &SessionTurnsManager{
		turns: make(map[string]int),
	}
}

// sessionKey 生成会话键
func sessionKey(roomID, userID string) string {
	return fmt.Sprintf("%s:%s", roomID, userID)
}

// Increment 增加计数
func (m *SessionTurnsManager) Increment(roomID, userID string) int {
	key := sessionKey(roomID, userID)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turns[key]++
	return m.turns[key]
}

// Get 获取计数
func (m *SessionTurnsManager) Get(roomID, userID string) int {
	key := sessionKey(roomID, userID)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.turns[key]
}

// Reset 重置计数
func (m *SessionTurnsManager) Reset(roomID, userID string) {
	key := sessionKey(roomID, userID)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.turns, key)
}
