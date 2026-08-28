# Implementation roadmap

One checkbox per commit. Tick a box only after the commit lands, so that any future session can
resume the work by reading this file and `git log`.

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
and are written in English. Commits stay small and are reviewed before landing.

The layout every slice follows — one package per entity, one per use case — is in
[`architecture.md`](architecture.md), together with the places this project departs from the
reference implementation it is modelled on. Read it before starting a slice.

Thesis traceability: requirement identifiers (RF, RNF, RN, UC) refer to the TCC specification;
the entity-name mapping between the MER and the database schema lives in
[`mer-mapping.md`](mer-mapping.md), and everything the implementation found that has to reach
the thesis — corrections to the specification and deliberate divergences from it — lives in
[`tcc-corrections.md`](tcc-corrections.md). Append there when you find another one.

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

- [x] `feat: add postgres connection pool and transaction helper`
- [x] `feat: add identity and federation schema migrations`
- [x] `docs: add tcc correction log`
- [x] `feat: add library and reading schema migrations`
- [x] `feat: add sync schema migrations`
- [x] `build: add sqlc configuration and generation target`
- [x] `docs: add mer to schema mapping table`
- [x] `refactor: rename the access token entity to credential`
- [x] `docs: settle the hybrid logical clock decision`

## Phase 3 — Protobuf contracts

- [x] `build: add buf configuration for protobuf generation`
- [x] `feat: add shared protobuf messages and vector clock type`
- [x] `feat: add auth service protobuf definition`
- [x] `feat: add library service protobuf definition`
- [x] `feat: add reading service protobuf definition`
- [x] `feat: add sync service protobuf definition`
- [x] `docs: record what a home server migration has to carry`
- [x] `feat: add federation service protobuf definition`

## Phase 4 — Servers

- [x] `feat: add grpc server bootstrap with interceptor chain`
- [x] `feat: add error and recovery interceptors`
- [x] `feat: add request logging interceptor`
- [x] `feat: add http server for health and metrics`
- [x] `feat: add well-known discovery endpoints`
- [x] `feat: add node server entrypoint` — not in the original plan, which never
      creates `cmd/quired`, so `make build` had been failing since phase 0

## Phase 5 — identity slice (UC06–08, UC14)

