//go:build deps_lock

// Package tools pins confirmed V1 core module dependencies in go.mod/go.sum
// while packages are scaffolded incrementally across SA-01..SA-42.
// This file is excluded from normal builds and tests unless -tags=deps_lock is passed.
// Remove this file once all pinned modules are directly imported by production packages.
package tools

import (
	_ "github.com/coder/websocket"
	_ "github.com/gin-gonic/gin"
	_ "github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/pressly/goose/v3"
	_ "github.com/prometheus/client_golang/prometheus"
	_ "github.com/robfig/cron/v3"
	_ "github.com/shopspring/decimal"
	_ "github.com/sony/gobreaker/v2"
	_ "golang.org/x/crypto/bcrypt"
	_ "golang.org/x/net/html"
	_ "golang.org/x/sync/errgroup"
	_ "gopkg.in/yaml.v3"
)
