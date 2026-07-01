package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileSystemTemplateLoader 文件系统模板加载器
type FileSystemTemplateLoader struct {
	dataDir string
	agentID string
}

// NewFileSystemTemplateLoader 创建模板加载器
func NewFileSystemTemplateLoader(dataDir, agentID string) *FileSystemTemplateLoader {
	return &FileSystemTemplateLoader{
		dataDir: dataDir,
		agentID: agentID,
	}
}

// GetPath 获取模板文件路径
func (l *FileSystemTemplateLoader) GetPath(name string) string {
	return filepath.Join(l.dataDir, name)
}

// LoadSoul 加载 soul.md
func (l *FileSystemTemplateLoader) LoadSoul() (*Soul, error) {
	path := l.GetPath("soul.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 模板不存在，返回nil
		}
		return nil, fmt.Errorf("读取 soul.md 失败: %w", err)
	}

	return ParseSoulMarkdown(string(data))
}

// LoadBootstrap 加载 bootstrap.md
func (l *FileSystemTemplateLoader) LoadBootstrap() (*Bootstrap, error) {
	path := l.GetPath("bootstrap.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 bootstrap.md 失败: %w", err)
	}

	return ParseBootstrapMarkdown(string(data))
}

// LoadMeta 加载 meta.yaml
func (l *FileSystemTemplateLoader) LoadMeta() (*TemplateMeta, error) {
	path := l.GetPath("meta.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 meta.yaml 失败: %w", err)
	}

	var meta TemplateMeta
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("解析 meta.yaml 失败: %w", err)
	}

	return &meta, nil
}

// LoadTemplate 加载完整模板
func (l *FileSystemTemplateLoader) LoadTemplate() (*AgentTemplate, error) {
	soul, err := l.LoadSoul()
	if err != nil {
		return nil, err
	}

	bootstrap, err := l.LoadBootstrap()
	if err != nil {
		return nil, err
	}

	meta, err := l.LoadMeta()
	if err != nil {
		return nil, err
	}

	return &AgentTemplate{
		Soul:      soul,
		Bootstrap: bootstrap,
		Meta:      meta,
	}, nil
}

// CoordinatorTemplateLoader 从 Coordinator API 加载模板
type CoordinatorTemplateLoader struct {
	agentID        string
	coordinatorURL string
	httpClient     *HTTPClient
}

// HTTPClient HTTP客户端接口（简化版）
type HTTPClient interface {
	Get(url string) (*HTTPResponse, error)
}

// HTTPResponse HTTP响应
type HTTPResponse struct {
	StatusCode int
	Body       []byte
}

// NewCoordinatorTemplateLoader 创建 Coordinator 模板加载器
func NewCoordinatorTemplateLoader(agentID, coordinatorURL string) *CoordinatorTemplateLoader {
	return &CoordinatorTemplateLoader{
		agentID:        agentID,
		coordinatorURL: coordinatorURL,
	}
}

// GetTemplate 从 Coordinator 获取模板
func (l *CoordinatorTemplateLoader) GetTemplate() (*AgentTemplate, error) {
	url := fmt.Sprintf("%s/api/agent/%s/template", l.coordinatorURL, l.agentID)

	resp, err := httpGet(url)
	if err != nil {
		return nil, fmt.Errorf("从 Coordinator 获取模板失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, nil // 模板不存在
		}
		return nil, fmt.Errorf("获取模板失败，状态码: %d", resp.StatusCode)
	}

	var result struct {
		Success  bool           `json:"success"`
		Template *AgentTemplate `json:"template"`
		Error    string         `json:"error,omitempty"`
	}

	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("解析模板响应失败: %w", err)
	}

	if !result.Success {
		return nil, fmt.Errorf("获取模板失败: %s", result.Error)
	}

	return result.Template, nil
}

// httpGet 简化版 HTTP GET
func httpGet(url string) (*HTTPResponse, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return &HTTPResponse{
		StatusCode: resp.StatusCode,
		Body:       body,
	}, nil
}

