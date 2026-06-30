package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WorkspaceManager 工作区管理器
// 负责管理 Agent 的本地工作区，包括文件存储和检索
type WorkspaceManager struct {
	coordinatorURL string
	agentID        string
	workspaceDir   string
	httpClient     *http.Client

	// 缓存管理
	cache       map[string]*FileCacheEntry
	cacheMu     sync.RWMutex
	cacheExpiry time.Duration

	// 下载中的文件
	pendingDownloads map[string]*DownloadTask
	downloadMu       sync.Mutex
}

// FileCacheEntry 文件缓存条目
type FileCacheEntry struct {
	LocalPath    string
	TransferID   string
	DownloadedAt time.Time
	FileSize     int64
	MimeType     string
}

// DownloadTask 下载任务
type DownloadTask struct {
	TransferID string
	Status     string // pending / downloading / completed / failed
	Progress   int    // 0-100
	Error      error
}

// FileUploadRequest 文件上传请求
type FileUploadRequest struct {
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	MimeType string `json:"mime_type"`
	ToAgent  string `json:"to_agent"`
	RoomID   string `json:"room_id"`
	TaskID   string `json:"task_id,omitempty"`
}

// FileUploadResponse 文件上传响应
type FileUploadResponse struct {
	TransferID   string `json:"transfer_id"`
	PresignedURL string `json:"presigned_url"`
	S3Key        string `json:"s3_key"`
}

// FileDownloadRequest 文件下载请求
type FileDownloadRequest struct {
	TransferID string `json:"transfer_id"`
}

// FileDownloadResponse 文件下载响应
type FileDownloadResponse struct {
	TransferID   string `json:"transfer_id"`
	PresignedURL string `json:"presigned_url"`
	S3Key        string `json:"s3_key"`
}

