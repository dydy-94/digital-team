package main

import (
	"fmt"
	"os"
)

func main() {
	// 检查文件权限
	path := "data/coordinator.db"
	fi, err := os.Stat(path)
	if err != nil {
		fmt.Printf("Error getting file info: %v\n", err)
		return
	}

	fmt.Printf("File permissions: %o\n", fi.Mode().Perm())

	// 尝试打开文件进行写入
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, fi.Mode())
	if err != nil {
		fmt.Printf("Error opening file for writing: %v\n", err)
		return
	}
	defer file.Close()

	fmt.Println("Successfully opened file for writing")
}