- [x] `docs: record the architecture every slice follows` — not in the original plan. The
      slice layout is
      [`College-Redberry/open-adoption`](https://github.com/College-Redberry/open-adoption),
      and it had never been written down beyond the four layer names in the readme
- [x] `feat: add user and device domain entities` — the credential entity lands here as
      well: it is the third entity the slice owns, and login, refresh and recovery all
      write through it
- [x] `feat: add user and device repositories with postgres` — the credential repository
      lands with them, for the reason its entity did
- [x] `feat: add password hashing service` — the password policy lands with it, as
      `user.Password` in the domain: its ceiling is bcrypt's seventy-two bytes, and a
      reader has to be told about it in terms of their password
- [x] `feat: add jwt signing service and jwks endpoint` — the opaque refresh and recovery
      credentials are minted by the same port, since a session is made of both halves
- [x] `feat: add register user use case` — needs this node's own row in
      `federation.servers`, which phase 6 owns. It lands as the `LocalServer` port with a
      temporary adapter; phase 6 replaces the adapter and the use cases do not change
- [x] `feat: add login and logout use cases`
- [x] `feat: add refresh token use case` — rotation, and reuse of a spent credential ends
      the device's sessions, per D07 in [`tcc-corrections.md`](tcc-corrections.md)
- [x] `feat: add password recovery use cases` — RF09 needs a credential delivered to an
      address and the architecture has no component that can deliver one, per C13 in
      [`tcc-corrections.md`](tcc-corrections.md). The port lands with one adapter, which
      writes to the log and refuses to be built outside development
- [x] `feat: add device management use cases`
- [x] `feat: add user profile use cases` — not in the original plan. UC06 is «CRUD» and the
      contract has `GetUser`, `UpdateUser`, `ChangePassword` and `DeleteUser`; without them
      the handlers of the next commit would have four methods and no use cases
- [x] `feat: add authentication interceptor`
- [x] `feat: add auth grpc handlers`
- [x] `feat: add identity container and wiring` — not in the original plan. The handlers
      reach the node through the slice's `di.Container`, which is what `cmd/quired` builds
      and what installs the authentication interceptor
- [x] `test: add integration tests for auth service` — against a PostgreSQL the runner
      supplies rather than one a container library starts; the reasoning is in
      `test/integration`
- [x] `docs: record the questions phase 5 left open` — not in the original plan. The slice
      raised five questions whose answer belongs in the thesis, and a question that stays in
      the session that found it is a question nobody answers

## Phase 6 — federation slice (UC12, UC13, UC15)

- [x] `feat: add server domain entity and repository` — takes over
      `internal/identity/infra/service/localserver`, the temporary adapter phase 5 needed in
      order to bind a reader to this node (UC14). The replica authorization entity and its
      repository land here as well: it is the second entity the slice owns, one sqlc run
      generates both, and the use cases that follow then build on a slice whose persistence
      is finished. So does the slice's `di.Container`, because the identity slice needs the
      catalogue from it and `cmd/quired` is where the two are wired. C15 in
      [`tcc-corrections.md`](tcc-corrections.md) records what the table being node-wide means
      for UC12
- [x] `feat: add well-known discovery client` — carries the `grpc` authority and the
      `spki-sha256:` pin the endpoints publish, per D06 and C12 in
      [`tcc-corrections.md`](tcc-corrections.md); the column and the `ServerDescriptor`
      field land with it
- [x] `feat: add discover server use case` — the slice's `command.Usecase` shape and its
      `apptest` doubles land with it, as phase 5's first use case did
- [x] `feat: add known server management use cases` — six of them, one per method of UC12.
      Deactivating a node carries the same refusal as forgetting one, since `active` is
      node-wide too; C15 in [`tcc-corrections.md`](tcc-corrections.md) records both
- [x] `feat: add replica authorization use cases` — and the row lock that makes the two
      refusals of the previous commit hold against a grant arriving at the same moment
- [x] `feat: add federation grpc handlers` — ten calls and the slice's `di.Container`, which
      already existed for the identity slice's sake and grows the gRPC surface here.
      `MigrateHomeServer` stays Unimplemented until phase 9, and a test names it so that it
      is a decision rather than an omission
- [x] `test: add integration tests for server discovery` — against the same supplied
      PostgreSQL as phase 5, and against peers that really answer a `.well-known` lookup

## Phase 7 — library slice (UC01–03)

- [x] `feat: add ebook and collection domain entities` — the membership entity lands here as
      well: it is the third entity the slice owns, and it is an entity rather than a list held
      inside a collection because it replicates on its own terms (C06). So does
      `crdt.Revision`, in the shared core rather than in the slice, because three slices hold
      one — the library writes it, the reading slice will, and the sync reconciler is written
      against it. Its `UpdatedAt` is stamped with the per-record half of C01's rule; the
      node-wide hybrid logical clock of phase 9 strengthens that without changing it
- [x] `feat: add ebook and collection repositories with postgres` — the membership repository
      lands with them, for the reason its entity did, and so does
      `000006_library_pagination_index`: listing is keyset paginated and the index the schema
      shipped with covered only `user_id`, so every page sorted the whole collection. Clearing
      the filings of a deleted work is a loop over rows rather than one `UPDATE` with a
      `jsonb_set` expression — the stamping rule of C01 exists once, in `crdt.Revision`, and a
      `SET` clause that recomputed it would be a second copy in a language it could not be
      tested against
- [x] `feat: add blob store port with s3 adapter` — three adapters over one port, not one:
      `s3`, `minio` and `gcs`, each on the SDK its provider publishes, chosen in `di` by which
      section of `QUIRE_STORAGE_*` the deployment filled in. The `ebook_contents` entity and
      its repository land here, because the row and the object are the same fact — this node
      has the bytes (D02). The cost is measured and recorded in D08: 25 modules to 86
- [x] `feat: add ebook management use cases` — five of them, one per method of UC01, and the
      slice's `command.Usecase` shape, its `Clock` and `Transaction` ports and its `apptest`
      doubles land with them. The clock adapter is a wall clock and the entity applies the
      per-record half of C01 over it; phase 9 replaces the adapter behind the same port
- [x] `feat: add collection management use cases` — seven of them: five for the grouping
      itself and two for what is filed under it, since UC03 is a «CRUD» whose contract has
      `AddEbookToCollection` and `RemoveEbookFromCollection` as well. Both are idempotent, and
      filing a work that is already filed still stamps a write — the call is idempotent to the
      reader and is not a no-op to replication
- [x] `feat: add ebook content upload and download streaming` — the bytes are staged, checked
      and only then stored, because the object is named by its digest and a node that streamed
      straight through would be writing under a name that promises otherwise. The `Staging`
      port and its temporary-file adapter land with it, as does C16 in
      [`tcc-corrections.md`](tcc-corrections.md): the upload carries no work identifier, so
      without a check the object store is writable by any authenticated reader
- [x] `feat: add library grpc handlers` — fourteen calls, and the slice's `di.Container`,
      which is where the object store adapter is chosen. The `pagetoken` and `fieldmask`
      packages land with them: a page token is the domain's keyset in a form a client can hold,
      and a mask naming a path the call cannot write is refused rather than ignored, because on
      a per-field last-writer-wins entity an ignored path is a change nobody made
- [x] `test: add integration tests for library service` — against the same supplied
      PostgreSQL as phases 5 and 6, and against a supplied MinIO on the same terms, which is
      what settled the open question about a container library for both

## Phase 8 — reading slice (UC04, UC05)

- [x] `feat: add annotation and reading progress entities` — `crdt.Version` lands with them, in
      the shared core beside `crdt.Revision`, because the two entities of this slice reconcile
      differently and C05 is the difference: an annotation is written by every device and needs
      the full revision, a progress row is written by one and carries the clock and the
      timestamp without the two fields that break a tie. The `locator` package lands as well —
      a value object both entities hold and neither owns, which is a departure from the layout
      every other slice follows and is recorded in [`architecture.md`](architecture.md)
- [x] `feat: add annotation and reading progress repositories` — the reading block of
      `sqlc.yaml`, and `000007_reading_pagination_index`, which is 000006's finding on the
      other table: the index the reading schema shipped with covers `ebook_id` alone, and the
      planner would not use it at all for an ordered page, reading through the primary key and
      filtering instead. Measured on 220 000 marks: 1 881 buffers and 3.4 ms became 4 and
      0.3 ms, and the last page costs what the first does. `revision` moves from the library
      slice into `internal/shared/persist`, because what a NULL `device_id` means is now a
      question four repositories ask
- [x] `feat: add annotation management use cases` — five of them, one per method of UC04, and
      the slice's `command.Usecase` shape, its `Clock` and `Works` ports and its `apptest`
      doubles land with them. `Works` is the one port no earlier slice needed: everything here
      hangs off a work, `reading.annotations` references the work and not the reader, so
      establishing whose a mark is means reading a row this slice does not own. It answers one
      question — may this reader see it — on the pattern the identity slice's `LocalServer`
      set. There is no `Transaction`: no call in this slice writes two rows
- [x] `feat: add reading progress use cases` — two of them, one per method of UC05.
      `UpdateProgress` takes no device from the request and neither does the entity: the row
      has one writer and it is the one the row names, which is C05 expressed in the types
      rather than in a check somebody has to remember. The pair constraint plus one retry is
      what settles two calls from a device crossing, since there is no row to lock the first
      time through and an `ON CONFLICT DO UPDATE` would be a second copy of C01's stamping
      rule in SQL
- [x] `feat: add reading grpc handlers` — seven calls, the slice's `di.Container`, and the
      adapter of the `Works` port, which reads through the library slice's own repository and
      is wired in `cmd/quired` where the two containers meet. Two packages move into the
      shared core rather than being written a second time: `fieldmask`, because every update
      in this contract carries a mask over an entity that reconciles per field, and the
      rendering of a `crdt.Revision` on the wire, which becomes `internal/shared/crdtpb` —
      the compaction rule is the one that would drift
- [x] `test: add integration tests for reading service` — against the same supplied
      PostgreSQL as phases 5, 6 and 7. The keyset walk edits a mark it has already returned
      and still sees every mark exactly once, which is what ordering by the identifier buys
      and what `updated_at` would have cost

## Phase 9 — sync slice (UC09–11, UC16)

- [x] `feat: add sync operation entity and repository` — the delivery entity and its
      repository land with them: it is the second entity the slice owns, C07 is what split it
      out of the first, and one sqlc run generates both. The position allocator of phase 2
      moves into the append statement as a data-modifying CTE, which is what makes C08's
      requirement — allocate in the transaction that inserts — structural rather than a
      comment somebody has to obey, since a statement cannot straddle two transactions
- [x] `feat: add hybrid logical clock` — the other half of C01, node-wide, in the shared
      core rather than in the slice: it is what the library and reading `Clock` ports have
      been promising since phase 7, and swapping the wall clock behind them for it changed no
      use case. Hand-written after measuring the alternatives — every published Go
      implementation carries the Kulkarni pair of an instant and a counter, which is two
      values where C01 stamps one `timestamptz`, and `lafikl/hlc` keeps both of them
      unexported with no accessor at all
- [x] `feat: add operation reconciler with crdt merge` — the tie-break is
      `(updated_at, device_id)` over the hybrid logical clock, per C01 in
      [`tcc-corrections.md`](tcc-corrections.md). The rule itself lands in
      `internal/shared/crdt` beside the merge laws it is proved with, and the property tests
      there now cover the join over whole revisions: order-independent reduction over
      generated histories, and C01's own counterexample reduced two ways to show what a wall
      clock costs. The adapter is what knows the five tables, and writing it found three
      things the specification does not say — C17, C18 and C19
- [x] `feat: add push operations use case` — one use case for both transports, because a
      device pushing what it wrote offline and a peer replicating a reader are offering the
      same thing: the input carries who the caller is rather than the credential they proved
      it with, and RN10 is a check against a device the batch may name or against nothing at
      all. The unit of work is one change and not the batch — PostgreSQL aborts a whole
      transaction on any statement it refuses, so one rejected change would take a reader's
      whole push with it. The slice's `command.Usecase` shape, its `Clock`, `Transaction`
      and `Records` ports and its `apptest` doubles land with it; the clock is the one port
      in the node with a second method, because this is the slice that meets other people's
      clocks
- [ ] `feat: add pull operations use case`
- [ ] `feat: add bidirectional sync stream handler`
- [ ] `feat: add replication worker for authorized nodes`
- [ ] `feat: add node to node replication handler with mtls`
- [ ] `feat: add home server migration use case`
- [ ] `test: add integration tests for sync reconciliation`

## Phase 10 — Client and end-to-end

- [ ] `feat: add quirectl reference client` — and the `make build` target grows its second binary back
- [ ] `build: add docker compose with two federated nodes` — both need
      `QUIRE_FEDERATION_ALLOW_INSECURE_DISCOVERY`, since the `.well-known` documents are
      plain HTTP there and the discovery client refuses that without it
- [ ] `test: add end to end suite for offline reconciliation`
- [ ] `test: add end to end suite for cross node replication`
- [ ] `test: add end to end suite for home server migration`

## Phase 11 — Kubernetes and delivery

- [ ] `build: add container image for the node server`
- [ ] `build: add kustomize base manifests`
- [ ] `build: add istio gateway and authentication policies` — the three paths of the HTTP
      listener need different policies: `/.well-known` has to stay reachable by strangers,
      since being readable without a prior relationship is its entire function, while
      `/metrics` and `/readyz` stay inside the mesh
- [ ] `build: add cert manager issuer and certificate` — the `Certificate` must keep its
      private key across renewals (`privateKey.rotationPolicy` left at `Never`): the pin
      published by discovery is over the public key, per C12 in
      [`tcc-corrections.md`](tcc-corrections.md), and rotating the key breaks every peer's
      pin on renewal — the exact failure C12 exists to remove
- [ ] `build: add origin and replica overlays` — both set `QUIRE_GRPC_ADVERTISED_ADDRESS`
      to the authority the gateway answers on, which is not the port the node listens on;
      outside development the configuration refuses to load without it
- [ ] `build: add kind based local cluster setup`
- [ ] `ci: add end to end job on kind cluster`
- [ ] `test: add grpc latency benchmark script`
- [ ] `docs: add deployment and operations guide`

## Design decisions to settle

Questions found while implementing, whose answer belongs in the thesis and must be settled
before the commit that depends on them. A question stays here only while it is open; once it
is answered it becomes an entry in [`tcc-corrections.md`](tcc-corrections.md), so that the
answer travels with the correction it produced rather than with the doubt it started as.

Four are open, all found while implementing phase 5. Each names what the answer changes, so
that answering it is a decision rather than an archaeology.

**Should the contract carry a password on the call that changes an address?** C13 aside, this
is the one with a security consequence: a session may change the address, the address is the
channel UC08 recovers an account through, so a device left unlocked for a minute is an account
takeover — and the specification already applies the check that would stop it, to the password
and not to the field that makes the password replaceable. C14 has the two shapes the fix can
take. It is not implemented, because `UpdateUserRequest` has no field to carry the password;
answering this is a contract amendment and the check that follows it.

**Should a node without a way to deliver a recovery start at all?** The delivery adapter of
C13 refuses to be built outside development, and the container fails when it does, so the node
does not start under `QUIRE_ENV=production`. That is honest — UC08 would otherwise be broken
in silence — and it blocks phase 11 until a transport exists. The alternative is a node that
starts and answers `RequestPasswordRecovery` with a failed precondition.

**Is the reuse trade of D07 the right way round?** A device whose reply was lost on a mobile
network retries with the credential it still holds and is logged out for it, which on an
offline-first system is not rare. The implementation follows the OAuth 2.0 Security BCP; the
alternative — a grace window — needs `token_acesso` to record which credential replaced it,
which is a change to Appendix A.

**Should changing a password end the session that changed it?** The contract says only that
resetting one ends every session. The implementation makes changing one do the same, on the
reasoning that a reader who changes their password is responding to a suspicion. Sparing the
calling device is implementable — the access token names it — and needs one more statement on
the credential repository.

Two have been settled since. Whether `updated_at` could break ties as a wall clock, on
2026-08-26: it cannot, and it becomes a hybrid logical clock — the counterexample and the
argument are in C01. And whether the integration suite should have a container library start
its dependencies, on 2026-08-27: it should not, and phase 7 confirmed it rather than
reopening it. That second one leaves no entry in
[`tcc-corrections.md`](tcc-corrections.md), because it is a question about this project's
tooling and not about the specification — the suite now needs a MinIO as well as a
PostgreSQL, `make test-up` brings both up, and the 87 modules testcontainers costs are
still 87 modules.

## Divergences from the thesis specification

Moved to [`tcc-corrections.md`](tcc-corrections.md), which now holds both the corrections the
specification needs and the divergences subsection 4.2.4 has to record. Keeping them in one
document is what stops a finding from being written down twice and answered once.
