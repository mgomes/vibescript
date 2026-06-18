package runtime

import (
	"context"
	"fmt"
	"reflect"
	"sync"
)

func registerTaskBuiltins(engine *Engine) {
	engine.builtinsMu.Lock()
	defer engine.builtinsMu.Unlock()

	engine.builtins["Tasks"] = NewObject(map[string]Value{
		"run": NewBuiltin("Tasks.run", builtinTasksRun),
		"map": NewBuiltin("Tasks.map", builtinTasksMap),
	})
}

func builtinTasksRun(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 0 {
		return NewNil(), fmt.Errorf("Tasks.run does not take positional arguments")
	}
	if err := ensureBlock(block, "Tasks.run"); err != nil {
		return NewNil(), err
	}
	max, err := taskConcurrency(exec, "Tasks.run", kwargs, map[string]struct{}{
		"max": {},
	})
	if err != nil {
		return NewNil(), err
	}

	group := newTaskGroup(exec, max, true)
	exec.pushTaskGroup(group)
	defer exec.popTaskGroup()
	defer group.releaseRetainedResults()

	result, blockErr := exec.CallBlock(block, []Value{group.managerValue()})
	if blockErr != nil {
		group.cancel()
		_ = group.closeAndWait()
		return NewNil(), blockErr
	}
	if err := group.closeAndWait(); err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

func builtinTasksMap(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("Tasks.map does not accept blocks")
	}
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("Tasks.map expects one array argument")
	}
	if args[0].Kind() != KindArray {
		return NewNil(), fmt.Errorf("Tasks.map expects an array")
	}
	functionName, err := taskRequiredFunctionKeyword("Tasks.map", kwargs)
	if err != nil {
		return NewNil(), err
	}
	max, err := taskConcurrency(exec, "Tasks.map", kwargs, map[string]struct{}{
		"max":  {},
		"with": {},
	})
	if err != nil {
		return NewNil(), err
	}

	items := args[0].Array()
	if len(items) == 0 {
		return NewArray(nil), nil
	}

	group := newTaskGroup(exec, max, false)
	exec.pushTaskGroup(group)
	defer exec.popTaskGroup()
	defer group.releaseRetainedResults()

	handles := make([]*taskHandle, len(items))
	for i, item := range items {
		handle, err := group.spawn(exec.Context(), functionName, []Value{item}, nil)
		if err != nil {
			group.cancel()
			_ = group.closeAndWait()
			return NewNil(), err
		}
		handles[i] = handle
	}

	if err := group.closeAndWait(); err != nil {
		return NewNil(), err
	}

	results := make([]Value, len(handles))
	for i, handle := range handles {
		result, err := handle.result()
		if err != nil {
			return NewNil(), err
		}
		results[i] = result
	}
	result := NewArray(results)
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

type taskGroup struct {
	script               *Script
	ctx                  context.Context
	cancel               context.CancelFunc
	opts                 CallOptions
	globals              map[string]Value
	inheritedLazyGlobals *taskLazyGlobals
	jobs                 chan *taskJob

	tasks   sync.WaitGroup
	workers sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	firstErr error

	retainedResults map[*taskHandle]Value
}

type taskJob struct {
	functionName string
	args         []Value
	kwargs       map[string]Value
	handle       *taskHandle
}

type taskHandle struct {
	group *taskGroup
	done  chan struct{}

	value Value
	err   error
}

func newTaskGroup(exec *Execution, max int, detachRootGlobals bool) *taskGroup {
	ctx, cancel := context.WithCancel(exec.Context())
	inheritedLazyGlobals := taskLazyGlobalsFromContext(exec.Context())
	globals := exec.callOptions.Globals
	if inheritedLazyGlobals != nil {
		inheritedLazyGlobals = inheritedLazyGlobals.snapshotForNestedTasks()
	} else {
		globals = taskGlobalsFromRoot(exec.root, exec.callOptions.Globals)
		if detachRootGlobals {
			globals = cloneTaskGlobals(globals)
		}
	}
	group := &taskGroup{
		script:               taskScript(exec),
		ctx:                  ctx,
		cancel:               cancel,
		opts:                 exec.callOptions,
		globals:              globals,
		inheritedLazyGlobals: inheritedLazyGlobals,
		jobs:                 make(chan *taskJob, max),
	}
	for range max {
		group.workers.Go(group.worker)
	}
	return group
}

