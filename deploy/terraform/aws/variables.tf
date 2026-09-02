# What an operator supplies, and what is decided here.
#
# Two variables have no default and both are facts about the operator rather
# than about the deployment: the domain this node answers for, and the address
# Let's Encrypt writes to when a certificate is about to expire. Everything else
# has an answer that is right until a measurement says otherwise.

variable "region" {
  description = "The region everything except the CloudFront certificate lives in."
  type        = string
  default     = "us-east-1"
}

variable "name" {
  description = "The prefix every resource here is named with."
  type        = string
  default     = "quire"
}

variable "domain" {
  description = <<-TEXT
    The domain this stack creates a hosted zone for, e.g. "quire-aws.example".
    It is delegated rather than bought: the stack outputs the four nameservers
    and the registrar has to be pointed at them before anything answers.
  TEXT
  type        = string
}

variable "node_subdomain" {
  description = <<-TEXT
    The label the node answers on, below var.domain. It is what a peer types
    into RefreshKnownServer and what the discovery documents are fetched from,
    so it is a name a reader is expected to be able to write down.
  TEXT
  type        = string
  default     = "quire"
}

variable "app_subdomain" {
  description = "The label the Atril web client is served from, below var.domain."
  type        = string
  default     = "app"
}

variable "acme_email" {
  description = <<-TEXT
    Where Let's Encrypt writes about the gateway's certificate. It is the
    certificate the discovery documents are served behind, and a node whose
    documents stop verifying is a node no peer can discover — so the address
    should be one somebody reads.
  TEXT
  type        = string
}

variable "acme_server" {
  description = <<-TEXT
    The ACME directory. The staging one issues untrusted certificates and has
    rate limits an operator cannot exhaust, which is what a first apply against
    a new domain should use.
  TEXT
  type        = string
  default     = "https://acme-v02.api.letsencrypt.org/directory"
}

# --- the cluster -------------------------------------------------------------

variable "kubernetes_version" {
  description = "The EKS control plane version."
  type        = string
  default     = "1.34"
}

variable "node_instance_types" {
  description = <<-TEXT
    What the managed node group runs on. The node itself asks for 100m of CPU
    and 128Mi; what sizes this is the mesh beside it — istiod, a sidecar per
    pod, and the gateway's own proxy.
  TEXT
  type        = list(string)
  default     = ["t3.large"]
}

variable "node_disk_size" {
  description = <<-TEXT
    Gigabytes of EBS under each node. Twenty is the managed node group's own
    default and is close to the floor already: the AMI is about three gigabytes
    and the images on top of it — the proxy, the node, the schema, the CNI —
    come to roughly one more. Ten would work and would save eighty cents a
    month, which is not worth the first image pull that fails for want of room.
  TEXT
  type        = number
  default     = 20
}

variable "node_group_size" {
  description = <<-TEXT
    The desired, minimum and maximum size of the managed node group. These are
    counts for the group as a whole, not per zone — the group spans all three
    private subnets and places wherever it can.

    One node, and it fits with little to spare. The Quire node asks for 100m of
    CPU and 128Mi; what sizes this is the mesh beside it — istiod, a sidecar per
    pod and the gateway's own proxy come to roughly 1.2 of the 1.93 cores a
    t3.large makes allocatable. The ceiling is 3 so that a rolling replacement
    has somewhere to put the new node before it drains the old one.
  TEXT
  type        = object({ desired = number, min = number, max = number })
  default     = { desired = 1, min = 1, max = 3 }
}

variable "availability_zones" {
  description = <<-TEXT
    How many zones the VPC spans. Three is what EKS asks for and what a load
    balancer wants; two works and one does not.
  TEXT
  type        = number
  default     = 3
}

variable "vpc_cidr" {
  description = "The address space of this deployment's own VPC."
  type        = string
  default     = "10.60.0.0/16"
}

# --- the managed dependencies ------------------------------------------------

variable "database_instance_class" {
  description = "The RDS instance class. The node opens ten connections at most."
  type        = string
  default     = "db.t4g.micro"
}

variable "database_allocated_storage" {
  description = <<-TEXT
    Gigabytes for the database. It holds metadata, operations and vector clocks
    — never an e-book, which is what the bucket is for — so it grows with what
    readers do rather than with what they store.
  TEXT
  type        = number
  default     = 20
}

variable "database_deletion_protection" {
  description = "Whether RDS refuses to be destroyed. Off, so a demonstration can be torn down."
  type        = bool
  default     = false
}

# --- the images --------------------------------------------------------------

variable "image_repository" {
  description = <<-TEXT
    Where the node and migrate images are pulled from, without a tag. Empty
    means the ECR repositories this stack creates, which is the default because
    a private registry the cluster can already authenticate to is one fewer
    secret. Set it to "ghcr.io/anthonyvsmuller" to pull the published images
    instead.
  TEXT
  type        = string
  default     = ""
}

variable "image_tag" {
  description = <<-TEXT
    The tag both images carry. They move together because the schema is
    versioned with the binary that expects it — `make image-name` in the
    repository root prints what `make images` just built.
  TEXT
  type        = string
  default     = "dev"
}

# --- the platform ------------------------------------------------------------

variable "web_build_dir" {
  description = <<-TEXT
    The Atril web build to upload, e.g. "../../../../atril/build/web" after
    `make build-web` in that checkout. Empty provisions the bucket, the
    distribution and the certificate and uploads nothing, which is what a first
    apply should do: the infrastructure is this stack's, the artifact is a
    build's.

    This is the only stack that serves the web client. The GCP one deploys the
    node alone — CloudFront's always-free tier carries the build for nothing,
    where a Google global HTTPS load balancer bills $23 a month to do the same
    job, and one copy reaches both nodes because a shipped build knows none of
    them until a reader types a domain.
  TEXT
  type        = string
  default     = ""
}

variable "istio_version" {
  description = <<-TEXT
    The mesh, pinned. deploy/k8s/istio/envoyfilter.yaml is coupled to the shape
    of a gateway's HTTP filter chain, so this is a version this repository
    chooses rather than whatever is newest.
  TEXT
  type        = string
  default     = "1.30.3"
}

variable "cert_manager_version" {
  description = "cert-manager, pinned to what scripts/kind-up.sh installs locally."
  type        = string
  default     = "v1.19.2"
}

variable "tags" {
  description = "Applied to everything this stack creates."
  type        = map(string)
  default     = {}
}
