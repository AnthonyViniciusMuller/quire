# The cluster, its nodes, and the identity provider that lets a pod be somebody
# to AWS without holding a key.
#
# EKS rather than ECS or App Runner, and the reason is C23 rather than
# preference. The federation port is passed through untouched — the node
# presents the certificate whose public key it published and reads the caller's
# in return — so whatever fronts it may terminate nothing. That rules out every
# managed HTTP front there is and leaves an L4 load balancer, which is what a
# Service of type LoadBalancer produces here; and once there is a cluster,
# deploy/k8s is the deployment rather than a second one written in HCL.

resource "aws_iam_role" "cluster" {
  name = "${var.name}-cluster"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "eks.amazonaws.com" }
      Action    = ["sts:AssumeRole", "sts:TagSession"]
    }]
  })
}

resource "aws_iam_role_policy_attachment" "cluster" {
  for_each = toset([
    "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy",
    "arn:aws:iam::aws:policy/AmazonEKSVPCResourceController",
  ])

  # The load balancer for the gateway's Service is created by the in-tree
  # service controller, which runs in the control plane as this role. What
  # permits it is inside AmazonEKSClusterPolicy above rather than a policy of its
  # own — worth knowing, because a Service stuck in <pending> with no event is
  # what a missing one looks like.

  role       = aws_iam_role.cluster.name
  policy_arn = each.value
}

resource "aws_eks_cluster" "this" {
  name     = var.name
  version  = var.kubernetes_version
  role_arn = aws_iam_role.cluster.arn

  vpc_config {
    # Both tiers. The control plane places network interfaces of its own in
    # order to reach the nodes and may put them anywhere; the nodes themselves
    # are in the public subnets, because network.tf removed the NAT gateway they
    # would otherwise have reached the internet through.
    subnet_ids = concat(
      [for subnet in aws_subnet.public : subnet.id],
      [for subnet in aws_subnet.private : subnet.id],
    )
    # The API server is reachable from the internet, because the apply that
    # deploys the node runs from wherever the operator is and a private
    # endpoint would need a bastion or a VPN to be the thing this stack is
    # about. It is authenticated by IAM either way; restrict
    # public_access_cidrs if the operator has a fixed address.
    endpoint_public_access  = true
    endpoint_private_access = true
  }

  access_config {
    authentication_mode = "API"
    # Whoever ran the apply can reach the cluster afterwards. Without it the
    # first kubectl is a lockout with a documented workaround, which is a poor
    # thing to hand somebody at the end of a twenty-minute apply.
    bootstrap_cluster_creator_admin_permissions = true
  }

  # The control plane's logs, and the two that answer the questions this
  # deployment actually raises: who called the API server, and what the
  # authenticator made of them.
  enabled_cluster_log_types = ["api", "audit", "authenticator"]

  depends_on = [aws_iam_role_policy_attachment.cluster]
}

resource "aws_iam_role" "node" {
  name = "${var.name}-node"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "node" {
  for_each = toset([
    "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
    "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
    # Pulling the node's own image out of the ECR repositories below. It is the
    # read-only policy: a worker that could push would be a worker that could
    # replace the image it is about to run.
    "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
  ])

  role       = aws_iam_role.node.name
  policy_arn = each.value
}

resource "aws_eks_node_group" "this" {
  cluster_name    = aws_eks_cluster.this.name
  node_group_name = "${var.name}-nodes"
  node_role_arn = aws_iam_role.node.arn
  # The public subnets, and the trade is argued in network.tf: a node with an
  # address of its own needs no NAT gateway, and nothing reaches it anyway —
  # the cluster's security group admits the load balancer, the control plane
  # and the other nodes, and nothing else.
  subnet_ids      = [for subnet in aws_subnet.public : subnet.id]
  instance_types  = var.node_instance_types
  disk_size       = var.node_disk_size

  scaling_config {
    desired_size = var.node_group_size.desired
    min_size     = var.node_group_size.min
    max_size     = var.node_group_size.max
  }

  update_config {
    max_unavailable = 1
  }

  depends_on = [aws_iam_role_policy_attachment.node]

  lifecycle {
    # The desired size drifts the moment anything scales, and a plan that wants
    # to put it back is a plan that undoes an autoscaler.
    ignore_changes = [scaling_config[0].desired_size]
  }
}

# The three add-ons EKS no longer installs on its own. They are applied after
# the node group so that the ones with a DaemonSet have somewhere to land.
resource "aws_eks_addon" "this" {
  for_each = toset(["vpc-cni", "kube-proxy", "coredns"])

  cluster_name                = aws_eks_cluster.this.name
  addon_name                  = each.value
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"

  depends_on = [aws_eks_node_group.this]
}

# --- who a pod is, to AWS ----------------------------------------------------

# The cluster's OIDC issuer, registered as an identity provider, which is what
# makes IRSA work: a pod's projected service account token is exchanged for a
# role, and nothing in the cluster holds an access key.
#
# One thing in this deployment still does hold one, and it is deliberate rather
# than an oversight. The node's S3 adapter carries no credential chain — see the
# package comment in internal/library/infra/service/s3 — so the object store
# credentials are a key pair in a Secret. cert-manager, which does honour the
# chain, gets a role instead.
data "tls_certificate" "oidc" {
  url = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

resource "aws_iam_openid_connect_provider" "this" {
  url             = aws_eks_cluster.this.identity[0].oidc[0].issuer
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.oidc.certificates[0].sha1_fingerprint]
}

locals {
  oidc_host = replace(aws_iam_openid_connect_provider.this.url, "https://", "")
}
