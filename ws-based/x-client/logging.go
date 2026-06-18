package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"
)

type LogEntry struct {
	Timestamp  string `json:"timestamp"`
	Level      string `json:"level"`
	Component  string `json:"component"`
	ChannelID  string `json:"channel_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	MsgID      string `json:"msg_id,omitempty"`
	Intent     string `json:"intent,omitempty"`
	LatencyMs  int64  `json:"latency_ms,omitempty"`
	Message    string `json:"message"`
}

func (e *LogEntry) String() string {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Sprintf("[%s] %s: %s", e.Timestamp, e.Level, e.Message)
	}
	return string(data)
}

func logInfo(component, agentID, message string, fields ...interface{}) {
	entry := &LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     "INFO",
		Component: component,
		AgentID:   agentID,
		Message:   message,
	}
	applyFields(entry, fields)
	log.Println(entry)
}

func logError(component, agentID, message string, fields ...interface{}) {
	entry := &LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     "ERROR",
		Component: component,
		AgentID:   agentID,
		Message:   message,
	}
	applyFields(entry, fields)
	log.Println(entry)
}

func logWarn(component, agentID, message string, fields ...interface{}) {
	entry := &LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     "WARN",
		Component: component,
		AgentID:   agentID,
		Message:   message,
	}
	applyFields(entry, fields)
	log.Println(entry)
}

func logDebug(component, agentID, message string, fields ...interface{}) {
	entry := &LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     "DEBUG",
		Component: component,
		AgentID:   agentID,
		Message:   message,
	}
	applyFields(entry, fields)
	log.Println(entry)
}

func applyFields(entry *LogEntry, fields []interface{}) {
	for i := 0; i < len(fields); i += 2 {
		if i+1 >= len(fields) {
			break
		}
		key, ok := fields[i].(string)
		if !ok {
			continue
		}
		switch key {
		case "channel_id":
			entry.ChannelID, _ = fields[i+1].(string)
		case "msg_id":
			entry.MsgID, _ = fields[i+1].(string)
		case "intent":
			entry.Intent, _ = fields[i+1].(string)
		case "latency_ms":
			entry.LatencyMs, _ = fields[i+1].(int64)
		}
	}
}