# What has to exist in the cluster before a Quire node can be applied to it: the
# mesh that terminates one port and passes another through, and the authority
# that signs the two certificates the node holds.
#
# It is the cloud counterpart of the first half of scripts/kind-up.sh, and the
# versions are pinned for the same reason they are pinned there. The mesh is
# load-bearing twice over — deploy/k8s/istio/envoyfilter.yaml patches the
# structure of a gateway's HTTP filter chain, and an upgrade may move what it
# matches on.

resource "helm_release" "cert_manager" {
  name             = "cert-manager"
  repository       = "https://charts.jetstack.io"
  chart            = "cert-manager"
  version          = var.cert_manager_version
  namespace        = "cert-manager"
  create_namespace = true

  set {
    name  = "crds.enabled"
    value = "true"
  }

  # The DNS-01 solver talks to Cloud DNS as the service account below, through a
  # Workload Identity token rather than a key.
  set {
    name  = "serviceAccount.annotations.iam\\.gke\\.io/gcp-service-account"
    value = google_service_account.cert_manager.email
  }

  depends_on = [google_container_node_pool.this]
}

# Istio, in two charts, and the profile scripts/kind-up.sh installs: istiod and
# nothing else. The shared ingress gateway the default profile would install
# goes unused here, because each Quire node owns the gateway that answers for
# its domain — two nodes in a federation belong to two operators who share no
# authority and would share no ingress.
resource "helm_release" "istio_base" {
  name             = "istio-base"
  repository       = "https://istio-release.storage.googleapis.com/charts"
  chart            = "base"
  version          = var.istio_version
  namespace        = "istio-system"
  create_namespace = true

  set {
    name  = "defaultRevision"
    value = "default"
  }

  depends_on = [google_container_node_pool.this]
}

resource "helm_release" "istiod" {
  name       = "istiod"
  repository = "https://istio-release.storage.googleapis.com/charts"
  chart      = "istiod"
  version    = var.istio_version
  namespace  = "istio-system"

  # istiod is what injects the sidecar every pod in the node's namespace gets,
  # so nothing may be applied into that namespace before this is ready.
  wait = true

  # The chart asks for 500m and 2Gi, and that is a request sized for a mesh with
  # hundreds of proxies in it. This one has three — the node's sidecar, the
  # gateway's, and the migration job's while it runs. A request is a reservation
  # rather than a measurement, so the chart's number would hold 2Gi of an
  # e2-medium's 2.9Gi allocatable against pods that are never going to be
  # scheduled.
  #
  # It is what lets var.node_machine_type be an e2-medium rather than an
  # e2-standard-2: the two are one change and raising either alone is wrong. A
  # mesh that grows past a handful of proxies wants the chart's numbers back,
  # and a node large enough to put them on.
  set {
    name  = "pilot.resources.requests.cpu"
    value = "100m"
  }

  set {
    name  = "pilot.resources.requests.memory"
    value = "512Mi"
  }

  depends_on = [helm_release.istio_base]
}

# --- what cert-manager is allowed to change ----------------------------------

resource "google_service_account" "cert_manager" {
  account_id   = "${var.name}-cert-manager"
  display_name = "cert-manager, answering DNS-01 challenges"
}

# roles/dns.admin at the project, and the breadth is stated rather than hidden.
#
# A binding on the zone alone is not enough: the solver lists the project's
# managed zones to find the one holding the challenge, and that call is
# authorized at the project. This project holds one zone — the one this stack
# created — so what the role covers and what it needs are the same set today,
# and would stop being the same the moment another zone is added here.
resource "google_project_iam_member" "cert_manager" {
  project = var.project
  role    = "roles/dns.admin"
  member  = "serviceAccount:${google_service_account.cert_manager.email}"
}

resource "google_service_account_iam_member" "cert_manager_workload_identity" {
  service_account_id = google_service_account.cert_manager.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project}.svc.id.goog[cert-manager/cert-manager]"
}

# --- the two authorities -----------------------------------------------------

# Cluster-scoped, and applied once per cluster rather than as part of the
# overlay — which is exactly the shape deploy/k8s/certmanager/cluster has, and
# for the same reason: an authority two nodes could refer to is a fact about the
# cluster and not about either of them.
#
# They are applied with kubectl rather than declared as kubernetes_manifest
# resources, because that resource reads the CRD at plan time and the CRD does
# not exist until cert-manager has been installed by the apply the plan
# precedes.
resource "local_file" "issuers" {
  filename = "${local.overlay_dir}/issuers.yaml"
  content = templatefile("${path.module}/templates/issuers.yaml.tftpl", {
    acme_server = var.acme_server
    acme_email  = var.acme_email
    project     = var.project
  })
}

resource "terraform_data" "issuers" {
  triggers_replace = [local_file.issuers.content, google_container_cluster.this.endpoint]

  provisioner "local-exec" {
    environment = { KUBECONFIG = local_sensitive_file.kubeconfig.filename }
    command     = "kubectl apply -f ${local_file.issuers.filename}"
  }

  depends_on = [helm_release.cert_manager]
}

# The kubeconfig the two local-exec steps use. It holds no credential: the token
# is minted by the gke-gcloud-auth-plugin at the moment it is needed, which is
# what `gcloud container clusters get-credentials` writes and what the providers
# above are configured with by a different route.
resource "local_sensitive_file" "kubeconfig" {
  filename        = "${local.overlay_dir}/kubeconfig"
  file_permission = "0600"

  content = templatefile("${path.module}/templates/kubeconfig.yaml.tftpl", {
    endpoint  = google_container_cluster.this.endpoint
    authority = google_container_cluster.this.master_auth[0].cluster_ca_certificate
  })
}
