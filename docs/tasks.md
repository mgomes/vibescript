# Tasks and Structured Concurrency

`Tasks` lets a script run independent named functions concurrently while the
runtime keeps the work bounded and scoped. Use it for fanout work such as
scoring records, preparing independent payloads, publishing events, or calling
host capabilities that can run in parallel.

Tasks are structured:

- A task cannot outlive the `Tasks.run` or `Tasks.map` scope that created it.
- Leaving a task scope waits for every spawned task.
- Failed tasks report through `task.value` or at task scope exit.
- Task arguments, keyword arguments, results, and inherited mutable globals are
  cloned across the task boundary.

## Mapping over collections

Use `Tasks.map` when each input item should run through the same named function
and the script needs the ordered results:

```vibe
def score_user(user)
  user[:score] * user[:weight]
end

def score_users(users)
  Tasks.map(users, with: :score_user)
end
```

`Tasks.map` preserves input order, not completion order. The function name can
be a symbol or string.

Limit fanout with `max:` when a script needs a lower concurrency level for one
call:

```vibe
def score_users(users)
  Tasks.map(users, max: 2, with: :score_user)
end
```

## Task scopes

Use `Tasks.run` when the script needs to start different functions or combine
task results manually:

```vibe
def prepare_user(user)
  "prepared:" + user[:id]
end

def prepare_pair(first, second)
  Tasks.run(max: 2) do |tasks|
    left = tasks.spawn(:prepare_user, first)
    right = tasks.spawn(:prepare_user, second)

    [left.value, right.value]
  end
end
```

`tasks.spawn(:function_name, arg1, arg2, key: value)` starts a named function
call and returns a task handle. `task.value` waits for that task if it is still
running, then returns its result or raises its error. Call splats (`*args`) and
keyword splats (`**kwargs`) are not supported; pass the task arguments
explicitly.

`Tasks.run` returns the block value. It does not collect spawned task values
automatically; use `task.value` for individual handles or `Tasks.map` for
ordered collection.

## Explicit barriers

`Tasks.run` waits automatically at scope exit, so `tasks.wait` is not required
for cleanup. Use `tasks.wait` only when later code in the same block must wait
for all spawned work so far:

```vibe
def warm_cache()
  cache.warm()
end

def use_warmed_cache()
  Tasks.run do |tasks|
    tasks.spawn(:warm_cache)
    tasks.wait

    cache.fetch("ready")
  end
end
```

## Host concurrency settings

Hosts control task fanout through engine configuration:

```go
engine, err := vibes.NewEngine(vibes.Config{
    DefaultTaskConcurrency: 4,
    MaxTaskConcurrency:     16,
})
```

`DefaultTaskConcurrency` controls the default fanout when a script omits
`max:`. It defaults to `4`, or to `MaxTaskConcurrency` when the host cap is
lower.

`MaxTaskConcurrency` caps script-provided `max:` values. Requests above the cap
raise a runtime error instead of being silently clamped.

## Isolation and limits

Tasks run named functions through fresh execution state. They inherit the parent
call's capabilities, globals, strict-effects policy, module policy, and
cancellation context, but they do not share mutable local variables or captured
block state with the parent execution.

Task inputs and results must be data-only: scalars, arrays, hashes, objects, and
other non-callable data. Functions, blocks, builtins, capabilities, and cyclic
structures cannot cross the task boundary.

Completed task results retained by task handles count against the parent memory
quota while the task scope is alive.

Reference scripts live in `examples/tasks/`.
