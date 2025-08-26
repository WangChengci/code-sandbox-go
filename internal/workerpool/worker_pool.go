package workerpool

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Task struct {
	ID      string
	Execute func() (interface{}, error)
	Result  chan TaskResult
	Timeout time.Duration
	Ctx     context.Context
}

type TaskResult struct {
	Data  interface{}
	Error error
}

type WorkerPool struct {
	workerCount int
	taskQueue   chan *Task
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	started     bool
	mu          sync.RWMutex
}

func NewWorkerPool(workerCount, queueSize int) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerPool{
		workerCount: workerCount,
		taskQueue:   make(chan *Task, queueSize),
		ctx:         ctx,
		cancel:      cancel,
		started:     false,
	}
}

func (wp *WorkerPool) Start() {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	if wp.started {
		return
	}
	wp.started = true

	for i := 0; i < wp.workerCount; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
}

func (wp *WorkerPool) Stop() {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	if !wp.started {
		return
	}
	// TODO:感觉不对，这里close(wp.taskQueue)，同时阻塞wp.wg.Wait()，不对吧
	wp.cancel()
	close(wp.taskQueue)
	wp.wg.Wait()
	wp.started = false
}

func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()

	for {
		select {
		case task, ok := <-wp.taskQueue:
			if !ok {
				// 任务队列关闭
				return
			}
			wp.executeTask(task)
		case <-wp.ctx.Done():
			// worker pool已停止
			return
		}
	}
}

func (wp *WorkerPool) executeTask(task *Task) {
	defer func() {
		if r := recover(); r != nil {
			task.Result <- TaskResult{
				Data:  nil,
				Error: fmt.Errorf("task panic: %v", r),
			}
		}
		close(task.Result)
	}()

	// 创建带超时的context
	taskCtx := task.Ctx
	if task.Timeout > 0 {
		var cancel context.CancelFunc
		taskCtx, cancel = context.WithTimeout(task.Ctx, task.Timeout)
		defer cancel()
	}
	resultChan := make(chan TaskResult, 1)
	go func() {
		data, err := task.Execute()
		resultChan <- TaskResult{Data: data, Error: err}
	}()

	// 等待任务完成或超时
	select {
	case result := <-resultChan:
		task.Result <- result
	case <-taskCtx.Done():
		task.Result <- TaskResult{
			Data:  nil,
			Error: fmt.Errorf("task timeout or cancelled: %v", taskCtx.Err()),
		}
	}
}

// SubmitTask 提交任务到worker pool
func (wp *WorkerPool) SubmitTask(ctx context.Context, taskID string, execute func() (interface{}, error), timeout time.Duration) (*TaskResult, error) {
	wp.mu.RLock()
	started := wp.started
	wp.mu.RUnlock()

	if !started {
		return nil, fmt.Errorf("worker pool not started")
	}

	task := &Task{
		ID:      taskID,
		Execute: execute,
		Result:  make(chan TaskResult, 1),
		Timeout: timeout,
		Ctx:     ctx,
	}

	// 尝试提交任务
	select {
	case wp.taskQueue <- task:
		// 任务已提交，等待结果
		select {
		case result := <-task.Result:
			return &result, nil
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for task result: %v", ctx.Err())
		}
	case <-ctx.Done():
		return nil, fmt.Errorf("context cancelled while submitting task: %v", ctx.Err())
	default:
		// 任务队列已满
		return nil, fmt.Errorf("worker pool queue is full")
	}
}

// SubmitTaskWithWait 提交任务并等待，支持阻塞等待
func (wp *WorkerPool) SubmitTaskWithWait(ctx context.Context, taskID string, execute func() (interface{}, error), timeout time.Duration, maxWaitTime time.Duration) (*TaskResult, error) {
	wp.mu.RLock()
	started := wp.started
	wp.mu.RUnlock()

	if !started {
		return nil, fmt.Errorf("worker pool not started")
	}

	task := &Task{
		ID:      taskID,
		Execute: execute,
		Result:  make(chan TaskResult, 1),
		Timeout: timeout,
		Ctx:     ctx,
	}

	// 创建等待超时的context
	waitCtx := ctx
	if maxWaitTime > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, maxWaitTime)
		defer cancel()
	}

	// 尝试提交任务，支持等待
	select {
	case wp.taskQueue <- task:
		// 任务已提交，等待结果
		select {
		case result := <-task.Result:
			return &result, nil
		case <-waitCtx.Done():
			return nil, fmt.Errorf("timeout or cancelled while waiting for task result: %v", waitCtx.Err())
		}
	case <-waitCtx.Done():
		return nil, fmt.Errorf("timeout or cancelled while submitting task: %v", waitCtx.Err())
	}
}

// GetStats 获取worker pool统计信息
func (wp *WorkerPool) GetStats() map[string]interface{} {
	wp.mu.RLock()
	defer wp.mu.RUnlock()

	return map[string]interface{}{
		"worker_count":  wp.workerCount,
		"queue_size":    cap(wp.taskQueue),
		"pending_tasks": len(wp.taskQueue),
		"started":       wp.started,
	}
}
