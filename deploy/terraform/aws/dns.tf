# The zone, and the three names in it.
#
# The zone is created here and delegated by hand: the stack outputs four
# nameservers and the registrar has to be pointed at them. That is the one step
# no apply can do, and everything below waits on it — a certificate is issued by
# answering a DNS challenge, and a challenge in a zone nobody delegated is a
# record nobody reads.

resource "aws_route53_zone" "this" {
  name = var.domain

  tags = { Name = var.domain }
}

# The node, and it is a CNAME rather than an alias.
#
# The load balancer in front of the gateway is created by Kubernetes and not by
# this stack — a Service of type LoadBalancer is what publishes three ports, one
# of which must be passed through untouched — so what Terraform has is the name
# it was given, read back from the cluster in node.tf. An alias record wants a
# zone id this stack never learns; a CNAME wants the name it has.
#
# It costs nothing here because this is a subdomain. An apex cannot hold a CNAME,
# which is the one reason to put the node at quire.<domain> rather than at
# <domain> itself.
resource "aws_route53_record" "node" {
  zone_id = aws_route53_zone.this.zone_id
  name    = local.node_domain
  type    = "CNAME"
  ttl     = 60
  records = [data.kubernetes_service.gateway.status[0].load_balancer[0].ingress[0].hostname]
}

# The web client, which is a CloudFront distribution and therefore is an alias.
# Z2FDTNDATAQYW2 is CloudFront's own hosted zone and is the same in every region
# and every account; it is a constant AWS publishes rather than something this
# stack could look up.
resource "aws_route53_record" "app" {
  zone_id = aws_route53_zone.this.zone_id
  name    = local.app_domain
  type    = "A"

  alias {
    name                   = aws_cloudfront_distribution.app.domain_name
    zone_id                = "Z2FDTNDATAQYW2"
    evaluate_target_health = false
  }
}

resource "aws_route53_record" "app_ipv6" {
  zone_id = aws_route53_zone.this.zone_id
  name    = local.app_domain
  type    = "AAAA"

  alias {
    name                   = aws_cloudfront_distribution.app.domain_name
    zone_id                = "Z2FDTNDATAQYW2"
    evaluate_target_health = false
  }
}
