package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrBudgetExceeded  = errors.New("budget exceeded")
	ErrRedisDown       = errors.New("redis unavailable")
	ErrReservationGone = errors.New("reservation no longer exists")
)

const reserveTTL = 120 * time.Second
const RefreshInterval = reserveTTL / 3

// Atomic reserve: reclaim expired holds, then check spend+held+estimate <= budget.
const reserveLua = `
local spend_key = KEYS[1]
local held_key = KEYS[2]
local zset_key = KEYS[3]
local hash_key = KEYS[4]
local estimate = tonumber(ARGV[1])
local budget = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])
local resid = ARGV[5]
local month_ttl = tonumber(ARGV[6])

-- reclaim expired reservations
local expired = redis.call('ZRANGEBYSCORE', zset_key, '-inf', now)
for _, id in ipairs(expired) do
  local amt = tonumber(redis.call('HGET', hash_key, id) or '0')
  if amt > 0 then
    redis.call('HDEL', hash_key, id)
    redis.call('DECRBY', held_key, amt)
  end
  redis.call('ZREM', zset_key, id)
end
local held = tonumber(redis.call('GET', held_key) or '0')
if held < 0 then
  held = 0
  redis.call('SET', held_key, 0)
end

local spend = tonumber(redis.call('GET', spend_key) or '0')
if spend + held + estimate > budget then
  return {0, spend, held}
end

redis.call('INCRBY', held_key, estimate)
redis.call('HSET', hash_key, resid, estimate)
redis.call('ZADD', zset_key, now + ttl, resid)
redis.call('EXPIRE', spend_key, month_ttl)
redis.call('EXPIRE', held_key, month_ttl)
redis.call('EXPIRE', zset_key, month_ttl)
redis.call('EXPIRE', hash_key, month_ttl)
return {1, spend, held + estimate}
`

// Settle: release hold and charge only while this reservation is still held.
// After TTL reclaim another request may have reserved that capacity; charging
// then would double-count spend.
const settleLua = `
local spend_key = KEYS[1]
local held_key = KEYS[2]
local zset_key = KEYS[3]
local hash_key = KEYS[4]
local resid = ARGV[1]
local actual = tonumber(ARGV[2])
local month_ttl = tonumber(ARGV[3])

if redis.call('HEXISTS', hash_key, resid) == 0 then
  return {0, 0, 0}
end

local reserved = tonumber(redis.call('HGET', hash_key, resid) or '0')
redis.call('HDEL', hash_key, resid)
redis.call('ZREM', zset_key, resid)
if reserved > 0 then
  redis.call('DECRBY', held_key, reserved)
  local held = tonumber(redis.call('GET', held_key) or '0')
  if held < 0 then
    redis.call('SET', held_key, 0)
  end
end
redis.call('INCRBY', spend_key, actual)
redis.call('EXPIRE', spend_key, month_ttl)
return {1, reserved, actual}
`

const refreshLua = `
local spend_key = KEYS[1]
local held_key = KEYS[2]
local zset_key = KEYS[3]
local hash_key = KEYS[4]
local resid = ARGV[1]
local now = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local month_ttl = tonumber(ARGV[4])

if redis.call('HEXISTS', hash_key, resid) == 0 then
  return 0
end

redis.call('ZADD', zset_key, now + ttl, resid)
redis.call('EXPIRE', spend_key, month_ttl)
redis.call('EXPIRE', held_key, month_ttl)
redis.call('EXPIRE', zset_key, month_ttl)
redis.call('EXPIRE', hash_key, month_ttl)
return 1
`

type Service struct {
	rdb *redis.Client
}

type Reservation struct {
	ID       string
	Estimate int64
	Month    string
	Key      string
}

func NewService(rdb *redis.Client) *Service {
	return &Service{rdb: rdb}
}

func MonthUTC(t time.Time) string {
	return t.UTC().Format("200601")
}

func monthTTLSeconds(t time.Time) int64 {
	t = t.UTC()
	next := time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	sec := int64(next.Sub(t).Seconds()) + 86400
	if sec < 86400 {
		sec = 86400
	}
	return sec
}

func (s *Service) keys(virtualKey, month string) (spend, held, zset, hash string) {
	base := "budget:" + virtualKey + ":" + month
	return base + ":spend", base + ":held", base + ":z", base + ":h"
}

func (s *Service) Reserve(ctx context.Context, virtualKey string, budgetMicroCents, estimateMicroCents int64) (*Reservation, error) {
	if estimateMicroCents < 0 {
		estimateMicroCents = 0
	}
	now := time.Now().UTC()
	month := MonthUTC(now)
	spend, held, zset, hash := s.keys(virtualKey, month)
	id := "rsv_" + uuid.NewString()

	raw, err := s.rdb.Eval(ctx, reserveLua,
		[]string{spend, held, zset, hash},
		estimateMicroCents, budgetMicroCents, now.Unix(), int64(reserveTTL.Seconds()), id, monthTTLSeconds(now),
	).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRedisDown, err)
	}
	arr, ok := raw.([]any)
	if !ok || len(arr) < 1 {
		return nil, fmt.Errorf("%w: bad reserve result", ErrRedisDown)
	}
	allowed, _ := asInt64(arr[0])
	if allowed != 1 {
		return nil, ErrBudgetExceeded
	}
	return &Reservation{ID: id, Estimate: estimateMicroCents, Month: month, Key: virtualKey}, nil
}

func (s *Service) Settle(ctx context.Context, rsv *Reservation, actualMicroCents int64) error {
	if rsv == nil {
		return nil
	}
	if actualMicroCents < 0 {
		actualMicroCents = 0
	}
	spend, held, zset, hash := s.keys(rsv.Key, rsv.Month)
	now := time.Now().UTC()
	raw, err := s.rdb.Eval(ctx, settleLua,
		[]string{spend, held, zset, hash},
		rsv.ID, actualMicroCents, monthTTLSeconds(now),
	).Result()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRedisDown, err)
	}
	arr, ok := raw.([]any)
	if !ok || len(arr) < 1 {
		return fmt.Errorf("%w: bad settle result", ErrRedisDown)
	}
	charged, _ := asInt64(arr[0])
	if charged != 1 {
		return ErrReservationGone
	}
	return nil
}

// Refresh extends an in-flight reservation so long-running streams cannot
// release their budget hold while the upstream is still producing tokens.
func (s *Service) Refresh(ctx context.Context, rsv *Reservation) error {
	if rsv == nil {
		return nil
	}
	spend, held, zset, hash := s.keys(rsv.Key, rsv.Month)
	now := time.Now().UTC()
	raw, err := s.rdb.Eval(ctx, refreshLua,
		[]string{spend, held, zset, hash},
		rsv.ID, now.Unix(), int64(reserveTTL.Seconds()), monthTTLSeconds(now),
	).Int64()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRedisDown, err)
	}
	if raw != 1 {
		return ErrReservationGone
	}
	return nil
}

// Spend returns settled spend for the current UTC month (for admin/tests).
func (s *Service) Spend(ctx context.Context, virtualKey string) (int64, error) {
	month := MonthUTC(time.Now())
	spend, _, _, _ := s.keys(virtualKey, month)
	v, err := s.rdb.Get(ctx, spend).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return v, err
}

func asInt64(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case string:
		var out int64
		_, err := fmt.Sscan(n, &out)
		return out, err
	default:
		return 0, fmt.Errorf("not int: %T", v)
	}
}
