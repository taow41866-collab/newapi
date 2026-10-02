package modelroute

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

// MemoryStore is for isolated tests/single-process experiments, not the live
// multi-replica gateway. The production Default always uses shared Redis.
type MemoryStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{values: make(map[string][]byte)} }
func (m *MemoryStore) Read(ctx context.Context, keys []string) ([]State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]State, len(keys))
	for i, k := range keys {
		if b := m.values[k]; len(b) > 0 {
			if err := common.Unmarshal(b, &out[i]); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
func (m *MemoryStore) Update(ctx context.Context, key string, fn func(*State) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var s State
	if b := m.values[key]; len(b) > 0 {
		if err := common.Unmarshal(b, &s); err != nil {
			return err
		}
	}
	if err := fn(&s); err != nil {
		return err
	}
	b, err := common.Marshal(s)
	if err == nil {
		m.values[key] = b
	}
	return err
}

type RedisStore struct{ Client *redis.Client }

// ConfigureFromEnv runs once after dotenv loading, before serving requests or
// starting workers. A dedicated connection never enables the host's Redis/cache.
// No startup Ping: a routing-store outage must retain the legacy request path.
func ConfigureFromEnv() (func(), error) {
	config := configFromEnv()
	store := RedisStore{}
	closeClient := func() {}
	if raw := os.Getenv("MODEL_ROUTING_REDIS_URL"); raw != "" && (config.Mode == "active" || config.Mode == "shadow") {
		options, err := redis.ParseURL(raw)
		if err != nil {
			return nil, errors.New("invalid MODEL_ROUTING_REDIS_URL") // Never expose credentials from a parse error.
		}
		options.MaxRetries = -1
		options.PoolSize = 10
		options.DialTimeout = 150 * time.Millisecond
		options.ReadTimeout = 150 * time.Millisecond
		options.WriteTimeout = 150 * time.Millisecond
		options.PoolTimeout = 150 * time.Millisecond
		store.Client = redis.NewClient(options)
		closeClient = func() { _ = store.Client.Close() }
	}
	Default = New(config, store)
	return closeClient, nil
}

func (r RedisStore) client() (*redis.Client, error) {
	if r.Client != nil {
		return r.Client, nil
	}
	if !common.RedisEnabled || common.RDB == nil {
		return nil, errors.New("model routing requires shared Redis")
	}
	return common.RDB, nil
}
func (r RedisStore) Read(ctx context.Context, keys []string) ([]State, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	c, err := r.client()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	values, err := c.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	states := make([]State, len(keys))
	for i, v := range values {
		if v == nil {
			continue
		}
		str, ok := v.(string)
		if !ok {
			return nil, errors.New("invalid model routing state")
		}
		if err := common.UnmarshalJsonStr(str, &states[i]); err != nil {
			return nil, err
		}
	}
	return states, nil
}
func (r RedisStore) Update(ctx context.Context, key string, fn func(*State) error) error {
	c, err := r.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	for range 5 {
		err = c.Watch(ctx, func(tx *redis.Tx) error {
			var s State
			b, err := tx.Get(ctx, key).Bytes()
			if err != nil && err != redis.Nil {
				return err
			}
			if len(b) > 0 {
				if err := common.Unmarshal(b, &s); err != nil {
					return err
				}
			}
			if err := fn(&s); err != nil {
				return err
			}
			b, err = common.Marshal(s)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error { p.Set(ctx, key, b, stateTTL); return nil })
			return err
		}, key)
		if err != redis.TxFailedErr {
			return err
		}
	}
	return err
}
