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

	// 检查最近的消息的 mention_users 字段
	rows, err := db.Query(`
		SELECT msg_id, sender_id, sender_type, content, mention_users
		FROM messages
		ORDER BY created_at DESC
		LIMIT 20
	`)
	if err != nil {
		log.Fatalf("查询失败: %v", err)
	}
	defer rows.Close()

	fmt.Println("最近 20 条消息的 mention_users 字段：")
	fmt.Println("====================================")
	for rows.Next() {
		var msgID, senderID, senderType, content, mentionUsers sql.NullString

		rows.Scan(&msgID, &senderID, &senderType, &content, &mentionUsers)

		fmt.Printf("msg_id: %s\n", msgID.String)
		fmt.Printf("  sender: %s (%s)\n", senderID.String, senderType.String)
		fmt.Printf("  content: %s\n", content.String)
		if mentionUsers.Valid {
			fmt.Printf("  mention_users: |%s| (len=%d)\n", mentionUsers.String, len(mentionUsers.String))
		} else {
			fmt.Printf("  mention_users: NULL\n")
		}
		fmt.Println("------------------------------------")
	}
}
