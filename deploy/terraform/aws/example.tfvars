# The two variables that have no default, and the three worth setting on a first
# apply. Copy to terraform.tfvars.

# The domain this stack creates a hosted zone for. It has to be one you can
# point a registrar at: nothing below works until the delegation is live.
domain = "quire-aws.example"

# Where Let's Encrypt writes about the gateway's certificate.
acme_email = "you@example"

# The staging directory, for a first apply against a new domain. Its
# certificates are not trusted by anything, which is the point: the rate limits
# on the production directory are low enough that a misconfigured DNS-01 solver
# can exhaust a week of them in an afternoon.
#
# acme_server = "https://acme-staging-v02.api.letsencrypt.org/directory"

# The tag both images carry. `make image-name` in the repository root prints
# what `make images` just built.
# image_tag = "v0.1.0"

# The Atril web build to upload, relative to this directory. This stack serves
# the only copy; the GCP one deploys the node alone.
# web_build_dir = "../../../../atril/build/web"
