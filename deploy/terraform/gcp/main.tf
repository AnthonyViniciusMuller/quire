# The names and the two addresses everything else is derived from.

locals {
  labels = merge({
    "app-kubernetes-io-part-of" = "quire"
    "quire-stack"               = "gcp"
    "quire-managed-by"          = "terraform"
  }, var.labels)

  # The node's domain, and the only one this stack publishes. The Atril web
  # client is served by deploy/terraform/aws, where CloudFront's always-free
  # tier carries it for nothing — a global HTTPS load balancer here cost $23 a
  # month to do the same job. A shipped build knows no node until a reader types
  # one, so a single copy reaches both.
  node_domain = "${var.node_subdomain}.${var.domain}"

  # Where the cluster and its node pool live, and the one place that decides it.
  # A zone here is a zonal cluster: one control plane, billed against GKE's own
  # monthly credit rather than to you, and a node pool that is not multiplied by
  # the number of zones in the region. var.zonal argues both.
  cluster_location = var.zonal ? (var.zone != "" ? var.zone : "${var.region}-a") : var.region

  registry = "${var.region}-docker.pkg.dev/${var.project}/${google_artifact_registry_repository.this.repository_id}"

  image_repository = var.image_repository != "" ? var.image_repository : local.registry

  node_image    = "${local.image_repository}/quired:${var.image_tag}"
  migrate_image = "${local.image_repository}/quire-migrate:${var.image_tag}"

  # The generated layer: four manifests and a kustomization, written beside this
  # stack and applied from it. It is the only Kubernetes this stack writes by
  # hand, and it says exactly what deploy/k8s/overlays/cloud deliberately does
  # not — which domain, which image, how this cloud publishes a gateway, and
  # which identity the node borrows to reach its bucket.
  overlay_dir = "${path.module}/generated"
}