func taskScript(exec *Execution) *Script {
	if ctx := exec.currentModuleContext(); ctx != nil && ctx.script != nil {
		return ctx.script
	}
	return exec.script
}

func taskGlobalsFromRoot(root *Env, globals map[string]Value) map[string]Value {
	if len(globals) == 0 {
		return nil
	}
	out := make(map[string]Value, len(globals))
	for name, original := range globals {
		if val, ok := rootBindingValue(root, name); ok {
			out[name] = val
			continue
		}
		out[name] = original
	}
	return out
}

func rootBindingValue(root *Env, name string) (Value, bool) {
	if root == nil {
		return Value{}, false
	}
	if val, ok := root.values[name]; ok {
		return val, true
	}
	if val, ok := root.statics[name]; ok {
		return val, true
	}
	return Value{}, false
}

func (group *taskGroup) managerValue() Value {
	return NewObject(map[string]Value{
		"spawn": NewBuiltin("tasks.spawn", group.builtinSpawn),
		"wait":  NewAutoBuiltin("tasks.wait", group.builtinWait),
	})
}

func (group *taskGroup) builtinSpawn(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("tasks.spawn does not accept blocks")
	}
	if len(args) == 0 {
		return NewNil(), fmt.Errorf("tasks.spawn requires a function name")
	}
	functionName, err := taskFunctionName("tasks.spawn", args[0])
	if err != nil {
		return NewNil(), err
	}
	handle, err := group.spawn(exec.Context(), functionName, args[1:], kwargs)
	if err != nil {
		return NewNil(), err
	}
	return handle.valueObject(), nil
}

