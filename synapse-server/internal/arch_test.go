package internal_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePrefix = "github.com/Karlsk/synapse-os/"

// layerOfTopPkg defines the strict L0-L5 architectural layers under internal/.
// cmd/synapse-server is the L6 wiring root and may import any lower layer.
var layerOfTopPkg = map[string]int{
	"core":         0, // L0: apperror, tenant, crypto, egress, eventbus + pure contracts model/store/spi/api
	"infra":        0, // L0: config, obs, lifecycle, db, httpclient, memcache
	"storage":      1, // L1: postgres Store implementations
	"runtime":      1, // L1: launcher, runner, claude, codex, gateway, mcp, attachment
	"platform":     2, // L2: tenant, user, auth, settings, notice, integration, connector, provider, analytics
	"files":        2, // L2
	"project":      2, // L2
	"skills":       2, // L2
	"agent":        3, // L3
	"task":         3, // L3
	"cost":         3, // L3
	"docs":         3, // L3
	"event":        4, // L4
	"orchestrator": 4, // L4
	"web":          5, // L5
	"ui":           5, // L5
}

func TestArchitectureGuard(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	// Scan go.mod for Rule 6 (Zero Redis).
	goModPath := filepath.Join(repoRoot, "go.mod")
	goModBytes, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for _, v := range checkGoMod(string(goModBytes)) {
		t.Errorf("go.mod violation: %s", v)
	}

	// Scan all non-test Go files under internal/ and cmd/ (when present).
	roots := []string{"internal", "cmd"}
	fset := token.NewFileSet()
	checkedFiles := 0
	for _, dir := range roots {
		absDir := filepath.Join(repoRoot, dir)
		if _, err := os.Stat(absDir); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(absDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			relFile, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			srcRelPkg := filepath.ToSlash(filepath.Dir(relFile))
			fileAST, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				t.Errorf("parse %s: %v", relFile, err)
				return nil
			}
			checkedFiles++
			for _, imp := range fileAST.Imports {
				impPath, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Errorf("unquote import in %s: %v", relFile, err)
					continue
				}
				if violation := checkImport(srcRelPkg, impPath); violation != "" {
					t.Errorf("%s: %s", relFile, violation)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if checkedFiles == 0 {
		t.Fatalf("expected to inspect production Go files, checked 0")
	}
}

func TestArchRulesDetectViolations(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		srcRelPkg string
		source    string
		wantRule  string
	}{
		{
			name:      "Rule3_agent_imports_task",
			srcRelPkg: "internal/agent/chat",
			source:    `package chat; import _ "github.com/Karlsk/synapse-os/internal/task"`,
			wantRule:  "Rule 3",
		},
		{
			name:      "Rule3_task_imports_agent",
			srcRelPkg: "internal/task/watchdog",
			source:    `package watchdog; import _ "github.com/Karlsk/synapse-os/internal/agent"`,
			wantRule:  "Rule 3",
		},
		{
			name:      "Rule3_storage_imports_runtime",
			srcRelPkg: "internal/storage/postgres",
			source:    `package postgres; import _ "github.com/Karlsk/synapse-os/internal/runtime/launcher"`,
			wantRule:  "Rule 3",
		},
		{
			name:      "Rule3_platform_imports_files",
			srcRelPkg: "internal/platform/auth",
			source:    `package auth; import _ "github.com/Karlsk/synapse-os/internal/files"`,
			wantRule:  "Rule 3",
		},
		{
			name:      "Rule3_event_imports_orchestrator",
			srcRelPkg: "internal/event/feishu",
			source:    `package feishu; import _ "github.com/Karlsk/synapse-os/internal/orchestrator"`,
			wantRule:  "Rule 3",
		},
		{
			name:      "Rule3_web_imports_ui",
			srcRelPkg: "internal/web/server",
			source:    `package server; import _ "github.com/Karlsk/synapse-os/internal/ui"`,
			wantRule:  "Rule 3",
		},
		{
			name:      "Rule1_L1_imports_L2",
			srcRelPkg: "internal/storage/postgres",
			source:    `package postgres; import _ "github.com/Karlsk/synapse-os/internal/platform/tenant"`,
			wantRule:  "Rule 1",
		},
		{
			name:      "Rule1_infra_imports_L1",
			srcRelPkg: "internal/infra/httpclient",
			source:    `package httpclient; import _ "github.com/Karlsk/synapse-os/internal/runtime/mcp"`,
			wantRule:  "Rule 1",
		},
		{
			name:      "Rule2_core_imports_infra_db",
			srcRelPkg: "internal/core/crypto",
			source:    `package crypto; import _ "github.com/Karlsk/synapse-os/internal/infra/db"`,
			wantRule:  "Rule 2",
		},
		{
			name:      "Rule2_apperror_imports_obs",
			srcRelPkg: "internal/core/apperror",
			source:    `package apperror; import _ "github.com/Karlsk/synapse-os/internal/infra/obs"`,
			wantRule:  "Rule 2",
		},
		{
			name:      "Rule2_core_imports_database_sql",
			srcRelPkg: "internal/core/tenant",
			source:    `package tenant; import _ "database/sql"`,
			wantRule:  "Rule 2",
		},
		{
			name:      "Rule4_web_imports_pgx",
			srcRelPkg: "internal/web/handler",
			source:    `package handler; import _ "github.com/jackc/pgx/v5/pgxpool"`,
			wantRule:  "Rule 4",
		},
		{
			name:      "Rule4_platform_imports_goose",
			srcRelPkg: "internal/platform/tenant",
			source:    `package tenant; import _ "github.com/pressly/goose/v3"`,
			wantRule:  "Rule 4",
		},
		{
			name:      "Rule5_platform_imports_os_exec",
			srcRelPkg: "internal/platform/provider/installer",
			source:    `package installer; import _ "os/exec"`,
			wantRule:  "Rule 5",
		},
		{
			name:      "Rule6_redis_driver_import",
			srcRelPkg: "internal/infra/memcache",
			source:    `package memcache; import _ "github.com/redis/go-redis/v9"`,
			wantRule:  "Rule 6",
		},
	}

	fset := token.NewFileSet()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fileAST, err := parser.ParseFile(fset, tc.name+".go", tc.source, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			var got string
			for _, imp := range fileAST.Imports {
				impPath, _ := strconv.Unquote(imp.Path.Value)
				if v := checkImport(tc.srcRelPkg, impPath); v != "" {
					got = v
					break
				}
			}
			if !strings.Contains(got, tc.wantRule) {
				t.Fatalf("checkImport(%s) = %q, want violation containing %q", tc.srcRelPkg, got, tc.wantRule)
			}
		})
	}

	// Verify go.mod Rule 6 catches Redis drivers.
	fakeGoMod := "module github.com/Karlsk/synapse-os\n\nrequire (\n\tgithub.com/redis/go-redis/v9 v9.7.0\n)\n"
	if violations := checkGoMod(fakeGoMod); len(violations) == 0 {
		t.Fatalf("expected checkGoMod to detect github.com/redis/go-redis/v9")
	}

	// Verify sanctioned imports remain allowed.
	allowed := []struct {
		src string
		imp string
	}{
		{"internal/core/tenant", "github.com/Karlsk/synapse-os/internal/infra/obs"},
		{"internal/infra/db", "github.com/jackc/pgx/v5/pgxpool"},
		{"internal/storage/postgres", "github.com/pressly/goose/v3"},
		{"cmd/synapse-server", "github.com/jackc/pgx/v5"},
		{"internal/runtime/launcher", "os/exec"},
		{"internal/agent/chat", "github.com/Karlsk/synapse-os/internal/core/eventbus"},
		{"cmd/synapse-server", "github.com/Karlsk/synapse-os/internal/web/server"},
	}
	for _, tc := range allowed {
		if v := checkImport(tc.src, tc.imp); v != "" {
			t.Errorf("sanctioned import %s -> %s unexpectedly rejected: %s", tc.src, tc.imp, v)
		}
	}
}

