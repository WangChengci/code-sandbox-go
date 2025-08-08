package judge

import (
	"bytes"
	"code-sandbox-go/api/protos/file"
	"code-sandbox-go/api/protos/judge"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-units"
)

type DockerExecutor struct {
	client      *client.Client
	memoryLimit int64
	cpuLimit    int64
	timeLimit   time.Duration
}

func NewDockerExecutor() (*DockerExecutor, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %v", err)
	}

	return &DockerExecutor{
		client:      cli,
		memoryLimit: 256 * units.MB,  // 256MB
		cpuLimit:    1,               // 1 CPU核心
		timeLimit:   5 * time.Second, // 5秒超时
	}, nil
}

// CompileCode 编译代码
func (e *DockerExecutor) CompileCode(ctx context.Context, language string, codeFile *file.File) (*judge.ExecutionInfo, error) {
	switch strings.ToLower(language) {
	case "go":
		return e.compileGo(ctx, codeFile)
	case "java":
		return e.compileJava(ctx, codeFile)
	// case "cpp", "c++":
	// 	return e.compileCpp(ctx, codeFile)
	// case "c":
	// 	return e.compileC(ctx, codeFile)
	case "python", "python3":
		// Python不需要编译
		return &judge.ExecutionInfo{
			Status: judge.ExecutionStatus_COMPILE_SUCCESS,
			Stdout: "Python script ready for execution",
		}, nil
	default:
		return &judge.ExecutionInfo{
			Status: judge.ExecutionStatus_COMPILE_ERROR,
			Stderr: fmt.Sprintf("Unsupported language: %s", language),
		}, nil
	}
}

// createExecutionContainer 创建执行容器
func (e *DockerExecutor) CreateExecutionContainer(ctx context.Context, language, workDir string) (string, error) {
	var config *container.Config

	switch strings.ToLower(language) {
	case "go":
		config = &container.Config{
			Image: "golang:1.21-alpine",
			// Cmd:          []string{"sh", "-c", "while true; do sleep 1; done"}, // 保持容器运行
			WorkingDir:   "/workspace",
			AttachStdout: true,
			AttachStderr: true,
			AttachStdin:  true,
			OpenStdin:    true,
		}
		// Image:        "swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/openjdk:8u342-jdk",
	case "java":
		config = &container.Config{
			Image: "openjdk:latest",
			// Cmd:          []string{"sh", "-c", "while true; do sleep 1; done"},
			WorkingDir:   "/workspace",
			AttachStdout: true,
			AttachStderr: true,
			AttachStdin:  true,
			OpenStdin:    true,
		}
	// case "cpp", "c++":
	// 	config = &container.Config{
	// 		Image:        "gcc:latest ",
	// 		Cmd:          []string{"sh", "-c", "while true; do sleep 1; done"},
	// 		WorkingDir:   "/workspace",
	// 		AttachStdout: true,
	// 		AttachStderr: true,
	// 		AttachStdin:  true,
	// 		OpenStdin:    true,
	// 	}
	// case "c":
	// 	config = &container.Config{
	// 		Image:        "gcc:latest ",
	// 		Cmd:          []string{"sh", "-c", "while true; do sleep 1; done"},
	// 		WorkingDir:   "/workspace",
	// 		AttachStdout: true,
	// 		AttachStderr: true,
	// 		AttachStdin:  true,
	// 		OpenStdin:    true,
	// 	}
	case "python", "python3":
		config = &container.Config{
			Image: "python:3.9-alpine",
			// Cmd:          []string{"sh", "-c", "while true; do sleep 1; done"},
			WorkingDir:   "/workspace",
			AttachStdout: true,
			AttachStderr: true,
			AttachStdin:  true,
			OpenStdin:    true,
		}
	default:
		return "", fmt.Errorf("unsupported language: %s", language)
	}

	hostConfig := &container.HostConfig{
		// TODO:
		// Memory:    e.memoryLimit,
		// NanoCPUs:  e.cpuLimit * 1000000000,
		// CPUPeriod: 100000,
		// CPUQuota:  e.cpuLimit * 100000,
		Resources: container.Resources{
			Memory:   e.memoryLimit,
			NanoCPUs: e.cpuLimit * 1000000000, // 1 CPU = 1e9 nanoCPUs
		},
		NetworkMode:    "none",
		ReadonlyRootfs: false, // 执行时需要写入临时文件
		Binds: []string{
			fmt.Sprintf("%s:/workspace", workDir),
		},
		SecurityOpt: []string{
			"no-new-privileges",
		},
		CapDrop: []string{
			"ALL",
		},
	}

	createResp, err := e.client.ContainerCreate(ctx, config, hostConfig, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("failed to create container: %v", err)
	}

	if err := e.client.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("failed to start container: %v", err)
	}

	return createResp.ID, nil
}

