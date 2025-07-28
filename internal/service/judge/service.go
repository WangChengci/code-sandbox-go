package judge

import (
	filepb "code-sandbox-go/api/protos/file"
	"code-sandbox-go/api/protos/judge"
	"code-sandbox-go/internal/service/file"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type JudgeService struct {
	judge.UnimplementedJudgeServer
	executor    *DockerExecutor
	fileService *file.FileService
}

func NewJudgeService(fileService *file.FileService) (*JudgeService, error) {
	executor, err := NewDockerExecutor()
	if err != nil {
		return nil, fmt.Errorf("failed to create docker executor: %v", err)
	}

	return &JudgeService{
		executor:    executor,
		fileService: fileService,
	}, nil
}

func (js *JudgeService) ExecuteCode(ctx context.Context, req *judge.ExecuteCodeRequest) (*judge.ExecuteCodeResponse, error) {
	// 保存代码文件

	fileName := js.GetFileName(req.Language)
	saveResp, err := js.fileService.SaveFile(ctx, &filepb.SaveFileRequest{
		Content:  req.Code,
		FileName: fileName,
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		deleteReq := &filepb.DeleteFileRequest{
			File: saveResp.File,
		}
		js.fileService.DeleteFile(ctx, deleteReq)
	}()

	// 使用容器复用方案执行所有测试用例
	executionInfos, testCaseResult, err := js.executor.ExecuteWithCompileOptimized(
		ctx,
		req.Language,
		filepath.Dir(saveResp.File.AbsolutePath),
		saveResp.File.FileName,
		req.TestCase,
	)
	if err != nil {
		return nil, err
	}

	return &judge.ExecuteCodeResponse{
		TestCaseResult: testCaseResult,
		ExecuteInfo:    executionInfos,
	}, nil
}

// 辅助方法
func (s *JudgeService) GetFileName(language string) string {
	uniqueID := uuid.New().String()
	subDir := fmt.Sprintf("%s_%s", language, uniqueID)
	switch strings.ToLower(language) {
	case "go":
		return filepath.Join(subDir, "main.go")
	case "java":
		return filepath.Join(subDir, "Main.java")
	case "cpp", "c++":
		return filepath.Join(subDir, "main.cpp")
	case "python", "python3":
		return filepath.Join(subDir, "main.py")
	case "c":
		return filepath.Join(subDir, "main.c")
	default:
		return filepath.Join(subDir, "main.txt")
	}
}

// func (s *JudgeService) needCompilation(language string) bool {
// 	switch strings.ToLower(language) {
// 	case "go", "java", "cpp", "c++", "c":
// 		return true
// 	case "python", "python3", "javascript", "js":
// 		return false
// 	default:
// 		return false
// 	}
// }
