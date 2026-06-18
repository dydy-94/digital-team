package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStreamConfig Redis Stream配置
type RedisStreamConfig struct {
	Addr     string // Redis 地址
	Password string // 密码
	DB       int    // 数据库编号
}

// StreamMessage Stream消息结构
type StreamMessage struct {
	MsgID        string `json:"msgId"`
	ChannelID    string `json:"channelId"`
	Sender       string `json:"sender"`
	Target       string `json:"target"`
	MentionUsers string `json:"mentionUsers"`
	Intent       string `json:"intent"`
	Content      string `json:"content"`
	Timestamp    int64  `json:"timestamp"`
}

// RedisStreamStorage Redis Stream存储实现
type RedisStreamStorage struct {
	client *redis.Client
	ctx    context.Context
}

// NewRedisStreamStorage 创建Redis Stream存储
func NewRedisStreamStorage(addr, password string, db int) *RedisStreamStorage {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	ctx := context.Background()

	// 测试连接
	_, err := client.Ping(ctx).Result()
	if err != nil {
		log.Printf("Warning: Redis connection failed: %v, messages will be stored in memory only", err)
	} else {
		log.Printf("Redis Stream connected to: %s", addr)
	}

	return &RedisStreamStorage{
		client: client,
		ctx:    ctx,
	}
}

// Close 关闭连接
func (s *RedisStreamStorage) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// Ping 检查连接
func (s *RedisStreamStorage) Ping() error {
	if s.client == nil {
		return fmt.Errorf("redis client not initialized")
	}
	return s.client.Ping(s.ctx).Err()
}

// GetStreamKey 获取Stream的key（统一使用 "messages" Stream）
func GetStreamKey(roomID string) string {
	return "messages" // 所有聊天室共享同一个 Stream
}

// GetRoomStreamKey 获取聊天室对应的Stream key（保留兼容性）
func GetRoomStreamKey(roomID string) string {
	return "messages"
}

// GetLastIDKey 获取实例lastID的Redis key
func GetLastIDKey(instanceID string) string {
	return fmt.Sprintf("lastid:%s", instanceID)
}

// SaveLastID 保存实例的lastID到Redis
func (s *RedisStreamStorage) SaveLastID(instanceID string, lastID string) error {
	if s.client == nil {
		return nil
	}
	return s.client.Set(s.ctx, GetLastIDKey(instanceID), lastID, 0).Err()
}

// GetLastID 获取实例的lastID
func (s *RedisStreamStorage) GetLastID(instanceID string) (string, error) {
	if s.client == nil {
		return "0", nil
	}
	result, err := s.client.Get(s.ctx, GetLastIDKey(instanceID)).Result()
	if err == redis.Nil {
		return "0", nil
	}
	return result, err
}

// GetGroupName 获取消费者组名称（使用实例ID）
func GetGroupName(instanceID string) string {
	return fmt.Sprintf("group:%s", instanceID)
}

// AddMessage 发布消息到Stream
func (s *RedisStreamStorage) AddMessage(msg *StreamMessage) (string, error) {
	if s.client == nil {
		return "", fmt.Errorf("redis client not initialized")
	}

	// 生成消息ID，使用时间戳+随机数
	msgID := fmt.Sprintf("%d-%d", time.Now().UnixMilli(), time.Now().UnixNano()%1000)

	data := map[string]interface{}{
		"msgId":        msg.MsgID,
		"channelId":    msg.ChannelID,
		"sender":       msg.Sender,
		"target":       msg.Target,
		"mentionUsers": msg.MentionUsers,
		"intent":       msg.Intent,
		"content":      msg.Content,
		"timestamp":    msg.Timestamp,
	}

	_, err := s.client.XAdd(s.ctx, &redis.XAddArgs{
		Stream: "messages", // 所有聊天室共享同一个 Stream
		ID:     msgID,
		Values: data,
	}).Result()

	if err != nil {
		return "", err
	}

	// 异步清理旧消息，保留最近10000条（所有聊天室共享）
	go func() {
		ctx := context.Background()
		length, err := s.client.XLen(ctx, "messages").Result()
		if err == nil && length > 10000 {
			results, _ := s.client.XRange(ctx, "messages", "-", "+").Result()
			if len(results) > 9000 {
				for i := 0; i < len(results)-9000; i++ {
					s.client.XDel(ctx, "messages", results[i].ID)
				}
			}
		}
	}()

	return msgID, nil
}

