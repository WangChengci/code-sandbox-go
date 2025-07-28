package main

import (
	filepb "code-sandbox-go/api/protos/file"
	judgepb "code-sandbox-go/api/protos/judge"
	fileservice "code-sandbox-go/internal/service/file"
	judgeservice "code-sandbox-go/internal/service/judge"
	"log"
	"net"
	"os"
	"path/filepath"

	"github.com/docker/docker/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	server := grpc.NewServer()

	// 注册文件服务
	workDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("获取当前工作目录失败: %v", err)
	}
	tempDir := filepath.Join(workDir, "temp")

	// fileService
	// 创建文件服务实例
	fileService := fileservice.NewFileService(tempDir)
	// 注册服务到gRPC服务器
	filepb.RegisterFileServiceServer(server, fileService)

	// 初始化 Docker 客户端
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("创建 Docker 客户端失败: %v", err)
	}
	defer dockerClient.Close()

	// judgeService
	judgeService, err := judgeservice.NewJudgeService(fileService)
	if err != nil {
		log.Fatalf("创建判断服务失败: %v", err)
	}
	judgepb.RegisterJudgeServer(server, judgeService)
	reflection.Register(server)

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("监听端口失败: %v", err)
	}

	log.Println("gRPC服务器启动在端口 :50051")
	log.Println("文件服务已注册")
	if err := server.Serve(lis); err != nil {
		log.Fatalf("gRPC服务器启动失败: %v", err)
	}
}
