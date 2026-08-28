# Deployment and operations

How a Quire node is deployed to Kubernetes, what an operator has to supply, and what to do
when one of a handful of things goes wrong.

The reference deployment is [`deploy/k8s`](../deploy/k8s) with a local cluster in
[`deploy/kind`](../deploy/kind). It is not the only way to run a node — the compose
federation of [`deploy/docker`](../deploy/docker) is a whole federation on one machine, and
the README covers it — but it is the one the thesis describes and the one CI runs.

Two documents are load-bearing here and are referred to throughout: C12 and C23 in
[`tcc-corrections.md`](tcc-corrections.md). C12 is why a peer is identified by a published
public key rather than by a certificate chain; C23 is why the gateway may not terminate the
connection that key authenticates.

## What a node is made of

```
deploy/k8s/
├── base/                     the node: workload, service, identity, schema job, shared config
├── istio/                    a component: the node's own gateway, its two ports, its policies
├── certmanager/              a component: the two certificates, and the trust they imply
│   └── cluster/              applied once per cluster: the authority they come from
├── components/dependencies/  a component: PostgreSQL, an object store, a mail relay
└── overlays/
    ├── origin/               quire-a.example, in namespace quire-a
    └── replica/              quire-b.example, in namespace quire-b
```

An overlay says four things and nothing else: which namespace, which domain, what the node
needs beside it, and what signs its certificates. Everything about *how* a node is deployed
is in the base and the components and is identical for both, which is why there are two
overlays rather than two copies.

Two images are built from one Dockerfile, and `make images` builds both. `quired` is the
node on a distroless base — no shell, no package manager — and `quire-migrate` is the schema
carried by golang-migrate. They share a tag, so a deployment of one commit applies the
migrations of that commit.

## The two ports, and why they are two

This is the part of the deployment that is not obvious, and C23 is the entry that records
it.

| Port | Terminated at | Carries |
| --- | --- | --- |
| 80 | the gateway | a redirect to 443 |
| 443 | the gateway | `/.well-known/*`, routed by path |
| 9443 | **nothing** — passed through to the node | the federation and device gRPC |

The federation connection is mutually authenticated and pinned at both ends. The node
presents the certificate whose public key it published in its own discovery document, and a
peer compares the digest against that published value (C12). The node also reads the
*caller's* certificate and pins it the same way, which is the only thing that tells
`ReplicateOperations` which node is speaking.

A gateway that terminated that connection would present its own certificate — so the peer's
pin fails — and would have consumed the caller's — so the node sees a caller with no
certificate at all, which is what every device looks like. Neither is recoverable by
forwarding a header: a header is a claim made by whatever added it, and a certificate is a
proof the far end made. The whole reason the federation authenticates on certificates is that
two operators share no authority to appeal to.

The mesh's own mutual TLS is disabled on port 9090 for the same reason, with `portLevelMtls`
on the `PeerAuthentication`. What arrives there is already mutually authenticated, by two
parties the mesh has no identity for.

**The node's API port is named `tls-grpc` and not `grpc`, and that is load-bearing.** Istio
decides how to treat a port from the prefix of its name. What crosses 9090 is not gRPC — it is
TLS, terminated by the node itself — so a port named `grpc` has the sidecar reading a
ClientHello as an HTTP/2 frame, and the handshake dies inside the mesh with no useful error at
either end. The price is the rest of C23: the mesh can offer no per-method telemetry and no
per-method authorization for the API, because it cannot see inside a connection it must not
terminate. What it can still say is which workload talked to which, and for how long.

**Each node's gateway carries a label of its own**, patched by the overlay to
`istio: quire-gateway-<namespace>`. An Istio `Gateway` binds to every proxy in the mesh whose
labels match its selector, in *any* namespace, so two nodes sharing the value are two gateways
each serving whichever configuration istiod resolved last — which looks, from outside, like a
gateway answering for somebody else's domain.

`QUIRE_GRPC_ADVERTISED_ADDRESS` is what makes two ports workable at all: the node publishes
the authority peers dial rather than assuming one, and the configuration refuses to load
without it outside development.

Each node has a gateway of its own rather than sharing the mesh's. Two nodes in a federation
belong to two operators who share no authority, so a shared ingress models something this
project is not — and a node that does not own the thing answering for its domain cannot own
the certificate it presents either.

## The certificates

A node holds two, and the difference between them is the constraint C12 imposes.

**`quire-federation-tls`** is what the node itself presents, on the gRPC listener, and what it
dials a peer *with* — so it carries `client auth` as well as `server auth`. Its
`privateKey.rotationPolicy` is `Never`, and that line is load-bearing: the pin every peer
holds is the digest of this key's `SubjectPublicKeyInfo`, so a key rotated on renewal stops
every peer in the federation from talking to this node at once. Rotating it deliberately is
possible and has a published consequence — every peer has to re-read the discovery document,
which is what `RefreshKnownServer` is for.

