package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

type ProcessInfo struct {
	ID            string `json:"id"`
	XClientPort   int    `json:"xclientPort"`
	AgentcorePort int    `json:"agentcorePort"`
	Process       *exec.Cmd
}

var coordinatorProcess *exec.Cmd
var agentProcesses []*ProcessInfo

func main() {
	defer func() {
		if r := recover(); r != nil {
			now := time.Now().Format("2006-01-02 15:04:05")
			fmt.Printf("[%s] Panic recovered: %v\n", now, r)
			fmt.Printf("[%s] Stack trace:\n%s\n", now, debug.Stack())
		}
	}()

	var listenAddr string
	flag.StringVar(&listenAddr, "listen", ":9000", "HTTP listen address")
	flag.Parse()

	fmt.Println("1. Registering handlers...")
	http.HandleFunc("/api/health", handleHealth)
	http.HandleFunc("/api/status", handleGetStatus)
	http.HandleFunc("/api/coordinator/start", handleStartCoordinator)
	http.HandleFunc("/api/coordinator/stop", handleStopCoordinator)
	http.HandleFunc("/api/agents/start", handleStartAgents)
	http.HandleFunc("/api/agents/stop", handleStopAgents)

	// 获取 ui-test 目录的绝对路径（manager 二进制在 manager/ 目录下）
	fmt.Println("2. Getting ui-test dir path...")
	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("Error getting executable path: %v\n", err)
		return
	}
	fmt.Printf("exePath: %s\n", exePath)
	exeDir := filepath.Dir(exePath)
	fmt.Printf("exeDir: %s\n", exeDir)

	// manager/ 的上级目录就是 ui-test
	uiTestAbsPath := filepath.Dir(exeDir)
	fmt.Printf("uiTestAbsPath: %s\n", uiTestAbsPath)

	testFilePath := filepath.Join(uiTestAbsPath, "index.html")
	fmt.Printf("index.html path: %s\n", testFilePath)

	// 检查文件是否存在
	if _, err := os.Stat(testFilePath); os.IsNotExist(err) {
		fmt.Printf("Warning: index.html not found at %s\n", testFilePath)
	}

	// API代理到协调器（/api/rooms, /api/room/create, /api/room/delete等）
	// 注意：必须在静态文件服务之前注册，否则会被静态文件服务拦截
	fmt.Println("3. Creating proxy...")
	targetURL, err := url.Parse("http://localhost:8080")
	if err != nil {
		fmt.Printf("Error parsing target URL: %v\n", err)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	http.Handle("/api/", proxy)

	// WebSocket代理到协调器（使用自定义处理函数确保查询参数正确传递）
	http.HandleFunc("/ws/", handleWebSocketProxy)

	// 静态文件服务（ui-test目录）- 必须放在最后
	fmt.Printf("4. Serving static files from: %s\n", uiTestAbsPath)

	// 静态文件服务（ui-test目录）- 使用正确的方法
	fs := http.FileServer(http.Dir(uiTestAbsPath))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 禁用缓存，确保浏览器总是获取最新文件
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		if r.URL.Path == "/" {
			http.ServeFile(w, r, filepath.Join(uiTestAbsPath, "index.html"))
		} else {
			fs.ServeHTTP(w, r)
		}
	})

	fmt.Println("5. Creating server...")
	srv := &http.Server{
		Addr:              listenAddr,
		ReadHeaderTimeout: 3 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("Shutting down manager...")
		stopAll()
		srv.Shutdown(nil)
	}()

	fmt.Printf("6. Starting server on %s...\n", listenAddr)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func handleGetStatus(w http.ResponseWriter, r *http.Request) {
	isCoordinatorRunning := coordinatorProcess != nil

	agentList := make([]map[string]interface{}, 0)
	for _, agent := range agentProcesses {
		agentList = append(agentList, map[string]interface{}{
			"id":            agent.ID,
			"xclientPort":   agent.XClientPort,
			"agentcorePort": agent.AgentcorePort,
		})
	}

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"coordinatorRunning": isCoordinatorRunning,
		"agentsRunning":      len(agentProcesses) > 0,
		"agents":             agentList,
	})
}

func handleStartCoordinator(w http.ResponseWriter, r *http.Request) {
	if coordinatorProcess != nil {
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "协调器已在运行",
		})
		return
	}

	cmd := exec.Command("/Users/cdy/opensource/x-client/coordinator/bin/coordinator", "--config", "/Users/cdy/opensource/x-client/coordinator/config.json")
	cmd.Dir = "/Users/cdy/opensource/x-client/coordinator"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// 设置独立进程组，防止父进程退出时子进程被杀死
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}
	if err := cmd.Start(); err != nil {
		sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	coordinatorProcess = cmd
	log.Println("Coordinator started")

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

func handleStopCoordinator(w http.ResponseWriter, r *http.Request) {
	if coordinatorProcess == nil {
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "协调器未运行",
		})
		return
	}

	if coordinatorProcess.Process != nil {
		coordinatorProcess.Process.Kill()
	}
	coordinatorProcess.Wait()
	coordinatorProcess = nil
	log.Println("Coordinator stopped")

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

