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
		js.fileService.DeleteDir(ctx, deleteReq)
	}()

	// 1. 编译代码，
	// 使用容器复用方案执行所有测试用例
	compileInfo, err := js.executor.CompileCode(ctx, req.Language, saveResp.File)
	// 2. 编译完成，返回compileInfo（ExecutioinInfo）
	// 3. 判断编译是否成功
	if err != nil {
		// 系统级错误（如编译器不存在等）
		return &judge.ExecuteCodeResponse{
			TestCaseResult: nil,
			ExecuteInfo: []*judge.ExecutionInfo{{
				Status: judge.ExecutionStatus_SYSTEM_ERROR,
				Stderr: fmt.Sprintf("Compilation system error: %v", err),
			}},
		}, nil // 注意这里返回 nil 而不是 error
	}

	// 2. 编译完成，返回compileInfo（ExecutionInfo）
	// 3. 判断编译是否成功
	if compileInfo.Status != judge.ExecutionStatus_COMPILE_SUCCESS {
		// 编译失败，返回编译错误信息
		return &judge.ExecuteCodeResponse{
			TestCaseResult: nil,
			ExecuteInfo:    []*judge.ExecutionInfo{compileInfo},
		}, nil // 编译失败不是系统错误，应该返回 nil
	}

	containerID, err := js.executor.CreateExecutionContainer(ctx, req.Language, filepath.Dir(saveResp.File.AbsolutePath))
	if err != nil {
		return &judge.ExecuteCodeResponse{
			TestCaseResult: nil,
			ExecuteInfo: []*judge.ExecutionInfo{{
				Status:              judge.ExecutionStatus_SYSTEM_ERROR,
				Stderr:              fmt.Sprintf("Failed to create execution container: %v", err),
				ExecutionTimeUsedMs: 0,
				MemoryUsedKb:        0,
			}},
		}, nil
	}
	defer func() {
		js.executor.CleanupContainer(containerID)
	}()
	// 4. 成功？创建容器 : 返回编译错误
	// 5. 传入容器id，执行代码
	// 6. 执行完成，返回executionInfos（[]*judge.ExecutionInfo）和TestCaseResults（[]string）
	executionInfos := make([]*judge.ExecutionInfo, len(req.TestCase))
	testCaseResults := make([]string, len(req.TestCase))
	for i, testCase := range req.TestCase {
		resultInfo, testCaseOutput, err := js.executor.ExecuteInContainer(ctx, containerID, testCase, req.Language)
		if err != nil {
			executionInfos[i] = &judge.ExecutionInfo{
				Status: judge.ExecutionStatus_SYSTEM_ERROR,
				Stderr: fmt.Sprintf("Execution error: %v", err),
			}
			testCaseResults[i] = ""
		} else {
			executionInfos[i] = resultInfo
			testCaseResults[i] = testCaseOutput
		}
		// 清理容器状态（为下一个测试用例准备）
		if i < len(req.TestCase)-1 { // 最后一个测试用例不需要清理
			js.executor.ResetContainerState(ctx, containerID)
		}
	}
	// 6. 返回执行结果
	return &judge.ExecuteCodeResponse{
		TestCaseResult: testCaseResults,
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
