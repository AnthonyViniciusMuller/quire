package resetpassword

// Input is the credential a reader received and the password they want.
type Input struct {
	// RecoveryToken is the credential from the message, as its holder has it.
	RecoveryToken string
	// NewPassword is the plaintext, hashed and then dropped.
	NewPassword string
}

// Output is empty. What the caller needs to know is that it worked, and every
// session of every device has ended — which is not something to report field by
// field.
type Output struct{}
