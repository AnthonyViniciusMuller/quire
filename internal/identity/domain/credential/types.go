package credential

// Kind distinguishes the two credentials an origin server issues that outlive a
// single call.
//
// The access token is not one of them and is deliberately absent from this
// package: it is a JWT, verified by signature against the keys published under
// /.well-known (RNF11), so nothing about it is stored on either side. C09 in
// docs/tcc-corrections.md is why the entity is called a credential rather than a
// token — the name it carries in the MER describes the one thing it does not
// hold.
type Kind string

// The kinds identity.credentials_kind accepts.
const (
	// KindSessionRefresh is what a device presents to stay signed in (RF07,
	// RF08). Revoking a device revokes these, which is why one always names a
	// device.
	KindSessionRefresh Kind = "session_refresh"
	// KindPasswordRecovery is what a reader presents to set a new password
	// (RF09, UC08). It names no device: recovery happens when the reader has
	// lost access, possibly from an appliance that is not bound to the account
	// at all.
	KindPasswordRecovery Kind = "password_recovery"
)

// String renders the kind as the column stores it.
func (k Kind) String() string { return string(k) }

// Valid reports whether k is one of the two kinds the schema accepts.
func (k Kind) Valid() bool {
	switch k {
	case KindSessionRefresh, KindPasswordRecovery:
		return true
	default:
		return false
	}
}
