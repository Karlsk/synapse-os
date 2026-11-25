package db

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

// MapError translates PostgreSQL / pgx errors into deterministic SA-01 apperror
// classifications without exposing raw SQL statements or secrets in external messages.
func MapError(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.NotFound(op + ": not found")
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "57014": // query_canceled (e.g. statement_timeout)
			return err
		case "23505": // unique_violation
			return apperror.Wrap(apperror.CodeConflict, http.StatusConflict, op+": already exists", err)
		case "23503": // foreign_key_violation
			return apperror.Wrap(apperror.CodeConflict, http.StatusConflict, op+": reference conflict", err)
		case "22P02", "22001", "23502", "23514":
			return apperror.Wrap(apperror.CodeInvalidArgument, http.StatusBadRequest, op+": invalid input", err)
		case "40001", "40P01": // serialization_failure / deadlock_detected
			return apperror.Wrap(apperror.CodeBusy, http.StatusConflict, op+": concurrent conflict, retry", err)
		}
	}

	return apperror.Internal(op+": database error", err)
}

// IsNoRows reports whether err wraps pgx.ErrNoRows.
func IsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// IsUniqueViolation reports whether err wraps a PostgreSQL 23505 unique_violation.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// IsForeignKeyViolation reports whether err wraps a PostgreSQL 23503 foreign_key_violation.
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
