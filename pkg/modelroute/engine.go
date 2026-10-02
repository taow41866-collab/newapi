// Package modelroute ranks an already authorized set of same-model targets.
// It never discovers channels, changes persisted weights, or handles billing.
package modelroute

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const sampleTTL = 15 * time.Minute
const leaseTTL = 10 * time.Minute
const stateTTL = 24 * time.Hour

type Config struct {
	Mode           string   `json:"mode"`
	Groups         []string `json:"groups"`
	Models         []string `json:"models"`
	PriorityGroups []string `json:"priority_groups"`
}

// Off by default. Immutable startup configuration avoids racing live option writes.
var Default = New(configFromEnv(), RedisStore{})

func configFromEnv() Config {
	return Config{
		Mode:           os.Getenv("MODEL_ROUTING_MODE"),
		Groups:         strings.FieldsFunc(os.Getenv("MODEL_ROUTING_GROUPS"), splitComma),
		Models:         strings.FieldsFunc(os.Getenv("MODEL_ROUTING_MODELS"), splitComma),
		PriorityGroups: strings.FieldsFunc(os.Getenv("MODEL_ROUTING_PRIORITY_GROUPS"), splitComma),
	}
}

func splitComma(r rune) bool { return r == ',' || r == '\n' }

type Scope struct {
	Group    string `json:"group"`
	Model    string `json:"model"`
	Endpoint string `json:"endpoint"`
	Stream   bool   `json:"stream"`
	Effort   string `json:"effort"`
	Excluded []int  `json:"-"`
}

type Target struct {
	ID       int     `json:"channel_id"`
	Upstream string  `json:"upstream_model"`
	Version  string  `json:"-"`
	Weight   float64 `json:"configured_weight"`
	Priority int64   `json:"priority"`
}

type Outcome string

const (
	Success   Outcome = "success"
	Failure   Outcome = "failure"
	Ignored   Outcome = "ignored"
	Throttled Outcome = "throttled"
)

type Observation struct {
	Outcome        Outcome
	FirstContentMS int64
	DurationMS     int64
	OutputTokens   int64
}

type measurement struct {
	At      int64
	Latency float64
	TPS     float64
	Failed  bool
}
type flight struct {
	Epoch     uint64
	Expires   int64
	Probe     bool
	Synthetic bool
}
type State struct {
	Known       []Scope
	Epoch       uint64
	Failures    int
	Successes   int
	Degraded    bool
	LastEvent   int64
	LastProbe   int64
	LastRequest int64
	Cooldown    int64
	Samples     []measurement
	Flights     map[string]flight
}

type Lease struct {
	Key    string
	ID     string
	Stream bool
}

type Snapshot struct {
	Target
	Samples       int     `json:"samples"`
	LatencyMS     float64 `json:"latency_ms"`
	TPS           float64 `json:"tokens_per_second"`
	ErrorRate     float64 `json:"error_rate"`
	Inflight      int     `json:"inflight"`
	Degraded      bool    `json:"degraded"`
	Factor        float64 `json:"health_factor"`
	Score         float64 `json:"score"`
	Share         float64 `json:"selection_share"`
	Reason        string  `json:"reason"`
	LastProbeMS   int64   `json:"last_probe_ms"`
	LastRequestMS int64   `json:"last_request_ms"`
}

type ProbeCandidate struct {
	Scope  Scope
	Target Target
}

