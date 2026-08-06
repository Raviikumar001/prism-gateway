package limit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrRateLimited = errors.New("rate limit exceeded")
	ErrRedisDown   = errors.New("redis unavailable")
)

// Sliding-window RPM: ZSET of request timestamps, atomic Lua prune+count+add.
const rpmLua = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, 0, now - window_ms)
local count = redis.call('ZCARD', key)
if count >= limit then
  redis.call('PEXPIRE', key, window_ms)
  return {0, count, limit}
end
redis.call('ZADD', key, now, member)
redis.call('PEXPIRE', key, window_ms)
return {1, count + 1, limit}
`

type RPM struct {
	rdb      *redis.Client
	window   time.Duration
	sha      string
}

func NewRPM(rdb *redis.Client) *RPM {
	return &RPM{rdb: rdb, window: time.Minute}
}

func (r *RPM) EnsureScript(ctx context.Context) error {
	sha, err := r.rdb.ScriptLoad(ctx, rpmLua).Result()
	if err != nil {
		return err
	}
	r.sha = sha
	return nil
}

func (r *RPM) Allow(ctx context.Context, virtualKey string, limit int) error {
	if limit <= 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	member := fmt.Sprintf("%d-%d", now, time.Now().UnixNano())
	key := "rpm:" + virtualKey

	res, err := r.eval(ctx, []string{key}, now, r.window.Milliseconds(), int64(limit), member)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRedisDown, err)
	}
	allowed := res[0] == 1
	if !allowed {
		return ErrRateLimited
	}
	return nil
}

func (r *RPM) eval(ctx context.Context, keys []string, args ...any) ([]int64, error) {
	var cmd *redis.Cmd
	if r.sha != "" {
		cmd = r.rdb.EvalSha(ctx, r.sha, keys, args...)
	} else {
		cmd = r.rdb.Eval(ctx, rpmLua, keys, args...)
	}
	raw, err := cmd.Result()
	if err != nil && redis.HasErrorPrefix(err, "NOSCRIPT") {
		if loadErr := r.EnsureScript(ctx); loadErr != nil {
			return nil, loadErr
		}
		raw, err = r.rdb.EvalSha(ctx, r.sha, keys, args...).Result()
	}
	if err != nil {
		return nil, err
	}
	arr, ok := raw.([]any)
	if !ok || len(arr) < 3 {
		return nil, fmt.Errorf("unexpected rpm script result: %v", raw)
	}
	out := make([]int64, 3)
	for i := 0; i < 3; i++ {
		switch v := arr[i].(type) {
		case int64:
			out[i] = v
		case string:
			var n int64
			_, _ = fmt.Sscan(v, &n)
			out[i] = n
		default:
			return nil, fmt.Errorf("unexpected rpm result element: %T", v)
		}
	}
	return out, nil
}
