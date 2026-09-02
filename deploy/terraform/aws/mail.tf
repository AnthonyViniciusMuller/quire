# How a reader gets their password back.
#
# The node refuses to start under QUIRE_ENV=production with no delivery
# transport configured, and refuses to submit a recovery credential in the
# clear. That is C13 and the two commits that answer it: the adapter that would
# write the credential to the log exists, and it declines to be built in
# production, because logs are read by more people and kept in more places than
# a mailbox is.
#
# SES is what this cloud has, and it is reached over SMTP rather than through
# the API for one reason: the node speaks SMTP, and a node that had to know
# which cloud it was deployed in would be a node that could not be deployed in
# another. The GCP stack next door has no equivalent and takes a relay's
# credentials as variables, which is the honest shape of that asymmetry.

resource "aws_ses_domain_identity" "this" {
  domain = var.domain
}

resource "aws_ses_domain_dkim" "this" {
  domain = aws_ses_domain_identity.this.domain
}

# Three CNAMEs, which is how DKIM is published. Without them mail leaves
# unsigned and arrives, if at all, in a spam folder.
resource "aws_route53_record" "dkim" {
  count = 3

  zone_id = aws_route53_zone.this.zone_id
  name    = "${aws_ses_domain_dkim.this.dkim_tokens[count.index]}._domainkey.${var.domain}"
  type    = "CNAME"
  ttl     = 600
  records = ["${aws_ses_domain_dkim.this.dkim_tokens[count.index]}.dkim.amazonses.com"]
}

# The identity is verified by a TXT record in the zone this stack owns, so
# verification happens without anybody being asked to paste anything.
resource "aws_route53_record" "ses_verification" {
  zone_id = aws_route53_zone.this.zone_id
  name    = "_amazonses.${var.domain}"
  type    = "TXT"
  ttl     = 600
  records = [aws_ses_domain_identity.this.verification_token]
}

# There is deliberately no aws_ses_domain_identity_verification here. That
# resource blocks the apply until Amazon can read the TXT record above, which it
# cannot until the registrar has been pointed at this zone's nameservers — and
# those are an output of this same apply. SES verifies on its own once the
# delegation is live, and the node needs the relay only when somebody forgets a
# password, so an apply that waited would be an apply blocked on a person for no
# gain. `aws ses get-identity-verification-attributes` says whether it has.

# SES speaks SMTP with an IAM credential, and the password is not the secret
# access key: it is a version-4 signature derived from it, which the provider
# computes rather than the operator. The user may send and do nothing else.
resource "aws_iam_user" "mail" {
  name = "${var.name}-mail"
}

resource "aws_iam_user_policy" "mail" {
  name = "quire-ses-send"
  user = aws_iam_user.mail.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["ses:SendRawEmail"]
      Resource = "*"
      Condition = {
        # Only as this domain. A credential that leaked would still be a
        # credential that can only claim to be the node it belongs to.
        StringEquals = { "ses:FromAddress" = "no-reply@${var.domain}" }
      }
    }]
  })
}

resource "aws_iam_access_key" "mail" {
  user = aws_iam_user.mail.name
}
