package modelroute

import (
	"context"
	"errors"
	"github.com/QuantumNous/new-api/common"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type registryWriteCounter struct {
	Store
	writes int
	fail   bool
}

func (s *registryWriteCounter) Update(ctx context.Context, key string, fn func(*State) error) error {
	if strings.HasPrefix(key, "model-route:scopes:") {
		s.writes++
		if s.fail {
			return errors.New("registry temporarily unavailable")
		}
	}
	return s.Store.Update(ctx, key, fn)
}

func TestModelRoutingRegistryRefreshIsBoundedAndRetries(t *testing.T) {
	store := &registryWriteCounter{Store: NewMemoryStore()}
	e := New(Config{Mode: "active", Groups: []string{"g"}, Models: []string{"*"}}, store)
	now := time.Now()
	e.now = func() time.Time { return now }
	s := Scope{Group: "g", Model: "m", Endpoint: "/v1/responses"}
	target := Target{ID: 1}
	request := func() {
		lease, err := e.Begin(context.Background(), s, target, false)
		require.NoError(t, err)
		require.NoError(t, e.Finish(context.Background(), lease, Observation{Outcome: Success, DurationMS: 10}))
	}
	for range 5 {
		request()
	}
	known, err := e.KnownScopes(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, known, 1)
	require.Equal(t, 1, store.writes, "repeat traffic must not rewrite unchanged discovery metadata on every request")
	now = now.Add(6 * time.Minute)
	store.fail = true
	request()
	store.fail = false
	request()
	require.Equal(t, 3, store.writes, "failed registration must be retried, not cached as successful")
	request()
	require.Equal(t, 3, store.writes)
	s.Model = "another-model"
	request()
	known, err = e.KnownScopes(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, known, 2, "a new scope must be discovered immediately")
}

func TestModelRoutingRegistryAdmitsNewScopeWhenFull(t *testing.T) {
	store := NewMemoryStore()
	e := New(Config{Mode: "active", Groups: []string{"g"}, Models: []string{"*"}}, store)
	now := time.Now()
	e.now = func() time.Time { return now }
	target := Target{ID: 1}
	register := func(name string) {
		lease, err := e.Begin(context.Background(), Scope{Group: "g", Model: name, Endpoint: "/v1/responses"}, target, false)
		require.NoError(t, err)
		require.NoError(t, e.Finish(context.Background(), lease, Observation{Outcome: Success, DurationMS: 10}))
	}
	for i := range 64 {
		register("model-" + strconv.Itoa(i))
	}
	// Refresh the first scope so it remains recent when a new one arrives.
	now = now.Add(6 * time.Minute)
	register("model-0")
	register("model-new")
	known, err := e.KnownScopes(context.Background(), target.ID)
	require.NoError(t, err)
	require.Len(t, known, 64)
	models := make([]string, 0, len(known))
	for _, scope := range known {
		models = append(models, scope.Model)
	}
	assert.Contains(t, models, "model-0")
	assert.Contains(t, models, "model-new", "a full registry must not permanently exclude new live scopes")
	assert.NotContains(t, models, "model-1", "the least recently registered scope should be replaced")
}

func TestModelRoutingDedicatedRedisDoesNotEnableGlobalCache(t *testing.T) {
	oldEngine, oldRedis, oldCache, oldClient := Default, common.RedisEnabled, common.MemoryCacheEnabled, common.RDB
	t.Cleanup(func() {
		Default, common.RedisEnabled, common.MemoryCacheEnabled, common.RDB = oldEngine, oldRedis, oldCache, oldClient
	})
	common.RedisEnabled, common.MemoryCacheEnabled, common.RDB = false, false, nil
	r := miniredis.RunT(t)
	r.RequireAuth("routing-test-password")
	t.Setenv("MODEL_ROUTING_MODE", "active")
	t.Setenv("MODEL_ROUTING_GROUPS", "g")
	t.Setenv("MODEL_ROUTING_MODELS", "m")
	t.Setenv("MODEL_ROUTING_REDIS_URL", "redis://:routing-test-password@"+r.Addr()+"/0")
	closeClient, err := ConfigureFromEnv()
	require.NoError(t, err)
	t.Cleanup(closeClient)
	s := Scope{Group: "g", Model: "m", Endpoint: "/v1/responses"}
	lease, err := Default.Begin(context.Background(), s, Target{ID: 1}, false)
	require.NoError(t, err)
	require.NoError(t, Default.Finish(context.Background(), lease, Observation{Outcome: Success, DurationMS: 20}))
	states, err := Default.Snapshot(context.Background(), s, []Target{{ID: 1}})
	require.NoError(t, err)
	require.Equal(t, 1, states[0].Samples)
	require.False(t, common.RedisEnabled)
	require.False(t, common.MemoryCacheEnabled)
	require.Nil(t, common.RDB)
	t.Setenv("MODEL_ROUTING_REDIS_URL", "redis://:incorrect-test-password@"+r.Addr()+"/0")
	closeBadAuth, err := ConfigureFromEnv()
	require.NoError(t, err, "runtime authentication errors must not terminate the gateway")
	t.Cleanup(closeBadAuth)
	_, _, err = Default.Select(context.Background(), s, []Target{{ID: 1}}, 0.5)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "incorrect-test-password")
	r.Close()
	closeUnavailable, err := ConfigureFromEnv()
	require.NoError(t, err, "startup while routing Redis is down retains request fallback")
	t.Cleanup(closeUnavailable)
	_, _, err = Default.Select(context.Background(), s, []Target{{ID: 1}}, 0.5)
	require.Error(t, err, "unavailable routing store is returned to the host fallback, not a fatal exit")
	t.Setenv("MODEL_ROUTING_REDIS_URL", "redis://:do-not-log-this@host/invalid-db")
	_, err = ConfigureFromEnv()
	require.Error(t, err)
	require.NotContains(t, err.Error(), "do-not-log-this")
	t.Setenv("MODEL_ROUTING_MODE", "off")
	closeOff, err := ConfigureFromEnv()
	require.NoError(t, err, "off must not initialize an unused routing connection")
	closeOff()
	require.Equal(t, "off", Default.Config().Mode)
}

