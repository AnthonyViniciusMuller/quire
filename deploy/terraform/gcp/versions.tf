# The providers this stack is built on, and the state it keeps.
#
# Nothing here refers to Amazon, and nothing in deploy/terraform/aws refers to
# Google. The two stacks share no module, no variable file, no state and no
# resource — they are two deployments of one system, not one deployment across
# two clouds, and a shared module would be a place for one cloud's outage to
# reach the other's plan. Where that costs duplication it is paid: several files
# here have a near-twin next door, and each is free to diverge the moment the
# cloud underneath it does.
terraform {
  required_version = ">= 1.6"

  required_providers {
    google     = { source = "hashicorp/google", version = "~> 6.0" }
    tls        = { source = "hashicorp/tls", version = "~> 4.0" }
    random     = { source = "hashicorp/random", version = "~> 3.6" }
    local      = { source = "hashicorp/local", version = "~> 2.5" }
    kubernetes = { source = "hashicorp/kubernetes", version = "~> 2.35" }
    helm       = { source = "hashicorp/helm", version = "~> 2.17" }
  }

  # State lives wherever the operator puts it, and this stack does not choose.
  # A GCS backend is the obvious one and is left commented rather than
  # configured, because a bucket named here is a bucket that has to exist before
  # the first plan and cannot be created by the stack that needs it.
  #
  #   backend "gcs" {
  #     bucket = "quire-terraform-state"
  #     prefix = "gcp"
  #   }
}

provider "google" {
  project = var.project
  region  = var.region

  default_labels = local.labels
}

data "google_client_config" "this" {}

# The cluster, addressed with the caller's own access token. It is short-lived
# and is never written to state, which is the same property the AWS stack gets
# from an exec plugin by a different route.
provider "kubernetes" {
  host                   = "https://${google_container_cluster.this.endpoint}"
  token                  = data.google_client_config.this.access_token
  cluster_ca_certificate = base64decode(google_container_cluster.this.master_auth[0].cluster_ca_certificate)
}

provider "helm" {
  kubernetes {
    host                   = "https://${google_container_cluster.this.endpoint}"
    token                  = data.google_client_config.this.access_token
    cluster_ca_certificate = base64decode(google_container_cluster.this.master_auth[0].cluster_ca_certificate)
  }
}