func handleStartAgents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Invalid request",
		})
		return
	}

	if req.Count < 1 || req.Count > 3 {
		sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Agent count must be 1-3",
		})
		return
	}

	stopAgents()

	agents := make([]*ProcessInfo, 0)
	startPort := 8000

	for i := 0; i < req.Count; i++ {
		agentID := fmt.Sprintf("agent_%d", i+1)
		agentcorePort := startPort + i*2
		xclientPort := startPort + i*2 + 1

		agentcoreCmd := exec.Command("/Users/cdy/opensource/x-client/agentcore-mock/bin/agentcore-mock",
			fmt.Sprintf("-listen=:%d", agentcorePort))
		agentcoreCmd.Dir = "/Users/cdy/opensource/x-client/agentcore-mock"
		agentcoreCmd.Stdout = os.Stdout
		agentcoreCmd.Stderr = os.Stderr
		agentcoreCmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,
			Pgid:    0,
		}

		xclientCmd := exec.Command("/Users/cdy/opensource/x-client/x-client/bin/x-client",
			fmt.Sprintf("--agent-id=%s", agentID),
			"--coordinator=http://localhost:8080",
			fmt.Sprintf("--agentcore=http://localhost:%d", agentcorePort),
			fmt.Sprintf("--listen=:%d", xclientPort))
		xclientCmd.Dir = "/Users/cdy/opensource/x-client/x-client"
		xclientCmd.Stdout = os.Stdout
		xclientCmd.Stderr = os.Stderr
		xclientCmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,
			Pgid:    0,
		}

		if err := agentcoreCmd.Start(); err != nil {
			stopAgents()
			sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("Failed to start agentcore %d: %v", i+1, err),
			})
			return
		}

		time.Sleep(200 * time.Millisecond)

		if err := xclientCmd.Start(); err != nil {
			if agentcoreCmd.Process != nil {
				agentcoreCmd.Process.Kill()
				agentcoreCmd.Wait()
			}
			stopAgents()
			sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("Failed to start x-client %d: %v", i+1, err),
			})
			return
		}

		agents = append(agents, &ProcessInfo{
			ID:            agentID,
			XClientPort:   xclientPort,
			AgentcorePort: agentcorePort,
			Process:       xclientCmd,
		})

		log.Printf("Started agent %s: x-client=%d, agentcore=%d", agentID, xclientPort, agentcorePort)
	}

	agentProcesses = agents

	agentList := make([]map[string]interface{}, 0)
	for _, agent := range agents {
		agentList = append(agentList, map[string]interface{}{
			"id":            agent.ID,
			"xclientPort":   agent.XClientPort,
			"agentcorePort": agent.AgentcorePort,
		})
	}

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"agents":  agentList,
	})
}

func handleStopAgents(w http.ResponseWriter, r *http.Request) {
	stopAgents()
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

func stopAgents() {
	for _, agent := range agentProcesses {
		if agent.Process != nil && agent.Process.Process != nil {
			agent.Process.Process.Kill()
			agent.Process.Wait()
		}
	}
	agentProcesses = nil
	log.Println("All agents stopped")
}

func stopAll() {
	if coordinatorProcess != nil {
		if coordinatorProcess.Process != nil {
			coordinatorProcess.Process.Kill()
		}
		coordinatorProcess.Wait()
		coordinatorProcess = nil
	}
	stopAgents()
}

func sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// handleWebSocketProxy WebSocket 代理处理函数
// 使用 httputil.ReverseProxy 正确转发 WebSocket 连接和查询参数
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func handleWebSocketProxy(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("[WS Proxy] Incoming: %s?%s\n", r.URL.Path, r.URL.RawQuery)

	// 升级到 WebSocket
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("[WS Proxy] Upgrade error: %v\n", err)
		return
	}

	// 目标 WebSocket 地址
	targetURL := "ws://localhost:8080" + r.URL.Path + "?" + r.URL.RawQuery
	fmt.Printf("[WS Proxy] Dialing: %s\n", targetURL)

	// 连接到后端
	targetConn, _, err := websocket.DefaultDialer.Dial(targetURL, nil)
	if err != nil {
		fmt.Printf("[WS Proxy] Dial error: %v\n", err)
		conn.Close()
		return
	}

	// 双向复制数据（客户端 -> 后端）
	go func() {
		defer func() {
			fmt.Printf("[WS Proxy] Client->Target goroutine ending\n")
			targetConn.Close()
			conn.Close()
		}()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					fmt.Printf("[WS Proxy] Client read error: %v\n", err)
				} else {
					fmt.Printf("[WS Proxy] Client read (EOF): %v\n", err)
				}
				return
			}
			fmt.Printf("[WS Proxy] Forwarding client message to target: %s\n", string(msg))
			if err := targetConn.WriteMessage(websocket.TextMessage, msg); err != nil {
				fmt.Printf("[WS Proxy] Target write error: %v\n", err)
				return
			}
		}
	}()

	// 双向复制数据（后端 -> 客户端）
	go func() {
		defer func() {
			fmt.Printf("[WS Proxy] Target->Client goroutine ending\n")
			targetConn.Close()
			conn.Close()
		}()
		for {
			_, msg, err := targetConn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					fmt.Printf("[WS Proxy] Target read error: %v\n", err)
				} else {
					fmt.Printf("[WS Proxy] Target read (EOF): %v\n", err)
				}
				return
			}
			fmt.Printf("[WS Proxy] Forwarding message to client: %s\n", string(msg))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				fmt.Printf("[WS Proxy] Client write error: %v\n", err)
				return
			}
		}
	}()

	// 等待任一 goroutine 结束
	select {}
}
