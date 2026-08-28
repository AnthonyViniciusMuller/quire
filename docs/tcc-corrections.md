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

**Implemented in two levels, and the text should say why.** The rule is applied over one
record in `internal/shared/crdt` — a revision is stamped no earlier than one step past the
version it was derived from — and over the whole node in `internal/shared/hlc`. A maximum of
maxima is a maximum, so the second strengthens the first without changing it.

Two consequences follow, and both are properties of the correction rather than of the code.
The node-wide clock is held in memory, so a restart forgets it; that is survivable because
the cycle above lives between versions of *one* record, and every write reads the record it
is about to stamp, so the per-record floor holds whether or not the node-wide one does. And
an instant observed from a peer is adopted only within a five-minute ceiling: without one, a
single peer whose clock is a year fast pushes this node a year into the future and keeps
every tie there, and refusing the observation is safe for exactly the reason a restart is.

**Status** settled 2026-08-26, implemented 2026-08-28. Extends RN02 and RNF03 and has to be
recorded alongside them in the text, together with the two consequences above. In the code it
is `internal/shared/hlc`, behind the `Clock` port of the library and reading slices, and on
the wire a distinct `HybridTimestamp` message rather than a `google.protobuf.Timestamp`, so
that nothing can compare it against a wall clock by accident.

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

### C11 — a migration that carries no device identities restarts causality

**Where** RF17 and UC16, and the description of the migration in 4.2.2.

**What the TCC says** The reader migrates to another origin server preserving the collection,
the annotations and the reading progress, and — because every device keeps a complete local
copy — the migration depends neither on the cooperation nor on the availability of the
previous server.

**Why it does not hold** The independence is real and worth keeping. Two things follow from
it that the text does not say, and the second one is a defect only while it stays unsaid.

*The devices have to travel with the reader.* Every operation names the device that authored
it, and every vector clock is keyed by a device id. On the new node those ids have to be the
ones the devices already hold. If the new node mints fresh ones, the imported history names
devices that do not exist there — which the schema refuses outright, since `sync.operations`
references `identity.devices` — and even if it did not, a device writing under a new id
starts a second clock entry that never merges with the first. Two devices that had been in
sync would then read as concurrent for ever, and every write between them would fall through
to the tie-break. So migration is not "register, then push": the new node has to adopt the
device records, ids included, before the first operation can be inserted.

*The previous identity cannot be proved.* A node that needs nothing from the previous server
has nothing with which to check that the caller was `@anthony:old.example`. It can record the
claim; it cannot verify it. What UC16 preserves is therefore the data and not the name: the
domain half of the identifier necessarily changes, so the identifier changes, and the new one
is a new identity that happens to hold the old one's history. Peers that replicated the reader
hold an authorization naming the old identity (RF16), so they are authorized again rather than
following the reader on their own.

**Correction** Say both in the description of UC16. The migration carries the device records
with their ids; the identifier changes; the replicas are re-authorized. And state the trade-off
rather than leaving it implicit: identity continuity is purchasable, at the price of a
signature from the previous server — a key published in its JWKS, signing an assertion that
the reader is moving — which reintroduces exactly the dependency on that server's availability
that UC16 exists to remove. The thesis chooses availability over provable continuity, and that
is a defensible choice once it is a choice.

**Status** open. On the wire in `FederationService.MigrateHomeServer`, which carries the
devices for that reason; implemented in phase 9 (`feat: add home server migration use case`).

### C12 — a fingerprint pinned over the certificate breaks the federation on a schedule

**Where** RNF08, the description of the four channels in subsection 4.3 (Figura 19), and
`servidor.impressao_cert` in Quadro 13.

**What the TCC says** Node-to-node communication "emprega mTLS com verificação da impressão
digital do certificado obtida no processo de descobrimento", and `impressao_cert` holds "a
impressão digital fixada para a comunicação em mTLS". The same subsection states that
cert-manager obtains and renews the gateway certificates through ACME.

**Why it does not hold** Those two statements are about the same certificate. An ACME
certificate is issued for ninety days and renewed at sixty, and a renewal is a *new*
certificate: new serial number, new validity, new signature, and therefore a different
digest. A peer that pinned the digest during discovery stops matching it on the day of the
first renewal, and what it then refuses is the replication itself. Node-to-node
synchronization would break about every sixty days, for every pair of nodes in the
federation, permanently, with nobody having done anything wrong.

