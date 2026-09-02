# The names and the two addresses everything else is derived from.

locals {
  tags = merge({
    "app.kubernetes.io/part-of" = "quire"
    "quire:stack"               = "aws"
    "quire:managed-by"          = "terraform"
  }, var.tags)

  # The node's domain, and the web client's. They are two names and not two
  # paths of one, because they are two origins: the client is a static bundle a
  # CDN serves and the node is a gateway that must not be a CDN — 9443 is passed
  # through untouched, and there is no CloudFront distribution in the world that
  # will do that.
  #
  # Both are here and neither is in the GCP stack. That stack deploys the node
  # alone: CloudFront's always-free tier carries this build for nothing, and a
  # Google global HTTPS load balancer billed $23 a month to do the same job.
  node_domain = "${var.node_subdomain}.${var.domain}"
  app_domain  = "${var.app_subdomain}.${var.domain}"

  # Where the images are pulled from. The ECR repositories this stack creates
  # unless the operator named a registry, in which case they are still created
  # and simply not used — an empty repository costs nothing and is there for the
  # apply that changes its mind.
  image_repository = var.image_repository != "" ? var.image_repository : split("/", aws_ecr_repository.quired.repository_url)[0]

  node_image    = "${local.image_repository}/quired:${var.image_tag}"
  migrate_image = "${local.image_repository}/quire-migrate:${var.image_tag}"

  # The generated layer: three manifests and a kustomization, written beside
  # this stack and applied from it. It is the only Kubernetes this stack writes
  # by hand, and it says exactly what deploy/k8s/overlays/cloud deliberately
  # does not — which domain, which image, and how this cloud publishes a
  # gateway.
  overlay_dir = "${path.module}/generated"
}

data "aws_availability_zones" "available" {
  state = "available"

  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

locals {
  azs = slice(data.aws_availability_zones.available.names, 0, var.availability_zones)
}
