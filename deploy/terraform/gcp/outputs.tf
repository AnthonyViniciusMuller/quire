# What an operator needs after the apply, and nothing that is only interesting
# during it.

output "nameservers" {
  description = <<-TEXT
    The nameservers the registrar has to be pointed at. Until it is, this zone
    answers nobody: no certificate is issued, no peer discovers this node, and
    the web client's name does not resolve.
  TEXT
  value       = google_dns_managed_zone.this.name_servers
}

output "node_domain" {
  description = "What a reader types into Atril, and what a peer passes to RefreshKnownServer."
  value       = local.node_domain
}

output "gateway_address" {
  description = <<-TEXT
    The address the node's domain resolves to. All three ports go through it —
    80 redirects, 443 is terminated by the node's own gateway, and 9443 is
    passed through untouched to the node.
  TEXT
  value       = google_compute_address.gateway.address
}

output "kubeconfig_command" {
  description = "How to reach the cluster with a kubectl of your own."
  value       = "gcloud container clusters get-credentials ${google_container_cluster.this.name} --location ${local.cluster_location} --project ${var.project}"
}

output "image_push_commands" {
  description = <<-TEXT
    What to run in the repository root before the first apply, and again for
    every commit deployed after it. Both images carry one tag because the schema
    is versioned with the binary that expects it.
  TEXT
  value = <<-TEXT
    gcloud auth configure-docker ${var.region}-docker.pkg.dev
    make images IMAGE_REGISTRY=${local.registry} IMAGE_TAG=${var.image_tag}
    docker push ${local.node_image}
    docker push ${local.migrate_image}
  TEXT
}

output "database_address" {
  description = "The Cloud SQL instance's private address, reachable across the VPC peering and from nowhere else."
  value       = google_sql_database_instance.this.private_ip_address
}

output "contents_bucket" {
  description = "Where the e-books are. It holds no metadata — that is the database's."
  value       = google_storage_bucket.contents.name
}

output "discovery_document" {
  description = "The URL a peer fetches to learn this node's pinned public key."
  value       = "https://${local.node_domain}/.well-known/quire/server"
}
