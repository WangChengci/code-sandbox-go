package judge

import (
	"code-sandbox-go/api/protos/judge"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		memoryLimit: 128 * units.MB,  // 128MB
		cpuLimit:    1,               // 1 CPU核心
		timeLimit:   5 * time.Second, // 5秒超时
	}, nil
}

// ExecuteWithCompile 容器复用方案：编译一次，在同一容器中执行多个测试用例
// 这里执行多个
func (e *DockerExecutor) ExecuteWithCompileOptimized(ctx context.Context, language, workDir, fileName string, testCases []string) ([]*judge.ExecutionInfo, []string, error) {
	// 1. 编译阶段
	compileInfo, err := e.CompileCode(ctx, language, workDir, fileName)
	if err != nil {
		return nil, nil, err
	}

	// 如果编译失败，所有测试用例都返回编译错误
	if compileInfo.Status != judge.ExecutionStatus_COMPILE_SUCCESS {
		results := make([]*judge.ExecutionInfo, len(testCases))
		for i := range results {
			results[i] = &judge.ExecutionInfo{
				Status:              compileInfo.Status,
				Stderr:              compileInfo.Stderr,
				Stdout:              compileInfo.Stdout,
				ExecutionTimeUsedMs: compileInfo.ExecutionTimeUsedMs,
				MemoryUsedKb:        compileInfo.MemoryUsedKb,
			}
		}
		return results, nil, nil
	}

	// 2. 创建执行容器（复用于所有测试用例）
	containerID, err := e.createExecutionContainer(ctx, language, workDir)
	if err != nil {
		return nil, nil, err
	}
	defer e.cleanupContainer(containerID)

	// 3. 在同一容器中依次执行所有测试用例
	executionInfos := make([]*judge.ExecutionInfo, len(testCases))
	testCaseResults := make([]string, len(testCases))
	for i, testCase := range testCases {
		resultInfo, testCaseOutput, err := e.executeInContainer(ctx, containerID, testCase, language)
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
		if i < len(testCases)-1 { // 最后一个测试用例不需要清理
			e.resetContainerState(ctx, containerID)
		}
	}

	return executionInfos, testCaseResults, nil
}