func checkImport(srcRelPkg, impPath string) string {
	// Rule 6: Zero Redis anywhere in repository.
	if isRedisPath(impPath) {
		return "Rule 6 (zero Redis): forbidden Redis import " + impPath
	}

	// Rule 4: pgx / goose restricted to internal/infra/db, internal/storage/postgres, cmd/synapse-server.
	if isDBDriverImport(impPath) {
		if !isPkgOrSub(srcRelPkg, "internal/infra/db") &&
			!isPkgOrSub(srcRelPkg, "internal/storage/postgres") &&
			!isPkgOrSub(srcRelPkg, "cmd/synapse-server") {
			return "Rule 4 (DB driver isolation): " + srcRelPkg + " MUST NOT import " + impPath
		}
	}

	// Rule 5: os/exec restricted strictly to internal/runtime/launcher.
	if impPath == "os/exec" {
		if !isPkgOrSub(srcRelPkg, "internal/runtime/launcher") {
			return "Rule 5 (subprocess isolation): " + srcRelPkg + " MUST NOT import os/exec directly"
		}
	}

	// Rule 2: L0 core purity & sub-DAG.
	if isPkgOrSub(srcRelPkg, "internal/core/apperror") {
		if strings.HasPrefix(impPath, modulePrefix) {
			return "Rule 2 (L0 apperror purity): internal/core/apperror MUST NOT import internal package " + impPath
		}
	}
	if isPkgOrSub(srcRelPkg, "internal/core") {
		if isForbiddenCoreExternal(impPath) {
			return "Rule 2 (L0 core purity): " + srcRelPkg + " MUST NOT import " + impPath
		}
		if strings.HasPrefix(impPath, modulePrefix+"internal/infra") {
			relTarget := strings.TrimPrefix(impPath, modulePrefix)
			allowedObs := isPkgOrSub(srcRelPkg, "internal/core/tenant") && isPkgOrSub(relTarget, "internal/infra/obs")
			if !allowedObs {
				return "Rule 2 (L0 core sub-DAG): " + srcRelPkg + " MUST NOT import " + relTarget
			}
		}
	}

	// Rules 1 & 3: L0-L5 unidirectional layering and universal L1-L5 same-layer module isolation.
	if strings.HasPrefix(srcRelPkg, "internal/") && strings.HasPrefix(impPath, modulePrefix+"internal/") {
		srcTop := topInternalPkg(srcRelPkg)
		dstRel := strings.TrimPrefix(impPath, modulePrefix)
		dstTop := topInternalPkg(dstRel)

		srcLayer, okSrc := layerOfTopPkg[srcTop]
		if !okSrc {
			return "Rule 1 (unknown layer): unmapped source top package internal/" + srcTop
		}
		dstLayer, okDst := layerOfTopPkg[dstTop]
		if !okDst {
			return "Rule 1 (unknown layer): unmapped target top package internal/" + dstTop
		}
		if dstLayer > srcLayer {
			return "Rule 1 (unidirectional layering): L" + strconv.Itoa(srcLayer) + " (" + srcRelPkg + ") MUST NOT import L" + strconv.Itoa(dstLayer) + " (" + dstRel + ")"
		}
		if srcLayer >= 1 && dstLayer == srcLayer && srcTop != dstTop {
			return "Rule 3 (same-layer isolation): L" + strconv.Itoa(srcLayer) + " module " + srcTop + " (" + srcRelPkg + ") MUST NOT import sibling module " + dstTop + " (" + dstRel + ")"
		}
	}

	return ""
}

