package main

import (
	"testing"
)

func TestParseDelegateCommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(*DelegateCommand) bool
	}{
		{
			name:  "标准委托格式",
			input: "/delegate 测试任务 to agent1",
			check: func(cmd *DelegateCommand) bool {
				return cmd.Title == "测试任务" &&
					cmd.AssignedTo == "agent1" &&
					cmd.IsValid
			},
		},
		{
			name:  "带关注点",
			input: "/delegate 任务3 to agent3 with focus 第一步, 第二步",
			check: func(cmd *DelegateCommand) bool {
				return cmd.Title == "任务3" &&
					cmd.AssignedTo == "agent3" &&
					len(cmd.FocusItems) == 2 &&
					cmd.FocusItems[0] == "[ ] 第一步" &&
					cmd.FocusItems[1] == "[ ] 第二步" &&
					cmd.IsValid
			},
		},
		{
			name:  "无效-缺少标题",
			input: "/delegate to agent1",
			check: func(cmd *DelegateCommand) bool {
				return !cmd.IsValid
			},
		},
		{
			name:  "无效-缺少被分配者",
			input: "/delegate 任务",
			check: func(cmd *DelegateCommand) bool {
				return !cmd.IsValid
			},
		},
		{
			name:  "无效-非委托命令",
			input: "/hello world",
			check: func(cmd *DelegateCommand) bool {
				return !cmd.IsValid
			},
		},
		{
			name:  "无效-空输入",
			input: "",
			check: func(cmd *DelegateCommand) bool {
				return !cmd.IsValid
			},
		},
		{
			name:  "无效-只有 delegate",
			input: "/delegate",
			check: func(cmd *DelegateCommand) bool {
				return !cmd.IsValid
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseDelegateCommand(tt.input)
			if !tt.check(result) {
				t.Errorf("ParseDelegateCommand(%q) failed, got IsValid=%v, Title=%q, AssignedTo=%q, FocusItems=%v",
					tt.input, result.IsValid, result.Title, result.AssignedTo, result.FocusItems)
			}
		})
	}
}

func TestParseDelegateCommandFocusItems(t *testing.T) {
	cmd := ParseDelegateCommand("/delegate 任务 to agent1 with focus 第一步, 第二步, 第三步")
	if !cmd.IsValid {
		t.Fatal("Command should be valid")
	}
	if len(cmd.FocusItems) != 3 {
		t.Fatalf("Expected 3 focus items, got %d", len(cmd.FocusItems))
	}
	expected := []string{"[ ] 第一步", "[ ] 第二步", "[ ] 第三步"}
	for i, exp := range expected {
		if cmd.FocusItems[i] != exp {
			t.Errorf("FocusItems[%d] = %q, want %q", i, cmd.FocusItems[i], exp)
		}
	}
}

func TestParseDelegateCommandPreservesRawContent(t *testing.T) {
	input := "/delegate 测试 to agent1"
	cmd := ParseDelegateCommand(input)
	if cmd.RawContent != input {
		t.Errorf("RawContent = %q, want %q", cmd.RawContent, input)
	}
}

func TestParseDelegateCommandFocusItemFormat(t *testing.T) {
	// 测试已格式化的关注点（带 [x] 或 [ ]）
	cmd := ParseDelegateCommand("/delegate 任务 to agent1 with focus [x] 完成设计, [ ] 实现代码")
	if !cmd.IsValid {
		t.Fatal("Command should be valid")
	}
	if len(cmd.FocusItems) != 2 {
		t.Fatalf("Expected 2 focus items, got %d", len(cmd.FocusItems))
	}
	// 已带 [x] 的不应该再添加前缀
	if cmd.FocusItems[0] != "[x] 完成设计" {
		t.Errorf("FocusItems[0] = %q, want %q", cmd.FocusItems[0], "[x] 完成设计")
	}
	if cmd.FocusItems[1] != "[ ] 实现代码" {
		t.Errorf("FocusItems[1] = %q, want %q", cmd.FocusItems[1], "[ ] 实现代码")
	}
}
