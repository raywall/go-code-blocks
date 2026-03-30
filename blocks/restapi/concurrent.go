package restapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// ── Sentinel errors ───────────────────────────────────────────────────────────

// ErrSkipped is recorded on a step that was never executed because one or more
// of its dependencies failed or were themselves skipped.
// Wrap-unwrap via errors.Is works: errors.Is(sr.Err, ErrSkipped).
var ErrSkipped = errors.New("step skipped: dependency failed")

// skippedErr carries the name of the blocking dependency alongside ErrSkipped.
type skippedErr struct{ dep string }

func (e *skippedErr) Error() string        { return fmt.Sprintf("%s: %q", ErrSkipped, e.dep) }
func (e *skippedErr) Is(target error) bool { return target == ErrSkipped }
func (e *skippedErr) Unwrap() error        { return ErrSkipped }

// ── RetryPolicy ───────────────────────────────────────────────────────────────

// RetryPolicy defines how many times a step is retried on transient failure
// and how long to wait between attempts.
//
// Only transient errors trigger a retry: network errors and HTTP status codes
// 429, 500, 502, 503, and 504. Client errors (4xx except 429) are not retried.
//
//	// 3 total attempts, 200 ms → 400 ms → done (exponential ×2)
//	RetryPolicy{MaxAttempts: 3, Delay: 200*time.Millisecond, Backoff: 2.0}
//
//	// 5 total attempts, fixed 100 ms between each
//	RetryPolicy{MaxAttempts: 5, Delay: 100*time.Millisecond, Backoff: 1.0}
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts (initial + retries).
	// 1 means no retry; 0 is treated as 1.
	MaxAttempts int
	// Delay is the wait time before the second attempt.
	// Subsequent delays are Delay × Backoff^(attempt-1).
	Delay time.Duration
	// Backoff is the exponential multiplier applied to Delay each attempt.
	// 1.0 = constant delay; 2.0 = double each time. Clamped to ≥ 1.0.
	Backoff float64
}

func (p RetryPolicy) maxAttempts() int {
	if p.MaxAttempts <= 0 {
		return 1
	}
	return p.MaxAttempts
}

func (p RetryPolicy) delayFor(attempt int) time.Duration {
	if p.Delay <= 0 || attempt <= 0 {
		return 0
	}
	b := p.Backoff
	if b < 1.0 {
		b = 1.0
	}
	return time.Duration(float64(p.Delay) * math.Pow(b, float64(attempt-1)))
}

// isTransient reports whether the error or HTTP status code warrants a retry.
func isTransient(err error, resp *Response) bool {
	if err != nil {
		// Network-level errors (connection refused, timeout, DNS, etc.)
		return true
	}
	if resp != nil {
		switch resp.StatusCode {
		case 429, 500, 502, 503, 504:
			return true
		}
	}
	return false
}

// ── Results ───────────────────────────────────────────────────────────────────

// Results is the accumulated snapshot of all step outcomes in a Pipeline.
// It is passed read-only to each step's Build function so the step can use
// data from previously completed steps to construct its own request.
type Results struct {
	mu     sync.RWMutex
	data   map[string]*StepResult
	failed map[string]struct{} // names of steps that failed or were skipped
}

func newResults() *Results {
	return &Results{
		data:   make(map[string]*StepResult),
		failed: make(map[string]struct{}),
	}
}

func (r *Results) set(name string, sr *StepResult) {
	r.mu.Lock()
	r.data[name] = sr
	if sr.Err != nil {
		r.failed[name] = struct{}{}
	}
	r.mu.Unlock()
}

// hasFailed reports whether the named step failed or was skipped.
// Used internally to decide whether to cascade-abort dependents.
func (r *Results) hasFailed(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.failed[name]
	return ok
}

// Get returns the StepResult for the named step, or nil if unknown.
func (r *Results) Get(name string) *StepResult {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.data[name]
}

// JSON unmarshals the response body of the named step into v.
// Returns an error when the step is unknown, failed, skipped, or the body is
// not valid JSON.
func (r *Results) JSON(name string, v any) error {
	sr := r.Get(name)
	if sr == nil {
		return fmt.Errorf("results: step %q not found", name)
	}
	if sr.Err != nil {
		return fmt.Errorf("results: step %q: %w", name, sr.Err)
	}
	if err := json.Unmarshal(sr.Response.Body, v); err != nil {
		return fmt.Errorf("results: unmarshal step %q: %w", name, err)
	}
	return nil
}

