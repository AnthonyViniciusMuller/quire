# What an operator needs after the apply, and nothing that is only interesting
# during it.

output "nameservers" {
  description = <<-TEXT
    The four nameservers the registrar has to be pointed at. Until it is, this
    zone answers nobody: no certificate is issued, no peer discovers this node,
    and the web client's name does not resolve.
  TEXT
  value       = aws_route53_zone.this.name_servers
}

output "node_domain" {
  description = "What a reader types into Atril, and what a peer passes to RefreshKnownServer."
  value       = local.node_domain
}

output "gateway_load_balancer" {
  description = <<-TEXT
    The load balancer Kubernetes created for the node's gateway. All three
    ports go through it — 80 redirects, 443 is terminated by the gateway, and
    9443 is passed through untouched to the node.
  TEXT
  value       = data.kubernetes_service.gateway.status[0].load_balancer[0].ingress[0].hostname
}

output "app_domain" {
  description = "Where the web client is served from. It is the only copy, and it reaches both nodes."
  value       = "https://${local.app_domain}"
}

output "kubeconfig_command" {
  description = "How to reach the cluster with a kubectl of your own."
  value       = "aws eks update-kubeconfig --region ${var.region} --name ${aws_eks_cluster.this.name}"
}

output "image_push_commands" {
  description = <<-TEXT
    What to run in the repository root before the first apply, and again for
    every commit deployed after it. Both images carry one tag because the schema
    is versioned with the binary that expects it.
  TEXT
  value = <<-TEXT
    aws ecr get-login-password --region ${var.region} \
      | docker login --username AWS --password-stdin ${split("/", aws_ecr_repository.quired.repository_url)[0]}
    make images IMAGE_REGISTRY=${split("/", aws_ecr_repository.quired.repository_url)[0]} IMAGE_TAG=${var.image_tag}
    docker push ${local.node_image}
    docker push ${local.migrate_image}
  TEXT
}

output "database_endpoint" {
  description = "The RDS instance, reachable from the cluster's nodes and from nowhere else."
  value       = aws_db_instance.this.endpoint
}

output "contents_bucket" {
  description = "Where the e-books are. It holds no metadata — that is the database's."
  value       = aws_s3_bucket.contents.id
}

output "discovery_document" {
  description = "The URL a peer fetches to learn this node's pinned public key."
  value       = "https://${local.node_domain}/.well-known/quire/server"
}