func TestModelRoutingRealRedisProcessWorker(t *testing.T) {
	mode := os.Getenv("MODEL_ROUTE_TEST_WORKER")
	if mode == "" {
		t.Skip("subprocess worker")
	}
	client := redis.NewClient(&redis.Options{Addr: os.Getenv("MODEL_ROUTE_TEST_REDIS"), MaxRetries: -1})
	defer client.Close()
	e := New(Config{Mode: "active", Groups: []string{"test"}, Models: []string{"*"}}, RedisStore{Client: client})
	s := Scope{Group: "test", Model: os.Getenv("MODEL_ROUTE_TEST_MODEL"), Endpoint: "/v1/responses"}
	lease, err := e.Begin(context.Background(), s, Target{ID: 1, Weight: 100}, false)
	require.NoError(t, err)
	if mode == "abandon" {
		return
	} // Process exits without releasing its observation.
	outcome := Failure
	if mode == "success" {
		outcome = Success
	}
	observation := Observation{Outcome: outcome, DurationMS: 20}
	require.NoError(t, e.Finish(context.Background(), lease, observation))
	require.NoError(t, e.Finish(context.Background(), lease, observation))
}

func TestModelRoutingRealRedisProcesses(t *testing.T) {
	address := os.Getenv("MODEL_ROUTE_TEST_REDIS")
	if address == "" {
		t.Skip("dedicated real Redis required")
	}
	client := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	require.NoError(t, client.Ping(ctx).Err())
	e := New(Config{Mode: "active", Groups: []string{"test"}, Models: []string{"*"}}, RedisStore{Client: client})
	s := Scope{Group: "test", Model: "process-" + strconv.FormatInt(time.Now().UnixNano(), 10), Endpoint: "/v1/responses"}
	target := Target{ID: 1, Weight: 100}
	worker := func(mode string) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestModelRoutingRealRedisProcessWorker$", "-test.timeout=10s")
		cmd.Env = append(os.Environ(), "MODEL_ROUTE_TEST_WORKER="+mode, "MODEL_ROUTE_TEST_MODEL="+s.Model)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
	}
	for i := range 2 {
		worker("failure")
		states, err := e.Snapshot(ctx, s, []Target{target})
		require.NoError(t, err)
		require.Equal(t, i == 1, states[0].Degraded)
	}
	for i := range 2 {
		worker("success")
		states, err := e.Snapshot(ctx, s, []Target{target})
		require.NoError(t, err)
		require.Equal(t, i == 0, states[0].Degraded, "duplicate cross-process finish must not cause early recovery")
	}
	states, err := e.Snapshot(ctx, s, []Target{target})
	require.NoError(t, err)
	require.Equal(t, 1.0, states[0].Factor)
	require.Zero(t, states[0].Inflight)
	worker("abandon")
	states, err = e.Snapshot(ctx, s, []Target{target})
	require.NoError(t, err)
	require.Equal(t, 1, states[0].Inflight)
	now := time.Now().Add(11 * time.Minute)
	e.now = func() time.Time { return now }
	states, err = e.Snapshot(ctx, s, []Target{target})
	require.NoError(t, err)
	require.Zero(t, states[0].Inflight, "an exited worker cannot leave permanent load")
}

