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

	// 检查字段是否存在
	var exists int
	err = db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='xclient' AND table_name='message_delivery' AND column_name='notified_at'").Scan(&exists)
	if err != nil {
		log.Fatalf("检查字段失败: %v", err)
	}

	if exists > 0 {
		fmt.Println("字段 notified_at 已存在，跳过")
		return
	}

	// 添加字段
	_, err = db.Exec("ALTER TABLE message_delivery ADD COLUMN notified_at DATETIME NULL")
	if err != nil {
		log.Fatalf("添加字段失败: %v", err)
	}

	// 添加索引
	_, err = db.Exec("ALTER TABLE message_delivery ADD INDEX idx_notified (recipient_id, notified_at)")
	if err != nil {
		log.Fatalf("添加索引失败: %v", err)
	}

	fmt.Println("迁移成功！")
}
