#!/bin/bash

# ===============================================
# HTTP 架构停止脚本
# ===============================================

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

PROJECT_ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$PROJECT_ROOT"

echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}   HTTP 架构停止脚本${NC}"
echo -e "${YELLOW}========================================${NC}"

echo -e "\n${YELLOW}正在停止进程...${NC}"

# 强制停止函数（使用 pkill 确保停止）
cleanup_process() {
    local name=$1
    local pattern=$2

    # 先尝试优雅停止
    if pgrep -f "$pattern" > /dev/null 2>&1; then
        echo -e "停止 ${name}..."
        pkill -f "$pattern" 2>/dev/null || true
        sleep 1
    fi

    # 如果还在运行，强制杀死
    if pgrep -f "$pattern" > /dev/null 2>&1; then
        echo -e "  ${YELLOW}强制杀死 ${name}...${NC}"
        pkill -9 -f "$pattern" 2>/dev/null || true
        sleep 1
    fi

    # 验证已停止
    if pgrep -f "$pattern" > /dev/null 2>&1; then
        echo -e "  ${RED}停止失败: ${name}${NC}"
    else
        echo -e "  ${GREEN}${name} 已停止${NC}"
    fi
}

# 停止所有相关进程（按依赖顺序，从上到下）
cleanup_process "x-client" "x-client-http"
cleanup_process "AgentCore" "agentcore-mock"
cleanup_process "Coordinator HTTP" "coordinator-http"
cleanup_process "Coordinator WS" "ws-based/coordinator"
cleanup_process "Manager" "bin/manager"

# 清理 PID 文件
rm -f .http_pids

echo -e "\n${YELLOW}清理端口占用...${NC}"
for port in 8001 8002 8003 8080 9000 10001 10002 10003; do
    if lsof -ti :$port > /dev/null 2>&1; then
        echo -e "  ${YELLOW}强制释放端口 $port...${NC}"
        lsof -ti :$port | xargs kill -9 2>/dev/null || true
    fi
done

echo -e "\n${GREEN}========================================${NC}"
echo -e "${GREEN}   所有 HTTP 架构进程已停止${NC}"
echo -e "${GREEN}========================================${NC}"