func TestModelRoutingIsolationAndRecovery(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	e := New(Config{Mode: "active", Groups: []string{"g"}, Models: []string{"a", "b"}}, NewMemoryStore())
	e.now = func() time.Time { return now }
	s := Scope{Group: "g", Model: "a", Endpoint: "/v1/responses", Stream: true}
	target := Target{ID: 9, Upstream: "a", Weight: 100}
	stale, err := e.Begin(ctx, s, target, false)
	require.NoError(t, err)
	for range 2 {
		lease, err := e.Begin(ctx, s, target, false)
		require.NoError(t, err)
		require.NoError(t, e.Finish(ctx, lease, Observation{Outcome: Failure}))
	}
	require.NoError(t, e.Finish(ctx, stale, Observation{Outcome: Success, FirstContentMS: 10}))
	states, err := e.Snapshot(ctx, s, []Target{target})
	require.NoError(t, err)
	assert.True(t, states[0].Degraded, "late pre-degradation success must not restore")
	assert.Equal(t, 0.1, states[0].Factor)
	other := s
	other.Model = "b"
	states, err = e.Snapshot(ctx, other, []Target{target})
	require.NoError(t, err)
	assert.False(t, states[0].Degraded, "another model must not be degraded")
	for i := range 2 {
		lease, err := e.Begin(ctx, s, target, true)
		require.NoError(t, err)
		_, err = e.Begin(ctx, s, target, true)
		require.Error(t, err, "recovery probes must be serial")
		require.NoError(t, e.Finish(ctx, lease, Observation{Outcome: Success, FirstContentMS: 100}))
		require.NoError(t, e.Finish(ctx, lease, Observation{Outcome: Success, FirstContentMS: 100}))
		states, err = e.Snapshot(ctx, s, []Target{target})
		require.NoError(t, err)
		assert.Equal(t, i == 0, states[0].Degraded, "duplicate finish must not count twice")
	}
	assert.Equal(t, 1.0, states[0].Factor, "two distinct successful recovery observations restore the original weight")
	now = now.Add(16 * time.Minute)
	states, err = e.Snapshot(ctx, s, []Target{target})
	require.NoError(t, err)
	assert.Zero(t, states[0].Samples, "expired measurements are unknown, not fastest")
}

func TestModelRoutingProbeBudgetAndRotation(t *testing.T) {
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	e := New(Config{Mode: "active", Groups: []string{"g"}, Models: []string{"*"}}, NewMemoryStore())
	e.now = func() time.Time { return now }
	var candidates []ProbeCandidate
	for i, name := range []string{"bad-old", "bad-middle", "bad-new", "recent", "cold-old", "cold-new"} {
		s := Scope{Group: "g", Model: name, Endpoint: "/v1/responses"}
		target := Target{ID: 1, Upstream: name}
		candidates = append(candidates, ProbeCandidate{Scope: s, Target: target})
		err := e.store.Update(context.Background(), stateKey(s, target), func(st *State) error {
			st.Degraded = i < 3
			st.LastProbe = now.Add(time.Duration(i-10) * time.Minute).UnixMilli()
			if i == 3 {
				st.LastRequest = now.Add(-time.Minute).UnixMilli()
			}
			return nil
		})
		require.NoError(t, err)
	}
	candidates = append(candidates, candidates[0]) // Duplicate discovery cannot spend a second slot.
	selected, err := e.PlanProbes(context.Background(), candidates)
	require.NoError(t, err)
	var names []string
	for _, p := range selected {
		names = append(names, p.Scope.Model)
	}
	require.Equal(t, []string{"bad-old", "bad-middle", "recent", "cold-old"}, names)
	for _, p := range selected {
		lease, err := e.Begin(context.Background(), p.Scope, p.Target, true)
		require.NoError(t, err)
		require.NoError(t, e.Finish(context.Background(), lease, Observation{Outcome: Ignored}))
	}
	now = now.Add(15 * time.Minute)
	selected, err = e.PlanProbes(context.Background(), candidates)
	require.NoError(t, err)
	require.Len(t, selected, 4)
	require.Equal(t, "bad-new", selected[0].Scope.Model, "least recently probed degraded scope must get its turn")
	states, err := e.Snapshot(context.Background(), candidates[0].Scope, []Target{candidates[0].Target})
	require.NoError(t, err)
	require.Equal(t, now.Add(-15*time.Minute).UnixMilli(), states[0].LastProbeMS)
	// Unused reservations must be filled without losing least-recent ordering.
	for _, tc := range []struct {
		name       string
		candidates []ProbeCandidate
		want       []string
	}{
		{"no-recent", []ProbeCandidate{candidates[0], candidates[1], candidates[2], candidates[4], candidates[5]}, []string{"bad-new", "bad-old", "cold-new", "bad-middle"}},
		{"only-cold", []ProbeCandidate{candidates[4], candidates[5]}, []string{"cold-new", "cold-old"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, err := e.PlanProbes(context.Background(), tc.candidates)
			require.NoError(t, err)
			names := make([]string, len(selected))
			for i, p := range selected {
				names[i] = p.Scope.Model
			}
			if tc.name == "no-recent" {
				// Two equal timestamps have a stable hashed-key tie break; assert
				// membership, retaining exact oldest-first checks above.
				require.ElementsMatch(t, tc.want, names)
			} else {
				require.Equal(t, tc.want, names)
			}
		})
	}
}

