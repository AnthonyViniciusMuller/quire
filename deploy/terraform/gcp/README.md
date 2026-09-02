# A Quire node, on GCP

One node, one project, and nothing shared with [`../aws`](../aws). The Atril web
client is **not** deployed here: a global HTTPS load balancer billed about $23 a
month to serve a static build that CloudFront carries for nothing, so
[`../aws`](../aws) serves the one copy — and a shipped build knows no node until
a reader types a domain, so that copy reaches this node too. What this stack builds and why it is shaped this way is
argued in the file comments; this is how to run it.

## What it creates

```
VPC (one subnet, two secondary ranges, no NAT — public nodes)
├── GKE                     the cluster, istio and cert-manager on it
│   └── quire               the namespace deploy/k8s/overlays/cloud is applied into
│       └── quire-gateway   a Service of type LoadBalancer → a passthrough NLB
│                           on a reserved address, ports 80, 443 and 9443
├── Cloud SQL PostgreSQL    private address, over the VPC peering, TLS enforced
├── Cloud Storage           the e-book files, opened by Workload Identity
├── Artifact Registry       quired and quire-migrate
└── Cloud DNS               the zone, delegated by hand
```

The nodes hold **external addresses** and there is no Cloud NAT. Inbound is
closed by the VPC's default-deny ingress exactly as it was; what changed is that
a node is addressable, and `network.tf` says so in full rather than leaving it to
be inferred.

There is no mail here, and that is not an omission. This cloud has no mail
product, so the relay is yours — SendGrid, Mailgun, Brevo, or your own — and its
four values are the `mail` variable. The node refuses to start under
`QUIRE_ENV=production` with no transport configured, because the adapter that
would write a recovery credential to the log declines to be built there.

## Before the first apply

You need `terraform`, `gcloud`, `gke-gcloud-auth-plugin`, `kubectl` and `docker`
on the path, and a project you are willing to let this create a network in.

**1. Create the zone and delegate it.**

```bash
cd deploy/terraform/gcp
cp example.tfvars terraform.tfvars   # then edit: project, domain, acme_email, mail
terraform init
terraform apply -target=google_dns_managed_zone.this
terraform output nameservers
```

Point the registrar at those names and wait for the delegation to be live —
`dig NS <domain> @1.1.1.1` answers with them when it is. Everything that follows
depends on it: the gateway's certificate is issued by answering a DNS-01
challenge in this zone.

**2. Push the images.**

```bash
terraform apply -target=google_artifact_registry_repository.this
terraform output image_push_commands   # then run them from the repository root
```

Both images carry one tag, because the schema is versioned with the binary that
expects it: a deployment of one commit applies the migrations of that commit. Set
`image_tag` in `terraform.tfvars` to whatever you pushed.

**3. Apply the rest.**

```bash
terraform apply
```

Twenty minutes or so, most of it the cluster. It ends by waiting for the gateway,
the migration job and the node in that order, so an apply that returns is a node
that is serving.

## Checking it

```bash
gcloud container clusters get-credentials quire --location us-central1-a --project <project>
kubectl -n quire get pods,certificate,svc

# The document a peer reads to learn this node's pinned key.
curl https://quire.<domain>/.well-known/quire/server

# And the one the browser client reads.
curl https://quire.<domain>/.well-known/quire/client
```

A `Certificate` stuck at `False` is almost always the delegation: `kubectl -n
quire describe certificate quire-gateway-tls` names the challenge, and
`kubectl -n quire get challenge -o wide` says what the solver saw.

Point Atril at it by opening the copy the AWS stack serves at its own
`app.<domain>` — or any copy, including `flutter run -d chrome` — and typing this
node's domain on the server-selection screen. A shipped build knows no node until
a reader names one, which is why one copy reaches both clouds.

## The cluster is zonal, and that is a decision

`var.zonal` defaults to true, so the cluster and its two nodes live in
`<region>-a`. Two things follow, and both are the reason:

- **A node pool's counts are per zone.** On a regional cluster `min_node_count = 2`
  is six nodes, not two — which reads like a typo in a bill rather than in a
  variable.
- **GKE's monthly credit covers zonal and Autopilot clusters only.** A regional
  control plane is billed in full.

Together that is roughly $170 a month — the control plane, plus four more nodes
— for a deployment that runs a single replica of a single Quire node. What it
costs is control-plane high availability and workers spread across zones;
neither protects anything here, because the Quire node is not redundant and
nothing in its request path talks to the API server.

Set `zonal = false` when the deployment has grown past one replica, and raise
the replica count in the same commit or the change buys nothing.

## One thing that will surprise you

**Let's Encrypt has rate limits and they are low.** Five failed authorizations an
hour, fifty certificates a week per domain. A first apply against a new domain
should set `acme_server` to the staging directory — `example.tfvars` has the
line — and switch to production once a certificate has been issued once.

**The first plan shows less than you expect.** The Kubernetes and Helm providers
are configured from the cluster this same stack creates, so before it exists
their configuration is not yet known and Terraform defers everything that
depends on them to the apply. That is the cost of one stack per cloud rather than
two, it is not a mistake, and it also decides the order of a tear-down — see the
last section.

## Deploying a new commit

```bash
make images IMAGE_REGISTRY=<region>-docker.pkg.dev/<project>/quire IMAGE_TAG=<tag>
docker push …                          # both images
terraform apply -var image_tag=<tag>
```

The migration job is deleted before the overlay is applied, because a Job is
immutable in the fields that matter and applying a changed one is otherwise
refused. That is deliberate in the manifest and deliberate here: this stack is
what deploys a new commit, so applying a schema change is something it is
entitled to do.

## Tearing it down

Two steps, and the reason is the note above: the Kubernetes and Helm
providers authenticate to a cluster this stack owns, so a single destroy that
removes the cluster early leaves them with nothing to talk to and stalls on
resources it can no longer read.

```bash
terraform destroy -target=kubernetes_namespace.quire -target=helm_release.istiod
terraform destroy
```

The contents bucket has versioning on and is not force-destroyed, so it survives
and has to be emptied by hand — which is the right way round for the one bucket
holding somebody's books. The Cloud SQL instance's name carries a random suffix
for a related reason: an instance name cannot be reused for a week, and a
tear-down that made the next apply wait seven days would be a tear-down nobody
does twice.
