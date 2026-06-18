package main

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type A2AMessage struct {
	MsgId        string   `json:"msgId"`
	ChannelId    string   `json:"channelId"`
	Sender       string   `json:"sender"`
	Target       string   `json:"target"`
	MentionUsers []string `json:"mentionUsers,omitempty"`
	Intent       string   `json:"intent"`
	ContentText  string   `json:"contentText"`
	Timestamp    int64    `json:"timestamp"`
	ReplyToMsgId string   `json:"replyToMsgId,omitempty"`
	// 流式响应支持
	Status      string `json:"status,omitempty"`      // "thinking", "streaming", "completed"
	ParentMsgId string `json:"parentMsgId,omitempty"` // 父消息ID，用于流式响应的关联
}

type ClientMessage struct {
	Action string          `json:"action"`
	Data   json.RawMessage `json:"data"`
}

type ServerMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type JoinData struct {
	ChannelId string `json:"channelId"`
	AgentId   string `json:"agentId"`
}

type SpeakData struct {
	A2AMessage
}

type SyncData struct {
	ChannelId string `json:"channelId"`
	Count     int    `json:"count"`
}

func NewSystemMessage(channelId, content string) *A2AMessage {
	return &A2AMessage{
		MsgId:       uuid.New().String(),
		ChannelId:   channelId,
		Sender:      "system",
		Target:      "ALL",
		Intent:      "INFORM",
		ContentText: content,
		Timestamp:   time.Now().Unix(),
	}
}

func NewRejectMessage(originalMsgId, reason string) *ServerMessage {
	return &ServerMessage{
		Type: "error",
		Data: map[string]string{
			"replyToMsgId": originalMsgId,
			"reason":       reason,
		},
	}
}
