#!/bin/bash

# ===============================================
# HTTP 架构启动脚本
# 启动顺序：清理旧进程 -> AgentCore Mock -> Coordinator -> x-client -> Manager
# ===============================================

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 项目根目录
PROJECT_ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$PROJECT_ROOT"

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}   HTTP 架构启动脚本${NC}"
echo -e "${GREEN}========================================${NC}"

# ===============================================
# 1. 清理旧进程
# ===============================================
echo -e "\n${YELLOW}[1/6] 清理旧进程...${NC}"

# 清理函数
cleanup_process() {
    local name=$1
    local pattern=$2

    if pgrep -f "$pattern" > /dev/null 2>&1; then
        echo -e "  ${YELLOW}停止${name}...${NC}"
        pkill -f "$pattern" 2>/dev/null || true
        sleep 1
    else
        echo -e "  ${GREEN}未发现运行中的${name}${NC}"
    fi
}

# 清理相关进程
cleanup_process "ui-test manager" "ui-test/manager"
cleanup_process "coordinator (http)" "coordinator-http"
cleanup_process "coordinator (ws)" "ws-based/coordinator"
cleanup_process "x-client (http)" "x-client-http"
cleanup_process "x-client (ws)" "ws-based/x-client"
cleanup_process "agentcore-mock" "agentcore-mock"

# 确保端口空闲
echo -e "\n${YELLOW}检查端口占用...${NC}"
for port in 8000 8001 8002 8003 8080 9000; do
    if lsof -i :$port > /dev/null 2>&1; then
        echo -e "  ${RED}端口 $port 已被占用，正在释放...${NC}"
        lsof -ti :$port | xargs kill -9 2>/dev/null || true
        sleep 1
    else
        echo -e "  ${GREEN}端口 $port 空闲${NC}"
    fi
done

echo -e "${GREEN}进程清理完成${NC}"

# ===============================================
# 2. 编译组件（如果需要）
# ===============================================
echo -e "\n${YELLOW}[2/6] 检查编译产物...${NC}"

# 编译 AgentCore Mock
if [ ! -f "agentcore-mock/bin/agentcore-mock" ]; then
    echo -e "  ${YELLOW}编译 AgentCore Mock...${NC}"
    cd agentcore-mock
    go build -o bin/agentcore-mock . 2>/dev/null || go build -o agentcore-mock .
    cd "$PROJECT_ROOT"
fi

# 编译 Coordinator HTTP
if [ ! -f "http-based/coordinator-http/coordinator-http" ]; then
    echo -e "  ${YELLOW}编译 Coordinator HTTP...${NC}"
    cd http-based/coordinator-http
    go build -o coordinator-http .
    cd "$PROJECT_ROOT"
fi

# 编译 x-client HTTP
if [ ! -f "http-based/x-client-http/x-client-http" ]; then
    echo -e "  ${YELLOW}编译 x-client HTTP...${NC}"
    cd http-based/x-client-http
    go build -o x-client-http .
    cd "$PROJECT_ROOT"
fi

# 编译 Manager
if [ ! -f "ui-test/manager/bin/manager" ]; then
    echo -e "  ${YELLOW}编译 Manager...${NC}"
    cd ui-test/manager
    go build -o bin/manager . 2>/dev/null || go build -o manager .
    cd "$PROJECT_ROOT"
fi

echo -e "${GREEN}编译检查完成${NC}"

# ===============================================
# 3. 启动 AgentCore Mock (3个实例)
# ===============================================
echo -e "\n${YELLOW}[3/6] 启动 AgentCore Mock (3个实例)...${NC}"

AGENTCORE_PIDS=()

# AgentCore 端口从 10001 开始，避免与 x-client 冲突
for i in 1 2 3; do
    PORT=$((10000 + i))
    LOG_FILE="logs/agentcore_${i}.log"

    echo -e "  启动 AgentCore-$i (端口 $PORT)..."

    # 后台启动
    cd agentcore-mock
    ./bin/agentcore-mock -listen ":${PORT}" >> "../${LOG_FILE}" 2>&1 &
    AGENTCORE_PIDS+=($!)
    cd "$PROJECT_ROOT"

    sleep 0.5
done

echo -e "${GREEN}AgentCore Mock 启动完成 (PID: ${AGENTCORE_PIDS[*]})${NC}"

# ===============================================
# 4. 启动 Coordinator HTTP
# ===============================================
echo -e "\n${YELLOW}[4/6] 启动 Coordinator HTTP...${NC}"

COORD_LOG="logs/coordinator_http.log"

cd http-based/coordinator-http
./coordinator-http -config config.json >> "../../${COORD_LOG}" 2>&1 &
COORD_PID=$!
cd "$PROJECT_ROOT"

sleep 2

# 检查是否启动成功
if kill -0 $COORD_PID 2>/dev/null; then
    echo -e "${GREEN}Coordinator HTTP 启动成功 (PID: $COORD_PID)${NC}"
else
    echo -e "${RED}Coordinator HTTP 启动失败，查看日志: ${COORD_LOG}${NC}"
    cat $COORD_LOG
    exit 1
fi

# ===============================================
# 5. 启动 x-client (3个实例)
# ===============================================
echo -e "\n${YELLOW}[5/6] 启动 x-client (3个实例)...${NC}"

XCLIENT_PIDS=()

