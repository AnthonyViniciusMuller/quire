package progress

import (
	"math"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opParsePercent is the operation reported by this file, in the form the errs
// package expects.
const opParsePercent = "reading/progress: parse percent"

// CodeInvalidPercent is a proportion outside the range the column admits.
const CodeInvalidPercent = "invalid_percent"

// The bounds and the resolution reading.progress declares as numeric(5, 2).
const (
	minPercent = 0
	maxPercent = 100
	// decimals is how many the column keeps. A value the database would round
	// is rounded here instead, so that what a client is told it stored is what
	// a later read returns.
	decimals = 2
)

// Percent is how far through the work the reader is, absent when the client
// did not say.
//
// It is a struct and not a float, because absence and zero are different
// claims and both occur. A reader who has opened a work and read nothing is at
// zero; a client that cannot compute a proportion — a fixed-layout format it
// resolves positions in without knowing the whole — sends nothing. The column
// is nullable for that reason, and a float would spell both as 0.
//
// It is stored at all only so that a client can show progress without
// resolving a position it may not be able to interpret: a device that cannot
// open a DjVu can still report that the reader is forty per cent through it.
// The locator remains the truth about where the reader is, and this remains
// derived from it.
type Percent struct {
	value float64
	known bool
}

// NoPercent is the proportion of a client that did not compute one.
func NoPercent() Percent { return Percent{} }

// NewPercent is the proportion value, refusing one the column cannot hold.
func NewPercent(value float64) (Percent, error) {
	percent := Percent{value: round(value), known: true}
	if err := percent.Validate(); err != nil {
		return Percent{}, err
	}

	return percent, nil
}

// IsKnown reports whether the client computed a proportion.
func (p Percent) IsKnown() bool { return p.known }

// Float64 renders the proportion, and zero when there is none. A caller that
// has to tell the two apart asks IsKnown first, which is what the pointer the
// column maps to is built from.
func (p Percent) Float64() float64 { return p.value }

// Validate reports why the proportion is not usable, or nil. An absent one is
// usable: the column is nullable.
//
// It is the check reading.progress_percent_range makes, and it admits exactly
// what that constraint admits. A NaN is refused here and not there — the
// comparisons the constraint is written as are both false for one, so the row
// would be accepted and every later comparison against it would be false as
// well.
func (p Percent) Validate() error {
	if !p.known {
		return nil
	}

	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the proportion read is not usable").
			WithOp(opParsePercent).
			WithCode(CodeInvalidPercent).
			WithField("percent", reason)
	}

	switch {
	case math.IsNaN(p.value) || math.IsInf(p.value, 0):
		return invalid("it must be a number")
	case p.value < minPercent || p.value > maxPercent:
		return invalid("it must be between 0 and 100")
	default:
		return nil
	}
}

// ParsePercent reads what a client that may have sent nothing meant.
func ParsePercent(value *float64) (Percent, error) {
	if value == nil {
		return NoPercent(), nil
	}

	return NewPercent(*value)
}

// round is the resolution numeric(5, 2) keeps.
//
// A value the database would round is rounded before it is stored, for the
// reason a timestamp is truncated to the microsecond: a proportion that
// changed between the reply and the next read would be a value this node
// reported and does not hold.
func round(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}

	scale := math.Pow(10, decimals)

	return math.Round(value*scale) / scale
}
