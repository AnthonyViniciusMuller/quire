# The node itself: the secrets docs/deployment.md says an operator supplies, and
# the apply that puts deploy/k8s into this cluster.
#
# The five secrets in that document are four here. quire-postgres is the
# password the in-cluster PostgreSQL initializes itself with, and there is no
# in-cluster PostgreSQL: RDS holds it, and the connection string is the only
# form of it anything needs.
#
# Nothing below writes a manifest that deploy/k8s does not already contain. What
# it writes is a generated layer that says the three things an overlay cannot
# know until a cloud is chosen — which domain, which image, and how this cloud
# publishes a gateway — and then applies the overlay through it.

# The key the node signs access tokens with. ECDSA P-256 in PKCS#8, which is
# what docs/deployment.md tells an operator to generate with openssl and what
# the configuration refuses to start without.
#
# It is generated here rather than supplied, and that has a consequence worth
# knowing: it lives in Terraform state, so the state is a secret and belongs in
# an encrypted backend. It is also copied into Secrets Manager below, because a
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
    QUIRE_AUTH_KEY_ID = "aws-${substr(sha256(tls_private_key.signing.public_key_pem), 0, 12)}"
  }
}

resource "kubernetes_secret" "storage" {
  metadata {
    name      = "quire-storage"
    namespace = kubernetes_namespace.quire.metadata[0].name
  }

  # The bucket's name is not here: it is public configuration and is patched
  # into the ConfigMap instead. What is here is the one long-lived credential in
  # this deployment, and the region that selects the S3 section.
  data = {
    QUIRE_STORAGE_S3_REGION            = var.region
    QUIRE_STORAGE_S3_ACCESS_KEY_ID     = aws_iam_access_key.storage.id
    QUIRE_STORAGE_S3_SECRET_ACCESS_KEY = aws_iam_access_key.storage.secret
  }
}

resource "kubernetes_secret" "mail" {
  metadata {
    name      = "quire-mail"
    namespace = kubernetes_namespace.quire.metadata[0].name
  }

  data = {
    QUIRE_MAIL_SMTP_HOST     = "email-smtp.${var.region}.amazonaws.com"
    QUIRE_MAIL_SMTP_PORT     = "587"
    QUIRE_MAIL_SMTP_SECURITY = "starttls"
    QUIRE_MAIL_SMTP_USERNAME = aws_iam_access_key.mail.id
    # Not the secret access key. SES derives its SMTP password from it with a
    # version-4 signature, and the provider computes the derivation so that
    # nobody has to run the script Amazon publishes for it.
    QUIRE_MAIL_SMTP_PASSWORD = aws_iam_access_key.mail.ses_smtp_password_v4
    QUIRE_MAIL_FROM_ADDRESS  = "no-reply@${var.domain}"
    QUIRE_MAIL_FROM_NAME     = "Quire"
  }

  depends_on = [aws_ses_domain_dkim.this]
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
    domain = local.node_domain
    bucket = aws_s3_bucket.contents.id
  })
}

resource "local_file" "gateway_service" {
  filename = "${local.overlay_dir}/gateway-service.yaml"
  content  = file("${path.module}/templates/gateway-service.yaml")
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
    aws_db_instance.this,
    aws_eks_node_group.this,
  ]
}

# What the load balancer ended up being called. It is read back rather than
# declared because Kubernetes created it: the Service is what publishes the
# three ports, and the third of them is the one nothing may terminate.
data "kubernetes_service" "gateway" {
  metadata {
    name      = "quire-gateway"
    namespace = kubernetes_namespace.quire.metadata[0].name
  }

  depends_on = [terraform_data.node]
}

# --- where the secrets are kept -----------------------------------------------

# The two that cannot be regenerated without consequence. The signing key is one
# because tokens outlive an apply; the database password is one because the
# database does.
resource "aws_secretsmanager_secret" "node" {
  name                    = "${var.name}/node"
  description             = "The Quire node's signing key and database URL."
  recovery_window_in_days = 7
}

resource "aws_secretsmanager_secret_version" "node" {
  secret_id = aws_secretsmanager_secret.node.id

  secret_string = jsonencode({
    QUIRE_AUTH_PRIVATE_KEY_PEM = tls_private_key.signing.private_key_pem_pkcs8
    QUIRE_AUTH_KEY_ID          = kubernetes_secret.signing_key.data["QUIRE_AUTH_KEY_ID"]
    QUIRE_DATABASE_URL         = local.database_url
  })
}
