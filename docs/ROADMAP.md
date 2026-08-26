# Implementation roadmap

One checkbox per commit. Tick a box only after the commit lands, so that any future session can
resume the work by reading this file and `git log`.

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
and are written in English. Commits stay small and are reviewed before landing.

Thesis traceability: requirement identifiers (RF, RNF, RN, UC) refer to the TCC specification;
the entity-name mapping between the MER and the database schema lives in
[`mer-mapping.md`](mer-mapping.md).

## Phase 0 — Repository bootstrap

- [x] `chore: initialize go module and repository layout`
- [x] `docs: add readme and license`
- [x] `docs: add implementation roadmap`
- [x] `build: add makefile with development targets`
- [x] `chore: add golangci-lint configuration`
- [x] `ci: add github actions workflow for lint and unit tests`

## Phase 1 — Shared core

- [x] `feat: add configuration loading from environment`
- [x] `feat: add domain error type with wrapping`
- [x] `feat: add structured logging setup`
- [x] `feat: add vector clock comparison primitives`
- [x] `test: add property tests for crdt merge laws`

## Phase 2 — Persistence

- [ ] `feat: add postgres connection pool and transaction helper`
- [ ] `feat: add identity and federation schema migrations`
- [ ] `feat: add library and reading schema migrations`
- [ ] `feat: add sync schema migrations`
- [ ] `build: add sqlc configuration and generation target`
- [ ] `docs: add mer to schema mapping table`

## Phase 3 — Protobuf contracts

- [ ] `build: add buf configuration for protobuf generation`
- [ ] `feat: add shared protobuf messages and vector clock type`
- [ ] `feat: add auth service protobuf definition`
- [ ] `feat: add library service protobuf definition`
- [ ] `feat: add reading service protobuf definition`
- [ ] `feat: add sync service protobuf definition`
- [ ] `feat: add federation service protobuf definition`

## Phase 4 — Servers

- [ ] `feat: add grpc server bootstrap with interceptor chain`
- [ ] `feat: add error and recovery interceptors`
- [ ] `feat: add request logging interceptor`
- [ ] `feat: add http server for health and metrics`
- [ ] `feat: add well-known discovery endpoints`

## Phase 5 — identity slice (UC06–08, UC14)

- [ ] `feat: add user and device domain entities`
- [ ] `feat: add user and device repositories with postgres`
- [ ] `feat: add password hashing service`
- [ ] `feat: add jwt signing service and jwks endpoint`
- [ ] `feat: add register user use case`
- [ ] `feat: add login and logout use cases`
- [ ] `feat: add refresh token use case`
- [ ] `feat: add password recovery use cases`
- [ ] `feat: add device management use cases`
- [ ] `feat: add authentication interceptor`
- [ ] `feat: add auth grpc handlers`
- [ ] `test: add integration tests for auth service`

## Phase 6 — federation slice (UC12, UC13, UC15)

- [ ] `feat: add server domain entity and repository`
- [ ] `feat: add well-known discovery client`
- [ ] `feat: add discover server use case`
- [ ] `feat: add known server management use cases`
- [ ] `feat: add replica authorization use cases`
- [ ] `feat: add federation grpc handlers`
- [ ] `test: add integration tests for server discovery`

## Phase 7 — library slice (UC01–03)

- [ ] `feat: add ebook and collection domain entities`
- [ ] `feat: add ebook and collection repositories with postgres`
- [ ] `feat: add blob store port with s3 adapter`
- [ ] `feat: add ebook management use cases`
- [ ] `feat: add collection management use cases`
- [ ] `feat: add ebook content upload and download streaming`
- [ ] `feat: add library grpc handlers`
- [ ] `test: add integration tests for library service`

## Phase 8 — reading slice (UC04, UC05)

- [ ] `feat: add annotation and reading progress entities`
- [ ] `feat: add annotation and reading progress repositories`
- [ ] `feat: add annotation management use cases`
- [ ] `feat: add reading progress use cases`
- [ ] `feat: add reading grpc handlers`
- [ ] `test: add integration tests for reading service`

