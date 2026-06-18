package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
)

func main() {
	// 加载配置
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 初始化数据库
	storage, err := NewStorage(cfg)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	defer storage.Close()

	// 初始化处理器
	handler := NewHandler(storage, cfg)

	// 创建路由
	router := mux.NewRouter()

	// ============ Agent HTTP API ============
	router.HandleFunc("/api/agent/register", handler.RegisterHandler).Methods("POST")
	router.HandleFunc("/api/agent/heartbeat", handler.HeartbeatHandler).Methods("POST")
	router.HandleFunc("/api/poll", handler.PollHandler).Methods("GET")
	router.HandleFunc("/api/message", handler.SendMessageHandler).Methods("POST")

	// ============ User API ============
	router.HandleFunc("/api/user/register", handler.RegisterUserHandler).Methods("POST")
	router.HandleFunc("/api/user/get", handler.GetUserHandler).Methods("GET")
	router.HandleFunc("/api/user/login", handler.LoginHandler).Methods("POST")

	// ============ Room API ============
	router.HandleFunc("/api/rooms", handler.GetRoomsHandler).Methods("GET")
	router.HandleFunc("/api/room/create", handler.CreateRoomHandler).Methods("POST")
	router.HandleFunc("/api/room/join", handler.JoinRoomHandler).Methods("POST")
	router.HandleFunc("/api/room/leave", handler.LeaveRoomPOSTHandler).Methods("POST")  // POST 方法
	router.HandleFunc("/api/room/history", handler.GetHistoryHandler).Methods("GET")   // 历史消息
	router.HandleFunc("/api/room/members", handler.GetRoomMembersByQueryHandler).Methods("GET")  // 查询参数版本
	router.HandleFunc("/api/room/{room_id}/members", handler.GetRoomMembersHandler).Methods("GET")  // 路径参数版本
	router.HandleFunc("/api/room/{room_id}/leave/{member_id}", handler.LeaveRoomHandler).Methods("DELETE")

	// ============ User WebSocket ============
	router.HandleFunc("/ws/user", handler.UserWSHandler)
	router.HandleFunc("/ws/chat", handler.ChatWSHandler)  // 聊天室 WebSocket

	// ============ 健康检查 ============
	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// ============ 静态文件路由 ============
	router.PathPrefix("/ui-test/").Handler(http.StripPrefix("/ui-test/", http.FileServer(http.Dir("/Users/cdy/opensource/x-client/ui-test"))))

	// 创建 HTTP 服务
	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// 启动服务
	go func() {
		log.Printf("[INFO] Coordinator HTTP 服务启动，监听: %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[INFO] 正在关闭服务...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("服务关闭失败: %v", err)
	}

	log.Println("[INFO] 服务已关闭")
}