// Names returns the names of all completed steps (succeeded, failed, skipped).
func (r *Results) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.data))
	for name := range r.data {
		names = append(names, name)
	}
	return names
}

// ── StepResult ────────────────────────────────────────────────────────────────

// StepResult holds the outcome of a single pipeline step.
type StepResult struct {
	// Name is the step identifier.
	Name string
	// Response is the HTTP response. Nil when Err is non-nil.
	Response *Response
	// Err is the execution error. Nil on success.
	// errors.Is(sr.Err, ErrSkipped) reports whether the step was cascade-aborted.
	Err error
	// Attempts is the number of HTTP calls made (1 = no retry needed).
	Attempts int
	// Latency is the total wall-clock time including all retry attempts.
	Latency time.Duration
}

// OK reports whether the step completed without error and returned a 2xx status.
func (sr *StepResult) OK() bool {
	return sr.Err == nil && sr.Response != nil && sr.Response.OK()
}

// Skipped reports whether the step was cascade-aborted due to a dependency failure.
func (sr *StepResult) Skipped() bool { return errors.Is(sr.Err, ErrSkipped) }

// ── PipelineStep ──────────────────────────────────────────────────────────────

// PipelineStep is a single unit of work in a Pipeline.
//
// Steps without DependsOn are placed in the first execution wave and run
// concurrently. Steps that declare dependencies are placed in a later wave,
// after all their dependencies have completed. If any direct dependency fails
// or is skipped, this step is automatically skipped (cascade abort) — its
// Build function is never called.
//
//	// Wave 0 — independent, run in parallel
//	{Name: "user",    Build: func(ctx, _) (Request, error) { ... }},
//	{Name: "catalog", Build: func(ctx, _) (Request, error) { ... }},
//
//	// Wave 1 — depends on "user"; auto-skipped if "user" fails
//	{
//	    Name:      "orders",
//	    DependsOn: []string{"user"},
//	    Retry:     &restapi.RetryPolicy{MaxAttempts: 3, Delay: 200*time.Millisecond, Backoff: 2.0},
//	    Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
//	        var u User
//	        prev.JSON("user", &u)
//	        return restapi.Request{Path: "/orders?user_id=" + u.ID}, nil
//	    },
//	},
type PipelineStep struct {
	// Name uniquely identifies this step within the pipeline.
	Name string
	// DependsOn lists names of steps that must complete successfully before
	// this step runs. If any dependency fails or is skipped, this step is
	// cascade-aborted with ErrSkipped.
	DependsOn []string
	// Retry overrides the block-level default retry policy for this step only.
	// nil means: use the block default (if any), or no retry.
	Retry *RetryPolicy
	// Build constructs the HTTP Request for this step. It receives the Results
	// of all previously completed waves. It is never called when a dependency
	// has failed (cascade abort fires first).
	Build func(ctx context.Context, prev *Results) (Request, error)
}

// ── PipelineOptions ───────────────────────────────────────────────────────────

// PipelineOption configures pipeline execution behaviour.
type PipelineOption func(*pipelineConfig)

type pipelineConfig struct {
	continueOnError bool
	maxConcurrency  int
	waveTimeout     time.Duration
	defaultRetry    *RetryPolicy
}

// WithContinueOnError instructs the pipeline to continue executing subsequent
// waves even when steps in the current wave fail (but not when they are
// cascade-aborted — skipped steps never block the next wave).
// By default the pipeline aborts on the first step failure.
func WithContinueOnError() PipelineOption {
	return func(c *pipelineConfig) { c.continueOnError = true }
}

// WithMaxConcurrency limits the number of goroutines used simultaneously per wave.
// Useful to respect downstream rate limits. Defaults to unlimited.
func WithMaxConcurrency(n int) PipelineOption {
	return func(c *pipelineConfig) { c.maxConcurrency = n }
}

