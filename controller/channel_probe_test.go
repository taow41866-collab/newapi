package controller

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/modelroute"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Worker nodes are complete authenticated relay HTTP servers in separate OS
// processes. Only routing state is shared; every node owns its test database.
func TestModelRoutingGatewayWorker(t *testing.T) {
	addressFile := os.Getenv("MODEL_ROUTE_NODE_ADDRESS_FILE")
	if addressFile == "" {
		t.Skip("gateway subprocess helper")
	}
	db := modelManagementDB(t, "sqlite", "")
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Log{}, &model.UserSubscription{}, &model.SubscriptionPlan{}))
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	common.RetryTimes = 0
	mode := os.Getenv("MODEL_ROUTE_NODE_MODE")
	if mode == "" {
		mode = "active"
	}
	t.Setenv("MODEL_ROUTING_MODE", mode)
	t.Setenv("MODEL_ROUTING_GROUPS", "default")
	t.Setenv("MODEL_ROUTING_MODELS", "routing-node")
	t.Setenv("MODEL_ROUTING_REDIS_URL", "redis://"+os.Getenv("MODEL_ROUTE_TEST_REDIS")+"/0")
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	closeRouting, err := modelroute.ConfigureFromEnv()
	require.NoError(t, err)
	defer closeRouting()
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"routing-node":0.002}`))
	operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
	user := model.User{Username: "routing-node-user", Group: "default", Status: common.UserStatusEnabled, Quota: 1000000, AffCode: "routing-node"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: strings.Repeat("c", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000000}
	require.NoError(t, db.Create(&token).Error)
	base := os.Getenv("MODEL_ROUTE_NODE_UPSTREAM")
	channel := model.Channel{Id: 1, Name: "node-upstream", Group: "default", Models: "routing-node", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI, BaseURL: &base, Key: "local-node-key", Weight: common.GetPointer(uint(100))}
	require.NoError(t, channel.Insert())
	isolatedBase := base + "/isolated"
	isolated := model.Channel{Id: 2, Name: "isolated-upstream", Group: "isolated", Models: "routing-node", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI, BaseURL: &isolatedBase, Key: "local-node-key", Weight: common.GetPointer(uint(100))}
	require.NoError(t, isolated.Insert())
	router := gin.New()
	router.POST("/v1/chat/completions", middleware.TokenAuth(), middleware.Distribute(), func(c *gin.Context) { Relay(c, types.RelayFormatOpenAI) })
	router.GET("/_test/quota", middleware.TokenAuth(), func(c *gin.Context) {
		var currentUser model.User
		var currentToken model.Token
		if db.First(&currentUser, user.Id).Error != nil || db.First(&currentToken, token.Id).Error != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.JSON(http.StatusOK, gin.H{"wallet": currentUser.Quota, "token": currentToken.RemainQuota})
	})
	server := httptest.NewServer(router)
	defer server.Close()
	require.NoError(t, os.WriteFile(addressFile, []byte(server.URL), 0600))
	// A parent-owned marker allows graceful teardown; the parent also bounds
	// process lifetime and kills only its own worker on an unexpected failure.
	deadline := time.NewTimer(150 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("parent did not stop gateway worker")
		case <-tick.C:
			if _, err := os.Stat(addressFile + ".stop"); err == nil {
				return
			}
		}
	}
}

func startModelRoutingGateway(t *testing.T, upstream, mode string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "node.address")
	logFile, err := os.Create(file + ".log")
	require.NoError(t, err)
	cmd := exec.Command(os.Args[0], "-test.run=^TestModelRoutingGatewayWorker$", "-test.timeout=160s")
	cmd.Env = append(os.Environ(), "MODEL_ROUTE_NODE_ADDRESS_FILE="+file, "MODEL_ROUTE_NODE_UPSTREAM="+upstream, "MODEL_ROUTE_NODE_MODE="+mode)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = os.WriteFile(file+".stop", nil, 0600)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("gateway worker did not exit cleanly")
		}
		_ = logFile.Close()
	})
	var node []byte
	require.Eventually(t, func() bool { node, err = os.ReadFile(file); return err == nil && len(node) > 0 }, 15*time.Second, 20*time.Millisecond)
	return string(node)
}

func TestModelRoutingGatewayPerformance(t *testing.T) {
	address := os.Getenv("MODEL_ROUTE_TEST_REDIS")
	if address == "" {
		t.Skip("dedicated real Redis required")
	}
	client := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1})
	defer client.Close()
	require.NoError(t, client.Ping(context.Background()).Err())
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond) // Fixed workload, not tuned against the acceptance result.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"local","object":"chat.completion","model":"routing-node","choices":[{"index":0,"message":{"role":"assistant","content":"local-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()
	modes := []string{"off", "shadow", "active"}
	nodes := make(map[string]string)
	for _, mode := range modes {
		nodes[mode] = startModelRoutingGateway(t, upstream.URL, mode)
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	defer httpClient.CloseIdleConnections()
	send := func(mode string) float64 {
		request, err := http.NewRequest(http.MethodPost, nodes[mode]+"/v1/chat/completions", strings.NewReader(`{"model":"routing-node","messages":[{"role":"user","content":"hello"}],"max_tokens":8}`))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer sk-"+strings.Repeat("c", 48))
		started := time.Now()
		response, err := httpClient.Do(request)
		require.NoError(t, err)
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		elapsed := float64(time.Since(started)) / float64(time.Millisecond)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode, "%s", body)
		require.Contains(t, string(body), "local-ok")
		return elapsed
	}
	for _, mode := range modes {
		for range 10 {
			send(mode)
		}
	}
	type measurement struct {
		Mode              string  `json:"mode"`
		Round             int     `json:"round"`
		Requests          int     `json:"requests"`
		P50MS             float64 `json:"p50_ms"`
		P95MS             float64 `json:"p95_ms"`
		P99MS             float64 `json:"p99_ms"`
		RequestsPerSecond float64 `json:"requests_per_second"`
	}
	report := struct {
		UpstreamDelayMS int           `json:"upstream_delay_ms"`
		Concurrency     int           `json:"concurrency"`
		WarmupPerMode   int           `json:"warmup_per_mode"`
		Rounds          []measurement `json:"rounds"`
		Aggregate       []measurement `json:"aggregate"`
		RedisBefore     string        `json:"redis_before"`
		RedisAfter      string        `json:"redis_after"`
	}{UpstreamDelayMS: 30, Concurrency: 1, WarmupPerMode: 10}
	var err error
	report.RedisBefore, err = client.Info(context.Background(), "commandstats", "memory").Result()
	require.NoError(t, err)
	samples := make(map[string][]float64)
	durations := make(map[string]time.Duration)
	summarize := func(mode string, round int, values []float64, elapsed time.Duration) measurement {
		ordered := slices.Clone(values)
		slices.Sort(ordered)
		return measurement{Mode: mode, Round: round, Requests: len(values), P50MS: ordered[(len(values)*50+99)/100-1], P95MS: ordered[(len(values)*95+99)/100-1], P99MS: ordered[(len(values)*99+99)/100-1], RequestsPerSecond: float64(len(values)) / elapsed.Seconds()}
	}
	for round := range 3 {
		for position := range 3 {
			mode := modes[(round+position)%len(modes)]
			values := make([]float64, 0, 100)
			started := time.Now()
			for range 100 {
				values = append(values, send(mode))
			}
			elapsed := time.Since(started)
			report.Rounds = append(report.Rounds, summarize(mode, round+1, values, elapsed))
			samples[mode] = append(samples[mode], values...)
			durations[mode] += elapsed
		}
	}
	for _, mode := range modes {
		report.Aggregate = append(report.Aggregate, summarize(mode, 0, samples[mode], durations[mode]))
	}
	report.RedisAfter, err = client.Info(context.Background(), "commandstats", "memory").Result()
	require.NoError(t, err)
	encoded, err := common.Marshal(report)
	require.NoError(t, err)
	if file := os.Getenv("MODEL_ROUTE_PERF_REPORT"); file != "" {
		require.NoError(t, os.WriteFile(file, encoded, 0600))
	}
	for _, result := range report.Aggregate {
		t.Logf("mode=%s n=%d p50=%.3fms p95=%.3fms p99=%.3fms throughput=%.3f/s", result.Mode, result.Requests, result.P50MS, result.P95MS, result.P99MS, result.RequestsPerSecond)
	}
	baseline := report.Aggregate[0]
	for _, result := range report.Aggregate[1:] {
		assert.LessOrEqual(t, result.P95MS-baseline.P95MS, 10.0, "%s additional p95 exceeds 10ms", result.Mode)
		assert.GreaterOrEqual(t, result.RequestsPerSecond, baseline.RequestsPerSecond*0.95, "%s throughput declines over 5%%", result.Mode)
	}
}

func TestModelRoutingTwoGatewayProcesses(t *testing.T) {
	address := os.Getenv("MODEL_ROUTE_TEST_REDIS")
	if address == "" {
		t.Skip("dedicated real Redis required")
	}
	client := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1})
	defer client.Close()
	require.NoError(t, client.Ping(context.Background()).Err())
	var failUpstream atomic.Bool
	var holdUpstream atomic.Bool
	var upstreamCalls atomic.Int64
	var isolatedCalls atomic.Int64
	arrivals := make(chan struct{}, 2)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if strings.Contains(r.URL.Path, "/isolated/") {
			isolatedCalls.Add(1)
		}
		if holdUpstream.Load() {
			arrivals <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if failUpstream.Load() {
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"error":{"message":"local failure","type":"server_error"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"local","object":"chat.completion","model":"routing-node","choices":[{"index":0,"message":{"role":"assistant","content":"local-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()
	var nodes []string
	for range 2 {
		nodes = append(nodes, startModelRoutingGateway(t, upstream.URL, "active"))
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	send := func(node string, status int) {
		request, err := http.NewRequest(http.MethodPost, node+"/v1/chat/completions", strings.NewReader(`{"model":"routing-node","messages":[{"role":"user","content":"hello"}],"max_tokens":8}`))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer sk-"+strings.Repeat("c", 48))
		response, err := httpClient.Do(request)
		require.NoError(t, err)
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		require.NoError(t, err)
		require.Equal(t, status, response.StatusCode, "%s", body)
	}
	e := modelroute.New(modelroute.Config{Mode: "active", Groups: []string{"default"}, Models: []string{"routing-node"}}, modelroute.RedisStore{Client: client})
	scope := modelroute.Scope{Group: "default", Model: "routing-node", Endpoint: "/v1/chat/completions"}
	target := model.ModelRoutingTarget(&model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, BaseURL: &upstream.URL, Weight: common.GetPointer(uint(100))}, scope.Model)
	failUpstream.Store(true)
	for i, node := range nodes {
		send(node, 503)
		states, err := e.Snapshot(context.Background(), scope, []modelroute.Target{target})
		require.NoError(t, err)
		require.Equal(t, i == 1, states[0].Degraded)
	}
	failUpstream.Store(false)
	for i, node := range nodes {
		send(node, 200)
		require.Eventually(t, func() bool {
			states, err := e.Snapshot(context.Background(), scope, []modelroute.Target{target})
			return err == nil && len(states) == 1 && states[0].Inflight == 0
		}, time.Second, 5*time.Millisecond, "HTTP body can arrive just before the server records completion")
		states, err := e.Snapshot(context.Background(), scope, []modelroute.Target{target})
		require.NoError(t, err)
		require.Equal(t, i == 0, states[0].Degraded)
		require.Zero(t, states[0].Inflight)
		if i == 1 {
			require.Equal(t, 1.0, states[0].Factor)
		}
	}
	t.Run("concurrent_inflight", func(t *testing.T) {
		holdUpstream.Store(true)
		results := make(chan error, 2)
		for _, node := range nodes {
			go func() {
				request, err := http.NewRequest(http.MethodPost, node+"/v1/chat/completions", strings.NewReader(`{"model":"routing-node","messages":[{"role":"user","content":"hello"}],"max_tokens":8}`))
				if err != nil {
					results <- err
					return
				}
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer sk-"+strings.Repeat("c", 48))
				response, err := httpClient.Do(request)
				if err != nil {
					results <- err
					return
				}
				_, err = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				if err == nil && response.StatusCode != http.StatusOK {
					err = fmt.Errorf("concurrent relay status %d", response.StatusCode)
				}
				results <- err
			}()
		}
		// Release blocked handlers even if an observation fails.
		defer func() {
			holdUpstream.Store(false)
			close(release)
			for range 2 {
				require.NoError(t, <-results)
			}
		}()
		for range 2 {
			select {
			case <-arrivals:
			case <-time.After(5 * time.Second):
				t.Fatal("both nodes must reach the held upstream")
			}
		}
		states, err := e.Snapshot(context.Background(), scope, []modelroute.Target{target})
		require.NoError(t, err)
		require.Equal(t, 2, states[0].Inflight, "two independent gateway processes share in-flight accounting")
	})
	require.Eventually(t, func() bool {
		states, err := e.Snapshot(context.Background(), scope, []modelroute.Target{target})
		return err == nil && len(states) == 1 && states[0].Inflight == 0
	}, 2*time.Second, 10*time.Millisecond)
	t.Run("redis_interruption", func(t *testing.T) {
		if os.Getenv("MODEL_ROUTE_ALLOW_REDIS_PAUSE") != "1" {
			t.Skip("explicit disposable Redis permission required for CLIENT PAUSE")
		}
		host, _, err := net.SplitHostPort(address)
		require.NoError(t, err)
		require.True(t, net.ParseIP(host).IsLoopback(), "failure injection is restricted to the dedicated loopback Redis")
		readQuota := func(node string) map[string]int {
			request, err := http.NewRequest(http.MethodGet, node+"/_test/quota", nil)
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer sk-"+strings.Repeat("c", 48))
			response, err := httpClient.Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusOK, response.StatusCode)
			var quota map[string]int
			require.NoError(t, common.DecodeJson(response.Body, &quota))
			return quota
		}
		quotaBefore := readQuota(nodes[0])
		before := upstreamCalls.Load()
		// Auto-resumes without teardown commands even if the test aborts.
		require.NoError(t, client.Do(context.Background(), "CLIENT", "PAUSE", 1800, "ALL").Err())
		started := time.Now()
		send(nodes[0], http.StatusOK)
		require.Less(t, time.Since(started), 1500*time.Millisecond, "routing failure must fall back before Redis resumes")
		require.Equal(t, before+1, upstreamCalls.Load(), "Redis interruption must not duplicate upstream forwarding")
		quotaAfter := readQuota(nodes[0])
		charge := common.QuotaFromFloat(0.002 * common.QuotaPerUnit)
		require.Equal(t, charge, quotaBefore["wallet"]-quotaAfter["wallet"], "fallback bills wallet exactly once")
		require.Equal(t, charge, quotaBefore["token"]-quotaAfter["token"], "fallback bills finite token exactly once")
		request, err := http.NewRequest(http.MethodPost, nodes[1]+"/v1/chat/completions", strings.NewReader(`{"model":"routing-node","messages":[{"role":"user","content":"hello"}],"max_tokens":8}`))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer sk-"+strings.Repeat("c", 48)+"-2")
		response, err := httpClient.Do(request)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		require.Equal(t, http.StatusForbidden, response.StatusCode, "fallback does not bypass explicit-channel authorization")
		require.Equal(t, before+1, upstreamCalls.Load())
		require.NoError(t, client.Ping(context.Background()).Err())
		states, err := e.Snapshot(context.Background(), scope, []modelroute.Target{target})
		require.NoError(t, err)
		samples := states[0].Samples
		send(nodes[1], http.StatusOK)
		require.Eventually(t, func() bool {
			states, err := e.Snapshot(context.Background(), scope, []modelroute.Target{target})
			return err == nil && len(states) == 1 && states[0].Inflight == 0 && states[0].Samples > samples
		}, 2*time.Second, 10*time.Millisecond, "observations resume after Redis recovers")
		require.Equal(t, before+2, upstreamCalls.Load())
	})
	require.Zero(t, isolatedCalls.Load(), "neither concurrent requests nor Redis fallback may escape the authorized group")
}

// Local HTTP acceptance: authentication, selection, protocol relay and accounting
// are real; only the paid provider boundary is replaced with localhost servers.
func TestModelRoutingHTTPProtocolsAndAccounting(t *testing.T) {
	for _, protocol := range []struct {
		name, path, request, response, streamResponse string
		format                                        types.RelayFormat
		channelType                                   int
	}{
		{"chat", "/v1/chat/completions", `"messages":[{"role":"user","content":"hello"}],"max_tokens":8`, `{"id":"local","object":"chat.completion","model":"routing-paid","choices":[{"index":0,"message":{"role":"assistant","content":"local-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, "data: {\"id\":\"local\",\"object\":\"chat.completion.chunk\",\"model\":\"routing-paid\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"local-ok\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"local\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n", types.RelayFormatOpenAI, constant.ChannelTypeOpenAI},
		{"responses", "/v1/responses", `"input":"hello","max_output_tokens":8`, `{"id":"resp_local","object":"response","status":"completed","model":"routing-paid","output":[{"id":"msg_local","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"local-ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"local-ok\",\"output_index\":0,\"content_index\":0}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_local\",\"status\":\"completed\",\"model\":\"routing-paid\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n", types.RelayFormatOpenAIResponses, constant.ChannelTypeOpenAI},
		{"messages", "/v1/messages", `"messages":[{"role":"user","content":"hello"}],"max_tokens":8`, `{"id":"msg_local","type":"message","role":"assistant","model":"routing-paid","content":[{"type":"text","text":"local-ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_local\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"routing-paid\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"local-ok\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", types.RelayFormatClaude, constant.ChannelTypeAnthropic},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol.name, stream), func(t *testing.T) {
				db := modelManagementDB(t, "sqlite", "")
				require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Log{}, &model.UserSubscription{}, &model.SubscriptionPlan{}))
				require.NoError(t, i18n.Init())
				service.InitHttpClient()
				oldEngine, oldRetries := modelroute.Default, common.RetryTimes
				oldStreamingTimeout := constant.StreamingTimeout
				constant.StreamingTimeout = 30
				t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })
				oldTrust := operation_setting.GetQuotaSetting().TrustQuotaUSD
				oldGroupRatios := ratio_setting.GroupRatio2JSONString()
				t.Cleanup(func() {
					modelroute.Default = oldEngine
					common.RetryTimes = oldRetries
					operation_setting.GetQuotaSetting().TrustQuotaUSD = oldTrust
					require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroupRatios))
				})
				common.RetryTimes = 0
				operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
				require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"isolated":1}`))
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"routing-paid":0.002}`))
				modelroute.Default = modelroute.New(modelroute.Config{Mode: "active", Groups: []string{"default", "isolated"}, Models: []string{"routing-paid"}}, modelroute.NewMemoryStore())
				user := model.User{Username: "routing-paid-user", Group: "default", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Quota: 1000000, AffCode: "paid"}
				require.NoError(t, db.Create(&user).Error)
				token := model.Token{UserId: user.Id, Key: strings.Repeat("b", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000000}
				require.NoError(t, db.Create(&token).Error)
				charge := common.QuotaFromFloat(0.002 * common.QuotaPerUnit)
				require.Positive(t, charge)
				var beforeUser, beforeToken int
				var calls [4]atomic.Int64
				channels := make([]model.Channel, 4)
				for index := range channels {
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls[index].Add(1)
						assert.Equal(t, protocol.path, r.URL.Path)
						var currentUser model.User
						var currentToken model.Token
						assert.NoError(t, db.First(&currentUser, user.Id).Error)
						assert.NoError(t, db.First(&currentToken, token.Id).Error)
						assert.Equal(t, beforeUser-charge, currentUser.Quota, "wallet must be reserved before upstream")
						assert.Equal(t, beforeToken-charge, currentToken.RemainQuota, "token must be reserved before upstream")
						if index == 2 {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusServiceUnavailable)
							_, _ = io.WriteString(w, `{"error":{"message":"local outage","type":"server_error"}}`)
							return
						}
						// Controlled provider delay establishes observable fast/slow samples.
						delay := 5 * time.Millisecond
						if index == 1 {
							delay = 60 * time.Millisecond
						}
						time.Sleep(delay)
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(w, protocol.streamResponse)
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(w, protocol.response)
						}
					}))
					t.Cleanup(upstream.Close)
					weight := uint(100)
					group := "default"
					if index == 3 {
						group = "isolated"
					}
					channels[index] = model.Channel{Name: fmt.Sprintf("local-%d", index), Group: group, Models: "routing-paid", Status: common.ChannelStatusEnabled, Type: protocol.channelType, BaseURL: &upstream.URL, Key: "local-key", Weight: &weight}
					require.NoError(t, channels[index].Insert())
				}
				router := gin.New()
				router.POST(protocol.path, middleware.TokenAuth(), middleware.Distribute(), func(c *gin.Context) { Relay(c, protocol.format) })
				send := func(pin, wantStatus int) {
					var currentUser model.User
					var currentToken model.Token
					require.NoError(t, db.First(&currentUser, user.Id).Error)
					require.NoError(t, db.First(&currentToken, token.Id).Error)
					beforeUser, beforeToken = currentUser.Quota, currentToken.RemainQuota
					key := token.Key
					if pin > 0 {
						key += fmt.Sprintf("-%d", pin)
					}
					request := httptest.NewRequest(http.MethodPost, protocol.path, strings.NewReader(fmt.Sprintf(`{"model":"routing-paid","stream":%t,%s}`, stream, protocol.request)))
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("Authorization", "Bearer sk-"+key)
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					require.Equal(t, wantStatus, response.Code, response.Body.String())
					wantCharge := 0
					if wantStatus == http.StatusOK {
						wantCharge = charge
						require.Contains(t, response.Body.String(), "local-ok")
						if stream {
							require.Contains(t, response.Header().Get("Content-Type"), "text/event-stream")
						}
					}
					require.Eventually(t, func() bool {
						var u model.User
						var tok model.Token
						return db.First(&u, user.Id).Error == nil && db.First(&tok, token.Id).Error == nil && u.Quota == beforeUser-wantCharge && tok.RemainQuota == beforeToken-wantCharge
					}, 2*time.Second, 5*time.Millisecond, "success settles exactly once; failure refunds both reservations")
				}
				for index := range 2 {
					for range 5 {
						send(channels[index].Id, http.StatusOK)
					}
					require.Equal(t, int64(5), calls[index].Load(), "explicit pin must reach only its selected upstream")
				}
				for range 2 {
					send(channels[2].Id, http.StatusServiceUnavailable)
				}
				require.Equal(t, int64(2), calls[2].Load())
				scope := modelroute.Scope{Group: "default", Model: "routing-paid", Endpoint: protocol.path, Stream: stream}
				targets := []modelroute.Target{model.ModelRoutingTarget(&channels[0], scope.Model), model.ModelRoutingTarget(&channels[1], scope.Model), model.ModelRoutingTarget(&channels[2], scope.Model)}
				_, states, err := modelroute.Default.Select(context.Background(), scope, targets, 0)
				require.NoError(t, err)
				require.Len(t, states, 3)
				for _, state := range states[:2] {
					require.Equal(t, 5, state.Samples)
					require.Zero(t, state.Inflight)
				}
				require.Less(t, states[0].Score, states[1].Score)
				require.Greater(t, states[0].Share, states[1].Share)
				require.True(t, states[2].Degraded)
				require.Less(t, states[2].Share, states[1].Share)
				// Select the middle of each measured probability interval: no flaky histogram.
				lower := 0.0
				for _, state := range states {
					selected, _, err := modelroute.Default.Select(context.Background(), scope, targets, lower+state.Share/2)
					require.NoError(t, err)
					require.Equal(t, state.ID, selected)
					lower += state.Share
				}
				// Real unpinned HTTP requests may explore any authorized peer, never another group.
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("role", common.RoleCommonUser).Error)
				beforePin := calls[0].Load()
				send(channels[0].Id, http.StatusForbidden)
				require.Equal(t, beforePin, calls[0].Load(), "ordinary users cannot select a channel by token suffix")
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channels[2].Id).Update("status", common.ChannelStatusManuallyDisabled).Error)
				if protocol.name == "chat" && !stream {
					// Predetermined 120-draw acceptance, tolerance 15 percentage points.
					// Sum the probabilities before each real HTTP selection because
					// newly observed requests legitimately update the measured shares.
					const draws = 120
					const tolerance = 0.15
					startFast, startSlow := calls[0].Load(), calls[1].Load()
					expectedFast := 0.0
					for range draws {
						_, distribution, err := modelroute.Default.Select(context.Background(), scope, targets[:2], 0)
						require.NoError(t, err)
						expectedFast += distribution[0].Share
						send(0, http.StatusOK)
					}
					fast, slow := calls[0].Load()-startFast, calls[1].Load()-startSlow
					require.Equal(t, int64(draws), fast+slow)
					require.Greater(t, fast, slow, "faster peer receives a larger aggregate share, not every request")
					require.InDelta(t, expectedFast, float64(fast), draws*tolerance)
					t.Logf("real HTTP random dispatch: n=%d fast=%d slow=%d expected_fast=%.2f tolerance=%.0f", draws, fast, slow, expectedFast, draws*tolerance)
				}
				for range 3 {
					send(0, http.StatusOK)
				}
				require.NoError(t, db.Model(&model.Channel{}).Where("id IN ?", []int{channels[0].Id, channels[1].Id}).Update("status", common.ChannelStatusManuallyDisabled).Error)
				send(0, http.StatusServiceUnavailable)
				require.Zero(t, calls[3].Load(), "normal token cannot escape its default group")
			})
		}
	}
}

