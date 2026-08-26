# TCC correction log

Everything the implementation found that has to reach the thesis before it is submitted.
One entry per finding, never removed — an entry that has been carried into the text is
marked `carried`, so that a later reading of the document can tell what was addressed from
what was merely noticed.

The document has two parts, because the two need different work in the text:

- **Corrections** are places where the specification contradicts itself or states something
  that cannot hold. The text has to change.
- **Divergences** are places where the implementation deliberately departs from a
  specification that is internally consistent. The text has to *record* the departure,
  which is what subsection 4.2.4 exists for.

Open questions that block a specific commit are not here: they live under
[Design decisions to settle](ROADMAP.md#design-decisions-to-settle) in the roadmap until
they are answered, and become an entry here once they are.

**When you find another one, append it here.** The finding is worth as much as the code, and
it is lost the moment it stays in a commit message.

Identifiers used below: RF, RNF, RN and UC as numbered in subsections 4.2.1 and 4.2.2; MER
entities and Quadros as numbered in subsection 4.2.4 and Appendix A. The Portuguese-to-schema
name mapping is in [`mer-mapping.md`](mer-mapping.md).

## Corrections

### C01 — `atualizado_em` cannot break ties unless it is causally monotonic

**Where** RN02, RNF03, and the reconciliation described in 4.2.4.

**What the TCC implies** Per-field last-writer-wins: the vector clock decides first, and when
it reports two versions concurrent, the tie is broken on `(atualizado_em, id_dispositivo)`.

**Why it does not hold** Merging a set of versions means taking the maximum under that
relation, and a maximum exists only if the relation has no cycle. With `atualizado_em` a wall
clock, it can have one — from ordinary skew between devices, with no clock ever running
backwards.

Three devices:

| write | device | wall clock | `atualizado_em` | vector clock |
|---|---|---|---|---|
| `a` | phone, clock correct | `10:00:05` | `10:00:05` | `{phone:1}` |
| `b` | tablet, clock 10 s behind, has synced and seen `a` | `09:59:58` | `09:59:58` | `{phone:1, tablet:1}` |
| `c` | laptop, clock correct, has synced with nobody | `10:00:02` | `10:00:02` | `{laptop:1}` |

The three pairwise decisions:

| pair | vector clock | winner | by |
|---|---|---|---|
| `a` vs `b` | `a` precedes `b` | `b` | causality; the timestamp is not consulted |
| `b` vs `c` | concurrent | `c` | `10:00:02` > `09:59:58` |
| `c` vs `a` | concurrent | `a` | `10:00:05` > `10:00:02` |

That is `b > a`, `c > b`, `a > c`: a cycle, and therefore no maximum. Two nodes that saw all
three writes disagree permanently, according to the order the writes reached them:

- node X receives `a`, `b`, `c` → `merge(merge(a,b),c)` = `merge(b,c)` = `c`
- node Y receives `b`, `c`, `a` → `merge(merge(b,c),a)` = `merge(c,a)` = `a`

Neither node can detect it. Each applied the rule correctly and reached an answer that is
locally reasonable. What is lost is associativity, and eventual consistency (RNF03) is exactly
the promise that grouping and order cannot change the outcome.

The defect is one edge: `a` precedes `b` while `t(a) > t(b)` — a causally later write carrying
an earlier timestamp. Everything else in the relation is sound.

**Correction** Make `atualizado_em` a hybrid logical clock. Every write stamps

```
t = max(local wall clock, greatest t this replica has observed + 1)
```

In the example, the tablet had already seen `a` at `10:00:05`, so
`t(b) = max(09:59:58, 10:00:05 + 1µs) = 10:00:05.000001`, and the bad edge cannot be built.

In general, if `a` precedes `b` then the author of `b` had observed `a` — that is what
precedence means — so its greatest observed value is at least `t(a)` and `t(b) > t(a)`. Every
causal edge then points towards a larger timestamp, and so does every concurrent edge, since
that is the tie-break rule itself. A relation in which every edge increases a number has no
cycle, so the maximum always exists and merge is associative again.

Two concurrent writers that have never heard of each other can still land on the same `t`.
`id_dispositivo` decides between them; any fixed rule works, provided it is the same on every
node, and `(atualizado_em, id_dispositivo)` is then a total order — which is what taking a
maximum requires.

The cost is one extra read per write, for the greatest observed value, and that a device with
a fast clock pushes the value ahead of real time until real time catches up, bounded by the
skew. `atualizado_em` is `timestamptz`, so its microsecond resolution is the `+1`.

**Note** The vector clock itself is sound — a pointwise maximum is a genuine join
semilattice, proved by property test in `internal/shared/crdt`. The defect was only in the
tie-break layered on top of it.

**Status** settled 2026-08-26: hybrid logical clock, as above. Extends RN02 and RNF03 and has
to be recorded alongside them in the text. In the code it lands as `feat: add hybrid logical
clock` before the reconciler, and on the wire as a distinct `HybridTimestamp` message rather
than a `google.protobuf.Timestamp`, so that nothing can compare it against a wall clock by
accident.

### C02 — Quadros 18, 19 and 20 omit the attributes a replicable entity needs

**Where** Quadro 18 (`ebook`), Quadro 19 (`colecao`), Quadro 20 (`ebook_colecao`), against
the third modelling decision stated in 4.2.4.

**What the TCC says** 4.2.4 states that `relogio_vetorial` is present on the replicable
entities and that `removido` implements logical deletion on them. Appendix A declares
`relogio_vetorial` only on `progresso_leitura`, `anotacao` and `operacao_sync`, and
`removido` only on `ebook` and `anotacao`. Neither `ebook`, `colecao` nor `ebook_colecao`
has `atualizado_em`.

**Why it does not hold** `ebook` is unambiguously replicable — it carries `removido`, and
RF05 allows its metadata to be edited. Two devices editing the title offline produce two
versions with no causal information to compare, so RN02 has nothing to reconcile with.
`colecao` and `ebook_colecao` have no `removido` at all, so a collection deleted offline is
resurrected by the next node that had not yet heard about the deletion — exactly the failure
logical deletion is introduced to prevent.

A fourth attribute is missing for the same reason. The tie-break RN02 relies on is
`(atualizado_em, id_dispositivo)`, and none of the three entities records a device at all —
so even with a causally monotonic `atualizado_em` (C01), two concurrent writes that landed on
the same value have no deterministic winner. `anotacao` and `progresso_leitura` already carry
`id_dispositivo`; these three do not.

**Correction** Add `relogio_vetorial` (jsonb, not null), `atualizado_em`, `id_dispositivo`
and `removido` to Quadros 18, 19 and 20. The narrative in 4.2.4 already describes the
corrected version and needs no change.

**Status** open. Implemented in `000002_library_and_reading`.

### C03 — `usuario.email` and `usuario.senha_hash` cannot both be NOT NULL

**Where** Quadro 14, against 4.2.4 and RN08.

**What the TCC says** Appendix A declares both NOT NULL. 4.2.4 states that the address is
deliberately kept out of the replicated set so that the personal datum does not circulate
through the federation, and RN08 gives authentication to the origin server alone.

**Why it does not hold** A node that replicates a user for a peer holds a row for that user —
their e-books point at it — but has neither their address nor their password. The
constraint as written cannot be satisfied on any node that is not the origin server.

**Correction** Declare both nullable in Quadro 14, and state in 4.2.4 that they are
populated only on the origin server. The uniqueness of the address stays scoped to the
origin server, as RN09 already requires.

**Status** open.

### C04 — every temporal attribute should be `timestamp with time zone`

**Where** Quadros 13 to 23.

**What the TCC says** Every temporal attribute is typed `timestamp`.

**Why it does not hold** A `timestamp` without time zone carries no offset, so two instants
recorded on nodes in different zones cannot be compared, ordered or subtracted. A federation
spans operators and therefore zones by construction, and every temporal attribute in the
model is compared across nodes: `atualizado_em` breaks ties, `criado_em` and `aplicado_em`
identify pending operations, `expira_em` bounds a credential a peer may see.

**Correction** Type them `timestamptz` throughout Appendix A.

**Status** open.

### C05 — the vector clock on `progresso_leitura` can never record concurrency

**Where** Quadro 21 and the description of `progresso_leitura` in 4.2.4.

**What the TCC says** Progress is kept per work *and per device* ("mantém a posição de
leitura de cada obra por dispositivo"), and the entity carries `relogio_vetorial` to sustain
conflict resolution.

**Why it does not hold** If a row belongs to one device, that device is its only writer. Its
writes are totally ordered by its own counter, so two versions of the same row can never be
concurrent, and RN02 never fires on this entity.

**Correction** This is a result, not a defect, and stating it strengthens the argument:
reading progress is *conflict-free by construction*, and the clock on this entity serves as
a version counter for deduplication during replication rather than as a conflict resolver.
Say so in 4.2.4 rather than leaving the reader to infer that reconciliation applies here.
Add `UNIQUE (id_ebook, id_dispositivo)` to Quadro 21 — without it the rows accumulate
instead of being updated, and "the position of this device in this book" stops being a
single answer.

**Status** open. The grain itself stays as specified; the schema follows Quadro 21.
Implemented in `000002_library_and_reading`.

### C06 — `ebook_colecao` has no uniqueness constraint

**Where** Quadro 20.

**What the TCC says** The associative entity has `id_ebook_colecao` as its primary key and
two foreign keys.

**Why it does not hold** Nothing stops the same e-book from being inserted into the same
collection twice, and on an offline-first system two devices will do exactly that.

**Correction** Add `UNIQUE (id_ebook, id_colecao)` to Quadro 20.

**Status** open. Implemented in `000002_library_and_reading`.

### C07 — `operacao_sync` conflates the operation with its delivery

**Where** Quadro 23 and the description of `operacao_sync` in 4.2.4.

**What the TCC says** `id_servidor` is the destination node of the replication, and
`aplicado_em` is the instant the operation was applied at that destination.

**Why it does not hold** Those two attributes are properties of a *delivery*, not of an
operation. One change destined for three authorized replicas needs three rows, each carrying
its own copy of `carga_delta`, and the same change then has three different identifiers —
which makes deduplication at the receiving end depend on comparing payloads.

**Correction** Split the entity in two. `operacao_sync` keeps one row per change
(`id_operacao`, `id_dispositivo`, `entidade_alvo`, `id_entidade`, `tipo_operacao`,
`carga_delta`, `relogio_vetorial`, `criado_em`), and a new associative entity — `entrega_sync`
— carries `id_operacao`, `id_servidor` and `aplicado_em`, one row per destination. The delta
is then stored once and the operation keeps one identity across the whole federation.

**Correction, continued.** `entrega_sync` also needs what a replication worker cannot run
without: the number of attempts, the instant of the last one, and the last error. A peer
belonging to another operator is unreachable often enough that retrying it at full rate would
become the node's largest source of outbound traffic, and backing off requires that state to
be durable.

**Status** open. Implemented in `000003_sync` as `sync.operations` and `sync.deliveries`. Add
a Quadro for `entrega_sync` to Appendix A and the entity to Figura 18.

### C08 — `ultimo_sync_em` cannot serve as the synchronization cursor

**Where** Quadro 17 (`dispositivo.ultimo_sync_em`), RN06.

**What the TCC says** Only changes made after the last update are synchronized, and the
device records the instant of its last completed synchronization.

**Why it does not hold** The timestamp is assigned when the operation is written and the row
becomes visible when its transaction commits, and those two orders can differ. On a single
node with a perfect clock: transaction A writes an operation stamped `10:00:00.000` and is
slow to commit; transaction B writes one stamped `10:00:00.500` and commits at
`10:00:00.600`; a device pulls at `10:00:00.700`, sees only B, and advances its cursor to
`10:00:00.500`; A commits at `10:00:00.900` still carrying `10:00:00.000`. The next pull asks
for everything after `10:00:00.500`, and A never matches again. The operation is not delayed,
it is lost. Clock skew between devices only widens the window.

A monotonic sequence column has the same defect for the same reason: the number is taken at
insert time, not at commit time.

**Correction, in two parts.**

*The cursor.* Give each user a monotonic position allocated inside the writing transaction
itself, from a counter row (`UPDATE ... SET next_position = next_position + 1 ... RETURNING`).
The row lock is held until commit, so a later transaction cannot obtain its number before an
earlier one has committed: the order of the numbers *is* the order of the commits, and a
reader that has seen position N has necessarily seen every position below it. The cursor
becomes that position. `ultimo_sync_em` stays as a human-readable diagnostic.

*The safety net.* RNF09 already asks for a periodic verification of the device state against
the server, and this is what it should compare: a digest of the operation set, maintained
incrementally in the same transaction that writes the operation, as
`digest = digest XOR sha256(id_operacao)[:16]`. XOR is commutative and associative, so the
digest does not depend on the order the operations arrived in, and it is taken over
`id_operacao`, which is the same uuid on every node — so two nodes agree on the digest
regardless of the path each operation took through the federation. Equal digests end the
check in one round trip; unequal ones are narrowed by comparing the digest of successive
ranges of positions, which locates the gap in a logarithmic number of round trips rather than
by walking the history backwards. The client can maintain the same digest in SQLite at no
cost, since it writes each operation locally anyway.

The two are not alternatives. The cursor is the incremental path and is correct by
construction; the digest is an independent check that does not trust it.

**Rejected: comparing the number of rows.** The obvious cheap check — the device compares its
row count with the server's and walks backwards until they agree — does not detect
divergence. Two sets of the same size can differ, and on an offline-first device that is the
common case rather than a corner one: a device that lost operation 2 to the defect above and
holds its own unpushed operation 4 has `{1,3,4}` against the server's `{1,2,3}`, three rows
each, and the divergence is never noticed. Counting per origin would repair the star
topology but not this one — an operation can legitimately reach a device through a second
replicating node, so "how many operations the origin server holds" and "how many the device
holds" are different numbers with nobody at fault. A digest over the set is
topology-independent, which is precisely why anti-entropy protocols use one.

**Open sub-question, for when the digest is built.** Retaining every operation forever is not
an option, and pruning breaks the check: a server that trims its log and a device that trims
differently compute different digests over the same causal history, and the verification then
reports divergence permanently. XOR is its own inverse, so removing an operation from a
digest is the same operation as adding it — which means the fix is agreement on *what* was
pruned rather than on the digest itself. The likely shape is a watermark published with the
digest: the digest covers only the operations above it, and a device holding older ones
XORs them out before comparing. Settle it with the retention policy, not before.

**Status** settled 2026-08-26: cursor and digest, as above. The cursor is implemented in
`000003_sync`; the digest lands with the RNF09 verification in phase 9. Both still have to be
written into the text.

### C09 — `token_acesso` never stores an access token

**Where** Quadro 16 and the description of `token_acesso` in 4.2.4.

**What the TCC says** The entity is named `token_acesso`, and its own description states that
the access token is not persisted, because it is a JWT verified by signature (RNF11).

**Why it is worth changing** The entity holds the session refresh credential and the password
recovery credential. A reader of the model has to reach the end of the paragraph to learn
that the entity does not contain what its name says.

**Correction** Rename the entity to `credencial` in 4.2.4, in Quadro 16 and in Figura 18. The
description then needs no disclaimer: the entity holds the credentials that outlive a single
call, and the access token's absence from the model follows from RNF11 rather than having to
be excused.

**Status** settled 2026-08-26: renamed. The schema follows in
`000004_rename_access_tokens_to_credentials`, as `identity.credentials`.

### C10 — `anotacao.id_dispositivo` has to mean the last writer, not the originator

**Where** Quadro 22 and the description of `anotacao` in 4.2.4.

**What the TCC says** `id_dispositivo` is "o aparelho que originou a anotação".

**Why it does not hold** An annotation is editable (RF03, UC04 is marked «CRUD»), so it can
be rewritten from a device other than the one that created it. The model never uses the
originating device for anything, while the last-writer-wins tie-break of RN02 requires the
device whose write is currently reflected — and after an edit from a second device, those two
are different. Reading the attribute as the originator leaves the tie-break pointing at a
device that did not make the write it is meant to arbitrate.

**Correction** Describe `id_dispositivo` as the device whose write the row currently
reflects, on `anotacao` as on the entities C02 adds it to. The attribute then means the same
thing on every replicable entity, which is also what makes the reconciler uniform. On
`progresso_leitura` the two readings coincide, since a row there has a single writer (C05).

**Status** open. Implemented in `000002_library_and_reading`.

## Divergences

Deliberate departures from a specification that is internally consistent. Subsection 4.2.4
has to record them, with the reason.

### D01 — English identifiers, one schema per slice

The MER names entities in Portuguese (`usuario`, `relogio_vetorial`, `operacao_sync`); the
schema uses English and qualifies each entity with the slice that owns it (`identity.users`,
`vector_clock`, `sync.operations`), which removes the prefix the MER had to carry in a single
flat namespace. [`mer-mapping.md`](mer-mapping.md) holds the full mapping table.

### D02 — file storage

The MER models only metadata (`hash_conteudo`, `tamanho_bytes`). The schema adds
`library.ebook_contents`, hash-deduplicated pointers into object storage, and a `BlobStore`
port. An extension to the MER.

### D03 — the JWT is validated in the application as well

RNF12 delegates validation to Istio. An authentication interceptor in Go validates too, so
that the tests and `docker compose` are authenticated without a mesh. Both run in
production — defence in depth.

### D04 — Knative is out of scope

Mentioned in section 2.6 but absent from RNF12 and from the deployment diagram.

### D05 — Go reference client (`quirectl`)

Needed to exercise the end-to-end suites without the Flutter application, and used to
demonstrate the system to the examining board.
