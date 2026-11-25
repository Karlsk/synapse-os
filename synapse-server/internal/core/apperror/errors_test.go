package apperror

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestStandardConstructorsAndStatusMapping(t *testing.T) {
	t.Parallel()

	cause := errors.New("underlying cause")

	tests := []struct {
		name       string
		err        *AppError
		wantCode   Code
		wantStatus int
		wantMsg    string
		wantCause  error
	}{
		{
			name:       "InvalidArg",
			err:        InvalidArg("bad input"),
			wantCode:   CodeInvalidArgument,
			wantStatus: http.StatusBadRequest,
			wantMsg:    "bad input",
		},
		{
			name:       "Unauthorized",
			err:        Unauthorized("not logged in"),
			wantCode:   CodeUnauthorized,
			wantStatus: http.StatusUnauthorized,
			wantMsg:    "not logged in",
		},
		{
			name:       "PasswordChangeRequired",
			err:        PasswordChangeRequired("must change password"),
			wantCode:   CodePasswordChangeRequired,
			wantStatus: http.StatusForbidden,
			wantMsg:    "must change password",
		},
		{
			name:       "Forbidden",
			err:        Forbidden("denied"),
			wantCode:   CodeForbidden,
			wantStatus: http.StatusForbidden,
			wantMsg:    "denied",
		},
		{
			name:       "EgressDenied",
			err:        EgressDenied("private ip blocked"),
			wantCode:   CodeForbidden,
			wantStatus: http.StatusForbidden,
			wantMsg:    "private ip blocked",
			wantCause:  ErrEgressDenied,
		},
		{
			name:       "NotFound",
			err:        NotFound("missing resource"),
			wantCode:   CodeNotFound,
			wantStatus: http.StatusNotFound,
			wantMsg:    "missing resource",
		},
		{
			name:       "MethodNotAllowed",
			err:        MethodNotAllowed("get not allowed"),
			wantCode:   CodeMethodNotAllowed,
			wantStatus: http.StatusMethodNotAllowed,
			wantMsg:    "get not allowed",
		},
		{
			name:       "Conflict",
			err:        Conflict("already exists"),
			wantCode:   CodeConflict,
			wantStatus: http.StatusConflict,
			wantMsg:    "already exists",
		},
		{
			name:       "Busy",
			err:        Busy("session busy"),
			wantCode:   CodeBusy,
			wantStatus: http.StatusConflict,
			wantMsg:    "session busy",
		},
		{
			name:       "TooLarge",
			err:        TooLarge("单个文件不能超过 50 MB"),
			wantCode:   CodeTooLarge,
			wantStatus: http.StatusRequestEntityTooLarge,
			wantMsg:    "单个文件不能超过 50 MB",
		},
		{
			name:       "RateLimited",
			err:        RateLimited("too many attempts"),
			wantCode:   CodeRateLimited,
			wantStatus: http.StatusTooManyRequests,
			wantMsg:    "too many attempts",
		},
		{
			name:       "ServiceUnavailable",
			err:        ServiceUnavailable("breaker open", cause),
			wantCode:   CodeServiceUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantMsg:    "breaker open",
			wantCause:  cause,
		},
		{
			name:       "Internal",
			err:        Internal("internal failure", cause),
			wantCode:   CodeInternal,
			wantStatus: http.StatusInternalServerError,
			wantMsg:    "internal failure",
			wantCause:  cause,
		},
		{
			name:       "Wrap",
			err:        Wrap(CodeConflict, http.StatusConflict, "wrapped conflict", cause),
			wantCode:   CodeConflict,
			wantStatus: http.StatusConflict,
			wantMsg:    "wrapped conflict",
			wantCause:  cause,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.err.Code != tc.wantCode {
				t.Fatalf("Code = %q, want %q", tc.err.Code, tc.wantCode)
			}
			if tc.err.HTTPStatus != tc.wantStatus {
				t.Fatalf("HTTPStatus = %d, want %d", tc.err.HTTPStatus, tc.wantStatus)
			}
			if tc.err.Error() != tc.wantMsg {
				t.Fatalf("Error() = %q, want %q", tc.err.Error(), tc.wantMsg)
			}
			if HTTPStatusOf(tc.err) != tc.wantStatus {
				t.Fatalf("HTTPStatusOf() = %d, want %d", HTTPStatusOf(tc.err), tc.wantStatus)
			}
			if CodeOf(tc.err) != tc.wantCode {
				t.Fatalf("CodeOf() = %q, want %q", CodeOf(tc.err), tc.wantCode)
			}
			if !errors.Is(tc.err, &AppError{Code: tc.wantCode}) {
				t.Fatalf("errors.Is did not match Code %q", tc.wantCode)
			}
			if tc.wantCause != nil && !errors.Is(tc.err, tc.wantCause) {
				t.Fatalf("errors.Is did not match cause %v", tc.wantCause)
			}
		})
	}
}

