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

  # The DNS-01 solver talks to Route53 as the role below, through a projected
  # token rather than a key. The annotation is what tells the SDK inside
  # cert-manager to look for one, and the fsGroup is what lets a non-root
  # process read the file it is projected into.
  set {
    name  = "serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn"
    value = aws_iam_role.cert_manager.arn
  }

  set {
    name  = "securityContext.fsGroup"
    value = "1001"
  }

  depends_on = [aws_eks_addon.this]
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

  depends_on = [aws_eks_addon.this]
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
  # rather than a measurement, so the chart's number would hold 2Gi of a
  # t3.medium's 3.5Gi allocatable against pods that are never going to be
  # scheduled.
  #
  # It is what lets var.node_instance_types be a t3.medium rather than a
  # t3.large: the two are one change and raising either alone is wrong. A mesh
  # that grows past a handful of proxies wants the chart's numbers back, and a
  # node large enough to put them on.
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

# The DNS-01 challenge is a TXT record in the zone this stack owns, so
# cert-manager needs to write there and nowhere else. GetChange is on the
# wildcard because a change id is not a resource an operator can enumerate in
# advance; the two that matter are scoped to the one zone.
resource "aws_iam_role" "cert_manager" {
  name = "${var.name}-cert-manager"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Federated = aws_iam_openid_connect_provider.this.arn }
      Action    = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "${local.oidc_host}:aud" = "sts.amazonaws.com"
          "${local.oidc_host}:sub" = "system:serviceaccount:cert-manager:cert-manager"
        }
      }
    }]
  })
}

resource "aws_iam_role_policy" "cert_manager" {
  name = "route53-dns01"
  role = aws_iam_role.cert_manager.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = "route53:GetChange"
        Resource = "arn:aws:route53:::change/*"
      },
      {
        Effect   = "Allow"
        Action   = ["route53:ChangeResourceRecordSets", "route53:ListResourceRecordSets"]
        Resource = aws_route53_zone.this.arn
      },
      {
        Effect   = "Allow"
        Action   = "route53:ListHostedZonesByName"
        Resource = "*"
      },
    ]
  })
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
    region      = var.region
    zone_id     = aws_route53_zone.this.zone_id
  })
}

resource "terraform_data" "issuers" {
  triggers_replace = [local_file.issuers.content, aws_eks_cluster.this.endpoint]

  provisioner "local-exec" {
    environment = { KUBECONFIG = local_sensitive_file.kubeconfig.filename }
    command     = "kubectl apply -f ${local_file.issuers.filename}"
  }

  depends_on = [helm_release.cert_manager]
}

# The kubeconfig the two local-exec steps use. It holds no credential: the token
# is minted by the AWS CLI at the moment it is needed, which is what
# `aws eks update-kubeconfig` writes and what the providers above are configured
# with.
resource "local_sensitive_file" "kubeconfig" {
  filename        = "${local.overlay_dir}/kubeconfig"
  file_permission = "0600"

  content = templatefile("${path.module}/templates/kubeconfig.yaml.tftpl", {
    endpoint  = aws_eks_cluster.this.endpoint
    authority = aws_eks_cluster.this.certificate_authority[0].data
    cluster   = aws_eks_cluster.this.name
    region    = var.region
  })
}
