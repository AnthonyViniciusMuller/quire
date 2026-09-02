# The providers this stack is built on, and the state it keeps.
#
# Nothing here refers to Google, and nothing in deploy/terraform/gcp refers to
# Amazon. The two stacks share no module, no variable file, no state and no
# resource — they are two deployments of one system, not one deployment across
# two clouds, and a shared module would be a place for one cloud's outage to
# reach the other's plan. Where that costs duplication it is paid: several
# files here have a near-twin next door, and each is free to diverge the moment
# the cloud underneath it does.
terraform {
  required_version = ">= 1.6"

  required_providers {
    aws        = { source = "hashicorp/aws", version = "~> 6.0" }
    tls        = { source = "hashicorp/tls", version = "~> 4.0" }
    random     = { source = "hashicorp/random", version = "~> 3.6" }
    local      = { source = "hashicorp/local", version = "~> 2.5" }
    kubernetes = { source = "hashicorp/kubernetes", version = "~> 2.35" }
    helm       = { source = "hashicorp/helm", version = "~> 2.17" }
  }

  # State lives wherever the operator puts it, and this stack does not choose.
  # An S3 backend is the obvious one and is left commented rather than
  # configured, because a bucket named here is a bucket that has to exist before
  # the first plan and cannot be created by the stack that needs it.
  #
  #   backend "s3" {
  #     bucket       = "quire-terraform-state"
  #     key          = "aws/terraform.tfstate"
  #     region       = "us-east-1"
  #     encrypt      = true
  #     use_lockfile = true
  #   }
}

provider "aws" {
  region = var.region

  default_tags {
    tags = local.tags
  }
}

# The cluster, addressed the way kubectl would address it: a short-lived token
# minted by the AWS CLI rather than a credential written into state. The exec
# plugin is what `aws eks update-kubeconfig` writes, and it is used here for the
# same reason — a token in state is a token in every backup of it.
# CloudFront reads its certificate from us-east-1 and from nowhere else, which
# is the one thing in this stack that is not in var.region. It is an alias
# rather than a second stack because it holds exactly one resource: the
# certificate for the web client's domain.
provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"

  default_tags {
    tags = local.tags
  }
}

provider "kubernetes" {
  host                   = aws_eks_cluster.this.endpoint
  cluster_ca_certificate = base64decode(aws_eks_cluster.this.certificate_authority[0].data)

  exec {
    api_version = "client.authentication.k8s.io/v1beta1"
    command     = "aws"
    args        = ["--region", var.region, "eks", "get-token", "--cluster-name", aws_eks_cluster.this.name, "--output", "json"]
  }
}

provider "helm" {
  kubernetes {
    host                   = aws_eks_cluster.this.endpoint
    cluster_ca_certificate = base64decode(aws_eks_cluster.this.certificate_authority[0].data)

    exec {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "aws"
      args        = ["--region", var.region, "eks", "get-token", "--cluster-name", aws_eks_cluster.this.name, "--output", "json"]
    }
  }
}