func TestModelRoutingSyntheticProbesDoNotChangeTrafficScore(t *testing.T) {
	e := New(Config{Mode: "active", Groups: []string{"g"}, Models: []string{"m"}}, NewMemoryStore())
	s := Scope{Group: "g", Model: "m", Endpoint: "/v1/responses"}
	target := Target{ID: 1, Weight: 100}
	for range 5 {
		lease, err := e.Begin(context.Background(), s, target, false)
		require.NoError(t, err)
		require.NoError(t, e.Finish(context.Background(), lease, Observation{Outcome: Success, DurationMS: 100}))
	}
	for _, outcome := range []Outcome{Failure, Failure, Success, Success} {
		lease, err := e.Begin(context.Background(), s, target, true)
		require.NoError(t, err)
		require.NoError(t, e.Finish(context.Background(), lease, Observation{Outcome: outcome, DurationMS: 1}))
	}
	states, err := e.Snapshot(context.Background(), s, []Target{target})
	require.NoError(t, err)
	require.Equal(t, 5, states[0].Samples)
	require.Zero(t, states[0].ErrorRate)
	require.Equal(t, 100.0, states[0].LatencyMS)
	require.Equal(t, 1.0, states[0].Factor)
}

func TestModelRoutingSelectionAndEligibility(t *testing.T) {
	e := New(Config{Mode: "active", Groups: []string{"g"}, Models: []string{"a"}}, NewMemoryStore())
	s := Scope{Group: "g", Model: "a", Endpoint: "/v1/responses", Stream: true}
	ctx := context.Background()
	targets := []Target{{ID: 9, Upstream: "a", Weight: 100}, {ID: 18, Upstream: "a", Weight: 100}}
	for i, target := range targets {
		for range 5 {
			lease, err := e.Begin(ctx, s, target, false)
			require.NoError(t, err)
			require.NoError(t, e.Finish(ctx, lease, Observation{Outcome: Success, FirstContentMS: int64(100 + i*2000)}))
		}
	}
	id, scored, err := e.Select(ctx, s, targets, 0.2)
	require.NoError(t, err)
	assert.Equal(t, 9, id)
	assert.Equal(t, 100.0, scored[0].Score)
	assert.Equal(t, 2100.0, scored[1].Score)
	assert.InDelta(t, 0.95*(21.0/22)+0.025, scored[0].Share, 1e-12)
	assert.InDelta(t, 0.95*(1.0/22)+0.025, scored[1].Share, 1e-12)
	id, _, err = e.Select(ctx, s, targets[1:], 0.2)
	require.NoError(t, err)
	assert.Equal(t, 18, id, "scoring cannot add candidates outside the supplied eligible set")
	s.Group = "unauthorized"
	id, _, err = e.Select(ctx, s, targets, 0.2)
	require.NoError(t, err)
	assert.Zero(t, id)
}