func TestModelRoutingResponsesRateLimitedStream(t *testing.T) {
	for _, source := range []string{"live", "probe"} {
		t.Run(source, func(t *testing.T) {
			db := modelManagementDB(t, "sqlite", "")
			require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Log{}, &model.UserSubscription{}, &model.SubscriptionPlan{}))
			require.NoError(t, i18n.Init())
			service.InitHttpClient()
			oldEngine, oldRetries, oldTimeout := modelroute.Default, common.RetryTimes, constant.StreamingTimeout
			oldProbe := *operation_setting.GetProbeSetting()
			oldTrust := operation_setting.GetQuotaSetting().TrustQuotaUSD
			t.Cleanup(func() {
				modelroute.Default = oldEngine
				common.RetryTimes = oldRetries
				constant.StreamingTimeout = oldTimeout
				*operation_setting.GetProbeSetting() = oldProbe
				operation_setting.GetQuotaSetting().TrustQuotaUSD = oldTrust
			})
			common.RetryTimes, constant.StreamingTimeout = 0, 30
			operation_setting.GetProbeSetting().OpenAIReliableEnabled = true
			operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
			modelroute.Default = modelroute.New(modelroute.Config{Mode: "active", Groups: []string{"default"}, Models: []string{"routing-throttled"}}, modelroute.NewMemoryStore())
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"routing-throttled":0.002}`))
			user := model.User{Username: "stream-throttle-user", Group: "default", Status: common.UserStatusEnabled, Quota: 1000000, AffCode: "throttle"}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: strings.Repeat("d", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000000}
			require.NoError(t, db.Create(&token).Error)
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "/v1/responses", r.URL.Path)
				if source == "live" {
					var reservedUser model.User
					var reservedToken model.Token
					assert.NoError(t, db.First(&reservedUser, user.Id).Error)
					assert.NoError(t, db.First(&reservedToken, token.Id).Error)
					charge := common.QuotaFromFloat(0.002 * common.QuotaPerUnit)
					assert.Equal(t, user.Quota-charge, reservedUser.Quota)
					assert.Equal(t, token.RemainQuota-charge, reservedToken.RemainQuota)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_rate_limit\",\"status\":\"failed\",\"model\":\"routing-throttled\",\"output\":[],\"error\":{\"code\":\"rate_limit_exceeded\",\"type\":\"rate_limit_error\",\"message\":\"local rate limit\"}}}\n\n")
			}))
			defer upstream.Close()
			channel := model.Channel{Name: "stream-throttle", Group: "default", Models: "routing-throttled", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI, BaseURL: &upstream.URL, Key: "local-key", Weight: common.GetPointer(uint(100))}
			require.NoError(t, channel.Insert())
			if source == "live" {
				router := gin.New()
				router.POST("/v1/responses", middleware.TokenAuth(), middleware.Distribute(), func(c *gin.Context) { Relay(c, types.RelayFormatOpenAIResponses) })
				for range 2 {
					request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"routing-throttled","input":"hello","stream":true,"max_output_tokens":8}`))
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("Authorization", "Bearer sk-"+token.Key)
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					require.Contains(t, response.Body.String(), "rate_limit_exceeded")
				}
				require.Equal(t, int64(2), calls.Load())
			} else {
				summary, managed := testModelRoutingHealth(context.Background(), &channel, user.Id)
				require.True(t, managed)
				require.Equal(t, 1, summary.Tested)
				require.Equal(t, int64(1), calls.Load())
			}
			scope := modelroute.Scope{Group: "default", Model: "routing-throttled", Endpoint: "/v1/responses", Stream: true}
			states, err := modelroute.Default.Snapshot(context.Background(), scope, []modelroute.Target{model.ModelRoutingTarget(&channel, scope.Model)})
			require.NoError(t, err)
			require.Len(t, states, 1)
			assert.False(t, states[0].Degraded, "protocol-level rate limits must not count as channel failure streaks")
			assert.Equal(t, "rate_limited", states[0].Reason, "HTTP 200 response.failed rate-limit must enter the cooldown path")
			assert.Equal(t, 0.1, states[0].Factor)
			assert.Zero(t, states[0].Inflight)
			if source == "live" {
				var finalUser model.User
				var finalToken model.Token
				require.NoError(t, db.First(&finalUser, user.Id).Error)
				require.NoError(t, db.First(&finalToken, token.Id).Error)
				assert.Equal(t, user.Quota, finalUser.Quota, "failed stream with no usage refunds the wallet reservation")
				assert.Equal(t, token.RemainQuota, finalToken.RemainQuota, "failed stream with no usage refunds the token reservation")
			}
		})
	}
}

