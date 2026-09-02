# The node itself: the secrets docs/deployment.md says an operator supplies, and
# the apply that puts deploy/k8s into this cluster.
#
# The five secrets in that document are three here. quire-postgres is the
# password the in-cluster PostgreSQL initializes itself with, and there is no
# in-cluster PostgreSQL. quire-storage is gone too, and that one is worth
# noticing: the object store is opened by a Workload Identity token rather than
# a key, so the bucket's name is configuration and there is no credential to
# keep beside it. The stack next door needs the secret, because the S3 adapter
# carries no credential chain.
#
# Nothing below writes a manifest that deploy/k8s does not already contain. What
# it writes is a generated layer that says the four things an overlay cannot
# know until a cloud is chosen, and then applies the overlay through it.

# The key the node signs access tokens with. ECDSA P-256 in PKCS#8, which is
# what docs/deployment.md tells an operator to generate with openssl and what
# the configuration refuses to start without.
#
# It is generated here rather than supplied, and that has a consequence worth
# knowing: it lives in Terraform state, so the state is a secret and belongs in
# an encrypted backend. It is also copied into Secret Manager below, because a
# signing key that only exists in a state file is a key nobody can rotate
# without a plan.
resource "tls_private_key" "signing" {
  algorithm   = "ECDSA"
  ecdsa_curve = "P256"
}

resource "kubernetes_namespace" "quire" {
  metadata {
    name = "quire"
    labels = {
      # The same label deploy/k8s/overlays/cloud/namespace.yaml carries. Both
      # declare it and they agree, which makes applying both idempotent — and
      # the namespace has to exist here, before the overlay, because a secret
      # written after the workload is a workload that crash-loops until it is.
      istio-injection = "enabled"
    }
  }

  depends_on = [helm_release.istiod]
}

resource "kubernetes_secret" "database" {
  metadata {
    name      = "quire-database"
    namespace = kubernetes_namespace.quire.metadata[0].name
  }

  data = { QUIRE_DATABASE_URL = local.database_url }
}

resource "kubernetes_secret" "signing_key" {
  metadata {
    name      = "quire-signing-key"
    namespace = kubernetes_namespace.quire.metadata[0].name
  }

  data = {
    QUIRE_AUTH_PRIVATE_KEY_PEM = tls_private_key.signing.private_key_pem_pkcs8
    # What the JWKS document keys it by, and what makes rotation possible: a
    # token signed with the previous key stays verifiable while both are
    # published. The identifier carries the key's own fingerprint, so two keys
    # can never be issued the same name by two applies.
    QUIRE_AUTH_KEY_ID = "gcp-${substr(sha256(tls_private_key.signing.public_key_pem), 0, 12)}"
  }
}

# The bucket needs no secret at all, so the base's envFrom would name one that
# does not exist and the pod would never start. An empty Secret is what keeps
# the manifests identical across both clouds without either of them carrying a
# credential it does not have.
resource "kubernetes_secret" "storage" {
  metadata {
    name      = "quire-storage"
    namespace = kubernetes_namespace.quire.metadata[0].name
  }

  data = {}
}

resource "kubernetes_secret" "mail" {
  metadata {
    name      = "quire-mail"
    namespace = kubernetes_namespace.quire.metadata[0].name
  }

  # This cloud has no mail product, so nothing here was provisioned: it is the
  # relay the operator named, written where the node reads it. The node refuses
  # to start under QUIRE_ENV=production with no transport configured, because
  # the adapter that would write a recovery credential to the log declines to be
  # built there — logs are read by more people and kept in more places than a
  # mailbox is.
  data = merge(
    {
      QUIRE_MAIL_SMTP_HOST     = var.mail.host
      QUIRE_MAIL_SMTP_PORT     = tostring(var.mail.port)
      QUIRE_MAIL_SMTP_SECURITY = var.mail.security
      QUIRE_MAIL_FROM_ADDRESS  = var.mail.from_address
      QUIRE_MAIL_FROM_NAME     = var.mail.from_name
    },
    # Both or neither, which is what the configuration checks. A relay that
    # authenticates by address rather than by credential is unusual and is not
    # forbidden.
    var.mail.username == "" ? {} : {
      QUIRE_MAIL_SMTP_USERNAME = var.mail.username
      QUIRE_MAIL_SMTP_PASSWORD = var.mail.password
    },
  )
}

