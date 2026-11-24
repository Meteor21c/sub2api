package repository

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var decideMagentLogin = redis.NewScript(`
if redis.call("HGET", KEYS[1], "status") ~= "pending" then return 0 end
redis.call("HSET", KEYS[1], "status", ARGV[1], "user_id", ARGV[2], "token_version", ARGV[3])
return 1
`)

var redeemMagentLogin = redis.NewScript(`
if redis.call("HGET", KEYS[1], "proof") ~= ARGV[1] then return {"expired"} end
local status = redis.call("HGET", KEYS[1], "status")
if status ~= "approved" then return {status} end
local user = redis.call("HGET", KEYS[1], "user_id")
local version = redis.call("HGET", KEYS[1], "token_version")
redis.call("DEL", KEYS[1])
return {status, user, version}
`)

type MagentLoginStore struct {
	cache *redis.Client
}

func NewMagentLoginStore(cache *redis.Client) *MagentLoginStore {
	return &MagentLoginStore{cache: cache}
}

func magentLoginKey(requestID string) string {
	return "magent:device-login:" + requestID
}

func (s *MagentLoginStore) Create(ctx context.Context, requestID, proof string, ttl time.Duration) error {
	key := magentLoginKey(requestID)
	_, err := s.cache.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, key, "status", "pending", "proof", proof)
		pipe.Expire(ctx, key, ttl)
		return nil
	})
	return err
}

func (s *MagentLoginStore) Decide(ctx context.Context, requestID, status string, userID, tokenVersion int64) (bool, error) {
	applied, err := decideMagentLogin.Run(ctx, s.cache, []string{magentLoginKey(requestID)}, status, userID, tokenVersion).Int()
	return applied == 1, err
}

func (s *MagentLoginStore) Redeem(ctx context.Context, requestID, proof string) (string, int64, int64, error) {
	result, err := redeemMagentLogin.Run(ctx, s.cache, []string{magentLoginKey(requestID)}, proof).StringSlice()
	if err != nil {
		return "", 0, 0, err
	}
	if len(result) == 1 {
		switch result[0] {
		case "pending", "denied", "expired":
			return result[0], 0, 0, nil
		}
	}
	if len(result) != 3 || result[0] != "approved" {
		return "", 0, 0, errors.New("invalid MeteorAgent sign-in state")
	}
	userID, err := strconv.ParseInt(result[1], 10, 64)
	if err != nil || userID <= 0 {
		return "", 0, 0, errors.New("invalid MeteorAgent sign-in account")
	}
	version, err := strconv.ParseInt(result[2], 10, 64)
	if err != nil {
		return "", 0, 0, err
	}
	return result[0], userID, version, nil
}
