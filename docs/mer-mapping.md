# MER to schema mapping

The MER in subsection 4.2.4 of the TCC names its entities and attributes in Portuguese, in a
single flat namespace. The schema uses English and qualifies every entity with the slice that
owns it. This table is the bridge: it lets a reader holding the thesis find any attribute in
the code, and a reader holding the code find the Quadro in Appendix A that specifies it.

Where the schema does more than translate, the row says so and cites the entry in
[`tcc-corrections.md`](tcc-corrections.md) that explains why.

## Naming rules

Most rows follow from four rules, and reading them first makes the tables below almost
predictable.

| MER | Schema | |
|---|---|---|
| `id_<entidade>` | `id` | the entity's own key |
| `id_<outra>` | `<other>_id` | a foreign key |
| `<verbo>_em` | `<verb>ed_at` | `criado_em` → `created_at`, `expira_em` → `expires_at` |
| `tipo` | `kind` | `type` is a Go keyword in all but name, and reads as the language's own |

`relogio_vetorial` is `vector_clock`, `removido` is `deleted`, and `ativo`/`ativa` is
`active`, everywhere they appear.

Every temporal attribute is `timestamptz` rather than the `timestamp` Appendix A declares
(C04), so the type column below repeats it only where something else also changed.

## Entities

| MER entity | Schema | Quadro |
|---|---|---|
| `servidor` | `federation.servers` | 13 |
| `usuario` | `identity.users` | 14 |
| `replica_usuario` | `federation.user_replicas` | 15 |
| `token_acesso` → `credencial` | `identity.credentials` | 16 — renamed, C09 |
| `dispositivo` | `identity.devices` | 17 |
| `ebook` | `library.ebooks` | 18 |
| `colecao` | `library.collections` | 19 |
| `ebook_colecao` | `library.ebook_collections` | 20 |
| `progresso_leitura` | `reading.progress` | 21 |
| `anotacao` | `reading.annotations` | 22 |
| `operacao_sync` | `sync.operations` | 23 |
| `entrega_sync` | `sync.deliveries` | — (C07) |
| — | `sync.streams` | — (C08) |
| — | `library.ebook_contents` | — (D02) |

## `servidor` → `federation.servers`

| MER | Schema | Note |
|---|---|---|
| `id_servidor` | `id` | |
| `dominio` | `domain` | |
| `url_base` | `base_url` | |
| `uri_jwks` | `jwks_uri` | |
| `impressao_cert` | `certificate_fingerprint` | |
| `local` | `is_local` | renamed: the MER name is an adjective with no subject, and the column asserts something |
| `descoberto_em` | `discovered_at` | |
| `ativo` | `active` | |

Added: a partial unique index on `is_local`, so at most one row can claim to be this
instance. Two would make "is this user local" unanswerable, and that question decides whether
the node authenticates a user or merely replicates them.

## `usuario` → `identity.users`

| MER | Schema | Note |
|---|---|---|
| `id_usuario` | `id` | |
| `id_servidor_origem` | `origin_server_id` | |
| `usuario_local` | `local_name` | |
| `nome` | `display_name` | renamed: `local_name` is the other half of the identifier, and two columns called "name" would be read as the same kind of thing |
| `email` | `email` | **nullable** — C03 |
| `senha_hash` | `password_hash` | **nullable** — C03 |
| `criado_em` | `created_at` | |
| `atualizado_em` | `updated_at` | |

RN09 is two unique indexes: `(origin_server_id, local_name)` for the federated identifier, and
`(origin_server_id, lower(email))` for the address, which is unique only within the origin
server. Case is folded so the same address cannot be registered twice under different
capitalization.

## `replica_usuario` → `federation.user_replicas`

| MER | Schema | Note |
|---|---|---|
| `id_replica` | `id` | |
| `id_usuario` | `user_id` | |
| `id_servidor` | `server_id` | |
| `autorizada_em` | `authorized_at` | |
| `replica_arquivos` | `replicates_files` | |
| `ativa` | `active` | |

Added: unique `(user_id, server_id)`. One row per pair, reused as the decision changes, so
that a grant and its revocation stay in one place.

## `token_acesso` → `identity.credentials`

| MER | Schema | Note |
|---|---|---|
| `id_token` | `id` | |
| `id_usuario` | `user_id` | |
| `id_dispositivo` | `device_id` | null for a recovery token; recovery happens when the reader has lost access, possibly from a device not yet bound |
| `tipo` | `kind` | `session_refresh` or `password_recovery` |
| `token_hash` | `token_hash` | |
| `expira_em` | `expires_at` | |
| `consumido` | `consumed` | covers both "already used" and "revoked" |

The entity is renamed. `token_acesso` never stores an access token — that is a JWT, verified
by signature and never persisted (RNF11) — and the MER's own description says so. C09 carries
the rename into 4.2.4, Quadro 16 and Figura 18; the schema follows it in migration 000004.

## `dispositivo` → `identity.devices`

| MER | Schema | Note |
|---|---|---|
| `id_dispositivo` | `id` | also the key a vector clock entry is keyed by |
| `id_usuario` | `user_id` | |
| `nome` | `name` | |
| `plataforma` | `platform` | |
| `ultimo_sync_em` | `last_synced_at` | a diagnostic, not the synchronization cursor — C08 |
| `ativo` | `active` | unbinding clears this; the row is never deleted, or the clocks that name it become unreadable |

## `ebook` → `library.ebooks`

