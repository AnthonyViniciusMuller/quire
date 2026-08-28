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
      the handlers of the next commit would have four methods and no use cases. A fifth,
      `ChangeEmail`, was added after phase 11 when C14 was settled
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
- [x] `feat: add pull operations use case` — the whole of RN06, over the position C08
      settled on. The page includes the caller's own changes, which costs one comparison on
      the device and is what keeps the cursor meaning "everything this node holds below
      here": a page that hid them would leave gaps a device could not tell from gaps nobody
      told it about. An empty page leaves the cursor where the caller had it, since
      answering with a zero would send a device that had drained the log back to its
      beginning
- [x] `feat: add bidirectional sync stream handler` — three calls, the slice's
      `di.Container`, and the in-process hub that makes the stream a stream rather than a
      poll: the call that grows a reader's log wakes the streams waiting on it, and a poll
      is the backstop for what the hub cannot reach across two replicas of this node.
      Neither is load-bearing — a stream that missed both leaves the device with a cursor,
      which is the property C08 exists to give. `SyncAck` is given the function the contract
      describes: the node keeps at most one page in flight, because an operation written to
      a socket is not an operation written to disk. `ReplicateOperations` stays Unimplemented
      until the next commit, and a test names it so that it is a decision rather than an
      omission
- [x] `feat: add replication worker for authorized nodes` — the whole outbound half, and
      the queue is filled from the log rather than by the call that stored the change. That
      is what makes a peer authorized today (RF16, UC15) and a peer that missed a week the
      same case; rows written at ingest would leave a new replica permanently missing
      everything from before its own authorization, and nothing would notice. A peer is
      offered a reader's changes in the order this node committed them, which the pending
      query now joins the log to get — the row identifier would have been the cheap tie-break
      and is a random uuid, so it would have shuffled a history into an order the far end
      would refuse. The client speaks mTLS and pins the peer's public key per C12, including
      on a resumed session, where TLS 1.3 skips the certificate callback entirely
- [x] `feat: add node to node replication handler with mtls` — the inbound half, and the
      credentials the listener presents. The client certificate is requested and never
      required, because one listener serves devices and peers: a device carries a token and
      no certificate, so requiring one would refuse every device in order to identify a
      handful of nodes. The caller is read off the connection as a reader's identity is read
      off a token, and it is checked against the pin the catalogue learned from the peer's
      own discovery document — the two ends pin the same bytes. It is the only call in the
      contract refused on a reader's own instruction (RN03), and a reader who never
      authorized the node and one who is not hosted here are given the same words, because a
      peer able to tell them apart could enumerate this node's readers
- [x] `feat: add home server migration use case` — the whole of C11, and the two slices
      that had to stop needing each other first. The method is the federation service's and
      the work is the identity slice's, so the controller is the identity container's and
      `federationdi.Catalogue` is what breaks the knot: the identity slice takes the
      catalogue, and the federation slice takes the controller that came out of it. The
      devices are adopted with the identifiers they already hold, a migration carrying none
      is refused rather than accepted into a history nobody could continue, and
      `identity.users.migrated_from` records a claim this node cannot verify — provenance and
      never identity. Which device the session is for is not derivable from the contract,
      which is C20 in [`tcc-corrections.md`](tcc-corrections.md)
- [x] `test: add integration tests for sync reconciliation` — against the same supplied
      PostgreSQL as phases 5 to 8, and against all five slices at once, since the reconciler
      writes through two of them. What only a real database can answer is here: the cursor
      never skips under concurrent pushes, a refused change leaves nothing behind because the
      unit of work really unwinds, and the queue statement owes a peer authorized after the
      fact the whole log — which is what filling from the log rather than at ingest buys and
      what a fake would have imitated rather than checked. The delivery pass is assembled by
      hand rather than taken from the container, because what the container hands back is a
      loop around a timer and a test that waited for one would be testing the timer

- [x] `docs: record what phase 9 found and did not fix` — not in the original plan. Writing
      the log made it visible that nothing else writes to it: a change made through the API
      while a device is online produces no operation, so it reaches neither the reader's
      other devices nor an authorized replica, while the same change made offline reaches
      both. C21 in [`tcc-corrections.md`](tcc-corrections.md) records the finding and the
      shape of the fix, which is an outbox in every write use case of the library and reading
      slices — a larger change than the phase that found it

