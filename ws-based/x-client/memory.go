package main

import (
	"bytes"
	"fmt"
	"sync"
)

type MemoryWindow struct {
	messages     []string
	maxSize      int
	maxChars     int
	currentChars int
	mu           sync.RWMutex
}

func NewMemoryWindow(maxSize, maxChars int) *MemoryWindow {
	return &MemoryWindow{
		messages: make([]string, 0),
		maxSize:  maxSize,
		maxChars: maxChars,
	}
}

func (mw *MemoryWindow) Push(sender, content string) {
	mw.mu.Lock()
	defer mw.mu.Unlock()

	line := fmt.Sprintf("[%s]: %s", sender, content)
	lineChars := len(line)

	mw.messages = append(mw.messages, line)
	mw.currentChars += lineChars

	for len(mw.messages) > mw.maxSize || mw.currentChars > mw.maxChars {
		removed := mw.messages[0]
		mw.messages = mw.messages[1:]
		mw.currentChars -= len(removed)
	}
}

func (mw *MemoryWindow) BuildContext(currentSender, currentContent string) string {
	mw.mu.RLock()
	defer mw.mu.RUnlock()

	var buf bytes.Buffer
	buf.WriteString("【系统通知：以下是群聊历史记录，你未参与其中】\n")
	buf.WriteString("--------------------------------------------------\n")

	for _, line := range mw.messages {
		buf.WriteString(line + "\n")
	}

	buf.WriteString("--------------------------------------------------\n")
	buf.WriteString(fmt.Sprintf("【请根据以上历史回复以下消息】\n[%s 对你说]: %s",
		currentSender, currentContent))

	return buf.String()
}

func (mw *MemoryWindow) Clear() {
	mw.mu.Lock()
	defer mw.mu.Unlock()
	mw.messages = make([]string, 0)
	mw.currentChars = 0
}
