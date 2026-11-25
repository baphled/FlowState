package provider

import "errors"

// ErrContextOverflow marks a provider failure that says the request did not fit the model's
// context window. An adapter wraps it in its own typed error, so errors.Is finds it, only when
// it has evidence of the provider's overflow response; a provider with no such evidence never
// returns it, and the loop then treats the failure as any other provider failure (ADR-010 D10).
var ErrContextOverflow = errors.New("provider: context window exceeded")
