// blocks/flow/state.go
package flow

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/raywall/go-code-blocks/blocks/decision"
	"github.com/raywall/go-code-blocks/blocks/server"
)

// State is the shared execution context threaded through every step of a Flow.
//
// It accumulates enrichment data, CEL decision results and the final response,
// allowing each step to build on the work of previous steps without coupling
// them directly to each other.
//
// State is safe for concurrent reads from the same goroutine during sequential
// step execution. Do not share a State across goroutines.
type State struct {
	mu        sync.RWMutex
	data      map[string]any              // enrichment results, keyed by step name
	decisions map[string]*decision.Result // CEL results, keyed by step name
	response  *server.Response            // final HTTP response (set by Respond)
	aborted   bool                        // true after Abort is called
}

func newState() *State {
	return &State{
		data:      make(map[string]any),
		decisions: make(map[string]*decision.Result),
	}
}

// ── Data (Enrich results) ─────────────────────────────────────────────────────

// Set stores a value produced by an Enrich step under the given key.
// Typically called by step constructors, not by user code directly.
func (s *State) Set(key string, value any) {
	s.mu.Lock()
	s.data[key] = value
	s.mu.Unlock()
}

// Get returns the value stored under key, or nil if not present.
func (s *State) Get(key string) any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[key]
}

// Bind unmarshals the value stored under key into dest via JSON roundtrip.
// This allows retrieving typed structs from enrichment results without
// a manual type assertion.
//
//	var user User
//	if err := state.Bind("load-user", &user); err != nil { ... }
func (s *State) Bind(key string, dest any) error {
	val := s.Get(key)
	if val == nil {
		return fmt.Errorf("flow state: key %q not found", key)
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("flow state: marshal %q: %w", key, err)
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("flow state: bind %q into %T: %w", key, dest, err)
	}
	return nil
}

// Has reports whether a value exists for the given key.
func (s *State) Has(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.data[key]
	return ok
}

// Keys returns the names of all enrichment values currently stored.
func (s *State) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}

// ── Decision results ──────────────────────────────────────────────────────────

// setDecision stores the CEL result produced by a Decide step.
func (s *State) setDecision(key string, result *decision.Result) {
	s.mu.Lock()
	s.decisions[key] = result
	s.mu.Unlock()
}

// Decision returns the full *decision.Result stored under key, or nil.
// Key matches the name passed to flow.Step when using a Decide step.
func (s *State) Decision(key string) *decision.Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.decisions[key]
}

// Passed reports whether the named rule passed in the decision stored under
// decisionKey. Returns false when the decision or rule is not found.
//
//	// In a Transform or Respond step:
//	if state.Passed("route-decision", "is-pj") { ... }
func (s *State) Passed(decisionKey, ruleName string) bool {
	r := s.Decision(decisionKey)
	if r == nil {
		return false
	}
	return r.Passed(ruleName)
}

// Failed is the inverse of Passed.
func (s *State) Failed(decisionKey, ruleName string) bool {
	return !s.Passed(decisionKey, ruleName)
}

// ── Flow control ──────────────────────────────────────────────────────────────

// Abort short-circuits the flow immediately. The provided response is returned
// to the caller; no further steps are executed.
// Calling Abort from within a step function stops the pipeline cleanly —
// it is not an error.
//
//	// Inside a Validate step:
//	state.Abort(server.Error(http.StatusForbidden, "access denied"))
func (s *State) Abort(resp *server.Response) {
	s.mu.Lock()
	s.aborted = true
	s.response = resp
	s.mu.Unlock()
}

// Respond sets the final HTTP response that will be returned after all steps
// complete. Unlike Abort, it does not stop subsequent steps from running.
// The last call to Respond wins.
//
//	state.Respond(server.JSON(http.StatusOK, result))
func (s *State) Respond(resp *server.Response) {
	s.mu.Lock()
	s.response = resp
	s.mu.Unlock()
}
