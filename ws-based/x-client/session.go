package main

import (
	"fmt"
	"sync"
	"time"
)

type SessionManager struct {
	counter   int64
	channelID string
	mu        sync.Mutex
}

func NewSessionManager(channelID string) *SessionManager {
	return &SessionManager{
		channelID: channelID,
	}
}

func (s *SessionManager) GenerateGroupSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	return fmt.Sprintf("group_%s_%d_%d",
		s.channelID,
		time.Now().UnixNano(),
		s.counter)
}