func TestEgressDeniedSentinelAndWrappedChain(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("fetch url: %w", EgressDenied("这是内网或本机地址，不能读取"))
	if !errors.Is(err, ErrEgressDenied) {
		t.Fatalf("expected errors.Is(err, ErrEgressDenied) == true")
	}
	if !errors.Is(err, &AppError{Code: CodeForbidden}) {
		t.Fatalf("expected errors.Is(err, CodeForbidden) == true")
	}
	if got := HTTPStatusOf(err); got != http.StatusForbidden {
		t.Fatalf("HTTPStatusOf = %d, want 403", got)
	}
	if got := CodeOf(err); got != CodeForbidden {
		t.Fatalf("CodeOf = %q, want %q", got, CodeForbidden)
	}
}

func TestFromErrorSanitizesUnknownErrorsAndHandlesNil(t *testing.T) {
	t.Parallel()

	if FromError(nil) != nil {
		t.Fatalf("FromError(nil) must return nil")
	}
	if got := HTTPStatusOf(nil); got != http.StatusOK {
		t.Fatalf("HTTPStatusOf(nil) = %d, want 200", got)
	}
	if got := CodeOf(nil); got != CodeOK {
		t.Fatalf("CodeOf(nil) = %q, want %q", got, CodeOK)
	}

	var nilAppErr *AppError
	if got := nilAppErr.Error(); got != "" {
		t.Fatalf("(*AppError)(nil).Error() = %q, want empty", got)
	}
	if got := (&AppError{Code: CodeNotFound}).Error(); got != string(CodeNotFound) {
		t.Fatalf("fallback Error() = %q, want %q", got, CodeNotFound)
	}
	if got := (&AppError{Code: CodeInternal, Err: errors.New("boom")}).Error(); got != "boom" {
		t.Fatalf("err-fallback Error() = %q, want boom", got)
	}
	if (&AppError{Code: CodeNotFound}).Is(errors.New("other")) {
		t.Fatalf("Is(non-AppError) must return false")
	}

	raw := errors.New("pg password=supersecret host=10.0.0.1 failed")
	appErr := FromError(raw)
	if appErr.HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("HTTPStatus = %d, want 500", appErr.HTTPStatus)
	}
	if appErr.Code != CodeInternal {
		t.Fatalf("Code = %q, want %q", appErr.Code, CodeInternal)
	}
	if appErr.Message != "internal server error" {
		t.Fatalf("Message = %q, want generic safe message", appErr.Message)
	}
	if strings.Contains(appErr.Message, "supersecret") || strings.Contains(appErr.Message, "10.0.0.1") {
		t.Fatalf("unknown error leaked internal details: %q", appErr.Message)
	}
	if !errors.Is(appErr, raw) {
		t.Fatalf("FromError must retain original cause via Unwrap")
	}
}

func TestUnreadableMaterialsErrorFormattingAndConversion(t *testing.T) {
	t.Parallel()

	link1 := &UnreadableLinkError{
		URL:    "https://docs.feishu.cn/docx/abc",
		Reason: "没有访问权限",
		Hint:   "请先授权飞书文档",
	}
	link2 := &UnreadableLinkError{
		URL:    "https://example.com/404",
		Reason: "页面不存在",
		Hint:   "   ",
	}

	if got := link1.Error(); got != "没有访问权限" {
		t.Fatalf("UnreadableLinkError.Error() = %q, want %q", got, "没有访问权限")
	}
	if got := (&UnreadableLinkError{URL: "https://a.test"}).Error(); got != "https://a.test" {
		t.Fatalf("fallback UnreadableLinkError.Error() = %q, want URL", got)
	}

	matErr := &UnreadableMaterialsError{Items: []*UnreadableLinkError{link1, link2}}
	want := "任务没有创建：有 2 份资料读不到，先解决再建任务。\n" +
		"- https://docs.feishu.cn/docx/abc：没有访问权限（请先授权飞书文档）\n" +
		"- https://example.com/404：页面不存在"
	if got := matErr.Error(); got != want {
		t.Fatalf("UnreadableMaterialsError.Error() =\n%s\nwant:\n%s", got, want)
	}

	wrapped := fmt.Errorf("preflight: %w", matErr)
	appErr := FromError(wrapped)
	if appErr.HTTPStatus != http.StatusConflict || appErr.Code != CodeConflict {
		t.Fatalf("FromError(UnreadableMaterialsError) = (%d, %q), want (409, conflict)", appErr.HTTPStatus, appErr.Code)
	}
	if appErr.Message != want {
		t.Fatalf("AsAppError Message mismatch:\n%s", appErr.Message)
	}
	var extracted *UnreadableMaterialsError
	if !errors.As(appErr, &extracted) || len(extracted.Items) != 2 {
		t.Fatalf("expected errors.As to unwrap *UnreadableMaterialsError")
	}
}