func (e *DockerExecutor) ExecuteInContainer(ctx context.Context, containerID, input, language string) (*judge.ExecutionInfo, string, error) {
	start := time.Now()

	// 创建带超时的上下文
	ctxWithTimeout, cancel := context.WithTimeout(ctx, e.timeLimit)
	defer cancel()

	// 根据不同语言生成执行命令
	execCmd := e.getExecutionCommand(language)

	// 创建执行命令
	execConfig := container.ExecOptions{
		Cmd:          execCmd,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  true,
	}

	execResp, err := e.client.ContainerExecCreate(ctxWithTimeout, containerID, execConfig)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create exec: %v", err)
	}

	// 附加到执行实例
	attachResp, err := e.client.ContainerExecAttach(ctxWithTimeout, execResp.ID, container.ExecAttachOptions{
		Detach: false,
		Tty:    false,
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to attach exec: %v", err)
	}
	defer attachResp.Close()

	// 启动执行
	if execErr := e.client.ContainerExecStart(ctxWithTimeout, execResp.ID, container.ExecStartOptions{
		Detach: false,
		Tty:    false,
	}); execErr != nil {
		return nil, "", fmt.Errorf("failed to start exec: %v", execErr)
	}

	// 使用 channel 同步输入发送
	inputSent := make(chan struct{})
	if input != "" {
		go func() {
			defer close(inputSent)
			defer attachResp.CloseWrite()
			if !strings.HasSuffix(input, "\n") {
				input += "\n"
			}
			attachResp.Conn.Write([]byte(input))
		}()
	} else {
		close(inputSent)
		attachResp.CloseWrite()
	}

	// 等待输入发送完成
	select {
	case <-inputSent:
		// 输入发送完成
	case <-ctxWithTimeout.Done():
		return &judge.ExecutionInfo{
			Status: judge.ExecutionStatus_TIME_LIMIT_EXCEEDED,
			Stderr: "Input sending timeout",
		}, "", nil
	}

	// 读取输出
	outputData, err := io.ReadAll(attachResp.Reader)
	if err != nil {
		return &judge.ExecutionInfo{
			Status: judge.ExecutionStatus_SYSTEM_ERROR,
			Stderr: fmt.Sprintf("failed to read output: %v", err),
		}, "", nil
	}

	// 等待执行完成
	for {
		// 使用select语句监听超时信号，
		select {
		case <-ctxWithTimeout.Done():
			return &judge.ExecutionInfo{
				Status:              judge.ExecutionStatus_TIME_LIMIT_EXCEEDED,
				Stderr:              "Execution timeout",
				ExecutionTimeUsedMs: e.timeLimit.Milliseconds(),
			}, "", nil
		default:
			inspectResp, err := e.client.ContainerExecInspect(ctxWithTimeout, execResp.ID)
			if err != nil {
				return &judge.ExecutionInfo{
					Status: judge.ExecutionStatus_SYSTEM_ERROR,
					Stderr: fmt.Sprintf("failed to inspect exec: %v", err),
				}, "", nil
			}

			if !inspectResp.Running {
				// 执行完成，开始收集结果，解析输出
				stdout, stderr := e.parseContainerLogs(outputData)
				executionTime := time.Since(start)
				programOutput := strings.TrimSpace(stdout)

				// 获取内存使用情况
				var memoryUsage int64
				if statsResp, err := e.client.ContainerStats(ctxWithTimeout, containerID, false); err == nil {
					defer statsResp.Body.Close()
					var statsData container.StatsResponse
					if err := json.NewDecoder(statsResp.Body).Decode(&statsData); err == nil {
						memoryUsage = int64(statsData.MemoryStats.Usage)
					}
				}

				// 判断执行状态
				execStatus := e.determineExecutionStatus(int64(inspectResp.ExitCode), stderr)

				execInfo := &judge.ExecutionInfo{
					Status:              execStatus,
					Stdout:              stdout,
					Stderr:              stderr,
					ExitCode:            int32(inspectResp.ExitCode),
					ExecutionTimeUsedMs: executionTime.Milliseconds(),
					MemoryUsedKb:        memoryUsage / 1024,
				}

				return execInfo, programOutput, nil
			}

			// 增加轮询间隔，减少 CPU 消耗
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// getExecutionCommand 根据语言获取执行命令
func (e *DockerExecutor) getExecutionCommand(language string) []string {
	switch strings.ToLower(language) {
	case "go":
		return []string{"sh", "-c", "./main"}
	case "java":
		return []string{"sh", "-c", "java Main"}
	case "cpp", "c++":
		return []string{"sh", "-c", "./main"}
	case "c":
		return []string{"sh", "-c", "./main"}
	case "python", "python3":
		return []string{"sh", "-c", "python3 main.py"}
	default:
		return []string{"sh", "-c", "./main"}
	}
}

// resetContainerState 重置容器状态
func (e *DockerExecutor) ResetContainerState(ctx context.Context, containerID string) error {
	// 清理临时文件和重置工作目录状态
	execConfig := container.ExecOptions{
		Cmd: []string{"sh", "-c", "rm -f /tmp/* 2>/dev/null || true"},
	}

	execResp, err := e.client.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return err
	}

	return e.client.ContainerExecStart(ctx, execResp.ID, container.ExecStartOptions{})
}

// cleanupContainer 清理容器
func (e *DockerExecutor) CleanupContainer(containerID string) error {
	// TODO:这里的ctx不需要传进去吗
	ctx := context.Background()
	e.client.ContainerKill(ctx, containerID, "SIGTERM")
	return e.client.ContainerRemove(ctx, containerID, container.RemoveOptions{
		RemoveVolumes: true,
		Force:         true,
	})
}

// 编译方法实现
func (e *DockerExecutor) compileGo(ctx context.Context, codeFile *file.File) (*judge.ExecutionInfo, error) {
	start := time.Now()

	// 获取工作目录
	workDir := filepath.Dir(codeFile.GetAbsolutePath())

	// 初始化Go模块（如果不存在go.mod）
	goModPath := filepath.Join(workDir, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		// 创建go.mod文件
		initCmd := exec.CommandContext(ctx, "go", "mod", "init", "main")
		initCmd.Dir = workDir
		if err := initCmd.Run(); err != nil {
			// 如果初始化失败，继续尝试编译
		}
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-o", "main", codeFile.AbsolutePath)
	cmd.Dir = filepath.Dir(codeFile.GetAbsolutePath())

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	compileTime := time.Since(start)

	stdoutStr := strings.TrimSpace(stdout.String())
	stderrStr := strings.TrimSpace(stderr.String())

	// 判断编译结果
	var compileStatus judge.ExecutionStatus
	var exitCode int32

	if err != nil {
		// 出错
		compileStatus = judge.ExecutionStatus_COMPILE_ERROR
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = int32(exitError.ExitCode())
		} else {
			// 系统错误（如javac命令不存在）
			compileStatus = judge.ExecutionStatus_SYSTEM_ERROR
			if stderrStr == "" {
				stderrStr = fmt.Sprintf("Failed to execute javac: %v", err)
			}
			exitCode = -1
		}
	} else {
		compileStatus = judge.ExecutionStatus_COMPILE_SUCCESS
		exitCode = 0
		if stdoutStr == "" {
			stdoutStr = "Go compilation successful"
		}
		// 检查编译产物是否存在
		executablePath := filepath.Join(workDir, "main")
		if _, err := os.Stat(executablePath); os.IsNotExist(err) {
			compileStatus = judge.ExecutionStatus_COMPILE_ERROR
			stderrStr = "Compilation succeeded but executable not found"
			exitCode = 1
		}
	}
	return &judge.ExecutionInfo{
		Status:              compileStatus,
		Stdout:              stdoutStr,
		Stderr:              stderrStr,
		ExitCode:            exitCode,
		ExecutionTimeUsedMs: compileTime.Milliseconds(),
		MemoryUsedKb:        0,
	}, nil
}

func (e *DockerExecutor) compileJava(ctx context.Context, codeFile *file.File) (*judge.ExecutionInfo, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, "javac", "-encoding", "utf-8", codeFile.AbsolutePath)
	cmd.Dir = filepath.Dir(codeFile.GetAbsolutePath())

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	compileTime := time.Since(start)

	stdoutStr := strings.TrimSpace(stdout.String())
	stderrStr := strings.TrimSpace(stderr.String())

	// 判断编译结果
	var compileStatus judge.ExecutionStatus
	var exitCode int32

	if err != nil {
		// 出错
		compileStatus = judge.ExecutionStatus_COMPILE_ERROR
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = int32(exitError.ExitCode())
		} else {
			// 系统错误（如javac命令不存在）
			compileStatus = judge.ExecutionStatus_SYSTEM_ERROR
			if stderrStr == "" {
				stderrStr = fmt.Sprintf("Failed to execute javac: %v", err)
			}
			exitCode = -1
		}
	} else {
		compileStatus = judge.ExecutionStatus_COMPILE_SUCCESS
		exitCode = 0
		if stdoutStr == "" {
			stdoutStr = "Java compilation successful"
		}
	}
	return &judge.ExecutionInfo{
		Status:              compileStatus,
		Stdout:              stdoutStr,
		Stderr:              stderrStr,
		ExitCode:            exitCode,
		ExecutionTimeUsedMs: compileTime.Milliseconds(),
		MemoryUsedKb:        0,
	}, nil

}

