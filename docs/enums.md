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
Status::Draft.name    # "Draft"
Status::Draft.symbol  # :draft
Status::Draft.enum    # <Enum Status>
Status::Draft.to_s    # "Status::Draft"
Status::Draft.inspect # "Status::Draft"
```

`to_s` (alias `string`) and `inspect` return the same text interpolation
produces, so `"#{Status::Draft}"` and `Status::Draft.to_s` always agree.

The enum type itself answers the same conversions, plus `name`:

```vibe
Status.name     # "Status"
Status.to_s     # "<Enum Status>"
Status.inspect  # "<Enum Status>"
```

## Serialization

`JSON.stringify` and `string.template` serialize enum values using the enum
member symbol:

```vibe
JSON.stringify({ status: Status::Draft }) # {"status":"draft"}
"status={{value}}".template({ value: Status::Draft }) # "status=draft"
```

## Modules

Required modules export top-level enums alongside directly callable function
names, so callers can use both the enum type and helper calls from the same
namespace.

See `examples/enums/` for runnable scripts exercised by the test suite.
