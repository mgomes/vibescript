# Enums

Enums define closed, nominal sets of states:

```vibe
enum Status
  Draft
  Published
  Archived
end
```

Access enum members with `::`:

```vibe
Status::Draft
Status::Published
```

Enum values are distinct from raw symbols and from members of other enums:

```vibe
Status::Draft == Status::Draft      # true
Status::Draft == :draft             # false
Status::Draft == ReviewState::Draft # false
```

## Typed Boundaries

Enum names can be used in parameter and return annotations:

```vibe
def publish(status: Status) -> Status
  status
end
```

Typed function and block boundaries coerce matching symbols into enum values:

```vibe
publish(:draft) # coerces to Status::Draft
```

The coercion only happens when a typed boundary expects that enum. Untyped code
continues to receive plain symbols.

## Member Helpers

Enum values expose a few reflective properties:

```vibe
Status::Draft.name   # "Draft"
Status::Draft.symbol # :draft
Status::Draft.enum   # <Enum Status>
```

## Serialization

`JSON.stringify` and `string.template` serialize enum values using the enum
member symbol:

```vibe
JSON.stringify({ status: Status::Draft }) # {"status":"draft"}
"status={{value}}".template({ value: Status::Draft }) # "status=draft"
```

## Modules

Required modules export top-level enums alongside exported functions, so callers
can use both the enum type and helper functions from the same module.

See `examples/enums/` for runnable scripts exercised by the test suite.
