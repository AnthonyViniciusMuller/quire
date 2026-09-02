# The cluster and its nodes.
#
# GKE rather than Cloud Run, and the reason is C23 rather than preference. Cloud
# Run terminates TLS and hands the application an HTTP request: it would present
# its own certificate to a peer that pinned this node's (C12), and it would have
# consumed the caller's certificate that ReplicateOperations identifies a peer
# by. Neither is recoverable from a header, because a header is a claim and a
# certificate is a proof. What the federation port needs is something that
# forwards TCP and reads nothing, which is what a Service of type LoadBalancer
# produces here — and once there is a cluster, deploy/k8s is the deployment
# rather than a second one written in HCL.

resource "google_project_service" "this" {
  for_each = toset([
    "container.googleapis.com",
    "compute.googleapis.com",
    "sqladmin.googleapis.com",
    "servicenetworking.googleapis.com",
    "artifactregistry.googleapis.com",
    "dns.googleapis.com",
    "secretmanager.googleapis.com",
    "iamcredentials.googleapis.com",
  ])

  service = each.value

  # Disabling an API on destroy would take with it anything else in the project
  # that happened to be using it, which is not a decision this stack is entitled
  # to make about a project it shares.
  disable_on_destroy = false
}

resource "google_container_cluster" "this" {
  name = var.name
  # A zone or the region, and the difference is three nodes and a control-plane
  # bill. See var.zonal.
  location = local.cluster_location

  network    = google_compute_network.this.id
  subnetwork = google_compute_subnetwork.this.id

  # The default pool is removed and replaced below, which is the documented way
  # to have a pool this stack actually configures: the one a cluster is created
  # with cannot be changed without recreating the cluster.
  remove_default_node_pool = true
  initial_node_count       = 1

  deletion_protection = false

  networking_mode = "VPC_NATIVE"

  ip_allocation_policy {
    cluster_secondary_range_name  = "pods"
    services_secondary_range_name = "services"
  }

  # No private_cluster_config at all, and its absence is the decision.
  #
  # The nodes hold external addresses and there is no Cloud NAT for them to
  # reach the internet through — network.tf argues that trade and says what it
  # cost. Turning private nodes back on means restoring that block together with
  # its master_ipv4_cidr_block, which the provider refuses to accept without it,
  # and the router and NAT beside it.
  #
  # The control plane keeps a public endpoint either way. The apply that deploys
  # the node runs from wherever the operator is, and a private endpoint would
  # need a bastion or a VPN to be the thing this stack is about; it is
  # authenticated regardless.

  # What lets a pod be a service account without holding a key. The node's GCS
  # adapter honours application default credentials — its package comment says
  # so, and says that the S3 adapter beside it does not — so this is what keeps
  # the object store out of the secrets an operator has to supply.
  workload_identity_config {
    workload_pool = "${var.project}.svc.id.goog"
  }

  release_channel {
    channel = "REGULAR"
  }

  min_master_version = var.kubernetes_version != "" ? var.kubernetes_version : null

  depends_on = [google_project_service.this]
}

resource "google_service_account" "nodes" {
  account_id   = "${var.name}-nodes"
  display_name = "The Quire cluster's nodes"
}

# What a worker is allowed to do, which is write its own telemetry and read the
# images it runs. It is not the default service account, and that is the point:
# the default one is an editor of the whole project.
resource "google_project_iam_member" "nodes" {
  for_each = toset([
    "roles/logging.logWriter",
    "roles/monitoring.metricWriter",
    "roles/monitoring.viewer",
    "roles/artifactregistry.reader",
  ])

  project = var.project
  role    = each.value
  member  = "serviceAccount:${google_service_account.nodes.email}"
}

resource "google_container_node_pool" "this" {
  name    = "${var.name}-nodes"
  cluster = google_container_cluster.this.name
  # The same location as the cluster, and it has to be: a pool in a region is a
  # pool in every zone of it, and the counts below are per zone.
  location = local.cluster_location

  initial_node_count = var.node_pool_size.initial

  autoscaling {
    min_node_count = var.node_pool_size.min
    max_node_count = var.node_pool_size.max
  }

  node_config {
    machine_type    = var.node_machine_type
    disk_size_gb    = var.node_disk_size
    disk_type       = "pd-balanced"
    spot            = var.node_spot
    service_account = google_service_account.nodes.email
    oauth_scopes    = ["https://www.googleapis.com/auth/cloud-platform"]

    workload_metadata_config {
      # The metadata server a pod sees is the one Workload Identity mediates,
      # not the node's. Without this a pod could ask for the node's own service
      # account, which is a larger identity than any pod here needs.
      mode = "GKE_METADATA"
    }

    shielded_instance_config {
      enable_secure_boot          = true
      enable_integrity_monitoring = true
    }

    labels = local.labels
  }

  management {
    auto_repair  = true
    auto_upgrade = true
  }
}
