package tenant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var safeCompanyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// ValidateCompanyID checks that id conforms to ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$
// and contains no ".." path traversal segments.
func ValidateCompanyID(id string) error {
	if id == "" || strings.Contains(id, "..") || !safeCompanyRe.MatchString(id) {
		return fmt.Errorf("%w: %q", ErrInvalidCompany, id)
	}
	return nil
}

// Path resolves the tenant-isolated subdirectory under root using the tenant
// attached to ctx. It fails closed with ErrMissingTenant if ctx has no tenant.
func Path(ctx context.Context, root string) (string, error) {
	companyID, err := Require(ctx)
	if err != nil {
		return "", err
	}
	return PathFor(root, companyID)
}

// PathFor resolves the tenant-isolated subdirectory root/<companyID> after
// validating companyID and ensuring the path cannot escape root.
func PathFor(root, companyID string) (string, error) {
	if err := ValidateCompanyID(companyID); err != nil {
		return "", err
	}
	return SafeJoin(root, companyID)
}

// SafeJoin safely joins relParts beneath baseDir, rejecting absolute relative
// segments, lexical ".." escapes, and on-disk symlink escapes via EvalSymlinks.
func SafeJoin(baseDir string, relParts ...string) (string, error) {
	if strings.TrimSpace(baseDir) == "" {
		return "", fmt.Errorf("%w: empty base dir", ErrPathEscape)
	}
	for _, part := range relParts {
		if filepath.IsAbs(part) || strings.HasPrefix(part, "/") || strings.HasPrefix(part, `\`) {
			return "", fmt.Errorf("%w: absolute segment %q", ErrPathEscape, part)
		}
	}

	absBase, err := filepath.Abs(filepath.Clean(baseDir))
	if err != nil {
		return "", fmt.Errorf("%w: invalid base dir: %v", ErrPathEscape, err)
	}
	parts := make([]string, 0, len(relParts)+1)
	parts = append(parts, absBase)
	parts = append(parts, relParts...)
	target := filepath.Clean(filepath.Join(parts...))

	if !isWithinBase(absBase, target) {
		return "", fmt.Errorf("%w: %q escapes %q", ErrPathEscape, target, absBase)
	}

	// Verify real filesystem paths when baseDir exists to defeat symlink escapes,
	// checking the nearest existing ancestor when the leaf has not been created yet.
	if realBase, err := filepath.EvalSymlinks(absBase); err == nil {
		if err := verifyNoSymlinkEscape(absBase, realBase, target); err != nil {
			return "", err
		}
	}
	return target, nil
}

func isWithinBase(base, candidate string) bool {
	rel, err := filepath.Rel(base, candidate)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	return true
}

func verifyNoSymlinkEscape(absBase, realBase, target string) error {
	cur := target
	for {
		info, lerr := os.Lstat(cur)
		if lerr == nil {
			realCur, err := filepath.EvalSymlinks(cur)
			if err == nil {
				if !isWithinBase(realBase, realCur) {
					return fmt.Errorf("%w: symlink %q escapes %q", ErrPathEscape, realCur, realBase)
				}
				return nil
			}
			// Handle dangling symlinks pointing outside realBase.
			if info.Mode()&os.ModeSymlink != 0 {
				linkTarget, rerr := os.Readlink(cur)
				if rerr != nil {
					return fmt.Errorf("%w: unreadable symlink %q", ErrPathEscape, cur)
				}
				if !filepath.IsAbs(linkTarget) {
					linkTarget = filepath.Join(filepath.Dir(cur), linkTarget)
				}
				linkTarget = filepath.Clean(linkTarget)
				if !isWithinBase(realBase, linkTarget) && !isWithinBase(absBase, linkTarget) {
					return fmt.Errorf("%w: dangling symlink %q escapes %q", ErrPathEscape, linkTarget, realBase)
				}
				return nil
			}
			return fmt.Errorf("%w: cannot resolve %q: %v", ErrPathEscape, cur, err)
		}
		if cur == absBase {
			return nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil
		}
		cur = parent
	}
}