// PlanProbes reserves 2 degraded, 1 recently used and 1 cold scope; remaining
// slots are filled in that order. The caller's shared task lock serializes
// rounds; Redis timestamps retain rotation across replicas and restarts.
func (e *Engine) PlanProbes(ctx context.Context, candidates []ProbeCandidate) ([]ProbeCandidate, error) {
	unique := make([]ProbeCandidate, 0, len(candidates))
	keys := make([]string, 0, len(candidates))
	seen := make(map[string]bool)
	for _, c := range candidates {
		if !e.Active(c.Scope) {
			continue
		}
		key := stateKey(c.Scope, c.Target)
		if seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
		unique = append(unique, c)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	states, err := e.store.Read(ctx, keys)
	if err != nil {
		return nil, err
	}
	if len(states) != len(unique) {
		return nil, errors.New("invalid probe state count")
	}
	var buckets [3][]int
	now := e.now().UnixMilli()
	for i, state := range states {
		bucket := 2
		if state.Degraded {
			bucket = 0
		} else if state.LastRequest > 0 && now-state.LastRequest <= sampleTTL.Milliseconds() {
			bucket = 1
		}
		buckets[bucket] = append(buckets[bucket], i)
	}
	for i := range buckets {
		slices.SortStableFunc(buckets[i], func(a, b int) int {
			if states[a].LastProbe < states[b].LastProbe {
				return -1
			}
			if states[a].LastProbe > states[b].LastProbe {
				return 1
			}
			return strings.Compare(keys[a], keys[b])
		})
	}
	selected := make([]ProbeCandidate, 0, 4)
	var offsets [3]int
	for bucket, reserved := range []int{2, 1, 1} {
		for offsets[bucket] < len(buckets[bucket]) && offsets[bucket] < reserved {
			selected = append(selected, unique[buckets[bucket][offsets[bucket]]])
			offsets[bucket]++
		}
	}
	for bucket := range buckets {
		for len(selected) < 4 && offsets[bucket] < len(buckets[bucket]) {
			selected = append(selected, unique[buckets[bucket][offsets[bucket]]])
			offsets[bucket]++
		}
	}
	return selected, nil
}

type Store interface {
	Read(context.Context, []string) ([]State, error)
	Update(context.Context, string, func(*State) error) error
}
type Engine struct {
	config        Config
	store         Store
	now           func() time.Time
	registryMu    sync.Mutex
	registryUntil map[string]int64
}

func New(c Config, store Store) *Engine {
	if c.Mode != "active" && c.Mode != "shadow" {
		c.Mode = "off"
	}
	return &Engine{config: c, store: store, now: time.Now, registryUntil: make(map[string]int64)}
}
func (e *Engine) Config() Config { return e.config }
func (e *Engine) Enabled(s Scope) bool {
	return e != nil && e.config.Mode != "off" && len(s.Group) > 0 && len(s.Group) <= 64 && len(s.Model) > 0 && len(s.Model) <= 200 && len(s.Effort) <= 32 &&
		(s.Endpoint == "/v1/responses" || s.Endpoint == "/v1/chat/completions" || s.Endpoint == "/v1/messages") &&
		(slices.Contains(e.config.Groups, s.Group) || slices.Contains(e.config.Groups, "*")) &&
		(slices.Contains(e.config.Models, s.Model) || slices.Contains(e.config.Models, "*"))
}
func (e *Engine) Active(s Scope) bool { return e.Enabled(s) && e.config.Mode == "active" }
func (e *Engine) RespectsPriority(group string) bool {
	return slices.Contains(e.config.PriorityGroups, group)
}

func stateKey(s Scope, t Target) string {
	// Do not put caller strings, endpoint credentials or prompts in Redis keys.
	b, _ := common.Marshal([]any{s.Group, s.Model, s.Endpoint, s.Stream, s.Effort, t.ID, t.Upstream, t.Version})
	h := sha256.Sum256(b)
	return "model-route:v1:" + hex.EncodeToString(h[:])
}

func prune(s *State, now int64) {
	s.Samples = slices.DeleteFunc(s.Samples, func(m measurement) bool { return m.At < now-sampleTTL.Milliseconds() })
	for id, f := range s.Flights {
		if f.Expires <= now {
			delete(s.Flights, id)
		}
	}
	if s.Flights == nil {
		s.Flights = make(map[string]flight)
	}
	// Streaks outlive a full bounded probe sweep (64 scopes / 4 per round).
	// Old evidence is discarded together with the shared state retention window.
	if now-s.LastEvent > stateTTL.Milliseconds() {
		s.Failures = 0
		s.Successes = 0
	}
}

func (e *Engine) Begin(ctx context.Context, s Scope, t Target, probe bool) (Lease, error) {
	if !e.Enabled(s) {
		return Lease{}, nil
	}
	if t.ID <= 0 {
		return Lease{}, errors.New("invalid routing target")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Lease{}, err
	}
	l := Lease{Key: stateKey(s, t), ID: hex.EncodeToString(nonce[:]), Stream: s.Stream}
	now := e.now().UnixMilli()
	err := e.store.Update(ctx, l.Key, func(st *State) error {
		prune(st, now)
		if len(st.Flights) >= 256 {
			return errors.New("routing observation lease limit")
		}
		if probe {
			for _, f := range st.Flights {
				if f.Probe {
					return errors.New("recovery probe already running")
				}
			}
		}
		// Live traffic at reduced weight can provide recovery evidence too, but
		// only one admitted recovery observation may run at a time.
		recovery := probe || st.Degraded
		if !probe && recovery {
			for _, f := range st.Flights {
				if f.Probe {
					recovery = false
					break
				}
			}
		}
		st.Flights[l.ID] = flight{Epoch: st.Epoch, Expires: now + leaseTTL.Milliseconds(), Probe: recovery, Synthetic: probe}
		if probe {
			st.LastProbe = now
		} else {
			st.LastRequest = now
		}
		return nil
	})
	if err != nil {
		return Lease{}, err
	}
	// Registry contains bounded routing metadata only, so scheduled probes can
	// revisit the exact protocol/effort of live traffic (never request content).
	if !probe {
		e.registryMu.Lock()
		registered := e.registryUntil[l.Key] > now
		e.registryMu.Unlock()
		if registered {
			return l, nil
		}
		copy := s
		copy.Excluded = nil
		registryErr := e.store.Update(ctx, "model-route:scopes:"+strconv.Itoa(t.ID), func(st *State) error {
			for i, known := range st.Known {
				if known.Group == copy.Group && known.Model == copy.Model && known.Endpoint == copy.Endpoint && known.Stream == copy.Stream && known.Effort == copy.Effort {
					// Keep recently observed scopes ahead of cold entries when
					// the bounded registry needs room for a new combination.
					st.Known = append(slices.Delete(st.Known, i, i+1), copy)
					return nil
				}
			}
			if len(st.Known) >= 64 {
				st.Known = st.Known[len(st.Known)-63:]
			}
			st.Known = append(st.Known, copy)
			return nil
		})
		if registryErr == nil {
			// Cache discovery acknowledgement only, never health/load/score state.
			// Refresh well within Redis's 24h TTL; a failed write is retried next time.
			e.registryMu.Lock()
			if len(e.registryUntil) >= 4096 {
				clear(e.registryUntil)
			}
			e.registryUntil[l.Key] = now + (5 * time.Minute).Milliseconds()
			e.registryMu.Unlock()
		}
	}
	return l, nil
}

func (e *Engine) KnownScopes(ctx context.Context, channelID int) ([]Scope, error) {
	states, err := e.store.Read(ctx, []string{"model-route:scopes:" + strconv.Itoa(channelID)})
	if err != nil {
		return nil, err
	}
	if len(states) == 0 {
		return nil, nil
	}
	return states[0].Known, nil
}

func (e *Engine) Finish(ctx context.Context, l Lease, o Observation) error {
	if l.ID == "" {
		return nil
	}
	now := e.now().UnixMilli()
	return e.store.Update(ctx, l.Key, func(st *State) error {
		prune(st, now)
		f, ok := st.Flights[l.ID]
		if !ok {
			return nil
		}
		delete(st.Flights, l.ID)
		// A result from an earlier health generation cannot undo a later decision.
		if f.Epoch != st.Epoch || o.Outcome == Ignored {
			return nil
		}
		if o.Outcome == Throttled {
			st.Cooldown = now + time.Minute.Milliseconds()
			return nil
		}
		if o.Outcome != Success && o.Outcome != Failure {
			return nil
		}
		st.LastEvent = now
		if o.Outcome == Failure {
			st.Successes = 0
			st.Failures = min(2, st.Failures+1)
			if st.Failures == 2 && !st.Degraded {
				st.Degraded = true
				st.Epoch++
			}
		} else {
			st.Failures = 0
			if st.Degraded && f.Probe {
				st.Successes++
				if st.Successes >= 2 {
					st.Degraded = false
					st.Successes = 0
					st.Epoch++
				}
			}
		}
		lat := o.DurationMS
		if l.Stream {
			lat = o.FirstContentMS
		}
		if !f.Synthetic && (o.Outcome == Failure || lat > 0) {
			m := measurement{At: now, Latency: float64(lat), Failed: o.Outcome == Failure}
			if o.OutputTokens > 0 && o.DurationMS > o.FirstContentMS {
				m.TPS = float64(o.OutputTokens) * 1000 / float64(o.DurationMS-o.FirstContentMS)
			}
			st.Samples = append(st.Samples, m)
			if len(st.Samples) > 100 {
				st.Samples = st.Samples[len(st.Samples)-100:]
			}
		}
		return nil
	})
}

func (e *Engine) Snapshot(ctx context.Context, s Scope, targets []Target) ([]Snapshot, error) {
	if !e.Enabled(s) {
		return nil, nil
	}
	if len(targets) > 256 {
		return nil, errors.New("too many routing candidates")
	}
	keys := make([]string, len(targets))
	for i, t := range targets {
		keys[i] = stateKey(s, t)
	}
	states, err := e.store.Read(ctx, keys)
	if err != nil {
		return nil, err
	}
	now := e.now().UnixMilli()
	result := make([]Snapshot, len(targets))
	for i, st := range states {
		prune(&st, now)
		r := Snapshot{Target: targets[i], Factor: 1, Inflight: len(st.Flights), Degraded: st.Degraded, Reason: "insufficient_samples", LastProbeMS: st.LastProbe, LastRequestMS: st.LastRequest}
		if st.Degraded {
			r.Factor = 0.1
			r.Reason = "two_failures"
		}
		if st.Cooldown > now {
			r.Factor *= 0.1
			r.Reason = "rate_limited"
		}
		var latencies []float64
		failures := 0
		var speeds []float64
		for _, m := range st.Samples {
			if m.Failed {
				failures++
			} else if m.Latency > 0 {
				latencies = append(latencies, m.Latency)
				if m.TPS > 0 {
					speeds = append(speeds, m.TPS)
				}
			}
		}
		r.Samples = len(latencies)
		if len(st.Samples) > 0 {
			r.ErrorRate = float64(failures) / float64(len(st.Samples))
		}
		if r.Samples >= 5 {
			slices.Sort(latencies)
			r.LatencyMS = latencies[len(latencies)/2]
			if r.Reason == "insufficient_samples" {
				r.Reason = "measured"
			}
		}
		if len(speeds) >= 5 {
			slices.Sort(speeds)
			r.TPS = speeds[len(speeds)/2]
		}
		result[i] = r
	}
	return result, nil
}

// Select returns only a member of targets. Zero means no selection (or shadow).
// draw is injected by the host to make behavior reproducible in regression tests.
func (e *Engine) Select(ctx context.Context, s Scope, targets []Target, draw float64) (int, []Snapshot, error) {
	states, err := e.Snapshot(ctx, s, targets)
	if err != nil || len(states) == 0 {
		return 0, states, err
	}
	baseline := 1000.0
	var measured []float64
	for _, r := range states {
		if r.LatencyMS > 0 {
			measured = append(measured, r.LatencyMS)
		}
	}
	if len(measured) > 0 {
		slices.Sort(measured)
		baseline = measured[len(measured)/2]
	}
	maxPriority := int64(math.MinInt64)
	positiveWeight := false
	for _, r := range states {
		if !slices.Contains(s.Excluded, r.ID) {
			maxPriority = max(maxPriority, r.Priority)
		}
	}
	for _, r := range states {
		if !slices.Contains(s.Excluded, r.ID) && (!e.RespectsPriority(s.Group) || r.Priority == maxPriority) && r.Weight > 0 {
			positiveWeight = true
		}
	}
	var total float64
	for i := range states {
		r := &states[i]
		if slices.Contains(s.Excluded, r.ID) || e.RespectsPriority(s.Group) && r.Priority != maxPriority || positiveWeight && r.Weight <= 0 {
			r.Reason = "excluded_or_standby"
			continue
		}
		lat := r.LatencyMS
		if lat <= 0 {
			lat = baseline
		}
		r.Score = max(1, lat) * (1 + 4*r.ErrorRate) * (1 + float64(r.Inflight)/4)
		if r.TPS > 0 {
			r.Score *= 1 + 10/(10+r.TPS)
		}
		r.Share = max(1, r.Weight) * r.Factor / r.Score
		total += r.Share
	}
	if total <= 0 {
		return 0, states, nil
	}
	for i := range states {
		states[i].Share /= total
	}
	// A small exploration floor gives unmeasured/recovered peers new evidence.
	eligible := 0
	for _, r := range states {
		if r.Share > 0 {
			eligible++
		}
	}
	for i := range states {
		if states[i].Share > 0 {
			states[i].Share = 0.95*states[i].Share + 0.05/float64(eligible)
		}
	}
	if e.config.Mode != "active" {
		return 0, states, nil
	}
	if math.IsNaN(draw) || draw < 0 || draw >= 1 {
		draw = 0.5
	}
	last := 0
	for _, r := range states {
		if r.Share <= 0 {
			continue
		}
		last = r.ID
		draw -= r.Share
		if draw < 0 {
			return r.ID, states, nil
		}
	}
	return last, states, nil
}
