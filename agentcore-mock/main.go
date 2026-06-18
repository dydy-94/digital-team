package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type ChatRequest struct {
	Message    string `json:"message"`
	SessionID  string `json:"session_id"`
	Sender     string `json:"sender"`
}

type ChatResponse struct {
	Reply string `json:"reply"`
}

var mockResponses = []string{
	"好的，我已经收到了你的消息。",
	"根据历史记录，我来分析一下这个问题...",
	"我来帮你处理这个请求。",
	"收到，让我思考一下...",
	"这个问题很有意思，让我来分析。",
	"我已经了解了上下文，现在来回复。",
	"感谢你的提问，我来回答。",
	"好的，我明白了，这是我的回复。",
}

func chatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("[agentcore-mock] 收到请求 - Sender: %s, SessionID: %s, Message: %s", req.Sender, req.SessionID, req.Message)

	index := int(time.Now().UnixNano()) % len(mockResponses)
	reply := mockResponses[index]

	senderInfo := ""
	if req.Sender != "" {
		senderInfo = "收到了 " + req.Sender + " 发过来的消息，"
	}

	if len(req.Message) > 0 && req.Message[0] == '@' {
		reply = "[被@唤醒] " + senderInfo + reply
	} else {
		reply = senderInfo + reply
	}

	resp := ChatResponse{Reply: reply}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)

	log.Printf("[agentcore-mock] 返回响应 - Reply: %s", reply)
}

// chatStreamHandler SSE 流式响应
func chatStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("[agentcore-mock] 收到流式请求 - Sender: %s, SessionID: %s, Message: %s", req.Sender, req.SessionID, req.Message)

	// 生成回复
	index := int(time.Now().UnixNano()) % len(mockResponses)
	reply := mockResponses[index]

	senderInfo := ""
	if req.Sender != "" {
		senderInfo = "收到了 " + req.Sender + " 发过来的消息，"
	}

	if len(req.Message) > 0 && req.Message[0] == '@' {
		reply = "[被@唤醒] " + senderInfo + reply
	} else {
		reply = senderInfo + reply
	}

	// 设置 SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("[agentcore-mock] SSE 不支持")
		return
	}
	// 刷新 headers
	flusher.Flush()

	// 将回复分词逐步发送
	words := splitIntoWords(reply)

	for i, word := range words {
		// 模拟 AI 生成延迟（每个词之间随机延迟）
		delay := 50 + (i % 100) // 50-150ms 随机延迟
		time.Sleep(time.Duration(delay) * time.Millisecond)

		// 发送 SSE 数据
		fmt.Fprintf(w, "data: %s\n\n", word)
		flusher.Flush()
		log.Printf("[agentcore-mock] 发送流式数据 - Word: %s", word)
	}

	// 发送完成信号
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	log.Printf("[agentcore-mock] 流式响应完成 - Total words: %d", len(words))
}

// splitIntoWords 将文本分割成单词/词组用于流式传输
func splitIntoWords(text string) []string {
	var words []string
	var current strings.Builder

	for i, ch := range text {
		current.WriteRune(ch)

		// 在标点符号后、最后一个字符、或特定条件时分词
		isPunct := ch == '.' || ch == ',' || ch == '，' || ch == '。' || ch == '！' || ch == '？' || ch == '、'
		isLast := i == len(text)-1

		if isPunct || isLast || current.Len() >= 5 {
			words = append(words, current.String())
			current.Reset()
		}
	}

	// 确保最后一个词被添加
	if current.Len() > 0 {
		words = append(words, current.String())
	}

	return words
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func main() {
	var listenAddr string
	flag.StringVar(&listenAddr, "listen", ":8000", "HTTP listen address")
	flag.Parse()

	http.HandleFunc("/chat", chatHandler)
	http.HandleFunc("/chat/stream", chatStreamHandler) // SSE 流式端点
	http.HandleFunc("/health", healthHandler)

	srv := &http.Server{
		Addr:              listenAddr,
		ReadHeaderTimeout: 3 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("Shutting down agentcore-mock...")
		srv.Shutdown(context.Background())
	}()

	log.Printf("agentcore-mock 启动在 %s", listenAddr)
	log.Fatal(srv.ListenAndServe())
}