## Phase 10 — Client and end-to-end

- [x] `feat: add quirectl reference client` — and the `make build` target grows its second
      binary back. It is a device and not a caller, which is the whole of why it is a package
      rather than a handful of generated stubs: it is bound to an origin server, it carries
      the identifier every vector clock entry is keyed by, it stamps its own writes on a
      hybrid logical clock of its own, and it keeps all of that between two commands in one
      file — so a second `--state` is a second device, and UC10 is demonstrable on one
      laptop. A write is one method whichever path it takes: offline it is stamped and
      appended to the local log, connected it is the RPC, and the caller does not branch,
      which is the contract's own requirement that the two be indistinguishable once applied.
      What the client deliberately does not keep is a copy of the collection — it remembers
      the causal version it last saw of each record it touched, because that is what a later
      change has to be stamped on top of, and a local replica would have to be maintained by
      applying operations to it, which is a second reconciler in this repository. The client
      is `internal/client` and the terminal program over it is `cmd/quirectl`, recorded in
      [`architecture.md`](architecture.md) as the one thing under `internal/` that is not a
      slice; the command tree is `spf13/cobra`, which costs one direct dependency and two
      indirect ones against the twenty and sixty-six D08 measured. D05 in
      [`tcc-corrections.md`](tcc-corrections.md) records what of this has to reach 4.2.4
- [x] `build: add docker compose with two federated nodes` — both need
      `QUIRE_FEDERATION_ALLOW_INSECURE_DISCOVERY`, since the `.well-known` documents are
      plain HTTP there and the discovery client refuses that without it. The domains are
      network aliases, because a node is found by fetching a document from a path of its
      domain and `quire-b.example` therefore has to resolve — which is also why the
      discovery listener answers on port 80 and why the image grants the binary
      `cap_net_bind_service` rather than running as root. The image lands here rather than
      in phase 11: a compose file that cannot build the node is not a federation, and the
      manifests of that phase build on this Dockerfile instead of a second one. The keys
      are generated by `scripts/dev-certs.sh` and are in no clone of this repository — the
      certificates reach the nodes as a mounted directory, the signing keys through the
      environment `make dev-up` exports, because a key in the compose file is a key in the
      repository. Two things the stack made visible: a node that presents a certificate
      presents it to devices too, since one listener serves both, so the certificates carry
      the loopback address beside the domain and `quirectl` names one with `--ca`; and
      `QUIRE_FEDERATION_TLS_CA_FILE` is read by nothing, because the pin of C12 replaced the
      authority it was for
- [x] `fix: let a peer reach the replication handler` — not in the original plan, and found
      by the federation above: `ReplicateOperations` is not in the identity slice's list of
      methods that need no access token, so the authentication interceptor refuses every
      peer with `Unauthenticated` before the handler that checks its certificate ever runs.
      The whole inbound half of phase 9 is unreachable in a node that installs the
      interceptor, which is every node `cmd/quired` builds. The integration suite could not
      have caught it: its peer-facing test calls with a device's session, which passes the
      interceptor and is refused by the handler, so what it pinned was the second refusal
      and never the first. The test that replaces it calls with no token at all
- [x] `test: add end to end suite for offline reconciliation` — against the federation
      `make dev-up` starts, supplied rather than started for the reason the integration
      suite's database is, and driving `internal/client` in process: what it shows is what a
      suite with one node in one process cannot, since two devices there would be one clock.
      Each test registers a reader of its own and resets nothing, because the federation is
      long-lived and shared with the demonstration. The reconciliation test makes the order
      wrong on purpose — the later write pushed first — and both devices still converge on
      the write that wins on `(updated_at, device_id)`, which is C01 checked from the far
      end rather than from a property test. Two of the five tests had to be written around
      C21 and one of them pins it: a work created through the connected path is readable by
      every device and appears in no page any of them pulls, so the suite that would have
      shown a cursor moving had to author its work offline first