Worse than the outage is what it teaches. A fingerprint mismatch is precisely what a node
presenting a substituted certificate looks like, so routine renewal and the attack the pin
exists to detect produce the same signal. An operator who sees that signal every two months
and clears it by re-pinning has been trained to re-pin without checking — which is the same
security posture as not pinning at all, arrived at by a route that looks diligent.

**Correction** Pin the public key rather than the certificate. The digest is taken over the
SubjectPublicKeyInfo, and the published value says so: `spki-sha256:<base64>`, which is the
form and the recipe of RFC 7469. cert-manager reuses the private key across renewals unless
the Certificate asks it not to, so a renewed certificate carries the same key and the pin
still matches. It changes only when the key is deliberately rotated — which is the deliberate
act that `FederationService.RefreshKnownServer` was written for, and which an operator has
reason to look at.

The trade-off belongs in the text rather than in a footnote: a pin that survives renewal is a
key that outlives the certificates carrying it. The alternative is a pin every peer must
renew by hand every sixty days, which no operator sustains — and RNF08 only guarantees
anything while the check is one somebody still reads. Say which was chosen and why.

`impressao_cert varchar(128)` is wide enough for the new form (twelve characters of prefix
and forty-four of base64), so Quadro 13 needs only its description corrected.

**Status** settled 2026-08-27: the public key, as above. Implemented in
`internal/shared/wellknown`, which publishes the value, and pinned by the discovery client in
phase 6.

The correction rests on one deployment fact and is only as true as that fact: cert-manager must
not rotate the private key when it renews. Its `Certificate` defaults to keeping the key, and a
`privateKey.rotationPolicy` of `Always` — which is what a security review recommends in general,
and therefore what somebody will one day set here in good faith — reinstates this defect in
full. Whoever writes the manifest in phase 11 has to know that, so the roadmap says it there
too, and the manifest itself should carry the reason rather than the setting alone.


### C13 — RF09 requires delivering a credential, and nothing in the architecture can deliver one

**Where** RF09 and UC08, against the deployment described in subsection 4.3 (Figura 19).

**What the TCC says** The reader recovers their password; `token_acesso` holds a recovery
credential with an expiry, so the flow is a credential sent to the reader and presented back.
The deployment diagram holds a gateway, a service mesh, cert-manager, the node, PostgreSQL and
object storage.

**Why it does not hold** None of those can deliver a credential to a reader's address. The
recovery of UC08 begins by sending something to the address on record, and the address is the
only channel the reader has left — that is what makes it a recovery rather than a login. With
no component for it, the first half of UC08 has no implementation and the second half has
nothing to consume.

Two things are missing, not one.

*A transport.* An SMTP relay or a delivery provider, with the configuration a node needs in
order to reach it. `internal/shared/config` has no section for one, because none was specified.

*A queue.* The reply to a recovery request is deliberately the same whether or not the address
is registered here, so that the call cannot be used to find out who is. The time it takes is
not the same: an address that exists costs a delivery and one that does not costs nothing, and
a delivery is the slowest thing in the call. Closing that channel means handing the delivery to
something else and answering immediately, which is a queue — the same component the transport
needs in order to retry a provider that is down.

**Correction** State in 4.3 that the origin server needs an outbound delivery component and
that the recovery notification is queued rather than awaited, and add its configuration to the
node. The alternative is to say plainly that UC08 is specified but not deployed, which is a
weaker position than naming the component.

**Status** open. The implementation defines the delivery port and ships one adapter, which
writes the credential to the log and **refuses to be built outside the development profile** —
a node that printed a reader's recovery credential to its logs in production would hand it to
whoever reads them. Until the transport lands, that refusal is what stops the gap from being
shipped by accident.

### C14 — UC06 lets a session change the recovery address without proving the reader is present

**Where** UC06 and the `UpdateUser` call of the contract, against UC08.

**What the TCC says** UC06 is «CRUD»: the reader maintains their registration data, the address
among them. Changing the password is a separate act, and the contract asks for the current
password when it happens — because a session proves that a device is unlocked, not that the
reader is at it.

**Why it does not hold** The address is not one registration field among others. It is the
channel UC08 recovers an account through, so whoever can change it can have a recovery
credential sent to an address of their choosing, and then set the password. A session that may
change the address is therefore a session that may take the account, and the check the
specification already applies to the password — prove you are the reader, not merely a device
somebody left unlocked — is missing from the one field that makes the password replaceable.

The gap is not theoretical: a device left unlocked for a minute is the exact threat the
password check on `ChangePassword` was written for, and this path goes around it.

