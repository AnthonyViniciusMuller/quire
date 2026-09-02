# What an operator supplies, and what is decided here.
#
# More has no default here than in the stack next door, and all of the
# difference is mail. AWS has SES; this cloud has no mail product at all, so the
# relay is the operator's and its four values are asked for rather than
# invented. The node refuses to start under QUIRE_ENV=production without them.

variable "project" {
  description = "The project everything here is created in."
  type        = string
}

variable "region" {
  description = "The region the cluster, the database and the bucket live in."
  type        = string
  default     = "us-central1"
}

variable "name" {
  description = "The prefix every resource here is named with."
  type        = string
  default     = "quire"
}

variable "domain" {
  description = <<-TEXT
    The domain this stack creates a managed zone for, e.g. "quire-gcp.example".
    It is delegated rather than bought: the stack outputs the nameservers and
    the registrar has to be pointed at them before anything answers.
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
  description = "The GKE release channel's version prefix. Empty follows the channel."
  type        = string
  default     = ""
}

variable "node_machine_type" {
  description = <<-TEXT
    What the node pool runs on. The Quire node asks for 100m of CPU and 128Mi;
    what sizes this is the mesh beside it — istiod, a sidecar per pod, and the
    gateway's own proxy.
  TEXT
  type        = string
  default     = "e2-standard-2"
}

variable "node_disk_size" {
  description = <<-TEXT
    Gigabytes of boot disk under each node. GKE's own default is 100, which is
    ten dollars a month of space nothing uses: the images this node pulls come
    to about a gigabyte. Thirty leaves room for image churn across redeploys and
    for the logs a node writes before they are shipped.

    It stays pd-balanced rather than dropping to pd-standard. A standard disk's
    throughput scales with its size, and thirty gigabytes of it would make every
    image pull slow enough to notice — which is a poor trade for the two dollars
    it saves.
  TEXT
  type        = number
  default     = 30
}

variable "node_pool_size" {
  description = <<-TEXT
    The initial, minimum and maximum size of the node pool, **per zone**. That
    word is the whole reason var.zonal exists: on a regional cluster these are
    multiplied by the number of zones, so a minimum of 1 is three nodes and
    reads like one.

    One node fits with little to spare. The Quire node asks for 100m of CPU and
    128Mi; what sizes this is the mesh beside it — istiod, a sidecar per pod and
    the gateway's own proxy come to roughly 1.2 of the 1.93 cores an
    e2-standard-2 makes allocatable.
  TEXT
  type        = object({ initial = number, min = number, max = number })
  default     = { initial = 1, min = 1, max = 2 }
}

variable "zonal" {
  description = <<-TEXT
    Whether the cluster lives in one zone rather than across the region.

    True, and it is the more consequential default in this file. A regional
    cluster replicates the node pool into every zone — so `node_pool_size` above
    is multiplied by three — and it is billed for its control plane, because
    GKE's monthly credit covers zonal and Autopilot clusters and not regional
    ones. Together that is roughly $180 a month for a deployment that runs one
    replica of one node.

    What it costs is real and is worth saying: no control-plane high
    availability, and every node in one zone. Neither is worth much here. The
    node is a single replica that cannot survive its own pod being rescheduled,
    so a second zone protects a workload that was never redundant to begin with;
    and a control plane that is briefly unavailable does not stop the node
    serving, because nothing in the request path talks to the API server.

    Set it false for a deployment that has grown past one replica, and raise
    the replica count in the same commit or the change buys nothing.
  TEXT
  type        = bool
  default     = true
}

variable "zone" {
  description = <<-TEXT
    Which zone, when var.zonal is true. Empty takes the region's first, which is
    what every region has and what nothing depends on.
  TEXT
  type        = string
  default     = ""
}

variable "subnet_cidr" {
  description = "The address space of this deployment's own subnet."
  type        = string
  default     = "10.70.0.0/20"
}

variable "pod_cidr" {
  description = "The secondary range pods are addressed from."
  type        = string
  default     = "10.71.0.0/16"
}

variable "service_cidr" {
  description = "The secondary range cluster services are addressed from."
  type        = string
  default     = "10.72.0.0/20"
}

# --- the managed dependencies ------------------------------------------------

variable "database_tier" {
  description = "The Cloud SQL tier. The node opens ten connections at most."
  type        = string
  default     = "db-g1-small"
}

variable "database_disk_size" {
  description = <<-TEXT
    Gigabytes for the database. It holds metadata, operations and vector clocks
    — never an e-book, which is what the bucket is for — so it grows with what
    readers do rather than with what they store.

    Ten is Cloud SQL's own minimum, and `disk_autoresize` is on, so an instance
    that outgrows it grows rather than fails. RDS cannot go below twenty on gp3,
    which is why the two stacks differ here and neither is wrong.
  TEXT
  type        = number
  default     = 10
}

variable "database_deletion_protection" {
  description = "Whether Cloud SQL refuses to be destroyed. Off, so a demonstration can be torn down."
  type        = bool
  default     = false
}

# --- mail --------------------------------------------------------------------

variable "mail" {
  description = <<-TEXT
    The relay the node submits password recoveries through. This cloud has no
    mail product, so there is nothing to provision and these are the operator's
    — SendGrid, Mailgun, Brevo, or a relay of their own.

    The security must be starttls or tls: the node refuses to submit a recovery
    credential in the clear under QUIRE_ENV=production, and the credential being
    submitted is the one that replaces a password.
  TEXT

  type = object({
    host         = string
    port         = optional(number, 587)
    username     = optional(string, "")
    password     = optional(string, "")
    security     = optional(string, "starttls")
    from_address = string
    from_name    = optional(string, "Quire")
  })

  sensitive = true

  validation {
    condition     = contains(["starttls", "tls"], var.mail.security)
    error_message = "The relay must be reached over starttls or tls; the node refuses to submit a recovery credential in the clear."
  }

  validation {
    condition     = (var.mail.username == "") == (var.mail.password == "")
    error_message = "Set both the username and the password, or neither — the node's configuration says the same."
  }
}

# --- the images --------------------------------------------------------------

variable "image_repository" {
  description = <<-TEXT
    Where the node and migrate images are pulled from, without a tag. Empty
    means the Artifact Registry repository this stack creates, which is the
    default because a private registry the cluster can already authenticate to
    is one fewer secret. Set it to "ghcr.io/anthonyvsmuller" to pull the
    published images instead.
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

variable "labels" {
  description = "Applied to everything this stack creates that takes labels."
  type        = map(string)
  default     = {}
}
