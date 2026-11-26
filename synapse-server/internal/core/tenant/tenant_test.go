package tenant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
	"github.com/Karlsk/synapse-os/internal/infra/obs"
)

func TestFailClosedTenantContextAndDetach(t *testing.T) {
	t.Parallel()

	// Empty / nil context must fail closed
	if _, err := Require(context.Background()); !errors.Is(err, ErrMissingTenant) {
		t.Fatalf("Require(empty) = %v, want ErrMissingTenant", err)
	}
	if _, err := Require(nil); !errors.Is(err, ErrMissingTenant) {
		t.Fatalf("Require(nil) = %v, want ErrMissingTenant", err)
	}
	if _, ok := Optional(context.Background()); ok {
		t.Fatalf("Optional(empty) should be false")
	}

	// Spoofing via obs.WithCompanyID alone MUST NOT satisfy tenant.Require
	spoofed := obs.WithCompanyID(context.Background(), "acme")
	if _, err := Require(spoofed); !errors.Is(err, ErrMissingTenant) {
		t.Fatalf("Require(obs-only) = %v, want ErrMissingTenant", err)
	}

	// Invalid companyID rejected by WithCompany
	if _, err := WithCompany(context.Background(), "../evil"); !errors.Is(err, ErrInvalidCompany) {
		t.Fatalf("WithCompany(../evil) = %v, want ErrInvalidCompany", err)
	}

	// Valid WithCompany + obs metadata + Detach survives parent cancel
	parent, cancel := context.WithCancel(context.Background())
	ctx, err := WithCompany(parent, "acme")
	if err != nil {
		t.Fatalf("WithCompany(acme) error = %v", err)
	}
	ctx = obs.WithRequestID(ctx, "req-100")
	ctx = obs.WithUserID(ctx, "usr-42")

	if got, err := Require(ctx); err != nil || got != "acme" {
		t.Fatalf("Require(ctx) = (%q, %v), want (acme, nil)", got, err)
	}
	if got := obs.CompanyIDFrom(ctx); got != "acme" {
		t.Fatalf("obs.CompanyIDFrom(ctx) = %q, want acme", got)
	}

	detached := Detach(ctx)
	cancel() // Cancel parent immediately

	if detached.Err() != nil {
		t.Fatalf("Detach(ctx) must strip parent cancellation, got Err()=%v", detached.Err())
	}
	if got, err := Require(detached); err != nil || got != "acme" {
		t.Fatalf("Require(detached) = (%q, %v), want (acme, nil)", got, err)
	}
	if obs.RequestIDFrom(detached) != "req-100" || obs.CompanyIDFrom(detached) != "acme" || obs.UserIDFrom(detached) != "usr-42" {
		t.Fatalf("Detach lost obs metadata: req=%q comp=%q user=%q",
			obs.RequestIDFrom(detached), obs.CompanyIDFrom(detached), obs.UserIDFrom(detached))
	}
}

func TestValidateOrgSlugAndReservedWords(t *testing.T) {
	t.Parallel()

	slug, err := ValidateOrgSlug("  Acme-China  ")
	if err != nil || slug != "acme-china" {
		t.Fatalf("ValidateOrgSlug('  Acme-China  ') = (%q, %v), want (acme-china, nil)", slug, err)
	}

	invalid := []string{
		"",
		"   ",
		"ab",        // < 3 chars
		"1acme",     // starts with digit
		"acme-",     // ends with hyphen
		"acme_corp", // underscore not allowed in slug
		strings.Repeat("a", 33),
	}
	for _, raw := range invalid {
		if _, err := ValidateOrgSlug(raw); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
			t.Errorf("ValidateOrgSlug(%q) code = %q, want invalid_argument", raw, apperror.CodeOf(err))
		}
	}

	reserved := []string{
		"admin", "api", "www", "root", "system", "login",
		"mcp", "static", "support", "help", "default", "platform",
	}
	for _, r := range reserved {
		_, err := ValidateOrgSlug(r)
		if apperror.CodeOf(err) != apperror.CodeInvalidArgument || !strings.Contains(err.Error(), "保留词") {
			t.Errorf("ValidateOrgSlug(%q) = %v, want InvalidArg mentioning 保留词", r, err)
		}
	}
}