## Phase 9 — sync slice (UC09–11, UC16)

- [ ] `feat: add sync operation entity and repository`
- [ ] `feat: add operation reconciler with crdt merge` — first settle the `updated_at`
      question under [Design decisions to settle](#design-decisions-to-settle)
- [ ] `feat: add push operations use case`
- [ ] `feat: add pull operations use case`
- [ ] `feat: add bidirectional sync stream handler`
- [ ] `feat: add replication worker for authorized nodes`
- [ ] `feat: add node to node replication handler with mtls`
- [ ] `feat: add home server migration use case`
- [ ] `test: add integration tests for sync reconciliation`

## Phase 10 — Client and end-to-end

- [ ] `feat: add quirectl reference client`
- [ ] `build: add docker compose with two federated nodes`
- [ ] `test: add end to end suite for offline reconciliation`
- [ ] `test: add end to end suite for cross node replication`
- [ ] `test: add end to end suite for home server migration`

## Phase 11 — Kubernetes and delivery

- [ ] `build: add container image for the node server`
- [ ] `build: add kustomize base manifests`
- [ ] `build: add istio gateway and authentication policies`
- [ ] `build: add cert manager issuer and certificate`
- [ ] `build: add origin and replica overlays`
- [ ] `build: add kind based local cluster setup`
- [ ] `ci: add end to end job on kind cluster`
- [ ] `test: add grpc latency benchmark script`
- [ ] `docs: add deployment and operations guide`

## Design decisions to settle

Questions found while implementing, whose answer belongs in the thesis and must be settled
before the commit that depends on them.

### `updated_at` has to be causally monotonic — settle before commit 64

The planned per-field rule is "causal order first, then break ties on
`(updated_at, device_id)`". That is **not a total order** when `updated_at` is a wall clock,
and without a total order there is no maximum, so merge loses associativity: two nodes can
converge on different values depending on the order in which operations reached them.

The counterexample, with three writes on two devices:

| write | vector clock | relation |
|---|---|---|
| `a` | `{phone:1}` | `a` happens before `b` |
| `b` | `{phone:2}` | `b` concurrent with `c`, and `c` is later by the clock, so `b < c` |
| `c` | `{tablet:1}` | `c` concurrent with `a`, and `a` is later by the clock, so `c < a` |

That closes the cycle `a < b < c < a`. Clock skew between devices is what lets `updated_at`
contradict happens-before.

The vector clock itself is not at fault: a pointwise maximum is a genuine join semilattice,
and `internal/shared/crdt` proves the three laws by property test. The defect is only in the
tie-break layered on top of it.

**Proposed fix.** Make `updated_at` a hybrid logical clock: each write stamps
`max(local wall clock, greatest observed updated_at + 1)`. Then `a` happens-before `b`
implies `a.updated_at < b.updated_at`, the cycle cannot form, and the order is total. This
extends RN02/RNF03 and should be recorded in the thesis alongside them.

## Divergences from the thesis specification

Recorded here so they can be carried into section 4.2.4 of the TCC.

1. **English identifiers.** The MER names entities in Portuguese (`usuario`, `relogio_vetorial`,
   `operacao_sync`); the schema uses English (`users`, `vector_clock`, `sync_operations`).
   `mer-mapping.md` holds the full mapping table.
2. **File storage.** The MER models only metadata (`content_hash`, `size_bytes`). The schema adds
   `library.ebook_contents` (hash-deduplicated pointers into object storage) and a `BlobStore`
   port. An extension to the MER.
3. **JWT validated in the application too.** The TCC delegates validation to Istio (RNF12). An
   authentication interceptor in Go validates as well, so that tests and `docker compose` are
   authenticated without a mesh. Both run in production — defense in depth.
4. **Knative is out of scope.** Mentioned in section 2.6 but absent from RNF12 and from the
   deployment diagram.
5. **Go reference client (`quirectl`).** Needed to exercise the end-to-end suites without the
   Flutter application, and used to demonstrate the system to the examining board.
