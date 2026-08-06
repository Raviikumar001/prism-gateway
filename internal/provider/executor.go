package provider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/raviikumar001/prism-gateway/internal/breaker"
	"github.com/raviikumar001/prism-gateway/internal/gatewaycfg"
)

var (
	ErrAllFailed   = errors.New("all upstream attempts failed")
	ErrClientAbort = errors.New("client aborted")
	ErrOverloaded  = errors.New("too many in-flight upstreams")
	ErrNoAttempts  = errors.New("no upstream targets available")
)

type Executor struct {
	cfg       *gatewaycfg.Config
	clients   map[string]*OpenAICompat
	breakers  map[string]*breaker.Breaker
	sem       chan struct{}
	mu        sync.Mutex
	attemptTO time.Duration
}

type Result struct {
	Response *ChatResponse
	Provider string
	Model    string
	Fallback bool
	Retries  int
}

type StreamResult struct {
	Provider string
	Model    string
	Fallback bool
	Retries  int
	Usage    Usage
}

func NewExecutor(cfg *gatewaycfg.Config, timeout time.Duration, maxInFlight int) *Executor {
	if maxInFlight <= 0 {
		maxInFlight = 64
	}
	e := &Executor{
		cfg:       cfg,
		clients:   make(map[string]*OpenAICompat, len(cfg.Providers)),
		breakers:  make(map[string]*breaker.Breaker, len(cfg.Providers)),
		sem:       make(chan struct{}, maxInFlight),
		attemptTO: timeout,
	}
	bcfg := breaker.DefaultConfig()
	for _, p := range cfg.Providers {
		e.clients[p.Name] = NewOpenAICompat(p.Name, p.BaseURL, p.APIKey, timeout, p.ExtraHeaders)
		e.breakers[p.Name] = breaker.New(bcfg)
	}
	return e
}

func (e *Executor) acquire(ctx context.Context) error {
	select {
	case e.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrOverloaded
	}
}

func (e *Executor) release() {
	select {
	case <-e.sem:
	default:
	}
}

func isRetryable(err error) bool {
	var ue *UpstreamError
	if errors.As(err, &ue) {
		return ue.StatusCode == 429 || ue.StatusCode >= 500
	}
	return err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func isClientAbort(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, ErrClientAbort)
}

func fullJitter(base time.Duration, attempt int, mult int) time.Duration {
	if mult < 2 {
		mult = 2
	}
	max := base
	for i := 0; i < attempt; i++ {
		max *= time.Duration(mult)
		if max > 5*time.Second {
			max = 5 * time.Second
			break
		}
	}
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max) + 1))
}

func (e *Executor) Chat(ctx context.Context, chain []string, messages []ChatMessage) (*Result, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer e.release()

	maxAttempts := e.cfg.Retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	baseBackoff := time.Duration(e.cfg.Retry.InitialBackoffMs) * time.Millisecond
	mult := e.cfg.Retry.BackoffMultiplier

	attempts := 0
	var lastErr error
	primaryModel := ""
	if len(chain) > 0 {
		primaryModel = chain[0]
	}

	for _, model := range chain {
		prov, ok := e.cfg.ProviderForModel(model)
		if !ok {
			continue
		}
		br := e.breakers[prov.Name]
		client := e.clients[prov.Name]
		if br == nil || client == nil {
			continue
		}
		if !br.Allow() {
			slog.Info("skipping open breaker", "provider", prov.Name, "model", model)
			continue
		}

		providerAttempts := 0
		for providerAttempts < 2 && attempts < maxAttempts {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			attempts++
			providerAttempts++

			upCtx, cancel := context.WithTimeout(ctx, e.attemptTO)
			resp, err := client.ChatCompletion(upCtx, ChatRequest{Model: model, Messages: messages})
			cancel()

			if err == nil {
				br.Success()
				return &Result{
					Response: resp,
					Provider: prov.Name,
					Model:    model,
					Fallback: model != primaryModel,
					Retries:  attempts - 1,
				}, nil
			}

			if isClientAbort(err) || isClientAbort(ctx.Err()) {
				return nil, ErrClientAbort
			}

			lastErr = err
			var ue *UpstreamError
			if errors.As(err, &ue) {
				if ue.RetryAfter > 0 {
					br.OpenFor(ue.RetryAfter)
				}
				if isRetryable(err) {
					br.Failure()
				}
				if ue.StatusCode >= 400 && ue.StatusCode < 500 && ue.StatusCode != 429 {
					break
				}
			} else {
				br.Failure()
			}

			if providerAttempts < 2 && attempts < maxAttempts && isRetryable(err) {
				sleep := fullJitter(baseBackoff, providerAttempts-1, mult)
				timer := time.NewTimer(sleep)
				select {
				case <-ctx.Done():
					timer.Stop()
					if isClientAbort(ctx.Err()) {
						return nil, ErrClientAbort
					}
					return nil, ctx.Err()
				case <-timer.C:
				}
				continue
			}
			break
		}
	}

	if lastErr == nil {
		return nil, ErrNoAttempts
	}
	return nil, fmt.Errorf("%w: %v", ErrAllFailed, lastErr)
}

