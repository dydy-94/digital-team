package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	// 检查是否提供了消息 ID
	if len(os.Args) < 2 {
		fmt.Println("用法: go run check_message_delivery.go <msg_id> <recipient_id>")
		return
	}

	msgID := os.Args[1]
	recipientID := os.Args[2]

	// 连接到数据库
	db, err := sql.Open("mysql", "root:1994cheche@tcp(localhost:3306)/xclient?charset=utf8mb4&parseTime=True&loc=Local")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 检查连接
	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}

	// 查询 message_delivery 表中的记录
	var id int64
	var deliveredAt, notifiedAt sql.NullTime

	query := "SELECT id, delivered_at, notified_at FROM message_delivery WHERE msg_id = ? AND recipient_id = ?"
	err = db.QueryRow(query, msgID, recipientID).Scan(&id, &deliveredAt, &notifiedAt)
	if err == sql.ErrNoRows {
		fmt.Printf("没有找到消息 ID 为 %s 且接收者为 %s 的记录\n", msgID, recipientID)
	} else if err != nil {
		log.Fatal(err)
	} else {
		fmt.Printf("找到消息 ID 为 %s 且接收者为 %s 的记录:\n", msgID, recipientID)
		fmt.Printf("ID: %d\n", id)
		if deliveredAt.Valid {
			fmt.Printf("已投递时间: %s\n", deliveredAt.Time.Format("2006-01-02 15:04:05"))
		} else {
			fmt.Println("已投递时间: NULL")
		}
		if notifiedAt.Valid {
			fmt.Printf("已通知时间: %s\n", notifiedAt.Time.Format("2006-01-02 15:04:05"))
		} else {
			fmt.Println("已通知时间: NULL")
		}
	}
}