# 创建配置文件
for i in 1 2 3; do
    CONFIG_FILE="http-based/x-client-http/config_${i}.json"
    LOG_FILE="logs/xclient_http_${i}.log"

    cat > "$CONFIG_FILE" << EOF
{
  "agent_id": "agent_${i}",
  "coordinator_url": "http://localhost:8080",
  "agentcore_url": "http://localhost:$((10000 + i))",
  "listen_addr": ":$((8001 + i - 1))",
  "endpoint": "http://localhost:$((8001 + i - 1))",
  "poll_interval": 5,
  "poll_batch_size": 50,
  "heartbeat_interval": 30,
  "max_memory_size": 50,
  "max_memory_chars": 2000
}
EOF

    echo -e "  启动 x-client-$i (端口 $((8001 + i - 1)))..."

    cd http-based/x-client-http
    # 使用相对于当前目录的配置文件路径
    CONFIG_NAME="config_${i}.json"
    ./x-client-http -config "$CONFIG_NAME" >> "../../${LOG_FILE}" 2>&1 &
    XCLIENT_PIDS+=($!)
    cd "$PROJECT_ROOT"

    sleep 0.5
done

echo -e "${GREEN}x-client 启动完成 (PIDs: ${XCLIENT_PIDS[*]})${NC}"

# ===============================================
# 6. 启动 Manager (UI)
# ===============================================
echo -e "\n${YELLOW}[6/6] 启动 Manager (UI)...${NC}"

MANAGER_LOG="logs/manager_http.log"

cd ui-test/manager
./bin/manager -listen :9000 >> "../../${MANAGER_LOG}" 2>&1 &
MANAGER_PID=$!
cd "$PROJECT_ROOT"

sleep 2

# 检查是否启动成功
if kill -0 $MANAGER_PID 2>/dev/null; then
    echo -e "${GREEN}Manager 启动成功 (PID: $MANAGER_PID)${NC}"
else
    echo -e "${RED}Manager 启动失败，查看日志: ${MANAGER_LOG}${NC}"
    cat $MANAGER_LOG
fi

# ===============================================
# 保存 PID 到文件
# ===============================================
echo "# HTTP 架构进程 PID" > .http_pids
echo "AGENTCORE_PIDS=${AGENTCORE_PIDS[*]}" >> .http_pids
echo "COORD_PID=$COORD_PID" >> .http_pids
echo "XCLIENT_PIDS=${XCLIENT_PIDS[*]}" >> .http_pids
echo "MANAGER_PID=$MANAGER_PID" >> .http_pids

# ===============================================
# 注册 Agent 到聊天室
# ===============================================
echo -e "\n${YELLOW}注册 Agent 到聊天室...${NC}"

sleep 2

# 创建聊天室
ROOM_RESPONSE=$(curl -s -X POST "http://localhost:8080/api/room/create" \
    -H "Content-Type: application/json" \
    -d '{
        "name": "测试聊天室",
        "description": "HTTP 架构测试聊天室",
        "members": ["agent_1", "agent_2", "agent_3"],
        "created_by": "system"
    }')

ROOM_ID=$(echo "$ROOM_RESPONSE" | grep -o '"room_id":"[^"]*"' | cut -d'"' -f4)
echo -e "  聊天室 ID: ${GREEN}${ROOM_ID}${NC}"

# 注册 Agent
for i in 1 2 3; do
    curl -s -X POST "http://localhost:8080/api/agent/register" \
        -H "Content-Type: application/json" \
        -d "{\"agent_id\": \"agent_${i}\", \"endpoint\": \"http://localhost:$((8001 + i - 1))\"}" > /dev/null
    echo -e "  Agent-$i 注册成功"
done

# Agent 加入聊天室
for i in 1 2 3; do
    curl -s -X POST "http://localhost:8080/api/room/join" \
        -H "Content-Type: application/json" \
        -d "{\"room_id\": \"${ROOM_ID}\", \"member_id\": \"agent_${i}\", \"member_type\": \"agent\"}" > /dev/null
    echo -e "  Agent-$i 加入聊天室成功"
done

# ===============================================
# 完成
# ===============================================
echo -e "\n${GREEN}========================================${NC}"
echo -e "${GREEN}   启动完成！${NC}"
echo -e "${GREEN}========================================${NC}"
echo -e "\n服务地址:"
echo -e "  ${GREEN}AgentCore-1${NC}: http://localhost:10001"
echo -e "  ${GREEN}AgentCore-2${NC}: http://localhost:10002"
echo -e "  ${GREEN}AgentCore-3${NC}: http://localhost:10003"
echo -e "  ${GREEN}Coordinator${NC}: http://localhost:8080"
echo -e "  ${GREEN}x-client-1${NC}: http://localhost:8001"
echo -e "  ${GREEN}x-client-2${NC}: http://localhost:8002"
echo -e "  ${GREEN}x-client-3${NC}: http://localhost:8003"
echo -e "  ${GREEN}Manager UI${NC}: http://localhost:9000"
echo -e "  ${GREEN}聊天室 ID${NC}: ${ROOM_ID}"
echo -e "\n配置文件:"
echo -e "  Coordinator: http-based/coordinator-http/config.json"
echo -e "  x-client-1: http-based/x-client-http/config_1.json"
echo -e "  x-client-2: http-based/x-client-http/config_2.json"
echo -e "  x-client-3: http-based/x-client-http/config_3.json"
echo -e "\n日志文件:"
echo -e "  logs/agentcore_1.log"
echo -e "  logs/agentcore_2.log"
echo -e "  logs/agentcore_3.log"
echo -e "  logs/coordinator_http.log"
echo -e "  logs/xclient_http_1.log"
echo -e "  logs/xclient_http_2.log"
echo -e "  logs/xclient_http_3.log"
echo -e "  logs/manager_http.log"
echo -e "\n${YELLOW}停止命令: ./stop_http.sh${NC}"
echo -e "${GREEN}========================================${NC}"
