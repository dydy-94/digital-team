package main

import (
	"strings"
	"testing"
)

func TestGenerateS3Key(t *testing.T) {
	tests := []struct {
		name       string
		transferID string
		fileName   string
		wantPrefix string
	}{
		{
			name:       "标准文件名",
			transferID: "abc-123",
			fileName:   "report.pdf",
			wantPrefix: "transfers/abc-123/",
		},
		{
			name:       "带路径文件名",
			transferID: "def-456",
			fileName:   "uploads/image.png",
			wantPrefix: "transfers/def-456/",
		},
		{
			name:       "带空格的文件名",
			transferID: "ghi-789",
			fileName:   "my document.docx",
			wantPrefix: "transfers/ghi-789/",
		},
		{
			name:       "中文文件名",
			transferID: "中文-id",
			fileName:   "文档.pdf",
			wantPrefix: "transfers/中文-id/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateS3Key(tt.transferID, tt.fileName)
			if !strings.HasPrefix(result, tt.wantPrefix) {
				t.Errorf("GenerateS3Key(%q, %q) = %q, want prefix %q",
					tt.transferID, tt.fileName, result, tt.wantPrefix)
			}
			// 验证包含 transferID
			if !strings.Contains(result, tt.transferID) {
				t.Errorf("GenerateS3Key(%q, %q) = %q, should contain transferID %q",
					tt.transferID, tt.fileName, result, tt.transferID)
			}
		})
	}
}

func TestGenerateS3KeyUniqueness(t *testing.T) {
	// 相同的 transferID 和不同的文件名应该生成不同的 key
	key1 := GenerateS3Key("same-id", "file1.txt")
	key2 := GenerateS3Key("same-id", "file2.txt")
	if key1 == key2 {
		t.Errorf("Different filenames should produce different keys: %q vs %q", key1, key2)
	}

	// 不同的 transferID 和相同的文件名也应该生成不同的 key
	key3 := GenerateS3Key("id-1", "same.txt")
	key4 := GenerateS3Key("id-2", "same.txt")
	if key3 == key4 {
		t.Errorf("Different transferIDs should produce different keys: %q vs %q", key3, key4)
	}
}