// func (e *DockerExecutor) compileCpp(ctx context.Context, codeFile *file.File) (*judge.ExecutionInfo, error) {
// 	start := time.Now()
// 	workDir := filepath.Dir(codeFile.GetAbsolutePath())

// 	// 使用g++编译C++代码，生成可执行文件main
// 	cmd := exec.CommandContext(ctx, "g++", "-std=c++17", "-O2", "-o", "main", codeFile.AbsolutePath)
// 	cmd.Dir = workDir

// 	var stdout, stderr bytes.Buffer
// 	cmd.Stdout = &stdout
// 	cmd.Stderr = &stderr

// 	err := cmd.Run()
// 	compileTime := time.Since(start)

// 	stdoutStr := strings.TrimSpace(stdout.String())
// 	stderrStr := strings.TrimSpace(stderr.String())

// 	// 判断编译结果
// 	var compileStatus judge.ExecutionStatus
// 	var exitCode int32

// 	if err != nil {
// 		// 出错
// 		compileStatus = judge.ExecutionStatus_COMPILE_ERROR
// 		if exitError, ok := err.(*exec.ExitError); ok {
// 			exitCode = int32(exitError.ExitCode())
// 		} else {
// 			// 系统错误（如g++命令不存在）
// 			compileStatus = judge.ExecutionStatus_SYSTEM_ERROR
// 			if stderrStr == "" {
// 				stderrStr = fmt.Sprintf("Failed to execute g++: %v", err)
// 			}
// 			exitCode = -1
// 		}
// 	} else {
// 		compileStatus = judge.ExecutionStatus_COMPILE_SUCCESS
// 		exitCode = 0
// 		if stdoutStr == "" {
// 			stdoutStr = "C++ compilation successful"
// 		}
// 		// 检查编译产物是否存在
// 		executablePath := filepath.Join(workDir, "main")
// 		if _, err := os.Stat(executablePath); os.IsNotExist(err) {
// 			compileStatus = judge.ExecutionStatus_COMPILE_ERROR
// 			stderrStr = "Compilation succeeded but executable not found"
// 			exitCode = 1
// 		}
// 	}