| MER | Schema | Note |
|---|---|---|
| `id_ebook` | `id` | |
| `id_usuario` | `user_id` | |
| `titulo` | `title` | |
| `autor` | `author` | |
| `editora` | `publisher` | |
| `idioma` | `language` | |
| `formato` | `format` | |
| `hash_conteudo` | `content_hash` | not a foreign key to `ebook_contents` — see D02 below |
| `tamanho_bytes` | `size_bytes` | |
| `metadados_extra` | `extra_metadata` | |
| `importado_em` | `imported_at` | |
| `removido` | `deleted` | |
| — | `vector_clock` | added — C02 |
| — | `updated_at` | added — C02 |
| — | `device_id` | added — C02 |

## `colecao` → `library.collections`

| MER | Schema | Note |
|---|---|---|
| `id_colecao` | `id` | |
| `id_usuario` | `user_id` | |
| `nome` | `name` | |
| `tipo` | `kind` | `collection` or `category` |
| `descricao` | `description` | |
| `criado_em` | `created_at` | |
| — | `vector_clock`, `updated_at`, `device_id`, `deleted` | added — C02 |

## `ebook_colecao` → `library.ebook_collections`

| MER | Schema | Note |
|---|---|---|
| `id_ebook_colecao` | `id` | |
| `id_ebook` | `ebook_id` | |
| `id_colecao` | `collection_id` | |
| — | `vector_clock`, `updated_at`, `device_id`, `deleted` | added — C02 |

Added: unique `(ebook_id, collection_id)` — C06. Membership is a last-writer-wins register per
pair rather than a plain row, so two devices that add and remove the same book from the same
shelf while offline reach the same answer.

## `progresso_leitura` → `reading.progress`

| MER | Schema | Note |
|---|---|---|
| `id_progresso` | `id` | |
| `id_ebook` | `ebook_id` | |
| `id_dispositivo` | `device_id` | |
| `localizador` | `locator` | |
| `percentual` | `percent` | `numeric(5,2)`, constrained to 0–100 |
| `relogio_vetorial` | `vector_clock` | a version counter here, not a conflict resolver — C05 |
| `atualizado_em` | `updated_at` | |

Added: unique `(ebook_id, device_id)` — C05. Without it the rows accumulate instead of being
updated, and "where this device stopped in this book" stops having one answer.

## `anotacao` → `reading.annotations`

| MER | Schema | Note |
|---|---|---|
| `id_anotacao` | `id` | |
| `id_ebook` | `ebook_id` | |
| `id_dispositivo` | `device_id` | the device whose write the row reflects, not the one that created it — C10. Nullable, unlike on `progress`: an annotation outlives the purging of a device, while a progress row is meaningless without one |
| `tipo` | `kind` | `note`, `highlight` or `bookmark` |
| `texto` | `text` | |
| `localizador` | `locator` | |
| `relogio_vetorial` | `vector_clock` | |
| `atualizado_em` | `updated_at` | |
| `removido` | `deleted` | |

## `operacao_sync` → `sync.operations`

| MER | Schema | Note |
|---|---|---|
| `id_operacao` | `id` | minted by the authoring device and identical on every node, which is what makes receiving idempotent |
| `id_dispositivo` | `device_id` | |
| `id_servidor` | → `sync.deliveries.server_id` | moved — C07 |
| `entidade_alvo` | `target_entity` | `ebook`, `collection`, `ebook_collection`, `reading_progress` or `annotation` |
| `id_entidade` | `target_id` | |
| `tipo_operacao` | `operation` | `insert`, `update` or `delete` |
| `carga_delta` | `delta` | |
| `relogio_vetorial` | `vector_clock` | |
| `criado_em` | `created_at` | |
| `aplicado_em` | → `sync.deliveries.applied_at` | moved — C07 |
| — | `user_id` | added — C08. Reachable through the device, and stored anyway: the position is scoped per user, so the uniqueness constraint needs it |
| — | `position` | added — C08. This node's order for this user's log, and node-local: two nodes number the same operations differently |

## `entrega_sync` → `sync.deliveries`

The entity C07 splits out of `operacao_sync`, so that a change destined for three replicas is
three rows here and one row there — the delta stored once, and the operation keeping one
identity across the federation.

| Attribute | Type | Note |
|---|---|---|
| `id` | `uuid` | |
| `operation_id` | `uuid` | |
| `server_id` | `uuid` | the destination node |
| `applied_at` | `timestamptz` | null until the destination confirms; this is what makes the table a queue |
| `attempts` | `integer` | |
| `last_attempt_at` | `timestamptz` | |
| `last_error` | `text` | for the operator, never for a client |

Unique `(operation_id, server_id)`.

## `sync.streams`

The position allocator C08 introduces. One row per user, holding the last position handed out
for that user's log on this node.

| Attribute | Type |
|---|---|
| `user_id` | `uuid` |
| `last_position` | `bigint` |

## `library.ebook_contents`

The file storage D02 adds. The MER models only the metadata of a file — `hash_conteudo` and
`tamanho_bytes` on `ebook` — and this table records the objects the node actually holds,
keyed by the digest so that the same file imported by two readers, or by one reader on two
devices, converges on one object.

| Attribute | Type | Note |
|---|---|---|
| `content_hash` | `varchar(64)` | primary key |
| `size_bytes` | `bigint` | |
| `media_type` | `varchar(100)` | |
| `storage_bucket` | `varchar(255)` | recorded alongside the key so a node can be repointed without rewriting rows |
| `storage_key` | `varchar(512)` | |
| `created_at` | `timestamptz` | |

`library.ebooks.content_hash` deliberately does *not* reference this table. A node replicating
a user with `replicates_files` false holds every e-book row and none of the files, and a
foreign key would make the metadata unreplicable there. A row here means one thing: this node
has the bytes.