// GetRecentMessages 获取指定聊天室最近的消息
func (s *RedisStreamStorage) GetRecentMessages(roomID string, count int) ([]*StreamMessage, error) {
	if s.client == nil {
		return nil, fmt.Errorf("redis client not initialized")
	}

	// 使用统一的 Stream
	streamKey := "messages"

	// 先获取 Stream 的长度
	length, err := s.client.XLen(s.ctx, streamKey).Result()
	if err != nil {
		return nil, err
	}

	if length == 0 {
		return []*StreamMessage{}, nil
	}

	// 获取所有消息
	results, err := s.client.XRange(s.ctx, streamKey, "-", "+").Result()
	if err != nil {
		return nil, err
	}

	// 反转结果并限制数量，同时过滤指定聊天室的消息
	messages := make([]*StreamMessage, 0, count)
	for i := len(results) - 1; i >= 0 && len(messages) < count*2; i-- {
		msg := results[i]
		channelID := getFieldString(msg.Values, "channelId")

		// 只添加指定聊天室的消息
		if channelID != roomID {
			continue
		}

		streamMsg := &StreamMessage{
			ChannelID:    channelID,
			MsgID:        getFieldString(msg.Values, "msgId"),
			Sender:       getFieldString(msg.Values, "sender"),
			Target:       getFieldString(msg.Values, "target"),
			MentionUsers: getFieldString(msg.Values, "mentionUsers"),
			Intent:       getFieldString(msg.Values, "intent"),
			Content:      getFieldString(msg.Values, "content"),
			Timestamp:    getFieldInt64(msg.Values, "timestamp"),
		}
		messages = append(messages, streamMsg)

		if len(messages) >= count {
			break
		}
	}

	return messages, nil
}

// Subscribe 订阅Stream（使用XREAD阻塞读取）
// 注意：现在使用统一的 "messages" Stream
func (s *RedisStreamStorage) Subscribe(roomIDs []string, lastIDs []string, timeout time.Duration) ([]*StreamMessage, []string, error) {
	if s.client == nil {
		return nil, nil, fmt.Errorf("redis client not initialized")
	}

	// 使用统一的 Stream
	streamKey := "messages"

	// 确保 lastIDs 有值
	if len(lastIDs) == 0 {
		lastIDs = []string{"0"}
	}

	args := &redis.XReadArgs{
		Streams: []string{streamKey, lastIDs[0]},
		Count:   100,
		Block:   timeout,
	}

	results, err := s.client.XRead(s.ctx, args).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, lastIDs, nil
		}
		return nil, lastIDs, err
	}

	messages := make([]*StreamMessage, 0, 100)
	newLastID := lastIDs[0]

	for _, result := range results {
		for _, msg := range result.Messages {
			// 更新 lastID
			if msg.ID > newLastID {
				newLastID = msg.ID
			}

			streamMsg := &StreamMessage{
				ChannelID:    getFieldString(msg.Values, "channelId"),
				MsgID:        getFieldString(msg.Values, "msgId"),
				Sender:       getFieldString(msg.Values, "sender"),
				Target:       getFieldString(msg.Values, "target"),
				MentionUsers: getFieldString(msg.Values, "mentionUsers"),
				Intent:       getFieldString(msg.Values, "intent"),
				Content:      getFieldString(msg.Values, "content"),
				Timestamp:    getFieldInt64(msg.Values, "timestamp"),
			}
			messages = append(messages, streamMsg)
		}
	}

	return messages, []string{newLastID}, nil
}

