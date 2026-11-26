package httpclient

import (
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

// ReadLimited reads at most maxBytes from r; if the stream yields more than maxBytes,
// it returns *ClientError{Kind: KindResponseTooLarge} wrapping apperror.TooLarge.
func ReadLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return io.ReadAll(r)
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, &ClientError{
			Kind: KindResponseTooLarge,
			Err:  apperror.TooLarge("response body exceeds limit"),
		}
	}
	return data, nil
}

type bodyControlRT struct {
	next            http.RoundTripper
	maxBytes        int64
	idleReadTimeout time.Duration
	streaming       bool
	service         string
}

func (rt *bodyControlRT) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.next.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	svc := serviceFromContext(req.Context(), rt.service)
	host := ""
	if req.URL != nil {
		host = req.URL.Host
	}

	body := resp.Body
	if rt.streaming && rt.idleReadTimeout > 0 {
		body = newIdleTimeoutReadCloser(body, rt.idleReadTimeout, svc, host)
	}
	if rt.maxBytes > 0 {
		body = &limitedReadCloser{
			orig:    body,
			lr:      io.LimitReader(body, rt.maxBytes+1),
			limit:   rt.maxBytes,
			service: svc,
			host:    host,
		}
	}
	resp.Body = body
	return resp, nil
}

type limitedReadCloser struct {
	orig    io.ReadCloser
	lr      io.Reader
	limit   int64
	read    int64
	service string
	host    string
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	n, err := l.lr.Read(p)
	l.read += int64(n)
	if l.read > l.limit {
		_ = l.orig.Close()
		allowed := n - int(l.read-l.limit)
		if allowed < 0 {
			allowed = 0
		}
		return allowed, &ClientError{
			Kind:    KindResponseTooLarge,
			Service: l.service,
			Host:    l.host,
			Err:     apperror.TooLarge("response body exceeds limit"),
		}
	}
	return n, err
}

func (l *limitedReadCloser) Close() error {
	return l.orig.Close()
}

type idleTimeoutReadCloser struct {
	orig      io.ReadCloser
	timeout   time.Duration
	service   string
	host      string
	timer     *time.Timer
	timedOut  atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

func newIdleTimeoutReadCloser(orig io.ReadCloser, timeout time.Duration, service, host string) *idleTimeoutReadCloser {
	return &idleTimeoutReadCloser{
		orig:    orig,
		timeout: timeout,
		service: service,
		host:    host,
	}
}

func (r *idleTimeoutReadCloser) Read(p []byte) (int, error) {
	if r.timedOut.Load() {
		return 0, &ClientError{
			Kind:    KindTimeout,
			Service: r.service,
			Host:    r.host,
			Err:     context.DeadlineExceeded,
		}
	}
	if r.timer == nil {
		r.timer = time.AfterFunc(r.timeout, func() {
			r.timedOut.Store(true)
			_ = r.Close()
		})
	} else {
		r.timer.Reset(r.timeout)
	}
	n, err := r.orig.Read(p)
	if !r.timer.Stop() {
		// Callback already fired; do not reuse this timer instance.
		r.timer = nil
	}
	if err != nil && r.timedOut.Load() {
		return n, &ClientError{
			Kind:    KindTimeout,
			Service: r.service,
			Host:    r.host,
			Err:     context.DeadlineExceeded,
		}
	}
	return n, err
}

func (r *idleTimeoutReadCloser) Close() error {
	r.closeOnce.Do(func() {
		r.closeErr = r.orig.Close()
	})
	return r.closeErr
}