// WithWaveTimeout sets a per-wave deadline. If a wave does not complete within
// the duration, all running steps in that wave are cancelled and the pipeline
// aborts.
func WithWaveTimeout(d time.Duration) PipelineOption {
	return func(c *pipelineConfig) { c.waveTimeout = d }
}

// WithDefaultRetry sets the retry policy applied to every step that does not
// declare its own Retry field.
//
//	// Retry up to 3 times total, doubling the wait each attempt: 100ms, 200ms
//	restapi.WithDefaultRetry(restapi.RetryPolicy{
//	    MaxAttempts: 3,
//	    Delay:       100 * time.Millisecond,
//	    Backoff:     2.0,
//	})
func WithDefaultRetry(policy RetryPolicy) PipelineOption {
	return func(c *pipelineConfig) { c.defaultRetry = &policy }
}

// ── FanOut ────────────────────────────────────────────────────────────────────

// FanOut executes all provided requests concurrently and collects every result.
// It is a single-wave shorthand — all requests are independent and run
// simultaneously. Use Pipeline when steps have dependencies.
//
//	results, err := api.FanOut(ctx, map[string]restapi.Request{
//	    "user":    {Path: "/users/123"},
//	    "catalog": {Path: "/products?limit=50"},
//	    "rates":   {Path: "/shipping/rates"},
//	}, restapi.WithDefaultRetry(restapi.RetryPolicy{MaxAttempts: 3, Delay: 100*time.Millisecond, Backoff: 2.0}))
func (b *Block) FanOut(ctx context.Context, requests map[string]Request, opts ...PipelineOption) (*Results, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}

	cfg := buildPipelineCfg(opts)
	results := newResults()

	type item struct {
		name string
		sr   *StepResult
	}

	sem := newSemaphore(cfg.maxConcurrency)
	ch := make(chan item, len(requests))
	var wg sync.WaitGroup

	for name, req := range requests {
		name, req := name, req
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem.acquire()
			defer sem.release()

			// FanOut steps have no per-step retry; use the pipeline default.
			policy := cfg.defaultRetry
			sr := b.doWithRetry(ctx, name, req, policy)
			ch <- item{name: name, sr: sr}
		}()
	}

	go func() { wg.Wait(); close(ch) }()

	var errs []error
	for it := range ch {
		results.set(it.name, it.sr)
		if it.sr.Err != nil && !cfg.continueOnError {
			errs = append(errs, fmt.Errorf("step %q: %w", it.name, it.sr.Err))
		}
	}

	if len(errs) > 0 {
		return results, errors.Join(errs...)
	}
	return results, nil
}

// ── Pipeline ──────────────────────────────────────────────────────────────────

// Pipeline executes steps in dependency order using DAG wave levelling.
// Within each wave, all independent steps run concurrently.
//
// # Cascade abort
//
// When a step fails, every step that directly or transitively depends on it is
// automatically skipped — their Build functions are never called, and their
// StepResult carries ErrSkipped. This prevents corrupt data from propagating
// through the graph and surfaces the root cause immediately.
//
// # Retry
//
// Each step may declare its own RetryPolicy, or inherit the pipeline default
// set via WithDefaultRetry. Retries occur only on transient errors (network
// errors and HTTP 429/500/502/503/504). The delay between attempts grows by
// the configured Backoff multiplier.
//
//	steps := []restapi.PipelineStep{
//	    {Name: "user",    Build: ...},       // wave 0, no retry
//	    {Name: "catalog", Build: ...},       // wave 0, no retry
//	    {
//	        Name:      "orders",
//	        DependsOn: []string{"user"},     // wave 1; skipped if user fails
//	        Retry:     &restapi.RetryPolicy{ // overrides pipeline default
//	            MaxAttempts: 4,
//	            Delay:       200 * time.Millisecond,
//	            Backoff:     2.0,
//	        },
//	        Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
//	            var u User
//	            prev.JSON("user", &u)
//	            return restapi.Request{Path: "/orders?user_id=" + u.ID}, nil
//	        },
//	    },
//	}
//
//	results, err := api.Pipeline(ctx, steps,
//	    restapi.WithDefaultRetry(restapi.RetryPolicy{MaxAttempts: 2, Delay: 100*time.Millisecond, Backoff: 1.5}),
//	    restapi.WithContinueOnError(),
//	)
func (b *Block) Pipeline(ctx context.Context, steps []PipelineStep, opts ...PipelineOption) (*Results, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}
	if len(steps) == 0 {
		return newResults(), nil
	}

	cfg := buildPipelineCfg(opts)

	waves, err := buildWaves(steps)
	if err != nil {
		return nil, fmt.Errorf("pipeline: build execution graph: %w", err)
	}

	accumulated := newResults()

	for waveIdx, wave := range waves {
		waveCtx := ctx
		var waveCancel context.CancelFunc
		if cfg.waveTimeout > 0 {
			waveCtx, waveCancel = context.WithTimeout(ctx, cfg.waveTimeout)
		}

		waveResults, waveErr := b.executeWave(waveCtx, wave, accumulated, cfg)

		if waveCancel != nil {
			waveCancel()
		}

		for _, sr := range waveResults {
			accumulated.set(sr.Name, sr)
		}

		// A wave that produced only skips is not a real failure — the root
		// cause is already recorded in an earlier wave.
		if waveErr != nil && !cfg.continueOnError && !allSkipped(waveResults) {
			return accumulated, fmt.Errorf("pipeline wave %d: %w", waveIdx, waveErr)
		}
	}

	return accumulated, nil
}