- [x] `test: add end to end suite for cross node replication` — the whole path between two
      nodes: the origin discovers the peer over RFC 8615, pins the key it published, the
      reader authorizes it, and a change authored offline on a device reaches the other
      node's database without anybody asking for it. What the suite found is C22 in
      [`tcc-corrections.md`](tcc-corrections.md): a replica refuses everything until its own
      database holds the origin, the reader, the permission and every device that authored
      anything — and no call in the contract can tell it any of that, so a federation
      assembled through the API alone cannot replicate. The suite stands in for the missing
      call rather than around it, writing exactly what it would carry out of the document the
      origin already publishes; everything past that point is the real mechanism. The two
      refusals are checked as well — a node the reader never authorized gets nothing, and a
      peer whose key is not the one pinned is not spoken to (C12) — and the second of those
      has to put the pin back, because the catalogue is node-wide (C15) and a wrong pin left
      behind is a wrong pin for every reader on that node
- [x] `test: add end to end suite for home server migration` — a reader moves to a node they
      had no account on, carrying two devices, and both go on writing where they left off:
      what each was holding and had not handed over is pushed to the new node and applied
      there under the identifiers it was authored with, which is the whole of C11. The
      previous node is not asked anything and loses nothing, and a device binding there
      afterwards still finds the reader it always had. The identifier a reader arrives under
      is checked where it is visible at all — the database — because it is provenance and
      appears in no reply. It cost one change to the client: a session for a reader whose
      identifier is not the one the state was holding no longer clears the causal state,
      because that is the ordinary shape of UC16 and clearing it would complete the
      migration by discarding what was being migrated. The refusal of a migration carrying
      no device is checked with a hand-assembled call, since the client always sends the
      device making it

## Phase 11 — Kubernetes and delivery

- [x] `feat: add smtp delivery adapter for password recovery` — not in the original plan, and
      what unblocks everything below it: the node could not start under `QUIRE_ENV=production`
      at all, because the only adapter of the delivery port writes the credential to the log
      and refuses to be built there — so a manifest that declared the production profile
      declared a container that exits. C13 in [`tcc-corrections.md`](tcc-corrections.md) named
      the missing component; this is it, and the answer to the design decision that named it a
      blocker. The transport is `net/smtp` rather than a mail library, and the measurement is
      in the commit: what a library would replace is four standard-library calls for one
      plain-text message with no attachments and no alternatives, which is the trade this
      project refused for chi and accepted for the object store SDKs — the difference being
      whether the library is the code the service's own documentation describes. The `di`
      picks the adapter by which section of the configuration is filled in, never by a
      variable naming the transport, as the object store already does
- [x] `feat: hand a password recovery to a worker instead of awaiting it` — the second half of
      C13, and the one the uniform reply cannot do on its own: `RequestPasswordRecovery`
      answers the same way whether or not the address is registered here, but not in the same
      time, and a delivery is by far the slowest thing on that path. The queue is a decorator
      over the port rather than a behaviour of the transport, because the difference belongs to
      the call and not to the way the message travels — the node that writes the credential to
      its log has exactly the same one. A full queue is refused rather than waited on, since a
      call that blocked would take longest when the node is least able to deliver anything, and
      what was already accepted is drained when the node is asked to stop. It is not durable
      and does not claim to be
- [x] `build: add container image for the node server` — phase 10 already built one, in
      [`deploy/docker/Dockerfile`](../deploy/docker/Dockerfile), because a compose file that
      cannot build the node is not a federation. What is left here is whatever a cluster
      needs of it that a laptop does not, and it turned out to be a second runtime stage
      rather than a compromise between the two: the laptop image is Alpine because a node
      found by a domain alone answers discovery on 80 and binding 80 unprivileged needs a
      capability, which needs libcap to set; the cluster image is distroless with no shell
      and nothing to bind 80 with, because there the address a peer resolves belongs to the
      gateway and the pod listens on 8080. Both carry the same binary from the same build
      stage. The labels are the OCI set, with the version and the revision filled in by
      `make image`, and the tag is `git describe` rather than `latest` — a pod that cannot
      be traced back to a commit is a pod nobody can debug, which is what `latest` guarantees
      exactly when it matters. `.dockerignore` lands with it: the context carried `bin`,
      which would have copied the host'"'"'s binaries over the ones the build produces, and
      `deploy/docker/certs`, which has no business inside an image
- [x] `build: add kustomize base manifests` — [`deploy/k8s/base`](../deploy/k8s/base): the
      workload, what reaches it, the identity it runs as, the schema job, and the
      configuration every node shares. What it deliberately holds none of is a namespace, a
      domain, an advertised address or a secret — those are what make a deployment *this*
      node. The pod drops every capability, runs read-only and non-root, and can do all of
      that because the cluster image binds nothing privileged. The schema became a second
      image rather than a generated `ConfigMap`: kustomize will only generate one from files
      under its own root, which `migrations/` is not and should not be moved under, and an
      image versions the schema with the binary that expects it — one tag names both