// FileTransfer 文件传输记录
type FileTransfer struct {
	TransferID string `json:"transfer_id"`
	FileName   string `json:"file_name"`
	FileSize   int64  `json:"file_size"`
	MimeType   string `json:"mime_type"`
	FromAgent  string `json:"from_agent"`
	ToAgent    string `json:"to_agent"`
	RoomID     string `json:"room_id"`
	TaskID     string `json:"task_id,omitempty"`
	S3Key      string `json:"s3_key"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
}

// ConfirmUploadRequest 确认上传请求
type ConfirmUploadRequest struct {
	TransferID string `json:"transfer_id"`
	FromAgent  string `json:"from_agent"`
	ToAgent    string `json:"to_agent"`
}

// NewWorkspaceManager 创建工作区管理器
func NewWorkspaceManager(coordinatorURL, agentID, workspaceDir string) *WorkspaceManager {
	if workspaceDir == "" {
		// 默认使用 ~/.x-client/workspace
		homeDir, _ := os.UserHomeDir()
		workspaceDir = filepath.Join(homeDir, ".x-client", "workspace", agentID)
	}

	// 确保目录存在
	os.MkdirAll(workspaceDir, 0755)

	return &WorkspaceManager{
		coordinatorURL: coordinatorURL,
		agentID:        agentID,
		workspaceDir:   workspaceDir,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		cache:            make(map[string]*FileCacheEntry),
		cacheExpiry:      30 * time.Minute,
		pendingDownloads: make(map[string]*DownloadTask),
	}
}

// GetWorkspaceDir 获取工作区目录（基于当前 agent）
func (w *WorkspaceManager) GetWorkspaceDir() string {
	return w.workspaceDir
}

// GetWorkspaceDirForRoom 获取指定聊天室的工作区目录
// 目录结构: {agent_workspace}/{room_id}/
func (w *WorkspaceManager) GetWorkspaceDirForRoom(roomID string) string {
	return filepath.Join(w.workspaceDir, roomID)
}

// EnsureWorkspaceDirForRoom 确保指定聊天室的工作区目录存在
func (w *WorkspaceManager) EnsureWorkspaceDirForRoom(roomID string) error {
	dir := w.GetWorkspaceDirForRoom(roomID)
	return os.MkdirAll(dir, 0755)
}

// UploadFile 请求上传文件（获取 Presigned URL）
func (w *WorkspaceManager) UploadFile(fileName string, fileSize int64, mimeType, roomID string, taskID string) (*FileUploadResponse, error) {
	req := FileUploadRequest{
		FileName: fileName,
		FileSize: fileSize,
		MimeType: mimeType,
		RoomID:   roomID,
		TaskID:   taskID,
	}
	jsonData, _ := json.Marshal(req)

	resp, err := w.httpClient.Post(
		w.coordinatorURL+"/api/file/upload-url",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return nil, fmt.Errorf("请求上传 URL 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("请求上传 URL 失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	var uploadResp FileUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&uploadResp); err != nil {
		return nil, fmt.Errorf("解析上传响应失败: %w", err)
	}

	return &uploadResp, nil
}

// UploadFileToS3 使用 Presigned URL 直接上传文件到 S3
func (w *WorkspaceManager) UploadFileToS3(localFilePath, presignedURL, mimeType string) error {
	// 打开本地文件
	file, err := os.Open(localFilePath)
	if err != nil {
		return fmt.Errorf("打开本地文件失败: %w", err)
	}
	defer file.Close()

	// 创建请求
	req, err := http.NewRequest("PUT", presignedURL, file)
	if err != nil {
		return fmt.Errorf("创建上传请求失败: %w", err)
	}
	req.Header.Set("Content-Type", mimeType)

	// 上传到 S3
	client := &http.Client{Timeout: 0} // 大文件可能需要较长时间
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("上传到 S3 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("上传到 S3 失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	log.Printf("[INFO] [WorkspaceManager] 文件上传完成: %s", localFilePath)
	return nil
}

// UploadFileToS3Data 使用 Presigned URL 直接上传数据到 S3
func (w *WorkspaceManager) UploadFileToS3Data(presignedURL string, data []byte, mimeType string) error {
	req, err := http.NewRequest("PUT", presignedURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("创建上传请求失败: %w", err)
	}
	req.Header.Set("Content-Type", mimeType)

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("上传到 S3 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("上传到 S3 失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	log.Printf("[INFO] [WorkspaceManager] 数据上传完成，大小: %d bytes", len(data))
	return nil
}

// ConfirmUpload 确认上传完成
func (w *WorkspaceManager) ConfirmUpload(transferID, fromAgent, toAgent string) error {
	req := ConfirmUploadRequest{
		TransferID: transferID,
		FromAgent:  fromAgent,
		ToAgent:    toAgent,
	}
	jsonData, _ := json.Marshal(req)

	resp, err := w.httpClient.Post(
		w.coordinatorURL+"/api/file/confirm-upload/"+transferID,
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return fmt.Errorf("确认上传请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("确认上传失败，状态码: %d", resp.StatusCode)
	}

	return nil
}

// DownloadFile 请求下载文件（获取 Presigned URL）
func (w *WorkspaceManager) DownloadFile(transferID string) (*FileDownloadResponse, error) {
	resp, err := w.httpClient.Get(
		fmt.Sprintf("%s/api/file/download-url?transfer_id=%s", w.coordinatorURL, transferID),
	)
	if err != nil {
		return nil, fmt.Errorf("请求下载 URL 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("请求下载 URL 失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	var downloadResp FileDownloadResponse
	if err := json.NewDecoder(resp.Body).Decode(&downloadResp); err != nil {
		return nil, fmt.Errorf("解析下载响应失败: %w", err)
	}

	return &downloadResp, nil
}

// DownloadFileFromS3 使用 Presigned URL 从 S3 下载文件
func (w *WorkspaceManager) DownloadFileFromS3(transferID, presignedURL, localFileName string) error {
	// 创建目标文件
	localPath := filepath.Join(w.workspaceDir, localFileName)

	// 如果目录不存在，创建
	os.MkdirAll(filepath.Dir(localPath), 0755)

	// 下载文件
	resp, err := w.httpClient.Get(presignedURL)
	if err != nil {
		return fmt.Errorf("从 S3 下载文件失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("从 S3 下载文件失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	// 保存到本地
	outFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("创建本地文件失败: %w", err)
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, resp.Body)
	if err != nil {
		return fmt.Errorf("保存文件失败: %w", err)
	}

	// 更新缓存
	w.cacheMu.Lock()
	w.cache[transferID] = &FileCacheEntry{
		LocalPath:    localPath,
		TransferID:   transferID,
		DownloadedAt: time.Now(),
	}
	w.cacheMu.Unlock()

	log.Printf("[INFO] [WorkspaceManager] 文件下载完成: %s -> %s", transferID, localPath)
	return nil
}

// DownloadFileFromS3ToPath 使用 Presigned URL 从 S3 下载文件到指定目录
func (w *WorkspaceManager) DownloadFileFromS3ToPath(transferID, presignedURL, localFileName, downloadDir string) error {
	// 创建目标文件
	localPath := filepath.Join(downloadDir, localFileName)

	// 如果目录不存在，创建
	os.MkdirAll(downloadDir, 0755)

	// 下载文件
	resp, err := w.httpClient.Get(presignedURL)
	if err != nil {
		return fmt.Errorf("从 S3 下载文件失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("从 S3 下载文件失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	// 保存到本地
	outFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("创建本地文件失败: %w", err)
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, resp.Body)
	if err != nil {
		return fmt.Errorf("保存文件失败: %w", err)
	}

	// 更新缓存
	w.cacheMu.Lock()
	w.cache[transferID] = &FileCacheEntry{
		LocalPath:    localPath,
		TransferID:   transferID,
		DownloadedAt: time.Now(),
	}
	w.cacheMu.Unlock()

	log.Printf("[INFO] [WorkspaceManager] 文件下载完成: %s -> %s", transferID, localPath)
	return nil
}

// DownloadFileFromS3Data 使用 Presigned URL 从 S3 下载数据
func (w *WorkspaceManager) DownloadFileFromS3Data(presignedURL string) ([]byte, error) {
	resp, err := w.httpClient.Get(presignedURL)
	if err != nil {
		return nil, fmt.Errorf("从 S3 下载文件失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("从 S3 下载文件失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取文件数据失败: %w", err)
	}

	return data, nil
}

// GetTransfer 获取文件传输记录
func (w *WorkspaceManager) GetTransfer(transferID string) (*FileTransfer, error) {
	resp, err := w.httpClient.Get(
		fmt.Sprintf("%s/api/file/transfer?transfer_id=%s", w.coordinatorURL, transferID),
	)
	if err != nil {
		return nil, fmt.Errorf("获取传输记录失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("获取传输记录失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	var transfer FileTransfer
	if err := json.NewDecoder(resp.Body).Decode(&transfer); err != nil {
		return nil, fmt.Errorf("解析传输记录失败: %w", err)
	}

	return &transfer, nil
}

// GetCachedFile 获取缓存的文件路径
func (w *WorkspaceManager) GetCachedFile(transferID string) (string, bool) {
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()

	if entry, exists := w.cache[transferID]; exists {
		// 检查是否过期
		if time.Since(entry.DownloadedAt) < w.cacheExpiry {
			return entry.LocalPath, true
		}
	}
	return "", false
}

// SaveToWorkspace 保存文件到工作区
func (w *WorkspaceManager) SaveToWorkspace(transferID, fileName string, data []byte) (string, error) {
	localPath := filepath.Join(w.workspaceDir, fileName)

	// 确保目录存在
	os.MkdirAll(filepath.Dir(localPath), 0755)

	// 写入文件
	if err := os.WriteFile(localPath, data, 0644); err != nil {
		return "", fmt.Errorf("保存文件到工作区失败: %w", err)
	}

	// 更新缓存
	w.cacheMu.Lock()
	w.cache[transferID] = &FileCacheEntry{
		LocalPath:    localPath,
		TransferID:   transferID,
		DownloadedAt: time.Now(),
		FileSize:     int64(len(data)),
	}
	w.cacheMu.Unlock()

	log.Printf("[INFO] [WorkspaceManager] 文件已保存到工作区: %s", localPath)
	return localPath, nil
}

// ListWorkspaceFiles 列出工作区中的文件
func (w *WorkspaceManager) ListWorkspaceFiles() ([]string, error) {
	var files []string

	err := filepath.Walk(w.workspaceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			relPath, _ := filepath.Rel(w.workspaceDir, path)
			files = append(files, relPath)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("列出工作区文件失败: %w", err)
	}

	return files, nil
}

// ClearCache 清除过期缓存
func (w *WorkspaceManager) ClearCache() {
	w.cacheMu.Lock()
	defer w.cacheMu.Unlock()

	now := time.Now()
	for transferID, entry := range w.cache {
		if now.Sub(entry.DownloadedAt) > w.cacheExpiry {
			delete(w.cache, transferID)
		}
	}
}

// GetReportsPath 获取报告目录
func (w *WorkspaceManager) GetReportsPath(roomID string) string {
	return filepath.Join(w.workspaceDir, "reports", roomID)
}

// ReadReport 读取 AgentCore 产出的报告
// 返回最新生成的报告内容
func (w *WorkspaceManager) ReadReport(roomID string) ([]byte, error) {
	reportsPath := w.GetReportsPath(roomID)

	// 检查目录是否存在
	if _, err := os.Stat(reportsPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("报告目录不存在: %s", reportsPath)
	}

	// 读取目录中的所有文件
	entries, err := os.ReadDir(reportsPath)
	if err != nil {
		return nil, fmt.Errorf("读取报告目录失败: %w", err)
	}

	// 找最新的文件
	var latestFile os.DirEntry
	var latestModTime time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if latestFile == nil || info.ModTime().After(latestModTime) {
			latestFile = entry
			latestModTime = info.ModTime()
		}
	}

	if latestFile == nil {
		return nil, fmt.Errorf("未找到报告文件")
	}

	// 读取最新报告
	reportPath := filepath.Join(reportsPath, latestFile.Name())
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return nil, fmt.Errorf("读取报告文件失败: %w", err)
	}

	log.Printf("[INFO] [WorkspaceManager] 已读取报告: %s", reportPath)
	return data, nil
}

// ListReports 列出所有报告文件
func (w *WorkspaceManager) ListReports(roomID string) ([]string, error) {
	reportsPath := w.GetReportsPath(roomID)

	if _, err := os.Stat(reportsPath); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(reportsPath)
	if err != nil {
		return nil, fmt.Errorf("读取报告目录失败: %w", err)
	}

	var reports []string
	for _, entry := range entries {
		if !entry.IsDir() {
			reports = append(reports, entry.Name())
		}
	}
	return reports, nil
}

// GetUploadsPath 获取上传文件目录
func (w *WorkspaceManager) GetUploadsPath(roomID string) string {
	return filepath.Join(w.workspaceDir, "uploads", roomID)
}

// GetDownloadsPath 获取下载缓存目录
// 目录结构: {agent_workspace}/{room_id}/downloads/
func (w *WorkspaceManager) GetDownloadsPath(roomID string) string {
	return filepath.Join(w.GetWorkspaceDirForRoom(roomID), "downloads")
}

// GetInboxPath 获取收件箱目录
func (w *WorkspaceManager) GetInboxPath() string {
	return filepath.Join(w.workspaceDir, "inbox", "messages")
}