// executeWave runs all steps in one wave concurrently.
// Before dispatching a step, it checks whether any direct dependency has
// already failed — if so, it immediately records ErrSkipped without a network
// call (cascade abort).
func (b *Block) executeWave(
	ctx context.Context,
	wave []PipelineStep,
	prev *Results,
	cfg pipelineConfig,
) ([]*StepResult, error) {
	type item struct{ sr *StepResult }

	sem := newSemaphore(cfg.maxConcurrency)
	ch := make(chan item, len(wave))
	var wg sync.WaitGroup

	for _, step := range wave {
		step := step
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem.acquire()
			defer sem.release()

			// ── Cascade abort check ────────────────────────────────────────
			// If any direct dependency failed or was skipped, abort this step
			// immediately without calling Build or making any HTTP request.
			if dep := firstFailedDep(step.DependsOn, prev); dep != "" {
				ch <- item{sr: &StepResult{
					Name:     step.Name,
					Err:      &skippedErr{dep: dep},
					Attempts: 0,
				}}
				return
			}

			// ── Normal execution with retry ────────────────────────────────
			sr := b.runStep(ctx, step, prev, cfg.defaultRetry)
			ch <- item{sr: sr}
		}()
	}

	go func() { wg.Wait(); close(ch) }()

	var (
		results []*StepResult
		errs    []error
	)
	for it := range ch {
		results = append(results, it.sr)
		if it.sr.Err != nil && !errors.Is(it.sr.Err, ErrSkipped) {
			// Collect real errors; skips are not pipeline errors.
			errs = append(errs, fmt.Errorf("step %q: %w", it.sr.Name, it.sr.Err))
		}
	}

	return results, errors.Join(errs...)
}

// runStep builds the request then executes it with the effective retry policy.
// Per-step Retry takes precedence over the pipeline default.
func (b *Block) runStep(ctx context.Context, step PipelineStep, prev *Results, defaultRetry *RetryPolicy) *StepResult {
	start := time.Now()

	req, buildErr := step.Build(ctx, prev)
	if buildErr != nil {
		return &StepResult{
			Name:     step.Name,
			Err:      fmt.Errorf("build: %w", buildErr),
			Attempts: 0,
			Latency:  time.Since(start),
		}
	}

	// Effective retry policy: per-step overrides pipeline default.
	policy := defaultRetry
	if step.Retry != nil {
		policy = step.Retry
	}

	sr := b.doWithRetry(ctx, step.Name, req, policy)
	sr.Latency = time.Since(start) // overwrite with total latency incl. retries
	return sr
}

