// +build ignore

package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	db, err := sql.Open("mysql", "root:1994cheche@tcp(127.0.0.1:3306)/xclient")
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer db.Close()

	// 检查最近的消息和投递状态
	rows, err := db.Query(`
		SELECT m.msg_id, m.sender_id, m.content, m.created_at,
		       md.recipient_id, md.delivered_at, md.notified_at
		FROM messages m
		LEFT JOIN message_delivery md ON m.msg_id = md.msg_id
		ORDER BY m.created_at DESC
		LIMIT 10
	`)
	if err != nil {
		log.Fatalf("查询失败: %v", err)
	}
	defer rows.Close()

	fmt.Println("最近 10 条消息及投递状态：")
	fmt.Println("====================================")
	for rows.Next() {
		var msgID, senderID, content string
		var createdAt sql.NullTime
		var recipientID, deliveredAt, notifiedAt sql.NullString

		rows.Scan(&msgID, &senderID, &content, &createdAt, &recipientID, &deliveredAt, &notifiedAt)

		fmt.Printf("msg_id: %s\n", msgID)
		fmt.Printf("  sender: %s\n", senderID)
		fmt.Printf("  content: %s\n", content)
		if createdAt.Valid {
			fmt.Printf("  created_at: %s\n", createdAt.Time)
		}
		if recipientID.Valid {
			fmt.Printf("  delivery.recipient: %s\n", recipientID.String)
		}
		if deliveredAt.Valid {
			fmt.Printf("  delivery.delivered_at: %s\n", deliveredAt.String)
		} else {
			fmt.Printf("  delivery.delivered_at: NULL\n")
		}
		if notifiedAt.Valid {
			fmt.Printf("  delivery.notified_at: %s\n", notifiedAt.String)
		} else {
			fmt.Printf("  delivery.notified_at: NULL\n")
		}
		fmt.Println("------------------------------------")
	}
}