- [x] `build: add istio gateway and authentication policies` — the three paths of the HTTP
      listener need different policies: `/.well-known` has to stay reachable by strangers,
      since being readable without a prior relationship is its entire function, while
      `/metrics` and `/readyz` stay inside the mesh. Writing them found C23 in
      [`tcc-corrections.md`](tcc-corrections.md), which is larger than the policies: the
      gateway of 4.3 cannot terminate the federation connection at all. It is mutually
      authenticated and pinned at both ends — the node presents the key it published (C12)
      and reads the caller's — and a gateway that terminates it presents its own certificate
      and consumes the caller's. So there are two ports: 443 is terminated and routes the
      documents by path, 9443 is `PASSTHROUGH` matched by SNI, and the mesh's own mTLS is
      disabled on 9090 because what arrives there is already mutually authenticated by two
      parties the mesh has no identity for. Each node also gets a gateway of its own rather
      than sharing the mesh's: two nodes belong to two operators who share no authority, which
      is the premise C12 is built on, so a shared ingress models something else — and a node
      that does not own the thing answering for its domain cannot own the certificate it
      presents either
- [x] `build: add cert manager issuer and certificate` — the `Certificate` must keep its
      private key across renewals (`privateKey.rotationPolicy` left at `Never`): the pin
      published by discovery is over the public key, per C12 in
      [`tcc-corrections.md`](tcc-corrections.md), and rotating the key breaks every peer's
      pin on renewal — the exact failure C12 exists to remove. It is written out rather than
      left to the default, because a default that is load-bearing is one somebody changes
      without knowing what it was holding up. A node holds two certificates and not one, for
      the reason C23 gives: the key the federation pins may never rotate, and the key the
      document server presents is pinned by nobody and rotates on every renewal. The
      authority they come from is cluster-scoped and applied once, and is the one thing here
      a real deployment replaces rather than copies — two operators share none
- [x] `build: add origin and replica overlays` — both set `QUIRE_GRPC_ADVERTISED_ADDRESS`
      to the authority the gateway answers on, which is not the port the node listens on;
      outside development the configuration refuses to load without it — and after C23 it is
      not even the port the documents are served from, since the federation port is passed
      through and 443 is terminated. An overlay says four things and nothing else: the
      namespace, the domain, what the node needs beside it and what signs its certificates.
      The dependencies land here as a component that a real deployment deletes — a
      PostgreSQL, an object store and a mail relay, all three speaking TLS, because the node
      runs under `QUIRE_ENV=production` and a dependency stack that made it relax those
      checks would be a deployment testing something else. No overlay contains a secret
- [x] `build: add kind based local cluster setup` — [`scripts/kind-up.sh`](../scripts/kind-up.sh)
      and [`deploy/kind/cluster.yaml`](../deploy/kind/cluster.yaml): one Kubernetes node, six
      published ports, and two Quire nodes under `QUIRE_ENV=production`. Two things it does
      that a manifest cannot. It generates every credential and writes it straight into the
      cluster, so no key is in this repository and each run replaces the last — which is why
      it restarts the node afterwards. And it teaches CoreDNS to rewrite `quire-a.example` to
      the gateway that answers for it, because a node is discovered by fetching a document
      from a path of its domain and that domain has to resolve. The host half of that is
      `/etc/hosts`, which the script names and refuses to edit: a script that needed a
      password to bring up a test cluster is a script nobody should run. `istioctl validate`
      is what caught the two port names the mesh would have refused, `smtp` and `postgres`,
      and the four workloads whose telemetry would have arrived unattributed
- [x] `fix: match the federation route on sni and the documents on nothing` — not in the
      original plan, and found while writing the job above: an HTTP route matches the
      authority and an authority carries a port, so a caller reaching a gateway on 18443
      rather than 443 sends `quire-a.example:18443` and a route naming the domain alone
      answers 404 to it. That is how the end-to-end suite reaches any local cluster. The
      documents match no host at all, which the gateway per node makes correct rather than
      expedient — a request arriving at it is for that node whatever the caller called it,
      and the assertion about the domain a caller can act on is the certificate. The
      federation route keeps the domain, because SNI is a name and carries no port