func checkGoMod(content string) []string {
	var violations []string
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		modPath := fields[0]
		if modPath == "require" && len(fields) >= 2 {
			modPath = fields[1]
		}
		if isRedisPath(modPath) {
			violations = append(violations, "Rule 6 (zero Redis): forbidden Redis module in go.mod: "+modPath)
		}
	}
	return violations
}

func isPkgOrSub(pkg, prefix string) bool {
	return pkg == prefix || strings.HasPrefix(pkg, prefix+"/")
}

func topInternalPkg(relPkg string) string {
	trimmed := strings.TrimPrefix(relPkg, "internal/")
	if idx := strings.IndexByte(trimmed, '/'); idx >= 0 {
		return trimmed[:idx]
	}
	return trimmed
}

func isDBDriverImport(impPath string) bool {
	return impPath == "github.com/jackc/pgx/v5" ||
		strings.HasPrefix(impPath, "github.com/jackc/pgx/") ||
		impPath == "github.com/pressly/goose/v3" ||
		strings.HasPrefix(impPath, "github.com/pressly/goose/")
}

func isForbiddenCoreExternal(impPath string) bool {
	return impPath == "database/sql" ||
		impPath == "os/exec" ||
		impPath == "github.com/gin-gonic/gin" ||
		strings.HasPrefix(impPath, "github.com/gin-gonic/gin/") ||
		isDBDriverImport(impPath)
}

func isRedisPath(p string) bool {
	lower := strings.ToLower(p)
	forbidden := []string{
		"github.com/redis/go-redis",
		"github.com/go-redis/redis",
		"github.com/gomodule/redigo",
		"github.com/redis/rueidis",
		"github.com/mediocregopher/radix",
		"github.com/alicebob/miniredis",
	}
	for _, f := range forbidden {
		if strings.Contains(lower, f) {
			return true
		}
	}
	for seg := range strings.SplitSeq(lower, "/") {
		if seg == "redis" || seg == "go-redis" || seg == "redigo" || seg == "rueidis" {
			return true
		}
	}
	return false
}