func TestModelRoutingHTTPStreamFailureAccounting(t *testing.T) {
	for _, scenario := range []string{"interrupted", "timeout", "client_cancel", "retry"} {
		t.Run(scenario, func(t *testing.T) {
			db := modelManagementDB(t, "sqlite", "")
			require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Log{}, &model.UserSubscription{}, &model.SubscriptionPlan{}))
			require.NoError(t, i18n.Init())
			service.InitHttpClient()
			oldEngine, oldRetries, oldTimeout := modelroute.Default, common.RetryTimes, constant.StreamingTimeout
			oldTrust := operation_setting.GetQuotaSetting().TrustQuotaUSD
			t.Cleanup(func() {
				modelroute.Default = oldEngine
				common.RetryTimes = oldRetries
				constant.StreamingTimeout = oldTimeout
				operation_setting.GetQuotaSetting().TrustQuotaUSD = oldTrust
			})
			common.RetryTimes, constant.StreamingTimeout = 0, 1
			if scenario == "retry" {
				common.RetryTimes = 1
			}
			operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
			modelroute.Default = modelroute.New(modelroute.Config{Mode: "active", Groups: []string{"default"}, Models: []string{"routing-stream"}}, modelroute.NewMemoryStore())
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"routing-stream":0.002}`))
			user := model.User{Username: "stream-user", Group: "default", Status: common.UserStatusEnabled, Quota: 1000000, AffCode: "stream"}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: strings.Repeat("e", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000000}
			require.NoError(t, db.Create(&token).Error)
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if scenario == "retry" && n == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(503)
					_, _ = io.WriteString(w, `{"error":{"message":"local retry","type":"server_error"}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if scenario == "timeout" {
					_, _ = io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"local\",\"status\":\"in_progress\"}}\n\n")
				} else {
					_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"local-output\",\"output_index\":0,\"content_index\":0}\n\n")
				}
				w.(http.Flusher).Flush()
				switch scenario {
				case "timeout", "client_cancel":
					select {
					case <-r.Context().Done():
					case <-time.After(5 * time.Second):
					}
				case "retry":
					_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"local\",\"status\":\"completed\",\"model\":\"routing-stream\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
				}
			}))
			defer upstream.Close()
			var targets []modelroute.Target
			for index := range 2 {
				channel := model.Channel{Name: fmt.Sprintf("stream-%d", index), Group: "default", Models: "routing-stream", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI, BaseURL: &upstream.URL, Key: "local-key", Weight: common.GetPointer(uint(100))}
				require.NoError(t, channel.Insert())
				targets = append(targets, model.ModelRoutingTarget(&channel, "routing-stream"))
			}
			// Restrict non-retry cases to one channel so two failures form one streak.
			if scenario != "retry" {
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", targets[1].ID).Update("status", common.ChannelStatusManuallyDisabled).Error)
				targets = targets[:1]
			}
			finished := make(chan struct{}, 2)
			router := gin.New()
			router.POST("/v1/responses", middleware.TokenAuth(), middleware.Distribute(), func(c *gin.Context) { Relay(c, types.RelayFormatOpenAIResponses); finished <- struct{}{} })
			gateway := httptest.NewServer(router)
			defer gateway.Close()
			httpClient := &http.Client{Timeout: 8 * time.Second}
			defer httpClient.CloseIdleConnections()
			attempts := 2
			if scenario == "retry" {
				attempts = 1
			}
			charge := common.QuotaFromFloat(0.002 * common.QuotaPerUnit)
			for attempt := range attempts {
				ctx, cancel := context.WithCancel(context.Background())
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{"model":"routing-stream","input":"hello","stream":true,"max_output_tokens":8}`))
				require.NoError(t, err)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer sk-"+token.Key)
				response, err := httpClient.Do(request)
				require.NoError(t, err)
				if scenario == "client_cancel" {
					buffer := make([]byte, 1)
					_, err = response.Body.Read(buffer)
					require.NoError(t, err)
					cancel()
				} else {
					_, err = io.Copy(io.Discard, response.Body)
					require.NoError(t, err)
				}
				_ = response.Body.Close()
				cancel()
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Fatal("relay did not finish after stream termination")
				}
				var afterUser model.User
				var afterToken model.Token
				require.NoError(t, db.First(&afterUser, user.Id).Error)
				require.NoError(t, db.First(&afterToken, token.Id).Error)
				delta := user.Quota - afterUser.Quota
				require.Equal(t, delta, token.RemainQuota-afterToken.RemainQuota)
				require.GreaterOrEqual(t, delta, 0)
				require.LessOrEqual(t, delta, (attempt+1)*charge, "stream termination must never duplicate billing")
				if scenario == "timeout" {
					require.Zero(t, delta, "metadata-only timeout has no billable usage")
				}
				if scenario == "retry" {
					require.Equal(t, charge, delta, "failed attempt followed by success settles exactly once")
				}
			}
			require.Equal(t, int64(2), calls.Load(), "termination never retries; eligible pre-stream 503 retries exactly once")
			states, err := modelroute.Default.Snapshot(context.Background(), modelroute.Scope{Group: "default", Model: "routing-stream", Endpoint: "/v1/responses", Stream: true}, targets)
			require.NoError(t, err)
			for _, state := range states {
				require.Zero(t, state.Inflight)
			}
			if scenario == "client_cancel" {
				require.False(t, states[0].Degraded)
				require.Zero(t, states[0].ErrorRate)
			} else if scenario != "retry" {
				require.True(t, states[0].Degraded)
				require.Equal(t, 1.0, states[0].ErrorRate)
			} else {
				require.Equal(t, 1.0, states[0].ErrorRate+states[1].ErrorRate, "only failed attempt is attributed failure")
			}
		})
	}
}

func TestModelRoutingHTTPObservation(t *testing.T) {
	db := modelManagementDB(t, "sqlite", "")
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Log{}, &model.UserSubscription{}, &model.SubscriptionPlan{}))
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	oldEngine := modelroute.Default
	oldRetries := common.RetryTimes
	common.RetryTimes = 0
	t.Cleanup(func() { modelroute.Default = oldEngine; common.RetryTimes = oldRetries })
	modelroute.Default = modelroute.New(modelroute.Config{Mode: "active", Groups: []string{"default"}, Models: []string{"routing-http"}}, modelroute.NewMemoryStore())
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"routing-http":0}`))
	user := model.User{Username: "routing-http-user", Group: "default", Status: common.UserStatusEnabled, Quota: 1000000, AffCode: "routing-http"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: strings.Repeat("a", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(&token).Error)
	var failUpstream atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failUpstream.Load() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"simulated upstream outage","type":"server_error"}}`)
			return
		}
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer fake-upstream-key", r.Header.Get("Authorization"))
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), `"model":"routing-http"`)
		time.Sleep(5 * time.Millisecond) // Controlled upstream latency, not a timing assertion.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"local","object":"chat.completion","model":"routing-http","choices":[{"index":0,"message":{"role":"assistant","content":"local-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()
	weight := uint(100)
	channel := model.Channel{Name: "routing-local", Group: "default", Models: "routing-http", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI, BaseURL: &upstream.URL, Key: "fake-upstream-key", Weight: &weight}
	require.NoError(t, channel.Insert())
	router := gin.New()
	router.POST("/v1/chat/completions", middleware.TokenAuth(), middleware.Distribute(), func(c *gin.Context) { Relay(c, types.RelayFormatOpenAI) })
	send := func(wantStatus int) {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"routing-http","messages":[{"role":"user","content":"hello"}],"max_tokens":8}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer sk-"+token.Key)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, wantStatus, response.Code, response.Body.String())
		if wantStatus == http.StatusOK {
			require.Contains(t, response.Body.String(), "local-ok")
		}
	}
	for range 5 {
		send(http.StatusOK)
	}
	states, err := modelroute.Default.Snapshot(context.Background(), modelroute.Scope{Group: "default", Model: "routing-http", Endpoint: "/v1/chat/completions"}, []modelroute.Target{model.ModelRoutingTarget(&channel, "routing-http")})
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, 5, states[0].Samples, "real authenticated relays must feed the same selection scope")
	require.Zero(t, states[0].Inflight)
	require.Positive(t, states[0].LatencyMS)
	failUpstream.Store(true)
	for range 2 {
		send(http.StatusServiceUnavailable)
	}
	states, err = modelroute.Default.Snapshot(context.Background(), modelroute.Scope{Group: "default", Model: "routing-http", Endpoint: "/v1/chat/completions"}, []modelroute.Target{model.ModelRoutingTarget(&channel, "routing-http")})
	require.NoError(t, err)
	require.True(t, states[0].Degraded)
	require.Equal(t, 0.1, states[0].Factor)
	failUpstream.Store(false)
	for range 2 {
		send(http.StatusOK)
	}
	states, err = modelroute.Default.Snapshot(context.Background(), modelroute.Scope{Group: "default", Model: "routing-http", Endpoint: "/v1/chat/completions"}, []modelroute.Target{model.ModelRoutingTarget(&channel, "routing-http")})
	require.NoError(t, err)
	require.False(t, states[0].Degraded)
	require.Equal(t, 1.0, states[0].Factor)
	require.Zero(t, states[0].Inflight)
}

func TestModelRoutingProbeScheduleAndSelection(t *testing.T) {
	oldEngine := modelroute.Default
	monitor := operation_setting.GetMonitorSetting()
	oldMonitor := *monitor
	t.Cleanup(func() { modelroute.Default = oldEngine; *monitor = oldMonitor })
	modelroute.Default = modelroute.New(modelroute.Config{Mode: "active", Groups: []string{"default"}, Models: []string{"shared"}}, modelroute.NewMemoryStore())
	monitor.AutoTestChannelEnabled = false
	monitor.AutoTestChannelMinutes = 10
	require.False(t, (channelTestHandler{}).Enabled(), "model routing must not enable unrelated legacy probes")
	require.Equal(t, 10*time.Minute, (channelTestHandler{}).Interval())
	require.True(t, (modelRoutingProbeHandler{}).Enabled())
	require.Equal(t, 15*time.Minute, (modelRoutingProbeHandler{}).Interval())
	channels := []*model.Channel{
		{Id: 1, Group: "default", Models: "shared", Status: common.ChannelStatusEnabled},
		{Id: 2, Group: "default", Models: "shared", Status: common.ChannelStatusManuallyDisabled},
		{Id: 3, Group: "default", Models: "unmanaged", Status: common.ChannelStatusEnabled},
	}
	for _, mode := range []string{modelRoutingProbeTaskType} {
		selected := selectChannelsForAutomaticTest(channels, mode)
		require.Len(t, selected, 1)
		require.Equal(t, 1, selected[0].Id)
	}
	legacy := selectChannelsForAutomaticTest(channels, operation_setting.ChannelTestModeScheduledAll)
	require.Len(t, legacy, 1, "legacy scheduled task must not duplicate model probes")
	require.Equal(t, 3, legacy[0].Id)
	manual := selectChannelsForAutomaticTest(channels, operation_setting.ChannelTestModeScheduledAll, true)
	require.Len(t, manual, 2, "manual all-channel test retains managed channels")
	require.Equal(t, 1, manual[0].Id)
	require.Equal(t, 3, manual[1].Id)
}

func TestProbeLatencyUsesLatestSamples(t *testing.T) {
	probeStateByChannel.Lock()
	probeStateByChannel.values = make(map[int]*probeState)
	probeStateByChannel.Unlock()
	settings := &operation_setting.ProbeSetting{
		FirstTokenProtectionEnabled: true,
		FirstTokenThresholdSeconds:  45,
		FirstTokenMinimumSamples:    2,
	}

	assert.False(t, recordProbeLatency(2, 100_000, settings))
	assert.True(t, recordProbeLatency(2, 100_000, settings))
	assert.True(t, recordProbeLatency(2, 5_000, settings))
	assert.False(t, recordProbeLatency(2, 5_000, settings))
}

func TestProbeModelAndOpenAIReliableEndpoint(t *testing.T) {
	original := *operation_setting.GetProbeSetting()
	t.Cleanup(func() { *operation_setting.GetProbeSetting() = original })
	setting := operation_setting.GetProbeSetting()
	setting.PlatformModels = map[string]string{"openai": "gpt-probe"}
	setting.OpenAIReliableEnabled = true
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	assert.Equal(t, "gpt-probe", probeModelForChannel(channel))
	assert.Equal(t, string(constant.EndpointTypeOpenAIResponse), probeEndpointForChannel(channel))

	request, ok := buildTestRequest("gpt-probe", string(constant.EndpointTypeOpenAIResponse), channel, false).(*dto.OpenAIResponsesRequest)
	if assert.True(t, ok) {
		assert.Equal(t, setting.OpenAIReasoningEffort, request.Reasoning.Effort)
	}
}

func TestProbeDoesNotInferProvidersFromGenericChannelTypes(t *testing.T) {
	assert.Empty(t, probePlatformForChannel(constant.ChannelTypeAdvancedCustom))
	assert.Empty(t, probePlatformForChannel(constant.ChannelTypeNewAPI))
}

func TestProbeResponseBodyRecordsFirstBodyByte(t *testing.T) {
	body := &probeResponseBody{
		ReadCloser: io.NopCloser(strings.NewReader("first event")),
		startedAt:  time.Now().Add(-time.Second),
	}

	content, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, "first event", string(content))
	assert.GreaterOrEqual(t, body.firstByteMilliseconds, int64(900))
}
