# Atril, as a static origin behind a CDN.
#
# The web client is a build and not a service: `make build-web` in the Atril
# checkout produces a directory of files, and what serves them needs to do
# nothing but serve them. So there is no compute here at all — a private bucket,
# a distribution in front of it, and a certificate for the one name it answers
# on.
#
# **It is deployed once, and this is where.** The GCP stack served it first, and
# a Google global HTTPS load balancer bills about $23 a month before it carries a
# byte; CloudFront's always-free tier covers a terabyte of egress and ten million
# requests a month, which is more than this project will serve in four. So the
# one copy lives here, on the cheaper front, and the GCP stack deploys the node
# alone. A shipped build knows no node until a reader types a domain into it, so
# one copy reaches both — a second would be a second thing to rebuild on every
# release for no reader's benefit.
#
# It is a second name rather than a path of the node's, and that is not a
# convention. The node's domain is fronted by an L4 load balancer that must
# terminate nothing; a CDN is the opposite of that in every respect. Two origins,
# two names, and the client reaches whichever node a reader names cross-origin —
# which every node's gateway already admits, deliberately and for the reason
# deploy/k8s/istio/virtualservice.yaml gives: the credential is a bearer token in
# a header and never a cookie, so there is no ambient authority for a hostile
# page to borrow.
#
# A private bucket rather than a public one, and that is worth stating because
# the alternative was measured: served from a storage provider's shared hostname
# the application would share a browser origin with everything else on it, and
# Atril keeps a reader's books in the origin-private file system and its database
# in IndexedDB. An origin of its own is not a nicety here.

resource "aws_s3_bucket" "app" {
  bucket = "${var.name}-app-${data.aws_caller_identity.current.account_id}"
}

resource "aws_s3_bucket_public_access_block" "app" {
  bucket = aws_s3_bucket.app.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# The bucket is private and the distribution reads it as itself. A public bucket
# would be a second address for the same files, on a name with no certificate
# and no CDN, which nothing needs and somebody would eventually link to.
resource "aws_cloudfront_origin_access_control" "app" {
  name                              = "${var.name}-app"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

resource "aws_s3_bucket_policy" "app" {
  bucket = aws_s3_bucket.app.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "cloudfront.amazonaws.com" }
      Action    = "s3:GetObject"
      Resource  = "${aws_s3_bucket.app.arn}/*"
      Condition = {
        StringEquals = { "AWS:SourceArn" = aws_cloudfront_distribution.app.arn }
      }
    }]
  })
}

resource "aws_acm_certificate" "app" {
  provider = aws.us_east_1

  domain_name       = local.app_domain
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_route53_record" "app_validation" {
  for_each = {
    for option in aws_acm_certificate.app.domain_validation_options :
    option.domain_name => {
      name   = option.resource_record_name
      record = option.resource_record_value
      type   = option.resource_record_type
    }
  }

  zone_id         = aws_route53_zone.this.zone_id
  name            = each.value.name
  type            = each.value.type
  records         = [each.value.record]
  ttl             = 60
  allow_overwrite = true
}

resource "aws_acm_certificate_validation" "app" {
  provider = aws.us_east_1

  certificate_arn         = aws_acm_certificate.app.arn
  validation_record_fqdns = [for record in aws_route53_record.app_validation : record.fqdn]

  # This is the one resource that cannot finish before the zone is delegated:
  # Amazon validates the certificate by resolving a record in it, from outside.
  # Half an hour rather than the default three quarters, so that an apply run
  # before the registrar was pointed at this zone fails while somebody is still
  # watching it. The README says to create the zone first, read the nameservers,
  # delegate, and only then apply the rest.
  timeouts {
    create = "30m"
  }
}

