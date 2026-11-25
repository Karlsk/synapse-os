// Package apperror defines the unified L0 domain error taxonomy for Synapse-OS.
// It intentionally depends only on the Go standard library so every layer (L0-L6)
// can share error codes and HTTP status semantics without import cycles.
package apperror

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Code is a stable machine-readable API error code compatible with /api/v1.
type Code string

const (
	CodeOK                     Code = "ok"
	CodeInvalidArgument        Code = "invalid_argument"
	CodeUnauthorized           Code = "unauthorized"
	CodePasswordChangeRequired Code = "password_change_required"
	CodeForbidden              Code = "forbidden"
	CodeNotFound               Code = "not_found"
	CodeMethodNotAllowed       Code = "method_not_allowed"
	CodeConflict               Code = "conflict"
	CodeBusy                   Code = "busy"
	CodeTooLarge               Code = "too_large"
	CodeRateLimited            Code = "rate_limited"
	CodeServiceUnavailable     Code = "service_unavailable"
	CodeInternal               Code = "internal_error"
)

// ErrEgressDenied is a sentinel error allowing callers to detect SSRF / egress blocks via errors.Is.
var ErrEgressDenied = errors.New("egress denied")

// AppError represents a classified domain error with HTTP status semantics and optional root cause.
type AppError struct {
	Code       Code
	HTTPStatus int
	Message    string
	Err        error
}

// Error returns the external-facing message, falling back to the wrapped error or error code.
func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}

// Unwrap returns the underlying cause for errors.Is / errors.As traversal.
func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is enables matching against &AppError{Code: ...} by non-empty error Code.
func (e *AppError) Is(target error) bool {
	if e == nil {
		return false
	}
	if t, ok := target.(*AppError); ok {
		return t != nil && t.Code != "" && t.Code == e.Code
	}
	return false
}

// InvalidArg constructs a 400 invalid_argument domain error.
func InvalidArg(msg string) *AppError {
	return &AppError{Code: CodeInvalidArgument, HTTPStatus: http.StatusBadRequest, Message: msg}
}

// Unauthorized constructs a 401 unauthorized domain error.
func Unauthorized(msg string) *AppError {
	return &AppError{Code: CodeUnauthorized, HTTPStatus: http.StatusUnauthorized, Message: msg}
}

// PasswordChangeRequired constructs a 403 password_change_required domain error.
func PasswordChangeRequired(msg string) *AppError {
	return &AppError{Code: CodePasswordChangeRequired, HTTPStatus: http.StatusForbidden, Message: msg}
}

// Forbidden constructs a 403 forbidden domain error.
func Forbidden(msg string) *AppError {
	return &AppError{Code: CodeForbidden, HTTPStatus: http.StatusForbidden, Message: msg}
}

// EgressDenied constructs a 403 forbidden error wrapping ErrEgressDenied.
func EgressDenied(msg string) *AppError {
	return &AppError{
		Code:       CodeForbidden,
		HTTPStatus: http.StatusForbidden,
		Message:    msg,
		Err:        ErrEgressDenied,
	}
}

// NotFound constructs a 404 not_found domain error.
func NotFound(msg string) *AppError {
	return &AppError{Code: CodeNotFound, HTTPStatus: http.StatusNotFound, Message: msg}
}

// MethodNotAllowed constructs a 405 method_not_allowed domain error.
func MethodNotAllowed(msg string) *AppError {
	return &AppError{Code: CodeMethodNotAllowed, HTTPStatus: http.StatusMethodNotAllowed, Message: msg}
}

// Conflict constructs a 409 conflict domain error.
func Conflict(msg string) *AppError {
	return &AppError{Code: CodeConflict, HTTPStatus: http.StatusConflict, Message: msg}
}

// Busy constructs a 409 busy domain error.
func Busy(msg string) *AppError {
	return &AppError{Code: CodeBusy, HTTPStatus: http.StatusConflict, Message: msg}
}

// TooLarge constructs a 413 too_large domain error.
func TooLarge(msg string) *AppError {
	return &AppError{Code: CodeTooLarge, HTTPStatus: http.StatusRequestEntityTooLarge, Message: msg}
}

// RateLimited constructs a 429 rate_limited domain error.
func RateLimited(msg string) *AppError {
	return &AppError{Code: CodeRateLimited, HTTPStatus: http.StatusTooManyRequests, Message: msg}
}

// ServiceUnavailable constructs a 503 service_unavailable domain error wrapping cause.
func ServiceUnavailable(msg string, cause error) *AppError {
	return &AppError{
		Code:       CodeServiceUnavailable,
		HTTPStatus: http.StatusServiceUnavailable,
		Message:    msg,
		Err:        cause,
	}
}

// Internal constructs a 500 internal_error domain error wrapping cause.
func Internal(msg string, cause error) *AppError {
	return &AppError{
		Code:       CodeInternal,
		HTTPStatus: http.StatusInternalServerError,
		Message:    msg,
		Err:        cause,
	}
}

// Wrap constructs a custom AppError preserving cause for server-side inspection.
func Wrap(code Code, httpStatus int, msg string, cause error) *AppError {
	return &AppError{
		Code:       code,
		HTTPStatus: httpStatus,
		Message:    msg,
		Err:        cause,
	}
}

// FromError normalizes any error into an *AppError. Unknown errors are masked
// behind a generic 500 message to prevent leaking internal details to clients.
func FromError(err error) *AppError {
	if err == nil {
		return nil
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	var materialsErr *UnreadableMaterialsError
	if errors.As(err, &materialsErr) {
		return materialsErr.AsAppError()
	}
	return Internal("internal server error", err)
}

// HTTPStatusOf returns the HTTP status code associated with err (200 for nil).
func HTTPStatusOf(err error) int {
	if err == nil {
		return http.StatusOK
	}
	return FromError(err).HTTPStatus
}

// CodeOf returns the machine-readable Code associated with err (CodeOK for nil).
func CodeOf(err error) Code {
	if err == nil {
		return CodeOK
	}
	return FromError(err).Code
}

// UnreadableLinkError describes a single reference URL that could not be fetched or read.
type UnreadableLinkError struct {
	URL    string
	Reason string
	Hint   string
}

// Error returns the failure reason, falling back to URL when empty.
func (e *UnreadableLinkError) Error() string {
	if e == nil {
		return ""
	}
	if e.Reason != "" {
		return e.Reason
	}
	return e.URL
}

// UnreadableMaterialsError aggregates unreadable material links prior to task creation.
type UnreadableMaterialsError struct {
	Items []*UnreadableLinkError
}

// Error formats the unreadable links into the canonical Chinese multi-line user prompt.
func (e *UnreadableMaterialsError) Error() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "任务没有创建：有 %d 份资料读不到，先解决再建任务。\n", len(e.Items))
	for _, item := range e.Items {
		if item == nil {
			continue
		}
		fmt.Fprintf(&b, "- %s：%s", item.URL, item.Reason)
		if h := strings.TrimSpace(item.Hint); h != "" {
			fmt.Fprintf(&b, "（%s）", h)
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

// AsAppError converts the unreadable materials failure into an HTTP 409 conflict AppError.
func (e *UnreadableMaterialsError) AsAppError() *AppError {
	return &AppError{
		Code:       CodeConflict,
		HTTPStatus: http.StatusConflict,
		Message:    e.Error(),
		Err:        e,
	}
}
