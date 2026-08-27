package device

import (
	"strings"
	"unicode/utf8"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opParseName     = "identity/device: parse name"
	opParsePlatform = "identity/device: parse platform"
)

// The stable machine-readable codes this package attaches to the errors it
// raises.
const (
	// CodeInvalidName is a device name being blank or too long.
	CodeInvalidName = "invalid_device_name"
	// CodeInvalidPlatform is a device platform being blank or too long.
	CodeInvalidPlatform = "invalid_platform"
)

// The widths identity.devices declares.
const (
	maxNameLength     = 120
	maxPlatformLength = 40
)

// Name is what the reader calls an appliance, and the only field of a bound
// device that is editable.
type Name string

// String renders the name.
func (n Name) String() string { return string(n) }

// Validate reports why the name is not usable, or nil. The blank check is the
// one identity.devices_name_not_blank makes, and it exists because a device
// named nothing cannot be told from another in the list RF11 makes auditable.
func (n Name) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the device name is not usable").
			WithOp(opParseName).
			WithCode(CodeInvalidName).
			WithField("name", reason)
	}

	switch {
	case string(n) == "":
		return invalid("it must not be empty")
	case characterCount(string(n)) > maxNameLength:
		return invalid("it must be at most 120 characters long")
	default:
		return nil
	}
}

// ParseName removes the surrounding space from s and validates the result.
func ParseName(s string) (Name, error) {
	name := Name(strings.TrimSpace(s))
	if err := name.Validate(); err != nil {
		return "", err
	}

	return name, nil
}

// Platform is the operating system an appliance runs.
type Platform string

// String renders the platform.
func (p Platform) String() string { return string(p) }

// Validate reports why the platform is not usable, or nil.
//
// The value is not checked against a list. The schema holds none, and a node
// that refused an unknown platform would refuse a reader's new appliance until
// it was redeployed — a poor trade for a field nothing branches on.
func (p Platform) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the device platform is not usable").
			WithOp(opParsePlatform).
			WithCode(CodeInvalidPlatform).
			WithField("platform", reason)
	}

	switch {
	case string(p) == "":
		return invalid("it must not be empty")
	case characterCount(string(p)) > maxPlatformLength:
		return invalid("it must be at most 40 characters long")
	default:
		return nil
	}
}

// ParsePlatform removes the surrounding space from s and validates the result.
func ParsePlatform(s string) (Platform, error) {
	platform := Platform(strings.TrimSpace(s))
	if err := platform.Validate(); err != nil {
		return "", err
	}

	return platform, nil
}

// characterCount is the length PostgreSQL measures a varchar in: characters,
// not bytes.
func characterCount(s string) int { return utf8.RuneCountInString(s) }