// SubscribeWithGroup 使用消费者组订阅（推荐用于多实例）
// 注意：现在使用统一的 "messages" Stream
func (s *RedisStreamStorage) SubscribeWithGroup(roomID, groupName, consumerName string) (<-chan *StreamMessage, error) {
	if s.client == nil {
		return nil, fmt.Errorf("redis client not initialized")
	}

	// 使用统一的 Stream
	streamKey := "messages"

	// 创建消费者组（如果不存在）
	s.client.XGroupCreateMkStream(s.ctx, streamKey, groupName, "0")

	// 创建 channel 用于传递消息
	ch := make(chan *StreamMessage, 100)

	go func() {
		defer close(ch)

		for {
			// 从消费者组读取消息
			results, err := s.client.XReadGroup(s.ctx, &redis.XReadGroupArgs{
				Group:    groupName,
				Consumer: consumerName,
				Streams:  []string{streamKey, ">"},
				Count:    10,
				Block:    time.Second,
			}).Result()

			if err != nil {
				if err == redis.Nil {
					continue
				}
				log.Printf("Redis XReadGroup error: %v", err)
				time.Sleep(time.Second)
				continue
			}

			for _, result := range results {
				for _, msg := range result.Messages {
					streamMsg := &StreamMessage{
						ChannelID:    getFieldString(msg.Values, "channelId"),
						MsgID:        getFieldString(msg.Values, "msgId"),
						Sender:       getFieldString(msg.Values, "sender"),
						Target:       getFieldString(msg.Values, "target"),
						MentionUsers: getFieldString(msg.Values, "mentionUsers"),
						Intent:       getFieldString(msg.Values, "intent"),
						Content:      getFieldString(msg.Values, "content"),
						Timestamp:    getFieldInt64(msg.Values, "timestamp"),
					}

					// 发送消息到 channel
					select {
					case ch <- streamMsg:
						// 确认消息
						s.client.XAck(s.ctx, streamKey, groupName, msg.ID)
					default:
						// channel 满，跳过
					}
				}
			}
		}
	}()

	return ch, nil
}

// SubscribeMultipleWithGroups 使用消费者组订阅单个统一 Stream
// 所有聊天室的消息都在 "messages" Stream 中，通过 roomId 字段区分
// 这样只需要一个 goroutine 就能订阅所有聊天室的消息
func (s *RedisStreamStorage) SubscribeMultipleWithGroups(roomIDs []string, groupName string, consumerName string) (<-chan *StreamMessage, error) {
	if s.client == nil {
		return nil, fmt.Errorf("redis client not initialized")
	}

	// 所有聊天室共享同一个 Stream
	streamKey := "messages"

	// 创建消费者组（如果不存在），从 Stream 开头开始
	s.client.XGroupCreateMkStream(s.ctx, streamKey, groupName, "0")

	// 创建 channel 用于传递消息
	ch := make(chan *StreamMessage, 100)

	go func() {
		defer close(ch)

		for {
			// 从消费者组读取新消息
			results, err := s.client.XReadGroup(s.ctx, &redis.XReadGroupArgs{
				Group:    groupName,
				Consumer: consumerName,
				Streams:  []string{streamKey, ">"}, // ">" 表示只读新消息
				Count:    50,
				Block:    2 * time.Second,
			}).Result()

			if err != nil {
				if err == redis.Nil {
					continue
				}
				log.Printf("Redis XReadGroup error: %v", err)
				time.Sleep(time.Second)
				continue
			}

			for _, result := range results {
				for _, msg := range result.Messages {
					streamMsg := &StreamMessage{
						ChannelID:    getFieldString(msg.Values, "channelId"),
						MsgID:        getFieldString(msg.Values, "msgId"),
						Sender:       getFieldString(msg.Values, "sender"),
						Target:       getFieldString(msg.Values, "target"),
						MentionUsers: getFieldString(msg.Values, "mentionUsers"),
						Intent:       getFieldString(msg.Values, "intent"),
						Content:      getFieldString(msg.Values, "content"),
						Timestamp:    getFieldInt64(msg.Values, "timestamp"),
					}

					// 发送消息到 channel
					select {
					case ch <- streamMsg:
						// 确认消息
						s.client.XAck(s.ctx, result.Stream, groupName, msg.ID)
					default:
						// channel 满，跳过
					}
				}
			}
		}
	}()

	return ch, nil
}

// 辅助函数：获取字符串字段
func getFieldString(values map[string]interface{}, key string) string {
	if v, ok := values[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// 辅助函数：获取int64字段
func getFieldInt64(values map[string]interface{}, key string) int64 {
	if v, ok := values[key]; ok {
		switch val := v.(type) {
		case int64:
			return val
		case int:
			return int64(val)
		case float64:
			return int64(val)
		case string:
			var result int64
			json.Unmarshal([]byte(val), &result)
			return result
		}
	}
	return 0
}
