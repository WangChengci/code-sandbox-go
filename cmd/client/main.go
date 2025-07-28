package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"time"

	filepb "code-sandbox-go/api/protos/file"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// 连接到服务器
	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("连接失败: %v", err)
	}
	defer conn.Close()

	// 创建文件服务客户端
	client := filepb.NewFileServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	// 调用 SaveFile 方法
	log.Println("测试 SaveFile 方法")
	saveResp, err := client.SaveFile(ctx, &filepb.SaveFileRequest{
		Content:  "Hello, World!\nThis is a test file.",
		FileName: "test.txt",
	})
	if err != nil {
		log.Fatalf("保存文件失败: %v", err)
	}
	log.Printf("文件保存成功: %+v", saveResp)
	log.Printf("文件名: %s", saveResp.File.FileName)
	log.Printf("文件绝对路径: %s", saveResp.File.AbsolutePath)
	log.Printf("文件相对路径: %s", saveResp.File.RelativePath)
	temp := filepath.Dir(saveResp.File.AbsolutePath)
	fmt.Println(temp)

	// 调用 GetFile 方法
	log.Println("测试 GetFile 方法")
	getResp, err := client.GetFile(ctx, &filepb.GetFileRequest{
		File: saveResp.File,
	})

	if err != nil {
		log.Fatalf("获取文件失败: %v", err)
	}
	log.Printf("文件内容: %s", getResp.Content)

	// // 调用 DeleteFile 方法
	// log.Println("测试 DeleteFile 方法")
	// deleteResp, err := client.DeleteFile(ctx, &filepb.DeleteFileRequest{
	// 	File: saveResp.File,
	// })
	// if err != nil {
	// 	log.Fatalf("删除文件失败: %v", err)
	// }
	// log.Printf("删除文件成功: %+v", deleteResp)
}
