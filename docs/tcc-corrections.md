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

**What the TCC implies** Per-field last-writer-wins over concurrent versions, with the tie
broken on `(atualizado_em, id_dispositivo)`.

**Why it does not hold** With `atualizado_em` a wall clock, that relation is not a total
order, so there is no maximum and merge loses associativity. With writes `a = {phone:1}`,
`b = {phone:2}`, `c = {tablet:1}`: `a` causally precedes `b`; `b` and `c` are concurrent and
`c` is later by the clock; `c` and `a` are concurrent and `a` is later by the clock. That
closes the cycle `a < b < c < a`, and two nodes converge on different values depending on
the order the operations reached them.

**Correction** Make `atualizado_em` a hybrid logical clock: every write stamps
`max(local wall clock, greatest observed atualizado_em + 1)`. Then `a` happens-before `b`
implies `a.atualizado_em < b.atualizado_em`, the cycle cannot form, and the order is total.
Record it alongside RN02 and RNF03.

**Note** The vector clock itself is sound — a pointwise maximum is a genuine join
semilattice, proved by property test in `internal/shared/crdt`. The defect is only in the
tie-break layered on top of it.

**Status** open — settle before `feat: add operation reconciler with crdt merge`.

### C02 — Quadros 18, 19 and 20 omit the attributes 4.2.4 requires of them

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

**Correction** Add `relogio_vetorial` (jsonb, not null), `atualizado_em` and `removido` to
Quadros 18, 19 and 20. The narrative in 4.2.4 already describes the corrected version and
needs no change.

**Status** open.

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

### C06 — `ebook_colecao` has no uniqueness constraint

**Where** Quadro 20.

**What the TCC says** The associative entity has `id_ebook_colecao` as its primary key and
two foreign keys.

**Why it does not hold** Nothing stops the same e-book from being inserted into the same
collection twice, and on an offline-first system two devices will do exactly that.

**Correction** Add `UNIQUE (id_ebook, id_colecao)` to Quadro 20.

**Status** open.

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

**Status** open. Add a Quadro for `entrega_sync` to Appendix A and the entity to Figura 18.

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

**Status** settled 2026-08-26: cursor and digest, as above. The cursor lands with
`feat: add sync schema migrations`; the digest with the RNF09 verification in phase 9. Both
still have to be written into the text.

### C09 — `token_acesso` never stores an access token

**Where** Quadro 16 and the description of `token_acesso` in 4.2.4.

**What the TCC says** The entity is named `token_acesso`, and its own description states that
the access token is not persisted, because it is a JWT verified by signature (RNF11).

**Why it is worth changing** The entity holds the session refresh credential and the password
recovery credential. A reader of the model has to reach the end of the paragraph to learn
that the entity does not contain what its name says.

**Correction** Cosmetic, and the author's call: either rename the entity to something like
`credencial`, or leave the name and add a sentence to 4.2.4 making the exclusion explicit at
the point where the name is introduced.

**Status** open, low priority. The schema uses `identity.access_tokens`, faithful to the
current name.

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