func TestTenantPathsAndSafeJoinBlocksTraversalAndSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Invalid company IDs
	for _, badID := range []string{"", "../evil", "a..b", "/abs", "bad/slash", "bad space"} {
		if err := ValidateCompanyID(badID); !errors.Is(err, ErrInvalidCompany) {
			t.Errorf("ValidateCompanyID(%q) = %v, want ErrInvalidCompany", badID, err)
		}
		if _, err := PathFor(root, badID); !errors.Is(err, ErrInvalidCompany) {
			t.Errorf("PathFor(%q) = %v, want ErrInvalidCompany", badID, err)
		}
	}

	ctx, err := WithCompany(context.Background(), "acme.corp-1")
	if err != nil {
		t.Fatalf("WithCompany: %v", err)
	}
	p, err := Path(ctx, root)
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if !strings.HasSuffix(p, filepath.Join(filepath.Base(root), "acme.corp-1")) {
		t.Fatalf("unexpected Path() = %q", p)
	}
	if _, err := Path(context.Background(), root); !errors.Is(err, ErrMissingTenant) {
		t.Fatalf("Path(no tenant) = %v, want ErrMissingTenant", err)
	}

	// SafeJoin lexical traversal & absolute part rejection
	if _, err := SafeJoin(root, "../evil"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("SafeJoin(../evil) = %v, want ErrPathEscape", err)
	}
	if _, err := SafeJoin(root, "sub", "../../outside"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("SafeJoin(sub/../../outside) = %v, want ErrPathEscape", err)
	}
	if _, err := SafeJoin(root, "/etc/passwd"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("SafeJoin(/etc/passwd) = %v, want ErrPathEscape", err)
	}

	okPath, err := SafeJoin(root, "acme", "notes", "doc.md")
	if err != nil {
		t.Fatalf("SafeJoin(valid) error = %v", err)
	}
	if !strings.HasPrefix(okPath, root) {
		t.Fatalf("SafeJoin(valid) = %q not under %q", okPath, root)
	}

	// Real on-disk symlink escape defense
	outsideDir := t.TempDir()
	escapeLink := filepath.Join(root, "escape")
	if err := os.Symlink(outsideDir, escapeLink); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}
	if _, err := SafeJoin(root, "escape", "secret.txt"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("SafeJoin(symlink escape) = %v, want ErrPathEscape", err)
	}

	// Dangling symlink pointing outside root must also be blocked
	danglingLink := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(outsideDir, "not-there-yet"), danglingLink); err != nil {
		t.Fatalf("os.Symlink(dangling): %v", err)
	}
	if _, err := SafeJoin(root, "dangling"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("SafeJoin(dangling symlink escape) = %v, want ErrPathEscape", err)
	}

	// Safe internal symlink inside root is allowed
	innerDir := filepath.Join(root, "real-inner")
	if err := os.MkdirAll(innerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	innerLink := filepath.Join(root, "link-inner")
	if err := os.Symlink(innerDir, innerLink); err != nil {
		t.Fatalf("os.Symlink(inner): %v", err)
	}
	if _, err := SafeJoin(root, "link-inner", "ok.txt"); err != nil {
		t.Fatalf("SafeJoin(internal symlink) unexpected error = %v", err)
	}

	// Empty baseDir and nil context edge cases
	if _, err := SafeJoin("   ", "a"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("SafeJoin(empty base) = %v, want ErrPathEscape", err)
	}
	if _, ok := Optional(nil); ok {
		t.Fatalf("Optional(nil) should be false")
	}
	nilDerived, err := WithCompany(nil, "acme")
	if err != nil {
		t.Fatalf("WithCompany(nil, acme) error = %v", err)
	}
	if got, ok := Optional(nilDerived); !ok || got != "acme" {
		t.Fatalf("Optional(nilDerived) = (%q, %v), want (acme, true)", got, ok)
	}
}
