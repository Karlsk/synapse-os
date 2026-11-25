package db

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

func TestMapErrorDeterministicMappings(t *testing.T) {
	t.Parallel()

	if got := MapError("find_user", nil); got != nil {
		t.Fatalf("MapError(nil) = %v, want nil", got)
	}

	tests := []struct {
		name           string
		err            error
		wantCode       apperror.Code
		wantStatus     int
		wantPassthru   bool
		wantPgAs       bool
		forbidContains string
	}{
		{
			name:       "pgx.ErrNoRows",
			err:        fmt.Errorf("query: %w", pgx.ErrNoRows),
			wantCode:   apperror.CodeNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "23505 unique_violation",
			err:        &pgconn.PgError{Code: "23505", ConstraintName: "uq_app_user_username", Detail: "Key (username)=(alice) already exists."},
			wantCode:   apperror.CodeConflict,
			wantStatus: http.StatusConflict,
			wantPgAs:   true,
		},
		{
			name:       "23503 foreign_key_violation",
			err:        &pgconn.PgError{Code: "23503", ConstraintName: "fk_item_owner"},
			wantCode:   apperror.CodeConflict,
			wantStatus: http.StatusConflict,
			wantPgAs:   true,
		},
		{
			name:       "22P02 invalid_text_representation",
			err:        &pgconn.PgError{Code: "22P02", Message: "invalid input syntax for type uuid"},
			wantCode:   apperror.CodeInvalidArgument,
			wantStatus: http.StatusBadRequest,
			wantPgAs:   true,
		},
		{
			name:       "22001 string_data_right_truncation",
			err:        &pgconn.PgError{Code: "22001", Message: "value too long"},
			wantCode:   apperror.CodeInvalidArgument,
			wantStatus: http.StatusBadRequest,
			wantPgAs:   true,
		},
		{
			name:       "23502 not_null_violation",
			err:        &pgconn.PgError{Code: "23502", ColumnName: "title"},
			wantCode:   apperror.CodeInvalidArgument,
			wantStatus: http.StatusBadRequest,
			wantPgAs:   true,
		},
		{
			name:       "23514 check_violation",
			err:        &pgconn.PgError{Code: "23514", ConstraintName: "chk_progress"},
			wantCode:   apperror.CodeInvalidArgument,
			wantStatus: http.StatusBadRequest,
			wantPgAs:   true,
		},
		{
			name:       "40001 serialization_failure",
			err:        &pgconn.PgError{Code: "40001"},
			wantCode:   apperror.CodeBusy,
			wantStatus: http.StatusConflict,
			wantPgAs:   true,
		},
		{
			name:       "40P01 deadlock_detected",
			err:        &pgconn.PgError{Code: "40P01"},
			wantCode:   apperror.CodeBusy,
			wantStatus: http.StatusConflict,
			wantPgAs:   true,
		},
		{
			name:         "57014 query_canceled passthrough",
			err:          &pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"},
			wantPassthru: true,
		},
		{
			name:         "context.Canceled passthrough",
			err:          fmt.Errorf("exec: %w", context.Canceled),
			wantPassthru: true,
		},
		{
			name:         "context.DeadlineExceeded passthrough",
			err:          fmt.Errorf("exec: %w", context.DeadlineExceeded),
			wantPassthru: true,
		},
		{
			name:           "unknown error masked as 500 internal_error",
			err:            errors.New("SELECT secret_cipher FROM provider WHERE password='leak' failed"),
			wantCode:       apperror.CodeInternal,
			wantStatus:     http.StatusInternalServerError,
			forbidContains: "secret_cipher",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := MapError("create_item", tc.err)
			if tc.wantPassthru {
				if !errors.Is(got, tc.err) {
					t.Fatalf("expected passthrough error %v, got %v", tc.err, got)
				}
				var appErr *apperror.AppError
				if errors.As(got, &appErr) {
					t.Fatalf("passthrough error must not be wrapped as *apperror.AppError: %+v", appErr)
				}
				return
			}

			var appErr *apperror.AppError
			if !errors.As(got, &appErr) {
				t.Fatalf("expected *apperror.AppError, got %T (%v)", got, got)
			}
			if appErr.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", appErr.Code, tc.wantCode)
			}
			if appErr.HTTPStatus != tc.wantStatus {
				t.Errorf("HTTPStatus = %d, want %d", appErr.HTTPStatus, tc.wantStatus)
			}
			if tc.wantPgAs {
				var pgErr *pgconn.PgError
				if !errors.As(got, &pgErr) {
					t.Errorf("expected errors.As(*pgconn.PgError) to succeed")
				}
			}
			if tc.forbidContains != "" && strings.Contains(appErr.Message, tc.forbidContains) {
				t.Errorf("AppError.Message leaked sensitive SQL: %q", appErr.Message)
			}
		})
	}
}

func TestPGErrorPredicates(t *testing.T) {
	t.Parallel()

	noRows := fmt.Errorf("wrap: %w", pgx.ErrNoRows)
	uniq := MapError("insert", &pgconn.PgError{Code: "23505"})
	fk := MapError("insert", &pgconn.PgError{Code: "23503"})
	other := errors.New("other")

	if !IsNoRows(noRows) || IsNoRows(other) || IsNoRows(nil) {
		t.Errorf("IsNoRows predicate mismatch")
	}
	if !IsUniqueViolation(uniq) || IsUniqueViolation(fk) || IsUniqueViolation(other) {
		t.Errorf("IsUniqueViolation predicate mismatch")
	}
	if !IsForeignKeyViolation(fk) || IsForeignKeyViolation(uniq) || IsForeignKeyViolation(other) {
		t.Errorf("IsForeignKeyViolation predicate mismatch")
	}
}
