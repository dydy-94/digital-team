#!/bin/bash

# 多智能体群聊系统启动脚本
# 仅启动 UI 测试界面，其他服务通过界面按钮控制

set -e

echo "======================================"
echo "🚀 多智能体群聊系统 - UI测试启动脚本"
echo "======================================"
echo ""

# 创建日志目录
mkdir -p logs

# 停止已运行的服务
echo "⏹️  停止已运行的服务..."
pkill -f coordinator/bin/coordinator 2>/dev/null || true
pkill -f agentcore-mock/bin/agentcore-mock 2>/dev/null || true
pkill -f x-client/bin/x-client 2>/dev/null || true
pkill -f ui-test/manager/bin/manager 2>/dev/null || true
sleep 1

# 启动 UI 测试服务（仅启动这个）
echo "🌐 启动 UI 测试服务..."
cd ui-test && ./manager/bin/manager > ../logs/manager.log 2>&1 &
MANAGER_PID=$!
cd ..
echo "   UI服务 PID: $MANAGER_PID"
echo "   端口: 9000"
echo "   日志: logs/manager.log"

sleep 2

# 检查服务状态
echo ""
echo "✅ 启动完成!"
echo "======================================"
echo ""
echo "服务状态:"
echo "- UI测试: http://localhost:9000"
echo ""
echo "打开浏览器访问: http://localhost:9000"
echo ""
echo "在界面上点击按钮启动："
echo "  1. 启动协调器"
echo "  2. 选择 Agent 数量"
echo "  3. 启动所有 Agent"
echo ""
echo "停止服务: ./stop.sh"

# 保存 PID 文件
echo "$MANAGER_PID" > ../logs/manager.pid