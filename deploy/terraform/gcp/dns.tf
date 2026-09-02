# The zone, and the two names in it.
#
# The zone is created here and delegated by hand: the stack outputs its
# nameservers and the registrar has to be pointed at them. That is the one step
# no apply can do, and everything below waits on it — a certificate is issued by
# answering a DNS challenge, and a challenge in a zone nobody delegated is a
# record nobody reads.

resource "google_dns_managed_zone" "this" {
  name        = replace(var.domain, ".", "-")
  dns_name    = "${var.domain}."
  description = "Quire, on GCP"

  depends_on = [google_project_service.this]
}

# The node, and it is an A record because the address was reserved before the
# load balancer existed. The AWS stack has to settle for a CNAME to a name
# Kubernetes chose; here the Service is handed an address this stack already
# holds, which is also what keeps the address stable across a redeploy — and a
# node's address is something every peer in the federation recorded.
resource "google_dns_record_set" "node" {
  managed_zone = google_dns_managed_zone.this.name
  name         = "${local.node_domain}."
  type         = "A"
  ttl          = 60
  rrdatas      = [google_compute_address.gateway.address]
}