**Correction** Ask for the current password when the address changes, as `ChangePassword`
already does. In the contract that is either a `password` field on `UpdateUserRequest`, applied
only when the mask carries `email`, or a `ChangeEmail` call beside `ChangePassword` — the
second reads better, since the field mask of a general update has no way to say "this one field
needs a credential".

Sending a notice to the *previous* address is worth stating alongside it, and is the part that
survives a compromise: it is how a reader finds out. It needs the delivery component C13 is
about.

**Status** open, and **not yet implemented** — the use case follows the contract as it stands,
because the contract has no field to carry the password. The change belongs with the contract
amendment.

### C15 — UC12 is written as the reader's catalogue, and `servidor` names no reader

**Where** UC12 and RF13, against `servidor` in Quadro 13 and `replica_usuario` in Quadro 15.

**What the TCC says** UC12 is the reader maintaining «os servidores de sincronização
conhecidos», which reads as a catalogue belonging to whoever is signed in. `servidor` has no
reference to `usuario`: the only table that names a reader is `replica_usuario`.

**Why it does not hold** The two cannot both be true, and the schema is the one that is right.
A node is a domain, an origin, a signing key location and a pinned public key — facts about
that node, identical for everybody here, and `servidor.dominio` is unique for exactly that
reason. Giving the table a reader would give one node as many rows as it has readers on this
instance, and therefore as many pinned keys; the one that is wrong would then be invisible
against the others, which is the opposite of what a pin is for (C12).

What is per-reader is not the knowledge but the permission, and that is what `replica_usuario`
already is: nothing leaves this node for a peer without an active row there (RN03).

Two consequences follow, and both are visible in the contract.

*Adding.* A domain another reader added first is already in the catalogue, and the second
reader is told so rather than given a second row. Re-running discovery on it is
`RefreshKnownServer`, a deliberate act, and not a side effect of somebody else's addition —
C12 again.

*Removing.* Forgetting a node is not a private act. A node another reader still replicates to
must not be removable, or that reader would be left unable to revoke a peer that holds their
data — and RN03 is the promise that they can. The check is over every reader's
authorizations, not the caller's. It is worse than an oversight would suggest, because
`replica_usuario` cascades on the deletion of `servidor`: a removal that got past the check
would not be refused by the database, it would take that reader's authorization with it.

*Deactivating.* `servidor.ativo` is node-wide for the same reason the rest of the row is, so
clearing it stops the replication of every reader who authorized the node and not only of the
one who cleared it. UC12's update therefore carries the same check as its delete. What a
reader may do alone is revoke their own authorization, which is UC15 — and stating that
contrast is what keeps the two use cases from looking like two ways to do one thing.

**Correction** State in 4.2.4 that `servidor` is a catalogue of the node and `replica_usuario`
is the decision of the reader: knowing that a node exists is shared, permission for it to hold
a copy is not. UC12 then has to say that its delete is refused while any active authorization
names the node.

**Status** settled 2026-08-27: node-wide, as above. Implemented in
`internal/federation/domain/server`, whose package comment carries the reasoning, and enforced
by the known server management use cases of phase 6.

### C16 — UC02 lets any authenticated reader write bytes the node cannot attribute to them

**Where** UC02 (importar e-book) and, in the implementation's contract,
`LibraryService.UploadEbookContent`.

**What the TCC says** The reader imports a file; the node stores it, keyed by its digest,
and deduplicates identical files across readers.

**Why it does not hold** The upload carries the description of the file — digest, length,
media type — and no e-book identifier, because the object is keyed by the digest and shared
between every work that names it. That is right, and it has a consequence the specification
does not address: the node has nothing to check the upload against. Any authenticated reader
can stream any bytes under any digest, and the object store is then writable by anyone with
an account, without bound and without a row anywhere that says whose file it was.

The digest check is not the answer to this. It stops bytes from being stored under a name
that lies about them; it says nothing about whether the caller had any business storing them.

**Correction** State the precondition UC02 already implies and that the contract leaves
unwritten: the bytes may be uploaded only for a digest the calling reader already has a work
naming. A correct client is unaffected — the flow the contract describes is `CreateEbook`,
read `content_missing`, then `UploadEbookContent`, so the work always exists first — and a
node that checks it cannot be used as an object store by an account that holds no library.
Record it in 4.2.2 as a precondition of UC02.

**Status** open. Implemented in phase 7: the upload refuses a digest no work of the caller's
names, with a failed precondition.