// CompileCode 编译代码
func (e *DockerExecutor) CompileCode(ctx context.Context, language, workDir, fileName string) (*judge.ExecutionInfo, error) {
	switch strings.ToLower(language) {
	case "go":
		return e.compileGo(ctx, workDir, fileName)
	case "java":
		return e.compileJava(ctx, workDir, fileName)
	case "cpp", "c++":
		return e.compileCpp(ctx, workDir, fileName)
	case "c":
		return e.compileC(ctx, workDir, fileName)
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
func (e *DockerExecutor) createExecutionContainer(ctx context.Context, language, workDir string) (string, error) {
	var config *container.Config

	switch strings.ToLower(language) {
	case "go":
		config = &container.Config{
			Image:        "golang:1.21-alpine",
			Cmd:          []string{"sh", "-c", "while true; do sleep 1; done"}, // 保持容器运行
			WorkingDir:   "/workspace",
			AttachStdout: true,
			AttachStderr: true,
			AttachStdin:  true,
			OpenStdin:    true,
		}
	case "java":
		config = &container.Config{
			Image:        "openjdk:11-alpine",
			Cmd:          []string{"sh", "-c", "while true; do sleep 1; done"},
			WorkingDir:   "/workspace",
			AttachStdout: true,
			AttachStderr: true,
			AttachStdin:  true,
			OpenStdin:    true,
		}
	case "cpp", "c++":
		config = &container.Config{
			Image:        "gcc:alpine",
			Cmd:          []string{"sh", "-c", "while true; do sleep 1; done"},
			WorkingDir:   "/workspace",
			AttachStdout: true,
			AttachStderr: true,
			AttachStdin:  true,
			OpenStdin:    true,
		}
	case "c":
		config = &container.Config{
			Image:        "gcc:alpine",
			Cmd:          []string{"sh", "-c", "while true; do sleep 1; done"},
			WorkingDir:   "/workspace",
			AttachStdout: true,
			AttachStderr: true,
			AttachStdin:  true,
			OpenStdin:    true,
		}
	case "python", "python3":
		config = &container.Config{
			Image:        "python:3.9-alpine",
			Cmd:          []string{"sh", "-c", "while true; do sleep 1; done"},
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

// executeInContainer 在已存在的容器中执行代码
func (e *DockerExecutor) executeInContainer(ctx context.Context, containerID, input, language string) (*judge.ExecutionInfo, string, error) {
	start := time.Now()

	// 根据不同语言生成执行命令
	execCmd := e.getExecutionCommand(language)

	// 创建执行命令
	execConfig := container.ExecOptions{
		Cmd:          execCmd,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  true,
	}

	execResp, err := e.client.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create exec: %v", err)
	}

	// 附加到执行实例
	attachResp, err := e.client.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{
		Detach: false,
		Tty:    false,
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to attach exec: %v", err)
	}
	defer attachResp.Close()

	// 启动执行
	if err := e.client.ContainerExecStart(ctx, execResp.ID, container.ExecStartOptions{
		Detach: false,
		Tty:    false,
	}); err != nil {
		return nil, "", fmt.Errorf("failed to start exec: %v", err)
	}

	// 发送输入数据
	if input != "" {
		go func() {
			defer attachResp.CloseWrite()
			// 确保输入以换行符结尾
			if !strings.HasSuffix(input, "\n") {
				input += "\n"
			}
			attachResp.Conn.Write([]byte(input))
		}()
	}

	// 等待执行完成
	ctxWithTimeout, cancel := context.WithTimeout(ctx, e.timeLimit)
	defer cancel()

	done := make(chan struct{})
	var execInfo *judge.ExecutionInfo
	var programOutput string

	go func() {
		defer close(done)

		// 读取所有输出
		outputData, err := io.ReadAll(attachResp.Reader)
		if err != nil {
			execInfo = &judge.ExecutionInfo{
				Status: judge.ExecutionStatus_SYSTEM_ERROR,
				Stderr: fmt.Sprintf("failed to read output: %v", err),
			}
			return
		}

		// 等待执行完成并获取退出码
		for {
			inspectResp, err := e.client.ContainerExecInspect(ctx, execResp.ID)
			if err != nil {
				execInfo = &judge.ExecutionInfo{
					Status: judge.ExecutionStatus_SYSTEM_ERROR,
					Stderr: fmt.Sprintf("failed to inspect exec: %v", err),
				}
				return
			}

			if !inspectResp.Running {
				// 解析输出
				stdout, stderr := e.parseContainerLogs(outputData)
				executionTime := time.Since(start)

				// 程序的实际输出就是 stdout
				programOutput = strings.TrimSpace(stdout)

				// 获取内存使用情况
				statsResp, err := e.client.ContainerStats(ctx, containerID, false)
				var memoryUsage int64
				if err == nil {
					defer statsResp.Body.Close()
					var statsData container.Stats
					if err := json.NewDecoder(statsResp.Body).Decode(&statsData); err == nil {
						memoryUsage = int64(statsData.MemoryStats.Usage)
					}
				}

				// 判断执行状态
				execStatus := e.determineExecutionStatus(int64(inspectResp.ExitCode), stdout, stderr)

				execInfo = &judge.ExecutionInfo{
					Status:              execStatus,
					Stdout:              stdout,
					Stderr:              stderr,
					ExitCode:            int32(inspectResp.ExitCode),
					ExecutionTimeUsedMs: executionTime.Milliseconds(),
					MemoryUsedKb:        memoryUsage / 1024,
				}
				break
			}

			// 短暂等待后重试
			time.Sleep(10 * time.Millisecond)
		}
	}()

	select {
	case <-done:
		return execInfo, programOutput, nil
	case <-ctxWithTimeout.Done():
		// 超时处理
		return &judge.ExecutionInfo{
			Status:              judge.ExecutionStatus_TIME_LIMIT_EXCEEDED,
			Stderr:              "Execution timeout",
			ExecutionTimeUsedMs: e.timeLimit.Milliseconds(),
		}, "", nil
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
func (e *DockerExecutor) resetContainerState(ctx context.Context, containerID string) error {
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
func (e *DockerExecutor) cleanupContainer(containerID string) error {
	// TODO:这里的ctx不需要传进去吗
	ctx := context.Background()
	e.client.ContainerKill(ctx, containerID, "SIGTERM")
	return e.client.ContainerRemove(ctx, containerID, container.RemoveOptions{
		RemoveVolumes: true,
		Force:         true,
	})
}

// 编译方法实现
func (e *DockerExecutor) compileGo(ctx context.Context, workDir, fileName string) (*judge.ExecutionInfo, error) {
	config := &container.Config{
		Image:        "golang:1.21-alpine",
		Cmd:          []string{"sh", "-c", fmt.Sprintf("go build -o main %s", fileName)},
		WorkingDir:   "/workspace",
		AttachStdout: true,
		AttachStderr: true,
	}
	hostConfig := &container.HostConfig{
		NetworkMode: "none",
		AutoRemove:  true,
		Binds: []string{
			fmt.Sprintf("%s:/workspace", workDir),
		},
	}
	return e.runCompileContainer(ctx, config, hostConfig)
}

func (e *DockerExecutor) compileJava(ctx context.Context, workDir, fileName string) (*judge.ExecutionInfo, error) {
	config := &container.Config{
		Image:        "openjdk:11-alpine",
		Cmd:          []string{"sh", "-c", fmt.Sprintf("javac %s && mv *.class main", fileName)},
		WorkingDir:   "/workspace",
		AttachStdout: true,
		AttachStderr: true,
	}
	hostConfig := &container.HostConfig{
		NetworkMode: "none",
		AutoRemove:  true,
		Binds: []string{
			fmt.Sprintf("%s:/workspace", workDir),
		},
	}
	return e.runCompileContainer(ctx, config, hostConfig)
}

func (e *DockerExecutor) compileCpp(ctx context.Context, workDir, fileName string) (*judge.ExecutionInfo, error) {
	config := &container.Config{
		Image:        "gcc:alpine",
		Cmd:          []string{"sh", "-c", fmt.Sprintf("g++ -o main %s", fileName)},
		WorkingDir:   "/workspace",
		AttachStdout: true,
		AttachStderr: true,
	}
	hostConfig := &container.HostConfig{
		NetworkMode: "none",
		AutoRemove:  true,
		Binds: []string{
			fmt.Sprintf("%s:/workspace", workDir),
		},
	}
	return e.runCompileContainer(ctx, config, hostConfig)
}

func (e *DockerExecutor) compileC(ctx context.Context, workDir, fileName string) (*judge.ExecutionInfo, error) {
	config := &container.Config{
		Image:        "gcc:alpine",
		Cmd:          []string{"sh", "-c", fmt.Sprintf("gcc -o main %s", fileName)},
		WorkingDir:   "/workspace",
		AttachStdout: true,
		AttachStderr: true,
	}
	hostConfig := &container.HostConfig{
		NetworkMode: "none",
		AutoRemove:  true,
		Binds: []string{
			fmt.Sprintf("%s:/workspace", workDir),
		},
	}
	return e.runCompileContainer(ctx, config, hostConfig)
}

// runCompileContainer 运行编译容器
// 根据不同语言的编译命令，执行命令（命令在config里）
func (e *DockerExecutor) runCompileContainer(ctx context.Context, config *container.Config, hostConfig *container.HostConfig) (*judge.ExecutionInfo, error) {
	start := time.Now()

	createResp, err := e.client.ContainerCreate(ctx, config, hostConfig, nil, nil, "")
	if err != nil {
		return &judge.ExecutionInfo{
			Status: judge.ExecutionStatus_COMPILE_ERROR,
			Stderr: fmt.Sprintf("failed to create container: %v", err),
		}, nil
	}
	containerID := createResp.ID

	if err := e.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return &judge.ExecutionInfo{
			Status: judge.ExecutionStatus_SYSTEM_ERROR,
			Stderr: fmt.Sprintf("failed to start container: %v", err),
		}, nil
	}

	waitResp, errCh := e.client.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return &judge.ExecutionInfo{
				Status: judge.ExecutionStatus_SYSTEM_ERROR,
				Stderr: fmt.Sprintf("failed to wait container: %v", err),
			}, nil
		}
	case status := <-waitResp:
		logs, err := e.client.ContainerLogs(ctx, containerID, container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
		})
		if err != nil {
			return &judge.ExecutionInfo{
				Status: judge.ExecutionStatus_SYSTEM_ERROR,
				Stderr: fmt.Sprintf("failed to get container logs: %v", err),
			}, nil
		}
		defer logs.Close()

		logData, err := io.ReadAll(logs)
		if err != nil {
			return &judge.ExecutionInfo{
				Status: judge.ExecutionStatus_SYSTEM_ERROR,
				Stderr: fmt.Sprintf("failed to read container logs: %v", err),
			}, nil
		}

		stdout, stderr := e.parseContainerLogs(logData)
		executionTime := time.Since(start)

		var execStatus judge.ExecutionStatus
		if status.StatusCode == 0 {
			execStatus = judge.ExecutionStatus_COMPILE_SUCCESS
		} else {
			execStatus = judge.ExecutionStatus_COMPILE_ERROR
		}

		return &judge.ExecutionInfo{
			Status:              execStatus,
			Stdout:              stdout,
			Stderr:              stderr,
			ExitCode:            int32(status.StatusCode),
			ExecutionTimeUsedMs: executionTime.Milliseconds(),
		}, nil
	}
	return nil, nil
}

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

func (e *DockerExecutor) determineExecutionStatus(exitCode int64, stdout, stderr string) judge.ExecutionStatus {
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
