// Package tenant provides Fail-Closed multi-tenant context propagation,
// organization slug validation, and traversal/symlink-safe tenant path helpers.
package tenant

import (
	"context"
	"errors"

	"github.com/Karlsk/synapse-os/internal/infra/obs"
)

// DefaultSentinelID is reserved exclusively for pre-login branding and /mcp
// health probing; it does not represent a real enterprise tenant.
const DefaultSentinelID = "superagent"

var (
	// ErrMissingTenant indicates a tenant-scoped operation was invoked without
	// an explicit tenant context attached via WithCompany.
	ErrMissingTenant = errors.New("missing tenant context")
	// ErrInvalidCompany indicates a company ID violated format or safety rules.
	ErrInvalidCompany = errors.New("invalid company id")
	// ErrPathEscape indicates a joined or symlink-resolved path escapes the
	// permitted tenant base directory.
	ErrPathEscape = errors.New("path escapes tenant root")
)

type companyCtxKey struct{}

// WithCompany validates companyID and attaches it to ctx under a private
// authorization key while mirroring it into obs logging metadata.
// Require only trusts the private key and never reads obs.CompanyIDFrom.
func WithCompany(ctx context.Context, companyID string) (context.Context, error) {
	if err := ValidateCompanyID(companyID); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, companyCtxKey{}, companyID)
	ctx = obs.WithCompanyID(ctx, companyID)
	return ctx, nil
}

// Require extracts the validated tenant companyID from ctx.
// It fails closed with ErrMissingTenant when absent or spoofed via obs metadata.
func Require(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", ErrMissingTenant
	}
	id, ok := ctx.Value(companyCtxKey{}).(string)
	if !ok || id == "" {
		return "", ErrMissingTenant
	}
	return id, nil
}

// Optional extracts the validated tenant companyID from ctx if present.
func Optional(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	id, ok := ctx.Value(companyCtxKey{}).(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// Detach creates a fresh background context decoupled from parent cancellation,
// deadlines, and any DB transaction handle while preserving the validated
// tenant companyID and observability metadata (request_id, company_id, user_id).
func Detach(ctx context.Context) context.Context {
	out := context.Background()
	if id, ok := Optional(ctx); ok {
		out = context.WithValue(out, companyCtxKey{}, id)
	}
	return obs.CopyMeta(ctx, out)
}