**`quire-gateway-tls`** is what the gateway presents for the discovery documents. Nobody pins
it: a peer fetching `/.well-known/quire/server` verifies it the ordinary way, against a trust
store, because the pin covers the gRPC identity and not the document that publishes it. So it
rotates on every renewal, which is what a key nobody has written down should do.

The authority both come from is cluster-scoped, applied once from
`deploy/k8s/certmanager/cluster`, and is **the one thing a real deployment replaces rather
than copies**. In a real federation there is no shared authority: each node's certificate is
self-signed, exactly as `scripts/dev-certs.sh` generates them for the compose federation, and
the discovery documents sit behind publicly trusted certificates. What the local authority
buys is that two nodes on one machine can verify each other's documents at all.

That is also why the node carries `SSL_CERT_FILE`, added by the cert-manager component and
not by the base. cert-manager writes the issuing authority into `ca.crt` beside every
certificate it signs, so the bundle is already mounted with the federation certificate. The
variable *replaces* the system trust store rather than adding to it, which is right on one
machine — every endpoint this node verifies is signed by that authority — and wrong anywhere
else. A deployment that keeps the base and drops that directory gets the system store back.

## What an operator supplies

Nothing in `deploy/k8s` contains a secret. Five have to exist in the node's namespace before
it starts, and `scripts/kind-up.sh` generates throwaway ones for the local cluster.

| Secret | Keys |
| --- | --- |
| `quire-database` | `QUIRE_DATABASE_URL` |
| `quire-signing-key` | `QUIRE_AUTH_PRIVATE_KEY_PEM`, `QUIRE_AUTH_KEY_ID` |
| `quire-storage` | `QUIRE_STORAGE_MINIO_ENDPOINT` and its two credentials, or the S3 or GCS section |
| `quire-mail` | `QUIRE_MAIL_SMTP_HOST`, `QUIRE_MAIL_FROM_ADDRESS`, and the rest of the section |
| `quire-postgres` | `POSTGRES_PASSWORD` — only while the dependencies component is deployed |

The signing key is an ECDSA P-256 key in PKCS#8:

```bash
openssl ecparam -name prime256v1 -genkey -noout | openssl pkcs8 -topk8 -nocrypt
```

`QUIRE_AUTH_KEY_ID` is what the JWKS document keys it by, and is what makes rotation
possible: a token signed with the previous key stays verifiable while both are published.

The whole configuration surface of a node is one struct,
[`internal/shared/config`](../internal/shared/config). Nothing below `cmd/quired` reads a
variable, so reading that file is reading every knob there is.

## Bringing up the local cluster

```bash
echo "127.0.0.1 quire-a.example quire-b.example" | sudo tee -a /etc/hosts
make kind-up      # the cluster, istio, cert-manager, both nodes
make test-kind    # the end-to-end suite against them
make kind-down
```

`kind-up` is idempotent: running it again against a cluster that is already up is how a
change to the manifests is applied. Credentials are generated once and then left alone —
PostgreSQL reads `POSTGRES_PASSWORD` when it initializes an empty data directory and never
again, and MinIO does the same, so a second run that generated new ones would leave a stack
whose own secrets no longer open it. It generates every credential fresh on each run and
restarts the node afterwards, since a pod holding the previous password is a pod that has
stopped being able to connect.

The two `/etc/hosts` entries are the one thing it will not do itself — a script that needed a
password in order to bring up a test cluster is a script nobody should run — and the suite
needs them, because the gateway routes the federation port on SNI and presents a certificate
for the domain.

The nodes answer on `127.0.0.1:19443` and `127.0.0.1:29443` for gRPC, `18443` and `28443`
for the discovery documents, and their databases on `15433` and `25433`. None of those
collide with the compose federation, so both can run at once.

## What a real deployment changes

1. **Delete the dependencies component.** A PostgreSQL, an object store and a mail relay are
   what an operator already has — managed, backed up, and addressed by the secrets above.
   Removing `components/dependencies` from the overlay and pointing the secrets at real
   services is the whole of the difference.
2. **Replace the authority.** Drop `certmanager/cluster` and the `SSL_CERT_FILE` it implies.
   The document port needs a publicly trusted certificate, because a peer verifies it against
   an ordinary trust store; the federation certificate can stay self-signed, since what
   identifies it is the key it publishes.
3. **Point the mail section at a real relay.** The node refuses to start under
   `QUIRE_ENV=production` with no transport configured, and refuses to submit in the clear.
4. **Decide about replicas.** The base runs one. Nothing forbids a second — the schema is
   applied by a job rather than on startup for exactly that reason — but the replication
   worker ticks per process, so two replicas offer the same log to the same peer twice. The
   peer is idempotent about it and the traffic is not.