// 	return &judge.ExecutionInfo{
// 		Status:              compileStatus,
// 		Stdout:              stdoutStr,
// 		Stderr:              stderrStr,
// 		ExitCode:            exitCode,
// 		ExecutionTimeUsedMs: compileTime.Milliseconds(),
// 		MemoryUsedKb:        0,
// 	}, nil
// }

// func (e *DockerExecutor) compileC(ctx context.Context, codeFile *file.File) (*judge.ExecutionInfo, error) {
// 	start := time.Now()
// 	workDir := filepath.Dir(codeFile.GetAbsolutePath())

// 	// 使用gcc编译C代码，生成可执行文件main
// 	cmd := exec.CommandContext(ctx, "gcc", "-std=c11", "-O2", "-o", "main", codeFile.AbsolutePath)
// 	cmd.Dir = workDir

// 	var stdout, stderr bytes.Buffer
// 	cmd.Stdout = &stdout
// 	cmd.Stderr = &stderr

// 	err := cmd.Run()
// 	compileTime := time.Since(start)

// 	stdoutStr := strings.TrimSpace(stdout.String())
// 	stderrStr := strings.TrimSpace(stderr.String())

// 	// 判断编译结果
// 	var compileStatus judge.ExecutionStatus
// 	var exitCode int32

