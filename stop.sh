#!/bin/bash

# 多智能体群聊系统停止脚本

echo "======================================"
echo "⏹️  多智能体群聊系统停止脚本"
echo "======================================"
echo ""

# 停止所有服务
echo "停止协调器..."
pkill -f coordinator/bin/coordinator 2>/dev/null || true

echo "停止 UI 测试服务..."
pkill -f ui-test/manager/bin/manager 2>/dev/null || true

echo "停止 x-client..."
pkill -f x-client/bin/x-client 2>/dev/null || true

echo "停止 agentcore-mock..."
pkill -f agentcore-mock/bin/agentcore-mock 2>/dev/null || true

sleep 1

# 清理 PID 文件
rm -f logs/*.pid 2>/dev/null || true

echo ""
echo "✅ 所有服务已停止!"
echo "======================================"