package main

import (
	"context"
	"log"
	"log/slog"
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

	// 设置日志级别
	var logLevel slog.Level
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	logHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(logHandler))

	// 初始化数据库
	storage, err := NewStorage(cfg)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}

	// 初始化处理器
	h := NewHandler(storage, cfg)

	// 创建路由
	router := mux.NewRouter()

	// ============ Agent HTTP API ============
	router.HandleFunc("/api/agent/register", h.RegisterHandler).Methods("POST")
	router.HandleFunc("/api/agent/heartbeat", h.HeartbeatHandler).Methods("POST")
	router.HandleFunc("/api/poll", h.PollHandler).Methods("GET")
	router.HandleFunc("/api/message", h.SendMessageHandler).Methods("POST")

	// ============ User API ============
	router.HandleFunc("/api/user/register", h.RegisterUserHandler).Methods("POST")
	router.HandleFunc("/api/user/get", h.GetUserHandler).Methods("GET")
	router.HandleFunc("/api/user/login", h.LoginHandler).Methods("POST")

	// ============ Room API ============
	router.HandleFunc("/api/rooms", h.GetRoomsHandler).Methods("GET")
	router.HandleFunc("/api/room/create", h.CreateRoomHandler).Methods("POST")
	router.HandleFunc("/api/room/join", h.JoinRoomHandler).Methods("POST")
	router.HandleFunc("/api/room/leave", h.LeaveRoomPOSTHandler).Methods("POST")             // POST 方法
	router.HandleFunc("/api/room/history", h.GetHistoryHandler).Methods("GET")               // 历史消息
	router.HandleFunc("/api/room/members", h.GetRoomMembersByQueryHandler).Methods("GET")    // 查询参数版本
	router.HandleFunc("/api/room/{room_id}/members", h.GetRoomMembersHandler).Methods("GET") // 路径参数版本
	router.HandleFunc("/api/room/{room_id}/leave/{member_id}", h.LeaveRoomHandler).Methods("DELETE")

	// ============ User WebSocket ============
	router.HandleFunc("/ws/user", h.UserWSHandler)
	router.HandleFunc("/ws/chat", h.ChatWSHandler) // 聊天室 WebSocket

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
		slog.Info("Coordinator HTTP 服务启动", "listen_addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("正在关闭服务...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("服务关闭失败: %v", err)
	}

	slog.Info("服务已关闭")
}