func (group *taskGroup) builtinWait(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 0 {
		return NewNil(), fmt.Errorf("tasks.wait does not take positional arguments")
	}
	if len(kwargs) != 0 {
		return NewNil(), fmt.Errorf("tasks.wait does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("tasks.wait does not accept blocks")
	}
	if group.isClosed() {
		return NewNil(), fmt.Errorf("task manager cannot be used after task scope exits")
	}
	if err := group.wait(); err != nil {
		return NewNil(), err
	}
	return NewNil(), nil
}

func (group *taskGroup) spawn(ctx context.Context, functionName string, args []Value, kwargs map[string]Value) (*taskHandle, error) {
	if group.isClosed() {
		return nil, fmt.Errorf("task manager cannot be used after task scope exits")
	}
	if err := group.err(); err != nil {
		return nil, err
	}

	taskArgs, err := cloneTaskArgs("tasks.spawn", args)
	if err != nil {
		return nil, err
	}
	taskKwargs, err := cloneTaskKwargs("tasks.spawn", kwargs)
	if err != nil {
		return nil, err
	}

	handle := &taskHandle{
		group: group,
		done:  make(chan struct{}),
		value: NewNil(),
	}
	job := &taskJob{
		functionName: functionName,
		args:         taskArgs,
		kwargs:       taskKwargs,
		handle:       handle,
	}

	group.tasks.Add(1)
	select {
	case group.jobs <- job:
		return handle, nil
	case <-group.ctx.Done():
		err := group.ctx.Err()
		if groupErr := group.err(); groupErr != nil {
			err = groupErr
		}
		handle.complete(NewNil(), err)
		group.tasks.Done()
		return nil, err
	case <-ctx.Done():
		handle.complete(NewNil(), ctx.Err())
		group.tasks.Done()
		return nil, ctx.Err()
	}
}

func (group *taskGroup) worker() {
	for job := range group.jobs {
		group.runJob(job)
	}
}

func (group *taskGroup) runJob(job *taskJob) {
	defer group.tasks.Done()

	if err := group.ctx.Err(); err != nil {
		job.handle.complete(NewNil(), err)
		return
	}

	opts := group.callOptionsForJob(job)
	result, err := group.script.callWithLazyTaskGlobals(group.ctx, job.functionName, job.args, opts, group.lazyGlobalsForJob())
	if err != nil {
		taskErr := fmt.Errorf("task %s failed: %w", job.functionName, err)
		group.recordErr(taskErr)
		job.handle.complete(NewNil(), taskErr)
		return
	}

	result, err = cloneTaskResult(job.functionName, result)
	if err != nil {
		taskErr := fmt.Errorf("task %s failed: %w", job.functionName, err)
		group.recordErr(taskErr)
		job.handle.complete(NewNil(), taskErr)
		return
	}
	job.handle.complete(result, nil)
}

func (group *taskGroup) callOptionsForJob(job *taskJob) CallOptions {
	opts := group.opts
	opts.Globals = nil
	opts.Keywords = job.kwargs
	return opts
}

func (group *taskGroup) lazyGlobalsForJob() *taskLazyGlobals {
	if group.inheritedLazyGlobals != nil {
		return group.inheritedLazyGlobals.fork()
	}
	return newTaskLazyGlobals(group.globals, false)
}

func (group *taskGroup) wait() error {
	group.tasks.Wait()
	return group.err()
}

func (group *taskGroup) closeAndWait() error {
	group.mu.Lock()
	if !group.closed {
		group.closed = true
		close(group.jobs)
	}
	group.mu.Unlock()

	group.tasks.Wait()
	group.workers.Wait()
	group.cancel()
	return group.err()
}

func (group *taskGroup) isClosed() bool {
	group.mu.Lock()
	defer group.mu.Unlock()
	return group.closed
}

func (group *taskGroup) recordErr(err error) {
	if err == nil {
		return
	}
	group.mu.Lock()
	if group.firstErr == nil {
		group.firstErr = err
		group.cancel()
	}
	group.mu.Unlock()
}

func (group *taskGroup) err() error {
	group.mu.Lock()
	defer group.mu.Unlock()
	return group.firstErr
}

func (group *taskGroup) retainResult(handle *taskHandle, result Value) {
	group.mu.Lock()
	if group.retainedResults == nil {
		group.retainedResults = make(map[*taskHandle]Value)
	}
	group.retainedResults[handle] = result
	group.mu.Unlock()
}

func (group *taskGroup) retainedResultMemory(est *memoryEstimator) int {
	group.mu.Lock()
	defer group.mu.Unlock()

	total := 0
	for _, result := range group.retainedResults {
		total += est.value(result)
	}
	return total
}

func (group *taskGroup) releaseRetainedResults() {
	group.mu.Lock()
	for handle := range group.retainedResults {
		handle.value = NewNil()
	}
	group.retainedResults = nil
	group.mu.Unlock()
}

func (handle *taskHandle) valueObject() Value {
	return NewObject(map[string]Value{
		"value": NewAutoBuiltin("task.value", handle.builtinValue),
	})
}

func (handle *taskHandle) builtinValue(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 0 {
		return NewNil(), fmt.Errorf("task.value does not take positional arguments")
	}
	if len(kwargs) != 0 {
		return NewNil(), fmt.Errorf("task.value does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("task.value does not accept blocks")
	}
	if handle.group.isClosed() {
		return NewNil(), fmt.Errorf("task handle cannot be used after task scope exits")
	}
	return handle.wait(exec.Context())
}

func (handle *taskHandle) wait(ctx context.Context) (Value, error) {
	select {
	case <-handle.done:
		return handle.result()
	case <-ctx.Done():
		return NewNil(), ctx.Err()
	}
}

func (handle *taskHandle) result() (Value, error) {
	<-handle.done
	return handle.value, handle.err
}

func (handle *taskHandle) complete(result Value, err error) {
	if err == nil {
		handle.group.retainResult(handle, result)
	}
	handle.value = result
	handle.err = err
	close(handle.done)
}

func taskConcurrency(exec *Execution, method string, kwargs map[string]Value, allowed map[string]struct{}) (int, error) {
	for key := range kwargs {
		if _, ok := allowed[key]; !ok {
			return 0, fmt.Errorf("%s unknown keyword argument %s", method, key)
		}
	}

	max := exec.engine.config.DefaultTaskConcurrency
	if rawMax, ok := kwargs["max"]; ok {
		if rawMax.Kind() != KindInt {
			return 0, fmt.Errorf("%s max must be an integer", method)
		}
		requested := rawMax.Int()
		if requested < 1 {
			return 0, fmt.Errorf("%s max must be at least 1", method)
		}
		if requested > int64(exec.engine.config.MaxTaskConcurrency) {
			return 0, fmt.Errorf("%s max %d exceeds host maximum %d", method, requested, exec.engine.config.MaxTaskConcurrency)
		}
		max = int(requested)
	}
	return max, nil
}

func taskRequiredFunctionKeyword(method string, kwargs map[string]Value) (string, error) {
	rawFunction, ok := kwargs["with"]
	if !ok {
		return "", fmt.Errorf("%s requires with:", method)
	}
	return taskFunctionName(method+" with", rawFunction)
}

func taskFunctionName(method string, val Value) (string, error) {
	switch val.Kind() {
	case KindString, KindSymbol:
		name := val.String()
		if name == "" {
			return "", fmt.Errorf("%s function name cannot be empty", method)
		}
		return name, nil
	default:
		return "", fmt.Errorf("%s function name must be a symbol or string", method)
	}
}

func cloneTaskArgs(method string, args []Value) ([]Value, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out := make([]Value, len(args))
	for i, arg := range args {
		label := fmt.Sprintf("%s argument %d", method, i+1)
		cloned, err := cloneTaskValue(label, arg)
		if err != nil {
			return nil, err
		}
		out[i] = cloned
	}
	return out, nil
}

func cloneTaskKwargs(method string, kwargs map[string]Value) (map[string]Value, error) {
	if len(kwargs) == 0 {
		return nil, nil
	}
	out := make(map[string]Value, len(kwargs))
	for name, val := range kwargs {
		label := fmt.Sprintf("%s keyword %s", method, name)
		cloned, err := cloneTaskValue(label, val)
		if err != nil {
			return nil, err
		}
		out[name] = cloned
	}
	return out, nil
}

func cloneTaskResult(functionName string, result Value) (Value, error) {
	return cloneTaskValue(fmt.Sprintf("task %s return value", functionName), result)
}

func cloneTaskGlobals(globals map[string]Value) map[string]Value {
	if len(globals) == 0 {
		return nil
	}
	cloner := newTaskGlobalCloner()
	out := make(map[string]Value, len(globals))
	for name, val := range globals {
		out[name] = cloner.clone(val)
	}
	return out
}

type taskLazyGlobals struct {
	values          map[string]Value
	strictValidated bool
	cloner          *taskGlobalCloner
	rebinder        *callFunctionRebinder
	root            *Env
	clones          map[string]Value
}

func newTaskLazyGlobals(values map[string]Value, strictValidated bool) *taskLazyGlobals {
	if len(values) == 0 {
		return nil
	}
	return &taskLazyGlobals{
		values:          values,
		strictValidated: strictValidated,
		cloner:          newTaskGlobalCloner(),
		clones:          make(map[string]Value),
	}
}

func (globals *taskLazyGlobals) len() int {
	if globals == nil {
		return 0
	}
	return len(globals.values)
}

func (globals *taskLazyGlobals) fork() *taskLazyGlobals {
	if globals == nil {
		return nil
	}
	return newTaskLazyGlobals(globals.values, globals.strictValidated)
}

func (globals *taskLazyGlobals) snapshotForNestedTasks() *taskLazyGlobals {
	if globals == nil {
		return nil
	}
	values, detached := globals.valuesForFork()
	return newTaskLazyGlobals(values, globals.strictValidated && !detached)
}

func (globals *taskLazyGlobals) materialize(name string) Value {
	if clone, ok := globals.clones[name]; ok {
		return clone
	}
	source := globals.values[name]
	var cloned Value
	if globals.rebinder != nil {
		cloned = globals.rebinder.rebindValue(source)
	} else {
		cloned = globals.cloner.clone(source)
	}
	globals.clones[name] = cloned
	return cloned
}

func (globals *taskLazyGlobals) ensureStrictValidated() error {
	if globals.strictValidated {
		return nil
	}
	if err := validateStrictGlobals(globals.valuesForValidation()); err != nil {
		return err
	}
	globals.strictValidated = true
	return nil
}

func (globals *taskLazyGlobals) valuesForValidation() map[string]Value {
	if len(globals.clones) == 0 {
		return globals.values
	}
	out := make(map[string]Value, len(globals.values))
	for name, val := range globals.values {
		if clone, ok := globals.clones[name]; ok {
			out[name] = clone
			continue
		}
		out[name] = val
	}
	return out
}

func (globals *taskLazyGlobals) retainedCloneMemory(est *memoryEstimator) int {
	if globals == nil || len(globals.clones) == 0 {
		return 0
	}
	total := estimatedMapBaseBytes + len(globals.clones)*estimatedMapEntryBytes
	for name, val := range globals.clones {
		total += estimatedStringHeaderBytes + len(name)
		total += est.value(val)
	}
	return total
}

func (globals *taskLazyGlobals) valuesForFork() (map[string]Value, bool) {
	if len(globals.clones) == 0 && !globals.hasCurrentBindings() {
		return globals.values, false
	}
	cloner := newTaskGlobalCloner()
	out := make(map[string]Value, len(globals.values))
	for name := range globals.values {
		out[name] = cloner.clone(globals.currentValueForFork(name))
	}
	return out, true
}

func (globals *taskLazyGlobals) hasCurrentBindings() bool {
	if globals.root == nil {
		return false
	}
	for name := range globals.values {
		if val, ok := globals.rootValue(name); ok {
			if _, lazy := lazyValue(val); !lazy {
				return true
			}
		}
	}
	return false
}

func (globals *taskLazyGlobals) currentValueForFork(name string) Value {
	if val, ok := globals.rootValue(name); ok {
		if _, lazy := lazyValue(val); !lazy {
			return val
		}
	}
	return globals.materialize(name)
}

func (globals *taskLazyGlobals) rootValue(name string) (Value, bool) {
	return rootBindingValue(globals.root, name)
}

type taskLazyGlobalBinding struct {
	globals *taskLazyGlobals
	name    string
}

func (binding taskLazyGlobalBinding) materialize() Value {
	return binding.globals.materialize(binding.name)
}

type taskLazyGlobalsContext struct {
	context.Context
	globals *taskLazyGlobals
}

func contextWithTaskLazyGlobals(ctx context.Context, globals *taskLazyGlobals) context.Context {
	if globals == nil {
		return ctx
	}
	return taskLazyGlobalsContext{Context: ctx, globals: globals}
}

func taskLazyGlobalsFromContext(ctx context.Context) *taskLazyGlobals {
	if ctx == nil {
		return nil
	}
	taskCtx, ok := ctx.(taskLazyGlobalsContext)
	if !ok {
		return nil
	}
	return taskCtx.globals
}

type taskGlobalCloner struct {
	seenArrays map[sliceIdentity]Value
	seenMaps   map[uintptr]map[string]Value
}

func newTaskGlobalCloner() *taskGlobalCloner {
	return &taskGlobalCloner{
		seenArrays: make(map[sliceIdentity]Value),
		seenMaps:   make(map[uintptr]map[string]Value),
	}
}

func (cloner *taskGlobalCloner) clone(val Value) Value {
	switch val.Kind() {
	case KindArray:
		items := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(items).Pointer(),
			Len: len(items),
			Cap: cap(items),
		}
		if clone, seen := cloner.seenArrays[id]; seen {
			return clone
		}
		clonedItems := make([]Value, len(items))
		clonedArray := NewArray(clonedItems)
		cloner.seenArrays[id] = clonedArray
		for i, item := range items {
			clonedItems[i] = cloner.clone(item)
		}
		return clonedArray
	case KindHash:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if clone, seen := cloner.seenMaps[ptr]; seen {
			return NewHash(clone)
		}
		clonedEntries := make(map[string]Value, len(entries))
		cloner.seenMaps[ptr] = clonedEntries
		for key, item := range entries {
			clonedEntries[key] = cloner.clone(item)
		}
		return NewHash(clonedEntries)
	case KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if clone, seen := cloner.seenMaps[ptr]; seen {
			return NewObject(clone)
		}
		clonedEntries := make(map[string]Value, len(entries))
		cloner.seenMaps[ptr] = clonedEntries
		for key, item := range entries {
			clonedEntries[key] = cloner.clone(item)
		}
		return NewObject(clonedEntries)
	default:
		return val
	}
}

func cloneTaskValue(label string, val Value) (Value, error) {
	if err := validateCapabilityDataOnlyValue(label, val); err != nil {
		return NewNil(), err
	}
	return deepCloneValue(val), nil
}
