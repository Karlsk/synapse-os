package httpclient

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

func TestResponseBodyLimitPreventsOOM(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("A"), 2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	client, err := NewTrusted(Options{
		Service:          "link_reader",
		MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatalf("NewTrusted() error = %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/big", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	_, readErr := io.ReadAll(resp.Body)
	if readErr == nil {
		t.Fatalf("expected error reading >1024 bytes, got nil")
	}
	kind, ok := KindOf(readErr)
	if !ok || kind != KindResponseTooLarge {
		t.Fatalf("KindOf(readErr) = (%q, %v), want (%q, true); err=%v", kind, ok, KindResponseTooLarge, readErr)
	}
	var appErr *apperror.AppError
	if !errors.As(readErr, &appErr) || appErr.Code != apperror.CodeTooLarge || appErr.HTTPStatus != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected wrapped apperror.CodeTooLarge (413), got %+v", appErr)
	}

	// Verify standalone ReadLimited helper as well
	data, err := ReadLimited(bytes.NewReader(payload[:512]), 1024)
	if err != nil || len(data) != 512 {
		t.Fatalf("ReadLimited(512/1024) = (%d, %v), want (512, nil)", len(data), err)
	}
	_, err = ReadLimited(bytes.NewReader(payload), 1024)
	if k, ok := KindOf(err); !ok || k != KindResponseTooLarge {
		t.Fatalf("ReadLimited(2048/1024) kind = (%q, %v), want KindResponseTooLarge", k, ok)
	}
}

func TestStreamingModeNoTotalTimeoutAndIdleReadTimeout(t *testing.T) {
	t.Parallel()

	t.Run("continuous stream outlives TotalTimeout when Streaming=true", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Errorf("ResponseWriter does not implement http.Flusher")
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			for range 5 {
				_, _ = w.Write([]byte("data: frame\n\n"))
				flusher.Flush()
				time.Sleep(60 * time.Millisecond)
			}
		}))
		defer srv.Close()

		client, err := NewTrusted(Options{
			Service:   "llm-sse",
			Streaming: true,
			Timeouts: TimeoutPolicy{
				TotalTimeout:    50 * time.Millisecond, // MUST be forced to 0 in Streaming mode
				IdleReadTimeout: 180 * time.Millisecond,
			},
		})
		if err != nil {
			t.Fatalf("NewTrusted(Streaming) error = %v", err)
		}
		if client.StdClient().Timeout != 0 {
			t.Fatalf("Streaming client Timeout = %v, want 0", client.StdClient().Timeout)
		}

		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/sse", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do(sse) error = %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("expected full SSE stream without total timeout cut, got error: %v", err)
		}
		if bytes.Count(data, []byte("data: frame")) != 5 {
			t.Fatalf("expected 5 frames, got %q", string(data))
		}
	})

	t.Run("stalled stream aborts on IdleReadTimeout", func(t *testing.T) {
		t.Parallel()
		unblock := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			flusher, _ := w.(http.Flusher)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("first-chunk\n"))
			if flusher != nil {
				flusher.Flush()
			}
			select {
			case <-unblock:
			case <-r.Context().Done():
			}
		}))
		defer func() {
			close(unblock)
			srv.Close()
		}()

		client, err := NewTrusted(Options{
			Service:   "llm-sse-stall",
			Streaming: true,
			Timeouts: TimeoutPolicy{
				IdleReadTimeout: 120 * time.Millisecond,
			},
		})
		if err != nil {
			t.Fatalf("NewTrusted(Streaming) error = %v", err)
		}

		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/stall", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do(stall) error = %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		start := time.Now()
		_, err = io.ReadAll(resp.Body)
		elapsed := time.Since(start)
		if err == nil {
			t.Fatalf("expected idle read timeout error, got nil")
		}
		if k, ok := KindOf(err); !ok || k != KindTimeout {
			t.Fatalf("KindOf(err) = (%q, %v), want KindTimeout; err=%v", k, ok, err)
		}
		if elapsed > 1*time.Second {
			t.Fatalf("idle read timeout took too long: %v", elapsed)
		}
	})
}

func TestIdleTimeoutReadCloserReusesTimerAcrossReads(t *testing.T) {
	t.Parallel()

	rc := newIdleTimeoutReadCloser(io.NopCloser(bytes.NewReader([]byte("abcdefghij"))), 500*time.Millisecond, "sse", "example.com")
	defer func() { _ = rc.Close() }()

	buf := make([]byte, 2)
	var firstTimer *time.Timer
	for i := range 5 {
		n, err := rc.Read(buf)
		if err != nil || n != 2 {
			t.Fatalf("Read #%d = (%d, %v), want (2, nil)", i+1, n, err)
		}
		if rc.timer == nil {
			t.Fatalf("expected timer to be initialized on Read #%d", i+1)
		}
		if firstTimer == nil {
			firstTimer = rc.timer
		} else if rc.timer != firstTimer {
			t.Fatalf("expected timer to be reused across Reads without re-allocating")
		}
	}
}