func TestModelRoutingEffectiveContent(t *testing.T) {
	for _, tc := range []struct {
		payload string
		want    bool
	}{
		{`{"type":"response.created"}`, false},
		{`{"choices":[{"delta":{"role":"assistant"}}]}`, false},
		{`{"type":"response.output_text.delta","delta":"  "}`, false},
		{`{"type":"response.output_text.delta","delta":"hello"}`, true},
		{`{"choices":[{"delta":{"content":"hello"}}]}`, true},
		{`{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{"}}]}}]}`, true},
		{`{"type":"response.function_call_arguments.delta","delta":"{"}`, true},
		{`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`, true},
		{`{"error":{"message":"fast failure"}}`, false},
		{`not json`, false},
		{`{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`, true},
		{`{"candidates":[{"content":{"parts":[{"thought":true,"text":"reasoning"}]}}]}`, false},
	} {
		assert.Equal(t, tc.want, HasContent(tc.payload), tc.payload)
	}
}

func TestModelRoutingRecoveryAcrossProbeIntervals(t *testing.T) {
	now := time.Now()
	e := New(Config{Mode: "active", Groups: []string{"g"}, Models: []string{"m"}}, NewMemoryStore())
	e.now = func() time.Time { return now }
	s := Scope{Group: "g", Model: "m", Endpoint: "/v1/responses"}
	target := Target{ID: 1}
	for i, outcome := range []Outcome{Failure, Failure, Success, Success} {
		lease, err := e.Begin(context.Background(), s, target, true)
		require.NoError(t, err)
		require.NoError(t, e.Finish(context.Background(), lease, Observation{Outcome: outcome}))
		if i == 1 {
			states, err := e.Snapshot(context.Background(), s, []Target{target})
			require.NoError(t, err)
			assert.True(t, states[0].Degraded, "two scheduled failures must degrade")
		}
		now = now.Add(4*time.Hour + time.Second)
	}
	states, err := e.Snapshot(context.Background(), s, []Target{target})
	require.NoError(t, err)
	assert.False(t, states[0].Degraded)
}

func TestModelRoutingZeroWeightAndPriority(t *testing.T) {
	s := Scope{Group: "g", Model: "m", Endpoint: "/v1/responses"}
	targets := []Target{{ID: 1, Weight: 0, Priority: 10}, {ID: 2, Weight: 10, Priority: 1}}
	e := New(Config{Mode: "active", Groups: []string{"g"}, Models: []string{"m"}}, NewMemoryStore())
	id, states, err := e.Select(context.Background(), s, targets, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, id)
	assert.Zero(t, states[0].Share)
	targets[0].Weight = 10
	id, _, err = e.Select(context.Background(), s, targets, 0.99)
	require.NoError(t, err)
	assert.Equal(t, 2, id, "dynamic pool must include lower-priority eligible target")
	e.config.PriorityGroups = []string{"g"}
	id, _, err = e.Select(context.Background(), s, targets, 0.99)
	require.NoError(t, err)
	assert.Equal(t, 1, id, "explicit main/standby policy must remain hard")
}

func TestModelRoutingRedisSharedLeasesAndOutage(t *testing.T) {
	r := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: r.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	cfg := Config{Mode: "active", Groups: []string{"g"}, Models: []string{"m"}}
	a, b := New(cfg, RedisStore{Client: client}), New(cfg, RedisStore{Client: client})
	s := Scope{Group: "g", Model: "m", Endpoint: "/v1/responses"}
	target := Target{ID: 1, Weight: 10}
	ctx := context.Background()
	for _, e := range []*Engine{a, b} {
		lease, err := e.Begin(ctx, s, target, false)
		require.NoError(t, err)
		require.NoError(t, e.Finish(ctx, lease, Observation{Outcome: Failure}))
	}
	states, err := a.Snapshot(ctx, s, []Target{target})
	require.NoError(t, err)
	assert.True(t, states[0].Degraded)
	for _, e := range []*Engine{a, b} {
		lease, err := e.Begin(ctx, s, target, false)
		require.NoError(t, err)
		require.NoError(t, e.Finish(ctx, lease, Observation{Outcome: Success, DurationMS: 100}))
	}
	states, err = b.Snapshot(ctx, s, []Target{target})
	require.NoError(t, err)
	assert.False(t, states[0].Degraded)
	assert.Zero(t, states[0].Inflight)
	known, err := a.KnownScopes(ctx, 1)
	require.NoError(t, err)
	require.Len(t, known, 1)
	r.Close()
	_, _, err = a.Select(ctx, s, []Target{target}, 0.5)
	require.Error(t, err)
}
