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

	// 初始化 S3 客户端
	s3Client, err := NewS3Client(cfg)
	if err != nil {
		log.Fatalf("初始化 S3 客户端失败: %v", err)
	}

	// 初始化处理器
	h := NewHandler(storage, cfg, s3Client)

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

	// ============ Task API ============
	router.HandleFunc("/api/task/create", h.CreateTaskHandler).Methods("POST")
	router.HandleFunc("/api/task/{task_id}", h.GetTaskHandler).Methods("GET")
	router.HandleFunc("/api/task/{task_id}", h.UpdateTaskHandler).Methods("PUT")
	router.HandleFunc("/api/task/{task_id}", h.DeleteTaskHandler).Methods("DELETE")
	router.HandleFunc("/api/room/{room_id}/tasks", h.GetTasksByRoomHandler).Methods("GET")
	router.HandleFunc("/api/agent/{agent_id}/tasks", h.GetTasksByAgentHandler).Methods("GET")
	router.HandleFunc("/api/tasks/batch", h.BatchGetTasksHandler).Methods("POST")

	// ============ Focus Item API ============
	router.HandleFunc("/api/task/{task_id}/focus", h.CreateFocusItemHandler).Methods("POST")
	router.HandleFunc("/api/task/{task_id}/focus", h.GetFocusItemsHandler).Methods("GET")
	router.HandleFunc("/api/focus/{item_id}", h.UpdateFocusItemHandler).Methods("PUT")
	router.HandleFunc("/api/focus/{item_id}", h.DeleteFocusItemHandler).Methods("DELETE")

	// ============ Permission API ============
	router.HandleFunc("/api/agent/{agent_id}/permission", h.GetPermissionHandler).Methods("GET")
	router.HandleFunc("/api/agent/{agent_id}/permission", h.UpsertPermissionHandler).Methods("PUT")
	router.HandleFunc("/api/agent/{agent_id}/permission", h.DeletePermissionHandler).Methods("DELETE")
	router.HandleFunc("/api/agent/{agent_id}/permission/check", h.CheckPermissionHandler).Methods("GET")

	// ============ File Transfer API ============
	router.HandleFunc("/api/file/upload-url", h.RequestUploadURLHandler).Methods("POST")
	router.HandleFunc("/api/file/download-url", h.RequestDownloadURLHandler).Methods("GET")
	router.HandleFunc("/api/file/confirm-upload/{transfer_id}", h.ConfirmUploadHandler).Methods("POST")
	router.HandleFunc("/api/file/transfer", h.GetFileTransferHandler).Methods("GET")
	router.HandleFunc("/api/room/{room_id}/files", h.GetRoomFileTransfersHandler).Methods("GET")

	// ============ Agent 关系 API ============
	router.HandleFunc("/api/agent/relation", h.CreateRelationHandler).Methods("POST")
	router.HandleFunc("/api/agent/relations", h.GetAgentRelationsHandler).Methods("GET")
	router.HandleFunc("/api/agent/relation/{id}", h.DeleteRelationHandler).Methods("DELETE")
	router.HandleFunc("/api/agent/context", h.GetAgentContextHandler).Methods("GET")
	router.HandleFunc("/api/room/{room_id}/agents", h.GetRoomAgentsHandler).Methods("GET")
	router.HandleFunc("/api/room/{room_id}/config", h.UpdateRoomConfigHandler).Methods("PUT")

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
