# The network this deployment lives in, and nothing else lives in.
#
# One subnet with two secondary ranges, which is what a VPC-native cluster wants:
# pods and services get addresses the network itself knows about, so a pod is
# routable and a load balancer can send traffic straight to one. That matters
# here more than it usually does — the gateway's Service is what publishes the
# federation port, and the fewer hops between the balancer and the proxy, the
# fewer places there are for something to decide it understands TLS.
#
# **There is no Cloud NAT, and the nodes hold external addresses of their own.**
# That is not the shape this file had first, and it is worth being exact about
# what the change did and did not do rather than leaving somebody to infer it
# from two missing resources.
#
#   What is unchanged. Nothing reaches a node from outside. A VPC denies ingress
#   by default and GKE opens exactly two things — the control plane's range and
#   the health checkers — so inbound is closed by a rule, as it always was. The
#   database is still on a private address, still reached across the peering,
#   and still has no path from the internet at all.
#
#   What is not. A node is now addressable. A firewall rule that was wrong used
#   to need a pivot before it could be exploited and now does not, and there is
#   no longer one place through which everything this deployment sends outward
#   can be watched.
#
# It bought $31 a month on a deployment that runs for four of them. Putting the
# NAT back is a google_compute_router, a google_compute_router_nat, and
# enable_private_nodes on the cluster — and it should be put back before this
# becomes anything anybody depends on.

resource "google_compute_network" "this" {
  name                    = var.name
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "this" {
  name          = var.name
  network       = google_compute_network.this.id
  region        = var.region
  ip_cidr_range = var.subnet_cidr

  # Kept even though the nodes now have addresses of their own. It keeps traffic
  # to Artifact Registry, Cloud Storage and Secret Manager on Google's own
  # network rather than sending it out and back in, which is both faster and one
  # fewer thing leaving through an address anybody can see.
  private_ip_google_access = true

  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = var.pod_cidr
  }

  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = var.service_cidr
  }
}

# The address the node's domain resolves to, reserved here rather than left to
# Kubernetes.
#
# It is the one place this stack is meaningfully simpler than the one next door.
# A reserved regional address can be handed to a Service before the Service
# exists, so the A record is written in the same apply that creates the load
# balancer — where the AWS stack has to read back a name it was given and settle
# for a CNAME.
#
# It also survives the Service. A node's domain is what every peer in the
# federation recorded, and an address that changed because a manifest was
# reapplied would be a node that quietly stopped being findable.
resource "google_compute_address" "gateway" {
  name         = "${var.name}-gateway"
  region       = var.region
  address_type = "EXTERNAL"
}