resource "aws_cloudfront_distribution" "app" {
  enabled             = true
  is_ipv6_enabled     = true
  default_root_object = "index.html"
  aliases             = [local.app_domain]
  comment             = "Atril, the Quire web client"

  origin {
    origin_id                = "app"
    domain_name              = aws_s3_bucket.app.bucket_regional_domain_name
    origin_access_control_id = aws_cloudfront_origin_access_control.app.id
  }

  default_cache_behavior {
    target_origin_id       = "app"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD", "OPTIONS"]
    cached_methods         = ["GET", "HEAD"]
    compress               = true

    # CachingOptimized, which respects the Cache-Control this stack writes on
    # each object rather than overriding it. What is short-lived and what is not
    # is a property of the file — see local.web_revalidate below — and the
    # distribution is not the place to decide it a second time.
    cache_policy_id = "658327ea-f89d-4fab-a63d-7e88639e58f6"
  }

  # A request for a path that is not a file. Atril routes on the fragment, so
  # this should not happen in normal use — but a bucket with object access
  # blocked answers a missing key with 403 rather than 404, and an unhandled 403
  # is an XML error document where the application should be.
  dynamic "custom_error_response" {
    for_each = [403, 404]

    content {
      error_code            = custom_error_response.value
      response_code         = 200
      response_page_path    = "/index.html"
      error_caching_min_ttl = 10
    }
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    acm_certificate_arn      = aws_acm_certificate_validation.app.certificate_arn
    ssl_support_method       = "sni-only"
    minimum_protocol_version = "TLSv1.2_2021"
  }

  price_class = "PriceClass_100"
}

# --- the build ---------------------------------------------------------------

locals {
  # try() rather than a conditional, because a conditional evaluates both of its
  # branches and fileset() on an empty path is an error rather than an empty set.
  web_files = try(fileset(var.web_build_dir, "**"), toset([]))

  web_content_types = {
    html    = "text/html; charset=utf-8"
    js      = "text/javascript; charset=utf-8"
    mjs     = "text/javascript; charset=utf-8"
    css     = "text/css; charset=utf-8"
    json    = "application/json; charset=utf-8"
    wasm    = "application/wasm"
    png     = "image/png"
    jpg     = "image/jpeg"
    jpeg    = "image/jpeg"
    svg     = "image/svg+xml"
    ico     = "image/x-icon"
    webp    = "image/webp"
    ttf     = "font/ttf"
    otf     = "font/otf"
    woff    = "font/woff"
    woff2   = "font/woff2"
    txt     = "text/plain; charset=utf-8"
    xml     = "application/xml"
    map     = "application/json"
    epub    = "application/epub+zip"
    symbols = "text/plain; charset=utf-8"
    bin     = "application/octet-stream"
  }

  # The files that must never be served stale, and why each one is on the list.
  #
  # Atril's shell is precached by a service worker keyed to a version the build
  # writes into it (web/atril_service_worker.js). The worker fills its cache
  # with ordinary fetches, so a CDN still holding the previous build's
  # main.dart.js would be a shell assembled from two builds — the document of
  # one and the application of the other — and it would stay that way until the
  # cache expired.
  #
  # So the entry points revalidate on every request and everything they name
  # gets an hour. The distribution is invalidated on each upload as well, which
  # is what makes the hour safe rather than merely short.
  web_revalidate = toset([
    "index.html",
    "flutter_bootstrap.js",
    "flutter.js",
    "atril_service_worker.js",
    "manifest.json",
    "version.json",
  ])
}

resource "aws_s3_object" "app" {
  for_each = local.web_files

  bucket = aws_s3_bucket.app.id
  key    = each.value
  source = "${var.web_build_dir}/${each.value}"
  etag   = filemd5("${var.web_build_dir}/${each.value}")

  content_type = lookup(
    local.web_content_types,
    lower(element(split(".", each.value), length(split(".", each.value)) - 1)),
    "application/octet-stream",
  )

  cache_control = contains(local.web_revalidate, each.value) ? "no-cache" : "public, max-age=3600"
}

# The invalidation, which is what makes the hour above a bound rather than a
# gamble. It runs only when something was uploaded, and it names everything
# because a Flutter build's filenames do not change between builds — which is
# the same property that makes the caching question interesting at all.
resource "terraform_data" "app_invalidation" {
  count = length(local.web_files) > 0 ? 1 : 0

  triggers_replace = [
    sha1(join("", [for file in local.web_files : filesha1("${var.web_build_dir}/${file}")])),
  ]

  provisioner "local-exec" {
    command = "aws cloudfront create-invalidation --distribution-id ${aws_cloudfront_distribution.app.id} --paths '/*'"
  }

  depends_on = [aws_s3_object.app]
}