// doWithRetry executes a single Request with retry logic.
// It respects context cancellation between attempts.
// Returns after the first success, or after MaxAttempts are exhausted.
func (b *Block) doWithRetry(ctx context.Context, name string, req Request, policy *RetryPolicy) *StepResult {
	maxAttempts := 1
	var p RetryPolicy
	if policy != nil {
		p = *policy
		maxAttempts = p.maxAttempts()
	}

	var (
		lastResp *Response
		lastErr  error
		attempts int
	)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Respect context cancellation before each attempt.
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			break
		}

		// Wait before retries (not before the first attempt).
		if attempt > 1 {
			delay := p.delayFor(attempt - 1)
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					lastErr = ctx.Err()
					goto done
				}
			}
		}

		resp, err := b.Do(ctx, req)
		attempts++
		lastResp = resp
		lastErr = err

		// Success — stop immediately.
		if err == nil && resp.OK() {
			break
		}

		// Non-transient error — no point retrying.
		if !isTransient(err, resp) {
			break
		}

		// Transient — loop to next attempt (if any remain).
	}

done:
	return &StepResult{
		Name:     name,
		Response: lastResp,
		Err:      lastErr,
		Attempts: attempts,
	}
}

// ── DAG wave builder ──────────────────────────────────────────────────────────

// buildWaves topologically sorts steps into execution waves using level
// assignment. Steps in the same wave are independent and run concurrently.
// Returns an error on unknown dependencies, duplicate names, or cycles.
func buildWaves(steps []PipelineStep) ([][]PipelineStep, error) {
	byName := make(map[string]PipelineStep, len(steps))
	for _, s := range steps {
		if _, dup := byName[s.Name]; dup {
			return nil, fmt.Errorf("duplicate step name: %q", s.Name)
		}
		byName[s.Name] = s
	}

	for _, s := range steps {
		for _, dep := range s.DependsOn {
			if _, ok := byName[dep]; !ok {
				return nil, fmt.Errorf("step %q depends on unknown step %q", s.Name, dep)
			}
		}
	}

	const (
		unvisited  = -1
		inProgress = -2
	)

	level := make(map[string]int, len(steps))
	for name := range byName {
		level[name] = unvisited
	}

	var visit func(name string, chain []string) error
	visit = func(name string, chain []string) error {
		switch level[name] {
		case inProgress:
			return fmt.Errorf("cycle detected: %s → %s", strings.Join(chain, " → "), name)
		default:
			if level[name] >= 0 {
				return nil
			}
		}

		level[name] = inProgress
		maxDep := -1
		for _, dep := range byName[name].DependsOn {
			if err := visit(dep, append(chain, name)); err != nil {
				return err
			}
			if level[dep] > maxDep {
				maxDep = level[dep]
			}
		}
		level[name] = maxDep + 1
		return nil
	}

	for name := range byName {
		if err := visit(name, nil); err != nil {
			return nil, err
		}
	}

	maxLevel := 0
	for _, l := range level {
		if l > maxLevel {
			maxLevel = l
		}
	}

	waves := make([][]PipelineStep, maxLevel+1)
	for _, s := range steps {
		l := level[s.Name]
		waves[l] = append(waves[l], s)
	}

	return waves, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// firstFailedDep returns the name of the first dependency that has failed or
// been skipped, or an empty string if all dependencies succeeded.
func firstFailedDep(deps []string, prev *Results) string {
	for _, dep := range deps {
		if prev.hasFailed(dep) {
			return dep
		}
	}
	return ""
}

// allSkipped reports whether every result in the slice was cascade-aborted.
// Used to avoid treating a wave of skips as a real pipeline failure.
func allSkipped(results []*StepResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, sr := range results {
		if !errors.Is(sr.Err, ErrSkipped) {
			return false
		}
	}
	return true
}

// buildPipelineCfg applies all PipelineOptions to a fresh pipelineConfig.
func buildPipelineCfg(opts []PipelineOption) pipelineConfig {
	cfg := pipelineConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// ── Semaphore ─────────────────────────────────────────────────────────────────

// semaphore is a counting semaphore for bounding per-wave concurrency.
// A nil semaphore (max ≤ 0) is a no-op — unlimited concurrency.
type semaphore chan struct{}

func newSemaphore(max int) semaphore {
	if max <= 0 {
		return nil
	}
	return make(semaphore, max)
}

func (s semaphore) acquire() {
	if s != nil {
		s <- struct{}{}
	}
}

func (s semaphore) release() {
	if s != nil {
		<-s
	}
}