## Operations

### Applying a schema change

The migration job is immutable in the fields that matter, so re-applying after a schema
change fails until the finished one is deleted. That is deliberate: applying a schema change
should not happen because a manifest was re-applied.

```bash
kubectl -n quire-a delete job quire-migrate
kubectl apply -k deploy/k8s/overlays/origin
kubectl -n quire-a wait --for=condition=complete job/quire-migrate --timeout=5m
```

### Health, and which probe means what

`/healthz` is liveness and answers as long as the process is up. `/readyz` is readiness and
turns 503 when the database stops answering. The difference is on purpose: a database that is
down should take a node out of rotation, not have it restarted in a loop, because restarting
loses every connection it was holding and fixes nothing.

Neither is reachable from outside the mesh, and both are still answered: Istio rewrites a
pod's HTTP probes so that the kubelet reaches the agent and the agent reaches the container
locally, which never crosses the point the authorization policy is enforced at.

### Metrics

`/metrics` is Prometheus, inside the mesh only. It names every method the node serves, how
often each was called and how long it took, which is a description of who uses this node and
when — hence the policy.

The gRPC latency histogram puts **200 ms on a bucket boundary** so that RNF06 can be read off
it exactly rather than interpolated. `scripts/bench.sh` measures the same thing from outside,
with a real session, and fails when the 95th percentile is over budget:

```bash
make bench
```

On the compose federation, 2000 calls at concurrency 16, that reads 3.15 ms at the 95th
percentile for `PullOperations` and 4.53 ms for `PushOperations`.

### The gateway and its certificate

The gateway is handed its certificate over SDS, and istiod serves it only after asking the API
server whether the *gateway's service account* may read secrets in that namespace. The
`Role` beside the gateway is what answers yes. It cannot be narrowed to the one secret, and
that was tried: istiod asks about `list` with no resource name, so a Role restricted by
`resourceNames` answers no to exactly the question being asked.

What keeps that safe is the line above it: the gateway's service account has
`automountServiceAccountToken: false`. The proxy authenticates to the mesh with the projected
`istio-token` the injection adds and needs nothing from the API server itself, so the
permission exists for istiod's check and the pod carries no token to exercise it with. Without
that, a gateway sharing a namespace with the node could read the node's signing key.

A gateway with no certificate is a listener that resets every connection, and the symptom is a
handshake failure with nothing in the node's log. `istioctl proxy-config secret
deploy/quire-gateway -n <namespace>` says `WARMING` instead of `ACTIVE`, and istiod's log says
`attempted to access unauthorized certificates`.

### Rotating the signing key

Add the new key under a new `QUIRE_AUTH_KEY_ID` and restart. Tokens signed with the previous
key stay verifiable while it is still published at `/.well-known/jwks.json`, which is what the
key identifier in the token header is for. Do not confuse this with the federation
certificate's key, which is pinned and must not rotate.

## When something is wrong

**The node exits at startup naming a variable.** That is the configuration refusing to load,
and it reports every fault at once rather than the first. Under `QUIRE_ENV=production` it also
refuses plain HTTP discovery, a plain connection to the object store, submitting a recovery
credential in the clear, and a missing `QUIRE_GRPC_ADVERTISED_ADDRESS`.

**The node exits saying it has no way to deliver a password recovery.** No mail section is
configured, so the adapter that would write the credential to the log was selected — and it
refuses to be built in production, because logs are read by more people and kept in more
places than a mailbox is. Configure `QUIRE_MAIL_SMTP_HOST` and the rest of the section.

**A peer is refused with a pin mismatch.** Something rotated a key the federation had
recorded. Check `privateKey.rotationPolicy` on `quire-federation-tls`; if the rotation was
deliberate, every peer has to re-read the document through `RefreshKnownServer`. Note that
the catalogue is node-wide (C15), so a wrong pin is wrong for every reader on that node.

**Replication reaches a peer and it stores nothing.** A replica refuses everything until its
own database holds the origin, the reader, the authorization and every device that authored
anything — and no call in the contract can tell it any of that. That is C22, and it is a gap
in the specification rather than in this deployment.

**The migration job will not apply.** See above: delete it first.

**The migration job fails on one node and succeeds on the other.** golang-migrate does not wait
for anything: it connects, and a database that is still starting is a job that fails. The init
container beside it is what compose says with `depends_on: condition: service_healthy`, and
Kubernetes has no equivalent of.

**The relay crashes on a read-only root filesystem.** Mailpit keeps its messages in a file and
picks one under `/tmp` when none is configured. The dependencies component mounts a memory
`emptyDir` there, which keeps the root read-only and keeps a development recovery credential
off any disk.