# --- the generated layer -----------------------------------------------------

resource "local_file" "kustomization" {
  filename = "${local.overlay_dir}/kustomization.yaml"
  content = templatefile("${path.module}/templates/kustomization.yaml.tftpl", {
    domain        = local.node_domain
    node_image    = local.node_image
    migrate_image = local.migrate_image
  })
}

resource "local_file" "configuration" {
  filename = "${local.overlay_dir}/configuration.yaml"
  content = templatefile("${path.module}/templates/configuration.yaml.tftpl", {
    domain  = local.node_domain
    bucket  = google_storage_bucket.contents.name
    project = var.project
  })
}

resource "local_file" "gateway_service" {
  filename = "${local.overlay_dir}/gateway-service.yaml"
  content = templatefile("${path.module}/templates/gateway-service.yaml.tftpl", {
    address = google_compute_address.gateway.address
  })
}

resource "local_file" "serviceaccount" {
  filename = "${local.overlay_dir}/serviceaccount.yaml"
  content = templatefile("${path.module}/templates/serviceaccount.yaml.tftpl", {
    service_account = google_service_account.node.email
  })
}

# The apply, and the one line before it that makes re-applying possible.
#
# The migration job is immutable in the fields that matter, so applying it again
# after a schema change is refused until the finished one is deleted. That is
# deliberate in the manifest and it is deliberate here too: this stack is what
# deploys a new commit, so deleting the job is what this stack does before it
# applies one — the same decision scripts/kind-up.sh makes, for a throwaway
# cluster, for the same reason.
resource "terraform_data" "node" {
  triggers_replace = [
    local_file.kustomization.content,
    local_file.configuration.content,
    local_file.gateway_service.content,
    local_file.serviceaccount.content,
  ]

  provisioner "local-exec" {
    environment = { KUBECONFIG = local_sensitive_file.kubeconfig.filename }
    command     = <<-SHELL
      set -euo pipefail
      kubectl -n quire delete job quire-migrate --ignore-not-found
      kubectl apply -k ${local.overlay_dir}
      kubectl -n quire rollout status deployment/quire-gateway --timeout=10m
      kubectl -n quire wait --for=condition=complete job/quire-migrate --timeout=10m
      kubectl -n quire rollout status deployment/quire --timeout=10m
    SHELL
  }

  depends_on = [
    terraform_data.issuers,
    kubernetes_secret.database,
    kubernetes_secret.signing_key,
    kubernetes_secret.storage,
    kubernetes_secret.mail,
    google_sql_database.quire,
    google_sql_user.quire,
    google_storage_bucket_iam_member.node,
    google_service_account_iam_member.node_workload_identity,
    google_container_node_pool.this,
  ]
}

# --- where the secrets are kept -----------------------------------------------

# The two that cannot be regenerated without consequence. The signing key is one
# because tokens outlive an apply; the database password is one because the
# database does.
resource "google_secret_manager_secret" "node" {
  secret_id = "${var.name}-node"

  replication {
    auto {}
  }

  depends_on = [google_project_service.this]
}

resource "google_secret_manager_secret_version" "node" {
  secret = google_secret_manager_secret.node.id

  secret_data = jsonencode({
    QUIRE_AUTH_PRIVATE_KEY_PEM = tls_private_key.signing.private_key_pem_pkcs8
    QUIRE_AUTH_KEY_ID          = kubernetes_secret.signing_key.data["QUIRE_AUTH_KEY_ID"]
    QUIRE_DATABASE_URL         = local.database_url
  })
}
