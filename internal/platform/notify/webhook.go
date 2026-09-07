package notify

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"altalune.id/template/httpclient"
)

const (
	webhookQueueCap     = 128
	webhookDialTimeout  = 5 * time.Second
	webhookTotalTimeout = 10 * time.Second
)

type webhookSink struct {
	kind      string
	url       string
	client    *http.Client
	log       *slog.Logger
	q         chan []byte
	wg        sync.WaitGroup
	closeOnce sync.Once
	mu        sync.RWMutex
	closed    bool
	dropped   atomic.Uint64
}

func newWebhookSink(kind, url string, log *slog.Logger) *webhookSink {
	if log == nil {
		log = slog.Default()
	}
	s := &webhookSink{
		kind: kind,
		url:  url,
		log:  log,
		// SECURITY: the SSRF filter stays off — sink URLs are operator-configured and routinely cluster-local.
		client: httpclient.New(
			httpclient.WithAllowPrivateHosts(true),
			httpclient.WithDialTimeout(webhookDialTimeout),
			httpclient.WithTimeout(webhookTotalTimeout),
		),
		q: make(chan []byte, webhookQueueCap),
	}
	s.wg.Add(1)
	go s.run()
	return s
}

func (s *webhookSink) enqueue(ctx context.Context, payload []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.q <- payload:
	default:
		n := s.dropped.Add(1)
		s.log.WarnContext(ctx, "notify: sink queue full, dropping",
			slog.String("sink", s.kind),
			slog.Uint64("dropped_total", n),
		)
	}
}

func (s *webhookSink) run() {
	defer s.wg.Done()
	for payload := range s.q {
		s.deliver(payload)
	}
}

func (s *webhookSink) deliver(payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), webhookTotalTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(payload))
	if err != nil {
		s.log.Error("notify: build webhook request",
			slog.String("sink", s.kind),
			slog.Any("error", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Error("notify: webhook post",
			slog.String("sink", s.kind),
			slog.Any("error", err))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		s.log.Error("notify: webhook non-2xx",
			slog.String("sink", s.kind),
			slog.Int("status", resp.StatusCode))
	}
}

func (s *webhookSink) droppedCount() uint64 { return s.dropped.Load() }

func (s *webhookSink) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.q)
		s.mu.Unlock()
		s.wg.Wait()
	})
	return nil
}