### C17 — per-field last-writer-wins is not representable with one revision per row

**Where** RN02, the reconciliation described in 4.2.4, and the `update_mask` on every update
in the contract, which is documented as claiming a set of fields.

**What the TCC says** Reconciliation is per-field last-writer-wins: the vector clock decides
first, and a write that names two fields claims those two and leaves every other field to
whichever device wrote it last.

**Why it does not hold** Deciding *per field* requires knowing, per field, which write the
record currently reflects. What Appendix A gives a replicable entity — and what C02 completes
— is one `relogio_vetorial`, one `atualizado_em`, one `id_dispositivo` and one `removido`,
for the whole row. There is nowhere to record that the title came from one write and the
author from another.

The difference is not academic, and it loses a write nobody contested. A record with a title
and an author; device A, offline, writes the title at `10:00`; device B, offline, writes the
author at `09:59`. The two are concurrent. Per field, both survive: each wrote a field the
other did not touch. Per row, A's version wins the tie-break and B's operation is superseded
whole — the author it wrote is dropped, although no write ever contested it.

**Correction, in the text** State the granularity the model actually supports: the causal
decision is per *record*, and what is per field is the *write* — a delta names the fields it
changed, so the winner overwrites only those and the loser changes nothing. The mask is
therefore still load-bearing, and for the reason the contract already gives: a path the call
cannot write is refused rather than ignored, because an ignored path is a change nobody made.

**The alternative, and why it was not taken.** Genuine per-field reconciliation needs
per-field metadata — a jsonb of `{field: {relogio_vetorial, atualizado_em, id_dispositivo}}`
on each replicable entity — which multiplies the replication metadata by the number of
columns, has to be maintained by every write in the library and reading slices, and reopens
the attribute set C02 settled. The second alternative is to replay the log per record, which
is exact and needs no schema change, but requires the whole history of the record to be
present on the node deciding — which a node that was authorized as a replica after the record
was written does not have.

**Status** open. Implemented per record in `internal/sync/infra/service/records`, with the
delta applied field-wise. Amend 4.2.4 and the wording of the `update_mask` comments.

### C18 — a surrogate key minted per replica cannot identify a replicated record

**Where** Quadro 20 (`ebook_colecao`) and Quadro 21 (`progresso_leitura`), against the
operation log of Quadro 23, whose `id_entidade` names the record a change was made to.

**What the TCC says** Both associative entities carry their own `id` primary key, and an
operation names the record it changed by that identifier.

**Why it does not hold** Those two identifiers are minted by whichever replica first writes
the row, and both entities are written independently on several replicas: two devices that
file the same work under the same shelf while offline produce two rows with two identifiers
for one record, and so do two devices reporting a position in the same work. An operation
that named the record by such an identifier would be an operation no other node could
resolve — the receiving node holds the same record under a different name.

The e-book, the collection and the annotation do not have the problem, and the reason is
worth stating: each of them is *created once*, by one device, and the identifier that device
minted travels with the record for ever. An associative entity has no such moment.

**Correction** Say that `ebook_colecao` and `progresso_leitura` are identified across the
federation by their natural keys — `(id_ebook, id_colecao)` and `(id_ebook, id_dispositivo)`,
which are exactly the uniqueness constraints C06 and Quadro 21 already require — and that an
operation targeting one carries that key in `carga_delta`. The surrogate `id` stays as the
row's local handle, and `id_entidade` records the author's, which is provenance rather than
identity.

For `progresso_leitura` the device half is not carried at all: it is the device that authored
the operation, which C05 establishes is the only device that may write the row. Reading it
from anywhere else would let one appliance move another's bookmark.

**Status** open. Implemented in `internal/sync/infra/service/records`: those two are resolved
by pair, the other three by identifier. Add it to 4.2.4 beside C06.

### C19 — `carga_delta` has no specified shape

**Where** Quadro 23 (`operacao_sync.carga_delta`), RN06.

**What the TCC says** The column is `jsonb` and carries only the changed fields.

**Why that is not enough** Two nodes have to agree on how a value is written inside it, or
the same change is two changes. The field names could be the MER's, the schema's or the
contract's; a kind could be the enum constant the protobuf declares or the string the column
holds; an instant could be epoch milliseconds or RFC 3339. Nothing in the specification
chooses, and the choice is not private to one implementation — it is the wire format between
two nodes written by two people.

