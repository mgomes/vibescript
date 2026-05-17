# vibes/capability/db

Host-side database capability adapter for Vibescript. Exposes the
`db.find`, `db.query`, `db.update`, `db.sum`, and `db.each` builtins to
scripts and dispatches them to a host-provided implementation.

The public surface is re-exported by the top-level `vibes` package via
`vibes/capability_db_alias.go`, so embedders normally write
`vibes.Database` rather than importing this package directly.

## Interface segregation

The capability splits the host contract into composable pieces so
read-only embedders can satisfy a narrower surface:

```go
type DatabaseReader interface {
    Find(ctx context.Context, req DBFindRequest) (value.Value, error)
    Query(ctx context.Context, req DBQueryRequest) (value.Value, error)
    Sum(ctx context.Context, req DBSumRequest) (value.Value, error)
    Each(ctx context.Context, req DBEachRequest) ([]value.Value, error)
}

type DatabaseWriter interface {
    Update(ctx context.Context, req DBUpdateRequest) (value.Value, error)
}

type Database interface {
    DatabaseReader
    DatabaseWriter
}
```

`Database` is the full read/write surface scripts ultimately call. Any
type that implements both sub-interfaces satisfies it, so existing
implementations compile unchanged.

## Read-only hosts

Analytics jobs and sandboxed previews can implement only
`DatabaseReader` and compose it with a writer that rejects mutations:

```go
type readOnly struct{ db.DatabaseReader }

func (readOnly) Update(context.Context, db.DBUpdateRequest) (value.Value, error) {
    return value.NewNil(), errors.New("read-only host: db.update is disabled")
}

cap, err := vibes.NewDBCapability("db", readOnly{reader})
```

`vibes.NewDBCapability` accepts any `vibes.Database`, so the composed
value plugs in without further wiring.

## Request shapes

Every method receives a request struct with already-cloned arguments and
options; the host may keep the values without worrying about script
aliasing.

| Method | Request fields |
| --- | --- |
| `Find` | `Collection`, `ID`, `Options` |
| `Query` | `Collection`, `Options` |
| `Update` | `Collection`, `ID`, `Attributes`, `Options` |
| `Sum` | `Collection`, `Field`, `Options` |
| `Each` | `Collection`, `Options` (script-supplied block invoked per row) |

`Options` carries the kwargs passed at the call site (for example
`include:` on `db.find` or `where:` on `db.query`). `Attributes` on
`Update` is the script-provided update hash. Return values from the
host are validated as data-only and deep-copied before being handed
back to the script.
