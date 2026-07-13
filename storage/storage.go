package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-redis/redis/v8"
)

// Redis — обёртка над клиентом Redis с JSON-сериализацией.
// Безопасна при nil-клиенте: тогда кэш просто отключён (удобно для тестов без Redis).
type Redis struct {
	Redis *redis.Client
}

// Get читает значение по ключу в v. Возвращает (false, nil), если ключа нет или кэш отключён.
func (r *Redis) Get(ctx context.Context, key string, v any) (bool, error) {
	if r == nil || r.Redis == nil {
		return false, nil
	}
	s, err := r.Redis.Get(ctx, key).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(s), v)
}

// Set сохраняет значение по ключу с временем жизни ttl. При отключённом кэше — no-op.
func (r *Redis) Set(ctx context.Context, key string, v any, ttl time.Duration) error {
	if r == nil || r.Redis == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return r.Redis.Set(ctx, key, b, ttl).Err()
}