func (e *Executor) ChatStream(
	ctx context.Context,
	chain []string,
	messages []ChatMessage,
	writeHeaders func(provider, model string, fallback bool),
	writeEvent func(data []byte) error,
	writeDone func() error,
	writeInBandError func(msg string) error,
) (*StreamResult, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer e.release()

	maxAttempts := e.cfg.Retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	baseBackoff := time.Duration(e.cfg.Retry.InitialBackoffMs) * time.Millisecond
	mult := e.cfg.Retry.BackoffMultiplier

	attempts := 0
	var lastErr error
	primaryModel := ""
	if len(chain) > 0 {
		primaryModel = chain[0]
	}
	headersSent := false

	for _, model := range chain {
		prov, ok := e.cfg.ProviderForModel(model)
		if !ok {
			continue
		}
		br := e.breakers[prov.Name]
		client := e.clients[prov.Name]
		if br == nil || client == nil {
			continue
		}
		if headersSent {
			break
		}
		if !br.Allow() {
			slog.Info("skipping open breaker", "provider", prov.Name, "model", model)
			continue
		}

		providerAttempts := 0
		for providerAttempts < 2 && attempts < maxAttempts && !headersSent {
			if err := ctx.Err(); err != nil {
				return nil, ErrClientAbort
			}
			attempts++
			providerAttempts++

			upCtx, cancel := context.WithCancel(ctx)
			resp, err := client.ChatCompletionStream(upCtx, ChatRequest{Model: model, Messages: messages})
			if err != nil {
				cancel()
				if isClientAbort(err) || isClientAbort(ctx.Err()) {
					return nil, ErrClientAbort
				}
				lastErr = err
				var ue *UpstreamError
				if errors.As(err, &ue) {
					if ue.RetryAfter > 0 {
						br.OpenFor(ue.RetryAfter)
					}
					if isRetryable(err) {
						br.Failure()
					}
					if ue.StatusCode >= 400 && ue.StatusCode < 500 && ue.StatusCode != 429 {
						break
					}
				} else {
					br.Failure()
				}
				if providerAttempts < 2 && attempts < maxAttempts && isRetryable(err) {
					sleep := fullJitter(baseBackoff, providerAttempts-1, mult)
					timer := time.NewTimer(sleep)
					select {
					case <-ctx.Done():
						timer.Stop()
						return nil, ErrClientAbort
					case <-timer.C:
					}
					continue
				}
				break
			}

			fallback := model != primaryModel
			writeHeaders(prov.Name, model, fallback)
			headersSent = true

			usage, readErr := ReadSSE(upCtx, resp.Body, func(ev StreamEvent) error {
				if ev.Done {
					return writeDone()
				}
				return writeEvent(ev.Data)
			})
			cancel()

			if readErr != nil {
				if isClientAbort(readErr) || isClientAbort(ctx.Err()) {
					return &StreamResult{
						Provider: prov.Name,
						Model:    model,
						Fallback: fallback,
						Retries:  attempts - 1,
						Usage:    usage,
					}, ErrClientAbort
				}
				br.Failure()
				_ = writeInBandError(readErr.Error())
				return &StreamResult{
					Provider: prov.Name,
					Model:    model,
					Fallback: fallback,
					Retries:  attempts - 1,
					Usage:    usage,
				}, readErr
			}

			br.Success()
			return &StreamResult{
				Provider: prov.Name,
				Model:    model,
				Fallback: fallback,
				Retries:  attempts - 1,
				Usage:    usage,
			}, nil
		}
	}

	if lastErr == nil {
		return nil, ErrNoAttempts
	}
	return nil, fmt.Errorf("%w: %v", ErrAllFailed, lastErr)
}

func (e *Executor) HealthSnapshot() []map[string]any {
	out := make([]map[string]any, 0, len(e.cfg.Providers))
	for _, p := range e.cfg.Providers {
		br := e.breakers[p.Name]
		snap := map[string]any{
			"name":     p.Name,
			"base_url": p.BaseURL,
		}
		if br != nil {
			for k, v := range br.Snapshot() {
				snap[k] = v
			}
		}
		out = append(out, snap)
	}
	return out
}