- [x] `ci: add end to end job on kind cluster` — the manifests run rather than rendered, which
      is the one job that can answer whether a node starts under `QUIRE_ENV=production` and
      whether two of them find each other through a mesh that terminates one port and passes
      another through. It is also where the `kind` build tag finally earns the place
      `.golangci.yml` has held for it since phase 0: the only thing it changes is how long a
      test waits, because the manifests declare the production replication interval of thirty
      seconds and the compose file overrides it to five. Everything else is the same suite,
      seen through a mesh instead of through a bridge network
- [x] `test: add grpc latency benchmark script` — [`scripts/bench.sh`](../scripts/bench.sh),
      which measures the call a device actually waits on, with a real session, against a
      running node: `PullOperations` from the beginning, which is the largest answer that
      call ever gives, and an empty `PushOperations` beside it. The session is the point —
      every call is verified, logged, counted and translated, and a benchmark that skipped
      the token would be measuring a node this repository does not build. RNF06 states a
      number and not a percentile, so the script reads it at the 95th and says so. Against
      the compose federation on 2026-08-28: p95 of **3.15 ms** for the pull and **4.53 ms**
      for the push, against a budget of 200 ms
- [x] `docs: add deployment and operations guide` — [`deployment.md`](deployment.md), which the
      readme has been pointing at since phase 0. What it is mostly about is the two things a
      reader of the manifests would otherwise have to reconstruct: why the federation port is
      not terminated (C23) and why one of the two private keys may never rotate (C12). It also
      says plainly what a real deployment replaces — the dependency stack, the authority, and
      the relay — because a reference deployment that does not name its own local
      accommodations is one somebody copies whole

## Phase 12 — answers to the questions phase 5 left open

Not a slice. Each of these implements an answer that was given after phase 11, and each names
the entry in [`tcc-corrections.md`](tcc-corrections.md) the answer produced.

- [x] `feat: add change email use case with a password check` — C14, settled: the address is
      the channel UC08 recovers an account through, so whoever can change it can have a
      recovery sent somewhere of their choosing, and a session proves only that a device is
      unlocked. The shape is the second of the two C14 named — a `ChangeEmail` call beside
      `ChangePassword` — because a field mask has no way to say that one of its paths needs a
      credential. `email` left `UpdateUser` entirely, and a mask still carrying it is refused
      by naming where the check moved to. The notice to the previous address lands with it and
      is the half that survives somebody who learned the password; it is also why this could
      not be implemented before C13 was

## Design decisions to settle

Questions found while implementing, whose answer belongs in the thesis and must be settled
before the commit that depends on them. A question stays here only while it is open; once it
is answered it becomes an entry in [`tcc-corrections.md`](tcc-corrections.md), so that the
answer travels with the correction it produced rather than with the doubt it started as.

Two are open, both found while implementing phase 5. Each names what the answer changes, so
that answering it is a decision rather than an archaeology.

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

Four have been settled since. Whether `updated_at` could break ties as a wall clock, on
2026-08-26: it cannot, and it becomes a hybrid logical clock — the counterexample and the
argument are in C01. Whether a node with no way to deliver a recovery should start at all, on
2026-08-28: the question was the wrong way round, and the missing component was built instead —
the transport and the queue are both in C13, which is now settled, and 4.3 gains a component.
Whether the call that changes an address should carry the password, on 2026-08-28: it should,
and C14 records the amendment — a `ChangeEmail` call beside `ChangePassword`, since a field
mask cannot say that one of its paths needs a credential, plus the notice to the previous
address that the transport of C13 finally made possible. And whether the integration suite should have a container library start its
dependencies, on 2026-08-27: it should not, and phase 7 confirmed it rather than reopening it.
That last one leaves no entry in [`tcc-corrections.md`](tcc-corrections.md), because it is a
question about this project's tooling and not about the specification — the suite now needs a MinIO as well as a
PostgreSQL, `make test-up` brings both up, and the 87 modules testcontainers costs are
still 87 modules.

## Divergences from the thesis specification

Moved to [`tcc-corrections.md`](tcc-corrections.md), which now holds both the corrections the
specification needs and the divergences subsection 4.2.4 has to record. Keeping them in one
document is what stops a finding from being written down twice and answered once.
