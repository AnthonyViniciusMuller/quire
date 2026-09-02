# A Quire node and the Atril web client, on AWS

One node, one account, and nothing shared with [`../gcp`](../gcp). This stack
serves **the only copy of the web client** — CloudFront's always-free tier
carries it for nothing, where the equivalent on GCP billed $23 a month — and a
shipped build knows no node until a reader types a domain, so one copy reaches
both nodes. What this stack builds and why it is shaped this way is
argued in the file comments; this is how to run it.

## What it creates

```
VPC (2 zones, public + private, no NAT)
├── EKS                     the cluster, istio and cert-manager on it
│   └── quire               the namespace deploy/k8s/overlays/cloud is applied into
│       └── quire-gateway   a Service of type LoadBalancer → an NLB on 80, 443, 9443
├── RDS PostgreSQL          private, encrypted, TLS enforced
├── S3  quire-contents-…    the e-book files, and a key pair scoped to them
├── SES                     the domain identity, its DKIM records, an SMTP user
├── ECR                     quired and quire-migrate
├── Route53                 the zone, delegated by hand
└── S3 + CloudFront + ACM   the Atril build, at app.<domain>
```

The nodes sit in the **public** subnets and there is no NAT gateway. Inbound is
closed by the cluster's security group exactly as it was; what changed is that a
node is addressable, and `network.tf` says so in full rather than leaving it to
be inferred.

## Before the first apply

You need `terraform`, `aws`, `kubectl` and `docker` on the path, and credentials
for an account you are willing to let this create a VPC in.

**1. Create the zone and delegate it.**

```bash
cd deploy/terraform/aws
cp example.tfvars terraform.tfvars   # then edit: domain, acme_email
terraform init
terraform apply -target=aws_route53_zone.this
terraform output nameservers
```

Point the registrar at those four names and wait for the delegation to be live —
`dig NS <domain> @1.1.1.1` answers with them when it is. Everything that follows
depends on it: the gateway's certificate is issued by answering a DNS-01
challenge in this zone, the web client's certificate is validated by a record in
it, and SES verifies the domain by reading one.

**2. Push the images.**

```bash
terraform apply -target=aws_ecr_repository.quired -target=aws_ecr_repository.migrate
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

**4. Build and upload the web client.**

```bash
cd ../../../../atril && make build-web
```

Then set `web_build_dir = "../../../../atril/build/web"` in `terraform.tfvars`
and apply again. The files are uploaded, the distribution is invalidated, and
`app.<domain>` serves them — to readers of both nodes, since the build knows
neither until somebody types a domain.

## Checking it

```bash
aws eks update-kubeconfig --region us-east-1 --name quire
kubectl -n quire get pods,certificate,svc

# The document a peer reads to learn this node's pinned key.
curl https://quire.<domain>/.well-known/quire/server

# And the one the browser client reads.
curl https://quire.<domain>/.well-known/quire/client
```

A `Certificate` stuck at `False` is almost always the delegation: `kubectl -n
quire describe certificate quire-gateway-tls` names the challenge, and
`kubectl -n quire get challenge -o wide` says what the solver saw.

Point Atril at it by opening `https://app.<domain>` and typing a node's domain on
the server-selection screen — this one, the GCP one, or anybody else's. A shipped
build knows no node until a reader names one, which is why one copy reaches both
clouds and why the GCP stack does not deploy a second.

## Three things that will surprise you

**SES starts in the sandbox.** A new account may only send to addresses it has
verified, so a password recovery to a stranger is accepted by the node and
dropped by SES. Ask for production access in the SES console; nothing in this
stack can do it, and nothing in it is wrong while it is pending.

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
make images IMAGE_REGISTRY=<account>.dkr.ecr.<region>.amazonaws.com IMAGE_TAG=<tag>
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
holding somebody's books. Everything else goes.
