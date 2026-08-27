# Architecture

Quire follows the layered slice architecture of
[`College-Redberry/open-adoption`](https://github.com/College-Redberry/open-adoption). This
document is the working copy of that layout: what every slice must look like, and the places
where this project departs from the reference on purpose.

Read it before writing code in a new slice. The granularity is part of the architecture — one
package per entity and one package per use case — so a use case written as a file rather than
as a package is wrong even though it compiles.

## The layout of a slice

Every feature slice lives under `internal/<slice>/` and holds four layers. The dependency
arrow points inwards only: `infra` and `di` know the application layer, the application layer
knows the domain, and the domain knows nothing but the standard library and the shared core.

```
internal/<slice>/
├── domain/
│   └── <entity>/
│       ├── entity.go       the entity, its Props struct, its New, and its behaviour
│       ├── types.go        the value objects, each with a Validate method
│       └── repository.go   the repository interface — the port, not the adapter
├── application/
│   ├── service/
│   │   └── <port>.go       one interface per infrastructure service the use cases need
│   └── usecase/
│       ├── usecase.go      package command: Usecase[In, Out] { Execute(In) (Out, error) }
│       └── <action>/
│           ├── <action>.go the use case: a struct, New with its dependencies, and Execute
│           └── dto.go      Input and Output, in the vocabulary of the application layer
├── infra/
│   ├── persist/
│   │   ├── queries/        the .sql files sqlc reads
│   │   └── <slice>db/      the code sqlc writes
│   ├── repository/
│   │   └── <entity>/       the repository implementation, against the generated querier
│   ├── grpc/
│   │   ├── service.go      the registration of the slice's gRPC service
│   │   └── controller/
│   │       └── <action>/   one package per method: decode, Execute, encode
│   └── service/
│       └── <name>/         the implementation of a port declared in application/service
└── di/
    └── di.go               package di: a Container and the Initialize that wires the slice
```

Three rules carry most of the weight:

**The repository interface belongs to the domain, its implementation to `infra`.** The use
case holds `user.Repository`; what satisfies it is `infra/repository/user`. That is what lets
a use case be tested without a database, and what stops a query from deciding the shape of an
entity.

**A use case is a package.** It exposes exactly one behaviour — a `struct`, a `New` taking its
dependencies, and `Execute(Input) (Output, error)` — and its `Input` and `Output` live beside
it in `dto.go`. A package that would need two `Execute` methods is two use cases.

**A controller only translates.** It decodes the request into the use case's `Input`, calls
`Execute`, and renders the `Output`. A controller that decides anything is a use case that was
written in the wrong place.

## Where Quire departs from the reference, and why

Each of these is a deliberate divergence. They are recorded here rather than in
[`tcc-corrections.md`](tcc-corrections.md), which is about the thesis specification and not
about the reference implementation.

### One error vocabulary for the node, not one per slice

The reference gives every slice a `domain/error` package with its own `DomainError` and its
own sentinel values. Quire has a single [`internal/shared/errs`](../internal/shared/errs),
established in phase 1 and used by every layer.

A federated node is not one service: an error raised while reconciling a sync operation
crosses into the library slice and out through the gRPC translation, and with a vocabulary per
slice it would have to be translated at each boundary — or, more likely, would arrive as
`Internal`. One `Kind` for the whole repository is what lets the transport answer a duplicate
identifier with `AlreadyExists` no matter which slice raised it.

`errs.Error` also carries what the reference's `DomainError` cannot: an `Op`, a stable machine
readable `Code`, and the list of `Field`s that failed validation, which is what turns a
rejected registration into something a client can point at.

### Configuration is loaded once, not read from `os.Getenv` per slice

The reference reads the environment in `infra/constants`, at package initialization, into
package-level variables. Quire loads the whole configuration in
[`internal/shared/config`](../internal/shared/config) and hands each component the section it
needs through the slice's `di.Container`.

Nothing below `cmd/quired` reads a variable. That is what makes the configuration surface of
the node enumerable by reading one struct, what lets every package be tested without an
environment, and what allows one process to be misconfigured in a way that is reported in full
at startup rather than one variable at a time. It is also what the project's own linter
requires: `gochecknoglobals` forbids the package-level variables `infra/constants` is made of.

There is therefore no `infra/constants` in a Quire slice.

### `infra/grpc` rather than `infra/rest`, and an HTTP adapter where a standard demands one

RNF02 makes the API gRPC, so the transport adapter of a slice is a gRPC service rather than a
chi router. The mapping is direct: the reference's `routes.go` becomes the registration of the
slice's generated service, and a `controller/<action>` package becomes the handler of one RPC.

The node does serve plain HTTP as well — `/healthz`, `/readyz`, `/metrics` and the discovery
documents — and most of that surface belongs to no slice and lives in `internal/shared/httpx`.
The exception is a document a slice is the only holder of: the identity slice publishes its
signing keys at `/.well-known/jwks.json` (RNF11) from `infra/jwks`, because the package that
has the key should be the one that publishes it. A slice may therefore carry an HTTP adapter,
but only where a standard puts the surface on a URL rather than on a method — never as a
second way to reach something the gRPC service already serves.

### A use case's `Execute` takes a `context.Context`

The reference's `Usecase[In, Out]` is `Execute(In) (Out, error)`. Quire's carries a context, for
the same reason its repositories do: the transaction travels in one, so a use case that composes
two repositories into a unit of work cannot do it without the context, and a call nobody is
waiting for any more cannot be cancelled. The project's linters agree — `containedctx` forbids
the alternative of holding a context in the use case struct.

### Test doubles live in one package per slice

The reference has no tests. Quire's use case tests are written against fakes in
`internal/<slice>/application/apptest`, imported only by tests, rather than against a double
redefined in each test file: the use cases of a slice depend on the same handful of ports, and a
double written eight times drifts eight ways. They are fakes and not mocks — the reader
repository enforces the uniqueness of RN09, so a test can exercise the duplicate registration
path that an index decides in production.

### Repositories take a `context.Context`

The reference's repository methods take none and call `context.Background()` internally. Quire
passes the context, because [`persist.Manager`](../internal/shared/persist) carries the
transaction in it: a use case that registers a reader and binds their first device wraps both
repositories in one `Within`, and neither has to know it happened. A repository that opened its
own context could not take part in that, and could not be cancelled when the caller hangs up.

### Identifiers are `uuid.UUID`

The reference holds identifiers as `string` and calls `uuid.MustParse` in the repository, which
turns a malformed identifier into a panic in the data layer. Quire holds them as `uuid.UUID`
throughout, so the parse happens once at the edge and a user id cannot be passed where a device
id belongs. Go 1.27 serves the type from the standard library, so this costs no dependency.

### sqlc generates one package per slice

The reference generates a package per entity under `infra/persist/<entity>`. Quire generates
one per slice, into `infra/persist/<slice>db`, as [`sqlc.yaml`](../sqlc.yaml) has done since
phase 2 and as the sync slice already follows. The queries of one slice are checked against the
whole catalogue in a single run, and a join across two tables of the same slice has one
generated home rather than an arbitrary one.

### The entity constructor validates

The reference's `New` cannot fail, and validation is a `Validate` method the use case remembers
to call. Quire keeps the `Validate` methods on the value types, and calls them from `New`,
which returns an error. An entity that exists is then an entity that is valid, and a use case
cannot forget the call — the compiler asks about the error.

### The domain packages are named after the entity they own

`user.User` and `device.Device` repeat themselves, which `revive` reports as a stutter. The
name is the reference's and it is kept; the stutter check alone is excluded for
`internal/*/domain/*` in [`.golangci.yml`](../.golangci.yml), and the rest of `revive` still
applies there.
