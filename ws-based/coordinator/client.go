package main

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	hub       *Hub
	conn      *websocket.Conn
	send      chan *ServerMessage
	agentId   string
	channelId string
	mu        sync.Mutex
}

func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	return &Client{
		hub:  hub,
		conn: conn,
		send: make(chan *ServerMessage, 256),
	}
}

func (c *Client) Send(msg *ServerMessage) {
	defer func() {
		if r := recover(); r != nil {
			logWarn("client", "Send panicked: "+msg.Type, "agent_id", c.agentId)
		}
	}()
	select {
	case c.send <- msg:
	default:
		c.hub.Unregister <- c
	}
}

func (c *Client) readPump() {
	defer func() {
		c.hub.Unregister <- c
		c.conn.Close()
	}()

	for {
		var msg ClientMessage
		err := c.conn.ReadJSON(&msg)
		if err != nil {
			break
		}

		c.handleMessage(msg)
	}
}

func (c *Client) handleMessage(msg ClientMessage) {
	switch msg.Action {
	case "register":
		var data struct {
			AgentId string `json:"agentId"`
		}
		json.Unmarshal(msg.Data, &data)
		c.agentId = data.AgentId
		logInfo("client", "Agent registered", "agent_id", data.AgentId)

		// 注册到 agents 表（专门的心跳表）- 端口信息已通过 HTTP 注册时保存，这里传空
		c.hub.storage.RegisterAgent(data.AgentId, "")

		member, err := c.hub.storage.GetMemberByAgentID(data.AgentId)
		if err == nil && member != nil && member.RoomID != "" {
			logInfo("client", "Auto-joining agent to room from storage", "agent_id", data.AgentId, "room_id", member.RoomID)
			c.hub.JoinChannel <- &JoinEvent{
				Client:    c,
				ChannelId: member.RoomID,
				AgentId:   data.AgentId,
			}
		}

	case "join":
		var data JoinData
		json.Unmarshal(msg.Data, &data)
		c.agentId = data.AgentId
		c.channelId = data.ChannelId
		c.hub.JoinChannel <- &JoinEvent{Client: c, ChannelId: data.ChannelId, AgentId: data.AgentId}

	case "leave":
		c.hub.Unregister <- c

	case "heartbeat":
		// 处理心跳消息，更新在线状态（现在只更新 agents 表）
		c.hub.storage.UpdateAgentHeartbeat(c.agentId)

	case "speak":
		var data A2AMessage
		json.Unmarshal(msg.Data, &data)
		c.hub.Speak <- &SpeakEvent{Client: c, Message: &data}

	case "stream":
		// 处理流式消息更新
		var data A2AMessage
		json.Unmarshal(msg.Data, &data)
		c.hub.StreamMessage <- &StreamEvent{Client: c, Message: &data}

	case "stream_complete":
		// 处理流式消息完成
		var data A2AMessage
		json.Unmarshal(msg.Data, &data)
		c.hub.StreamComplete <- &StreamEvent{Client: c, Message: &data}

	case "sync":
		var data SyncData
		json.Unmarshal(msg.Data, &data)
		room := c.hub.GetRoom(data.ChannelId)
		if room != nil {
			history := room.GetRecentMessages(data.Count)
			c.Send(&ServerMessage{
				Type: "history",
				Data: history,
			})
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteJSON(msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
