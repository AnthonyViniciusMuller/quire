# Two clouds, and nothing between them

Two Terraform stacks, each deploying a Quire node into one cloud and sharing
nothing with the other. The Atril web client is deployed once, by the AWS stack,
because one copy reaches both nodes and CloudFront carries it for nothing.

```
deploy/terraform/
├── aws/    a node and the web client, on AWS
└── gcp/    a node and the web client, on GCP
```

They are not two halves of a deployment. They are two deployments, and the
separation is literal: no shared module, no shared variable file, no shared
state, no resource in one that names anything in the other, and no data flow
between them at run time. Either can be destroyed without the other noticing,
and either can be applied by somebody with credentials for one cloud and none
for the other.

The price is duplication, and it is paid rather than avoided. `frontend.tf`
exists twice, `network.tf` exists twice, and the two are structurally similar
enough that a module would be an obvious refactor. It is deliberately not made:
a module shared by two clouds is a place where a change made for one is applied
to the other, and where one cloud's outage reaches the other's plan. What the two
stacks share instead is the thing that ought to be shared — `deploy/k8s`, the
node itself, which is identical in both.

## What each stack deploys

| | The node — both stacks | The web client — AWS only |
| --- | --- | --- |
| Runs on | one node of a managed Kubernetes cluster, from `deploy/k8s/overlays/cloud` | nothing; it is a build behind a CDN |
| Reached at | `quire.<domain>` | `app.<domain>` |
| Database | RDS PostgreSQL / Cloud SQL | — |
| Objects | S3 / Cloud Storage | — |
| Mail | SES / a relay you name | — |
| Certificates | Let's Encrypt over DNS-01, and one self-signed | ACM |

**The web client is deployed once, not twice.** A shipped Atril build knows no
node until a reader types a domain into it, so one copy reaches both — and a
second would be a second thing to rebuild on every release for no reader's
benefit. It lives in the AWS stack, because CloudFront's always-free tier carries
it for nothing where a Google global HTTPS load balancer billed $23 a month; the
GCP stack deploys the node alone.

**Neither stack runs a NAT gateway.** The nodes hold addresses of their own and
inbound is closed by a security group and a firewall rather than by the absence
of a route. It saved $66 a month across the two, it is a real reduction in
defence in depth, and both `network.tf` files state exactly what changed and how
to put it back.

One node per cloud, not two. The compose federation and the kind cluster each run
two nodes because a federation of one demonstrates nothing; here the second node
is the other cloud, or anybody else's deployment, and a reader tells one node
about another by typing its domain. Nothing in either stack knows the other
exists, which is the same property a real federation has.

## Why Kubernetes, in both

Because of C23 in [`../../docs/tcc-corrections.md`](../../docs/tcc-corrections.md),
and not because of a preference for it.

The federation port carries a mutually authenticated connection pinned at both
ends: the node presents the certificate whose public key it published in its own
discovery document, and it reads the caller's certificate to know which node is
speaking. Anything that terminated that connection would break both halves — it
would present its own certificate to a peer that pinned this node's, and it would
have consumed the caller's. Neither is recoverable by forwarding a header,
because a header is a claim and a certificate is a proof.

So whatever fronts that port has to forward TCP and read nothing. That rules out
every managed HTTP front either cloud offers — an ALB, Cloud Run, App Runner, an
HTTPS load balancer — and leaves an L4 load balancer, which is exactly what a
Kubernetes `Service` of type `LoadBalancer` produces on both. And once there is a
cluster, `deploy/k8s` *is* the deployment: the manifests the thesis describes and
CI runs, with a thin generated layer on top saying which domain, which image, and
how this cloud publishes a gateway.

The two stacks reach the same arrangement by different mechanisms, and the
difference is worth naming rather than smoothing over:

- **The gateway's address.** GCP reserves it before the Service exists, so DNS is
  an `A` record written in the same apply and the address survives a redeploy.
  AWS is handed a name by Kubernetes after the fact, so DNS is a `CNAME` to it.
- **The object store credential.** GCP has none: the node's GCS adapter honours
  application default credentials, so a Workload Identity binding is the whole of
  it. AWS has a key pair in a Secret, because the S3 adapter carries no credential
  chain — its package comment says why.
- **Mail.** AWS provisions SES, including the DKIM records. GCP has no mail
  product at all, so the relay is an operator's and its four values are variables.
- **How one node is asked for.** An EKS node group's counts are for the group;
  a GKE node pool's are *per zone*, so the GCP cluster is zonal by default —
  which also puts its control plane inside GKE's monthly credit. `var.zonal`
  argues it.

## The order everything happens in

Both stacks want the same three things done before the whole of them can apply,
and both READMEs say it in their own terms:

1. **Create the DNS zone and delegate it.** `terraform apply -target` on the zone
   alone, read the nameservers, point the registrar at them. Certificates are
   issued by answering a challenge in that zone, and a zone nobody delegated is a
   record nobody reads.
2. **Push the images.** `make images` in the repository root, then push to the
   registry the stack created. Nothing here builds a container: an apply that
   shelled out to Docker would be an apply whose result depended on a local build
   cache.
3. **Apply the rest.** The stack creates the cluster, installs the mesh and
   cert-manager, writes the node's secrets, and applies `deploy/k8s` through a
   generated layer.

The web client is a fourth step and an optional one: `make build-web` in the
Atril checkout, then `web_build_dir` pointing at what it produced.

## What is deliberately not here

- **No CI.** Neither stack is applied by a workflow. What deploys a commit is a
  person running `terraform apply` with `image_tag` set to what `make image-name`
  printed.
- **No state backend.** Both `versions.tf` files carry a commented one. A bucket
  named in a stack is a bucket that has to exist before that stack's first plan.
- **No secrets in either directory.** The signing key is generated by the apply
  and lives in state, which is why the state belongs in an encrypted backend and
  why a copy is written to Secrets Manager / Secret Manager.