// 	if err != nil {
// 		// 出错
// 		compileStatus = judge.ExecutionStatus_COMPILE_ERROR
// 		if exitError, ok := err.(*exec.ExitError); ok {
// 			exitCode = int32(exitError.ExitCode())
// 		} else {
// 			// 系统错误（如gcc命令不存在）
// 			compileStatus = judge.ExecutionStatus_SYSTEM_ERROR
// 			if stderrStr == "" {
// 				stderrStr = fmt.Sprintf("Failed to execute gcc: %v", err)
// 			}
// 			exitCode = -1
// 		}
// 	} else {
// 		compileStatus = judge.ExecutionStatus_COMPILE_SUCCESS
// 		exitCode = 0
// 		if stdoutStr == "" {
// 			stdoutStr = "C compilation successful"
// 		}
// 		// 检查编译产物是否存在
// 		executablePath := filepath.Join(workDir, "main")
// 		if _, err := os.Stat(executablePath); os.IsNotExist(err) {
// 			compileStatus = judge.ExecutionStatus_COMPILE_ERROR
// 			stderrStr = "Compilation succeeded but executable not found"
// 			exitCode = 1
// 		}
// 	}

// 	return &judge.ExecutionInfo{
// 		Status:              compileStatus,
// 		Stdout:              stdoutStr,
// 		Stderr:              stderrStr,
// 		ExitCode:            exitCode,
// 		ExecutionTimeUsedMs: compileTime.Milliseconds(),
// 		MemoryUsedKb:        0,
// 	}, nil
// }

// 保留原有的辅助方法
func (e *DockerExecutor) parseContainerLogs(logData []byte) (stdout, stderr string) {
	var stdoutBuilder, stderrBuilder strings.Builder

	i := 0
	for i < len(logData) {
		if i+8 > len(logData) {
			break
		}

		streamType := logData[i]
		msgLen := int(logData[i+4])<<24 | int(logData[i+5])<<16 | int(logData[i+6])<<8 | int(logData[i+7])

		if msgLen < 0 || i+8+msgLen > len(logData) {
			break
		}

		msg := string(logData[i+8 : i+8+msgLen])

		switch streamType {
		case 1:
			stdoutBuilder.WriteString(msg)
		case 2:
			stderrBuilder.WriteString(msg)
		}

		i += 8 + msgLen
	}

	return strings.TrimSpace(stdoutBuilder.String()), strings.TrimSpace(stderrBuilder.String())
}

func (e *DockerExecutor) determineExecutionStatus(exitCode int64, stderr string) judge.ExecutionStatus {
	switch exitCode {
	case 0:
		return judge.ExecutionStatus_EXECUTE_SUCCESS
	case 124, 137:
		return judge.ExecutionStatus_TIME_LIMIT_EXCEEDED
	default:
		lowerStderr := strings.ToLower(stderr)
		if strings.Contains(lowerStderr, "out of memory") ||
			strings.Contains(lowerStderr, "memory") {
			return judge.ExecutionStatus_MEMORY_LIMIT_EXCEEDED
		}
		return judge.ExecutionStatus_RUNTIME_ERROR
	}
}
