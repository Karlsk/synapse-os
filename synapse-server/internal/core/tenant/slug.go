package tenant

import (
	"regexp"
	"strings"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

var (
	orgSlugRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)
	reservedSlugs = map[string]struct{}{
		"admin": {}, "api": {}, "www": {}, "root": {}, "system": {}, "login": {},
		"mcp": {}, "static": {}, "support": {}, "help": {}, "default": {}, "platform": {},
	}
)

// ValidateOrgSlug normalizes raw (trim + lowercase) and validates it against
// the organization slug format and the 12 reserved system slugs.
// Error messages match Java OrganizationService.java verbatim.
func ValidateOrgSlug(raw string) (string, error) {
	org := strings.ToLower(strings.TrimSpace(raw))
	if org == "" {
		return "", apperror.InvalidArg("请填写组织的英文名")
	}
	if !orgSlugRe.MatchString(org) {
		return "", apperror.InvalidArg("英文名只能用小写字母、数字和连字符，以字母开头，3 到 32 位，例如 acme 或 acme-china")
	}
	if _, reserved := reservedSlugs[org]; reserved {
		return "", apperror.InvalidArg("英文名「" + org + "」是保留词，请换一个")
	}
	return org, nil
}
