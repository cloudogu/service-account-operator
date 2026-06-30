package cron

import (
	"context"
	"fmt"

	"github.com/adhocore/gronx"
	"github.com/adhocore/gronx/pkg/tasker"
)

// JobFunc contains the payload function that will be run from Task. The int return value may or may not reflect on any error situations.
type JobFunc func(ctx context.Context) (int, error)

// TaskRunner allows executing functions in recurring points in time, depending on the system time. Considering
// container restarts or pod kills, this behavior is more flexible than a regular time ticker.
type TaskRunner interface {
	// Run starts the provided task. No errors will be returned during the execution since this would end the main
	// loop. Implementors should either provide an error channel to the task function or simply log errors to
	// indicate error situations.
	Run()
	// Stop interrupts the provided task.
	Stop()
}

type defaultTaskRunner struct {
	taskExecuter TaskRunner
}

// New creates a new instance for executing the same task. The task must be provided to its Run() function.
func New(ctx context.Context, expr string, jobClosure JobFunc) (TaskRunner, error) {
	if !gronx.IsValid(expr) {
		return nil, fmt.Errorf("cron expression %q is invalid", expr)
	}

	// NOTE: The gronx tasker often relies on os.exit(1)
	// If this start to occur in the controller, the library must be exchanged against a different task runner library.
	taskManager := tasker.New(tasker.Option{}).WithContext(ctx)

	return &defaultTaskRunner{
		taskExecuter: taskManager.Task(expr, tasker.TaskFunc(jobClosure), false),
	}, nil
}

// Run executes the given function. It can be stopped with Stop(). Please note that Run() does not return an error.
func (t *defaultTaskRunner) Run() {
	t.taskExecuter.Run()
}

// Stop stops the looping over the provided function given to Run().
func (t *defaultTaskRunner) Stop() {
	t.taskExecuter.Stop()
}