**Correction** State the rule the rest of the design already follows for `entidade_alvo`,
which 4.2.4 describes as named logically so that the same name travels in the contract, in
the node's schema and in the SQLite schema on the device. The delta follows it: the keys are
the field names those three share, and the values are the form the column holds — a kind is
`"note"`, an instant is RFC 3339, extra metadata is an object. One vocabulary, no translation
at any hop.

**Status** open. Implemented in `internal/sync/infra/service/records`. Add a paragraph to the
description of `operacao_sync` in 4.2.4.

### C20 — UC16 returns a session and does not say which device it is for

**Where** RF17 and UC16, and `MigrateHomeServerRequest`/`MigrateHomeServerResponse`.

**What the TCC implies** The migration carries the reader's devices and hands back a session
so that the reader can begin pushing immediately.

**Why that is not enough** A session belongs to exactly one device — the refresh credential
is revoked with it, and RN10 checks every operation against it — so a reply carrying one
session and a list of devices has to say which of them it is for. Nothing in the call does.
The list has an order, and the order is the only thing a client controls, so a client and a
server that disagree about whether it means anything will disagree about which device just
got a session; the device that thought it had one will push under an identifier its token
does not name, and RN10 will refuse every operation it sends.

**Correction** State the rule in the description of UC16: the first device in the list is the
one making the call, and the session comes back for it. The alternative is a field naming the
calling device, which is one more thing a caller can get wrong and buys nothing the order
does not — but either way it has to be written down, because it is not derivable.

**Status** open. Implemented as the first device in the list. Add the sentence to 4.2.2, and
to the comment on the `devices` field of the request.

### C21 — a change made through the API produces no operation, so it replicates to nobody

**Where** RF10, RF12, RN06, UC09 and UC10, against UC01 to UC05.

**What the TCC says** Changes are propagated between a reader's devices and between nodes as
`operacao_sync` rows, delivered after a cursor (RN06). It also gives the reader full CRUD over
their collection, their groupings, their annotations and their reading position, over the API.

**Why the two do not meet** Nothing writes an operation except the calls that receive one. A
device that is online and edits a work through `UpdateEbook` changes `library.ebooks` and
appends nothing to `sync.operations`, so the reader's other devices — which learn about
changes by pulling the log — never hear about it, and neither does any authorized replica.
The same change made offline and pushed later reaches all of them. The propagation of a
change therefore depends on whether the device that made it happened to be connected, which
is the opposite of what an offline-first design promises.

It is not visible from either side on its own. Every use case of UC01 to UC05 is correct
about the row it writes, and every use case of UC09 to UC11 is correct about the log it
reads; what is missing is that the first set does not feed the second, and the specification
never says it must because it never says where an operation comes from.

**Correction** State it in 4.2.4: an operation is written by every call that changes a
replicable record, and not only by the synchronization service. The shape is an outbox — the
operation is appended in the same transaction as the change, from the same revision the
entity just stamped, so that a change committed without its operation is impossible rather
than merely unlikely. The delta is the fields the call claimed, which the field mask already
names, and the target is the record it wrote.

**Why it is not implemented here.** It touches every write use case of the library and
reading slices and the port they would append through, which is a larger change than the
phase that found it. It is recorded rather than done, and what makes that tolerable rather
than a silent gap is that the client the specification describes writes to its own SQLite
first and pushes — the online path is the convenience, not the mechanism.

**Status** open. Found while implementing phase 9. Nothing in the code implements it yet.

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

**Status** open. Implemented in phase 10, as `internal/client` with `cmd/quirectl` over it.
Two things about it belong in 4.2.4 alongside the mention of the client itself.

It is a device and not a caller. It is bound to an origin server, it carries the identifier
its vector clock entries are keyed by, it stamps its own writes on a hybrid logical clock of
its own (C01), and it keeps all of that between two commands in one file — so two state files
on one machine are two devices, which is what makes UC10 demonstrable on a laptop. A change is
one method whichever path it takes: a client that cannot reach the node stamps the change and
appends it to its local log, and one that can calls the RPC, which is the contract's own
requirement that the two be indistinguishable once applied.

What it deliberately does not keep is a copy of the reader's collection. It remembers the
causal version it last saw of each record it touched — which is what a later change has to be
stamped on top of — and reads the collection from the node. A local replica would have to be
maintained by applying incoming operations to it, which is a second reconciler in the same
repository and therefore a second answer to what RN02 converges on; the device the thesis
describes has one because it has to render a library offline, and that is a property of the
application rather than of the protocol.

