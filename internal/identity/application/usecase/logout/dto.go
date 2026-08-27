package logout

// Input is the credential the device presents in order to end its session.
//
// It is presented rather than inferred from the caller's access token, and the
// contract says why: a device whose access token has already expired can still
// end its session, and should not have to refresh in order to do so. That also
// makes this the one call in the slice that authenticates itself — what the
// caller holds is the proof.
type Input struct {
	// RefreshToken is the credential, as its holder has it. Only its digest is
	// ever compared.
	RefreshToken string
}

// Output is empty. The call has nothing to report that the caller does not
// already know.
type Output struct{}
