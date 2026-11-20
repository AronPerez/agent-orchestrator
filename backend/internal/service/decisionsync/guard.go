package decisionsync

import (
	"context"
	"errors"
)

// ErrCredentialRevoked reports a request whose sync credential was revoked
// before its write ran.
var ErrCredentialRevoked = errors.New("decision sync credential was revoked")

type applyGuardKey struct{}

// WithApplyGuard returns ctx carrying guard, which a request's ledger writes
// run inside. The sync routes use it to hold the presenting device live
// around each write, so revoking that device waits for the write or refuses
// it. guard must run fn at most once and return ErrCredentialRevoked when it
// does not run it.
func WithApplyGuard(ctx context.Context, guard func(fn func() error) error) context.Context {
	return context.WithValue(ctx, applyGuardKey{}, guard)
}

// RunGuarded runs fn inside ctx's guard, or directly when there is none. Run
// only a short local write this way: never a wait on another lock, a gate or
// the network, since revoking the device waits for it.
func RunGuarded(ctx context.Context, fn func() error) error {
	if guard, ok := ctx.Value(applyGuardKey{}).(func(func() error) error); ok && guard != nil {
		return guard(fn)
	}
	return fn()
}

// SelfGuardedLedger is a Ledger whose ApplyDecisionEntries waits for any gate
// first and runs only its store write inside RunGuarded. The server then
// does not wrap the call itself.
type SelfGuardedLedger interface {
	GuardsApply()
}