### D06 — discovery publishes an explicit gRPC authority

The MER gives `servidor` a single `url_base`, "o endpoint efetivo obtido pelo descobrimento".
In the Kubernetes deployment that is enough: one Istio gateway answers for the domain on 443
and separates gRPC from HTTP by ALPN, so both endpoints share an authority and one field
addresses both.

They are not the same everywhere. The `.well-known` documents are plain HTTP because RFC 8615
requires it, and the API is gRPC; in the two-node `docker compose` federation of phase 10, and
in any deployment without a mesh in front, those listen on different ports. A peer that
learned only `url_base` has nowhere to dial for replication.

The discovery documents therefore carry the gRPC authority explicitly, as `grpc`, alongside
the base URL. Subsection 4.2.4 has to record that `servidor` gains a column for it and that
`ServerDescriptor` carries it: without it the federation works only where a mesh happens to
collapse the two endpoints into one address, which is an accident of deployment and not a
property of the protocol.

**Status** open. Implemented in phase 6: `servidor` gains `grpc_authority` in
`000005_server_grpc_authority`, `ServerDescriptor` gains `grpc`, and the discovery client
reads it out of the document. The column is nullable and the value optional, because a node
whose document publishes none is a node this instance can record and cannot replicate to —
refusing it at discovery would turn a peer that is merely unreachable into one that cannot be
described. The port is required where the value is present: an authority without one would
silently mean 443, which is the port the HTTP listener answers on in the deployment where the
two do differ, and that is the deployment the column exists for.

### D08 — the object store is addressed through three vendor SDKs

`servidor` and the deployment diagram assume one object store. The implementation declares
one port, `service.BlobStore`, and three adapters behind it — Amazon S3 through
`aws-sdk-go-v2`, MinIO through `minio-go`, and Cloud Storage through `cloud.google.com/go/storage`
— and the node builds whichever one the configuration named. Subsection 4.2.4 has to record
that the store is a port rather than a product, and that the row in `library.ebook_contents`
carries the bucket alongside the key so that a node moved between providers can still read
what it stored under the old one.

Two of the three speak the same protocol. MinIO *is* the S3 API, and Cloud Storage has an
XML API that accepts SigV4 with HMAC keys, so a single hand-written client would have served
all three at no dependency cost. The decision was to use the SDK each provider publishes
instead, and the price is worth stating rather than discovering: the module graph goes from
12 direct and 13 indirect dependencies to 20 and 66. `cloud.google.com/go/storage` accounts
for most of it — it links 628 packages against the 233 of the AWS client and the 216 of the
MinIO one, because its client carries an OpenTelemetry and Cloud Monitoring chain this node
never uses.

The reason to pay it is that each adapter is the code its provider's own documentation
describes, which is what a deployment against that provider is supported running, and what a
reader of this project can check against an upstream reference rather than against a
signature implementation nobody else has reviewed.

**Status** open. Implemented in phase 7.

### D07 — the refresh credential is rotated, and reuse of a spent one ends the device's sessions

RF07 and RF08 say that the reader logs in and out; `token_acesso` holds a renewal credential
with `expira_em` and `consumido`, and the MER says nothing about what happens when one is
presented twice. The implementation makes two choices there, and both belong in 4.2.4.

*Rotation.* Refreshing consumes the credential presented and issues a replacement, rather than
returning a new access token against a credential that stays valid for its whole thirty days.
Without it, a credential copied from a device's storage is usable for as long as the original,
and nothing distinguishes the two holders.

*Reuse detection.* A credential that is presented after it has been consumed is, by
construction, a credential that two parties hold: the legitimate device already exchanged it
and holds the replacement. The node answers by revoking every credential of that device, which
is what the OAuth 2.0 Security Best Current Practice prescribes for rotated refresh tokens, and
it is what makes rotation worth doing — without it a thief simply refreshes alongside the
reader.

*The cost, stated rather than hidden.* A device whose reply was lost on a mobile network
retries with the credential it still has, and is logged out for it. That false positive is
real, and on an offline-first system over mobile networks it is not rare. The alternative
that avoids it — a grace window in which a spent credential returns the replacement it was
already exchanged for — requires the credential to record which credential replaced it, and
`token_acesso` has no such attribute. Adding one is the change 4.2.4 would need if the trade
is judged the wrong way round; the cost of the choice as made is one re-authentication, and
the cost of not making it is a stolen credential that works for thirty days.

**Status** open. Implemented in the refresh use case of phase 5.
