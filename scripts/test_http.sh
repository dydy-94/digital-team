#!/bin/bash

# 测试脚本 - 验证 HTTP 轮询架构

COORDINATOR_URL="http://localhost:8080"

echo "=== 1. 创建聊天室 ==="
ROOM_RESPONSE=$(curl -s -X POST "$COORDINATOR_URL/api/room/create" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "测试聊天室",
    "description": "用于测试 HTTP 轮询架构",
    "members": ["agent_1", "agent_2"],
    "created_by": "test_user"
  }')
echo "$ROOM_RESPONSE"

ROOM_ID=$(echo "$ROOM_RESPONSE" | jq -r '.room_id')
echo "聊天室 ID: $ROOM_ID"

echo ""
echo "=== 2. 注册 Agent 1 ==="
curl -s -X POST "$COORDINATOR_URL/api/agent/register" \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "agent_1",
    "endpoint": "http://localhost:8001"
  }'
echo ""

echo ""
echo "=== 3. 注册 Agent 2 ==="
curl -s -X POST "$COORDINATOR_URL/api/agent/register" \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "agent_2",
    "endpoint": "http://localhost:8002"
  }'
echo ""

echo ""
echo "=== 4. Agent 1 加入聊天室 ==="
curl -s -X POST "$COORDINATOR_URL/api/room/join" \
  -H "Content-Type: application/json" \
  -d "{
    \"room_id\": \"$ROOM_ID\",
    \"member_id\": \"agent_1\",
    \"member_type\": \"agent\"
  }"
echo ""

echo ""
echo "=== 5. Agent 2 加入聊天室 ==="
curl -s -X POST "$COORDINATOR_URL/api/room/join" \
  -H "Content-Type: application/json" \
  -d "{
    \"room_id\": \"$ROOM_ID\",
    \"member_id\": \"agent_2\",
    \"member_type\": \"agent\"
  }"
echo ""

echo ""
echo "=== 6. 用户发送消息（不带 @，广播给所有人）==="
curl -s -X POST "$COORDINATOR_URL/api/message" \
  -H "Content-Type: application/json" \
  -d "{
    \"room_id\": \"$ROOM_ID\",
    \"sender_id\": \"user_1\",
    \"sender_type\": \"user\",
    \"content\": \"大家好！\",
    \"target_id\": \"ALL\"
  }"
echo ""

echo ""
echo "=== 7. 用户 @Agent1 发送消息 ==="
curl -s -X POST "$COORDINATOR_URL/api/message" \
  -H "Content-Type: application/json" \
  -d "{
    \"room_id\": \"$ROOM_ID\",
    \"sender_id\": \"user_1\",
    \"sender_type\": \"user\",
    \"content\": \"@agent_1 你好，帮我做点什么\",
    \"target_id\": \"agent_1\",
    \"mention_users\": [\"agent_1\"],
    \"intent\": \"REQUEST\"
  }"
echo ""

echo ""
echo "=== 8. Agent 1 轮询消息 ==="
curl -s "$COORDINATOR_URL/api/poll?agent_id=agent_1&since=0&limit=10" | jq .
echo ""

echo ""
echo "=== 9. Agent 2 轮询消息 ==="
curl -s "$COORDINATOR_URL/api/poll?agent_id=agent_2&since=0&limit=10" | jq .
echo ""

echo ""
echo "=== 10. 获取聊天室成员 ==="
curl -s "$COORDINATOR_URL/api/room/$ROOM_ID/members" | jq .
echo ""

echo ""
echo "=== 11. Agent 1 发送回复 ==="
curl -s -X POST "$COORDINATOR_URL/api/message" \
  -H "Content-Type: application/json" \
  -d "{
    \"room_id\": \"$ROOM_ID\",
    \"sender_id\": \"agent_1\",
    \"sender_type\": \"agent\",
    \"content\": \"好的，我来处理！\",
    \"target_id\": \"ALL\",
    \"intent\": \"RESPONSE\"
  }"
echo ""

echo ""
echo "=== 测试完成 ==="
