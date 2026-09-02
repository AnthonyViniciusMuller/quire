# The variables that have no default. Copy to terraform.tfvars.

project = "quire-example"

# The domain this stack creates a managed zone for. It has to be one you can
# point a registrar at: nothing below works until the delegation is live.
domain = "quire-gcp.example"

# Where Let's Encrypt writes about the gateway's certificate.
acme_email = "you@example"

# The relay password recoveries are submitted through. This cloud has no mail
# product, so there is nothing to provision and these are yours — the values
# below are the shape SendGrid takes; Mailgun, Brevo and a relay of your own
# differ only in the host and the username.
#
# The node refuses to submit a recovery credential in the clear under
# QUIRE_ENV=production, so the security has to be starttls or tls.
mail = {
  host         = "smtp.sendgrid.net"
  username     = "apikey"
  password     = "SG.replace-me"
  from_address = "no-reply@quire-gcp.example"
}

# The staging directory, for a first apply against a new domain. Its
# certificates are not trusted by anything, which is the point: the rate limits
# on the production directory are low enough that a misconfigured DNS-01 solver
# can exhaust a week of them in an afternoon.
#
# acme_server = "https://acme-staging-v02.api.letsencrypt.org/directory"

# The tag both images carry. `make image-name` in the repository root prints
# what `make images` just built.
# image_tag = "v0.1.0"