// ParseSoulMarkdown 解析 soul.md markdown 内容
func ParseSoulMarkdown(content string) (*Soul, error) {
	soul := &Soul{
		Personality: []string{},
		WorkStyle:   []string{},
		Boundaries:  []string{},
	}

	lines := strings.Split(content, "\n")
	currentSection := ""

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// 跳过空行
		if line == "" {
			continue
		}

		// 检测章节
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "## identity") {
			currentSection = "identity"
			continue
		} else if strings.HasPrefix(lower, "## personality") {
			currentSection = "personality"
			continue
		} else if strings.HasPrefix(lower, "## work style") || strings.HasPrefix(lower, "## work") {
			currentSection = "work_style"
			continue
		} else if strings.HasPrefix(lower, "## boundaries") {
			currentSection = "boundaries"
			continue
		}

		// 解析内容
		switch currentSection {
		case "identity":
			// 解析 key: value 格式
			if idx := strings.Index(line, ":"); idx > 0 {
				key := strings.TrimSpace(strings.ToLower(line[:idx]))
				value := strings.TrimSpace(line[idx+1:])
				switch key {
				case "role":
					soul.Identity.Role = value
				case "expertise":
					soul.Identity.Expertise = value
				case "creator":
					soul.Identity.Creator = value
				}
			}

		case "personality":
			// bullet points (以 - 或 * 开头)
			text := strings.TrimPrefix(line, "-")
			text = strings.TrimPrefix(text, "*")
			text = strings.TrimSpace(text)
			if text != "" {
				soul.Personality = append(soul.Personality, text)
			}

		case "work_style":
			text := strings.TrimPrefix(line, "-")
			text = strings.TrimPrefix(text, "*")
			text = strings.TrimSpace(text)
			if text != "" {
				soul.WorkStyle = append(soul.WorkStyle, text)
			}

		case "boundaries":
			text := strings.TrimPrefix(line, "-")
			text = strings.TrimPrefix(text, "*")
			text = strings.TrimSpace(text)
			if text != "" {
				soul.Boundaries = append(soul.Boundaries, text)
			}
		}
	}

	return soul, nil
}

// ParseBootstrapMarkdown 解析 bootstrap.md markdown 内容
func ParseBootstrapMarkdown(content string) (*Bootstrap, error) {
	bootstrap := &Bootstrap{
		Capabilities: []string{},
	}

	lines := strings.Split(content, "\n")
	inGreeting := false
	inDeliverable := false
	var greetingLines, deliverableLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 检测分支标记
		if strings.Contains(trimmed, "user_turns == 0") || strings.Contains(trimmed, "greeting turn") {
			inGreeting = true
			inDeliverable = false
			continue
		} else if strings.Contains(trimmed, "user_turns >= 1") || strings.Contains(trimmed, "deliverable turn") {
			inDeliverable = true
			inGreeting = false
			continue
		}

		// 收集内容
		if inGreeting {
			greetingLines = append(greetingLines, line)
		} else if inDeliverable {
			deliverableLines = append(deliverableLines, line)
		}

		// 解析能力列表
		if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") {
			text := strings.TrimPrefix(trimmed, "-")
			text = strings.TrimPrefix(text, "*")
			text = strings.TrimSpace(text)
			if text != "" && !strings.HasPrefix(text, "If ") && !strings.HasPrefix(text, "user_") {
				bootstrap.Capabilities = append(bootstrap.Capabilities, text)
			}
		}

		// 解析退出引导
		if strings.Contains(trimmed, "close") || strings.Contains(trimmed, "exit") {
			bootstrap.ExitPrompt = trimmed
		}
	}

	// 组合模板
	if len(greetingLines) > 0 {
		bootstrap.GreetingTemplate = strings.TrimSpace(strings.Join(greetingLines, "\n"))
	}
	if len(deliverableLines) > 0 {
		bootstrap.DeliverableTemplate = strings.TrimSpace(strings.Join(deliverableLines, "\n"))
	}

	return bootstrap, nil
}

// BuildSoulContext 构建 soul 上下文字符串，供 AgentCore 使用
func BuildSoulContext(soul *Soul) string {
	if soul == nil {
		return ""
	}

	var sb strings.Builder

	sb.WriteString("\n## Identity\n")
	sb.WriteString(fmt.Sprintf("- Role: %s\n", soul.Identity.Role))
	if soul.Identity.Expertise != "" {
		sb.WriteString(fmt.Sprintf("- Expertise: %s\n", soul.Identity.Expertise))
	}
	if soul.Identity.Creator != "" {
		sb.WriteString(fmt.Sprintf("- Creator: %s\n", soul.Identity.Creator))
	}

	if len(soul.Personality) > 0 {
		sb.WriteString("\n## Personality\n")
		for _, p := range soul.Personality {
			sb.WriteString(fmt.Sprintf("- %s\n", p))
		}
	}

	if len(soul.WorkStyle) > 0 {
		sb.WriteString("\n## Work Style\n")
		for _, w := range soul.WorkStyle {
			sb.WriteString(fmt.Sprintf("- %s\n", w))
		}
	}

	if len(soul.Boundaries) > 0 {
		sb.WriteString("\n## Boundaries\n")
		for _, b := range soul.Boundaries {
			sb.WriteString(fmt.Sprintf("- %s\n", b))
		}
	}

	return sb.String()
}

// EnsureTemplateDir 确保模板目录存在
func EnsureTemplateDir(dataDir string) error {
	return os.MkdirAll(dataDir, 0755)
}
