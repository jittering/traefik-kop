package traefikkop

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
)

type TraefikStore interface {
	Store(conf dynamic.Configuration) error
	Get(key string) (string, error)
	Gets(key string) (map[string]string, error)
	Ping() error
	KeepConfAlive() error
}

func collectKeys(m interface{}) []string {
	mk := reflect.ValueOf(m).MapKeys()
	// set := mapset.NewSet()
	set := make([]string, len(mk))
	for i := 0; i < len(mk); i++ {
		// set.Add(mk[i].String())
		set[i] = mk[i].String()
	}
	return set
}

type RedisStore struct {
	Hostname string
	Addr     string
	TTL      time.Duration // TTL in seconds, 0 means no TTL
	Pass     string
	DB       int

	client     *redis.Client
	lastConfig *dynamic.Configuration
}

func NewRedisStore(hostname string, addr string, ttl int, pass string, db int) TraefikStore {
	logrus.Infof("creating new redis store at %s for hostname %s", addr, hostname)

	store := &RedisStore{
		Hostname: hostname,
		Addr:     addr,
		TTL:      time.Duration(ttl) * time.Second,
		Pass:     pass,
		DB:       db,

		client: redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: pass,
			DB:       db,
		}),
	}
	return store
}

func (s *RedisStore) Ping() error {
	return s.client.Ping(context.Background()).Err()
}

// sk returns the 'set key' for keeping track of our services/routers/middlewares
// e.g., traefik_http_routers@culture.local
func (s RedisStore) sk(b string) string {
	return fmt.Sprintf("traefik_%s@%s", b, s.Hostname)
}

func (s *RedisStore) Get(key string) (string, error) {
	val, err := s.client.Get(context.Background(), key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", nil // key does not exist
		}
		return "", errors.Wrapf(err, "failed to get key %s", key)
	}
	return val, nil
}

func (s *RedisStore) Gets(key string) (map[string]string, error) {
	keys, err := s.client.Keys(context.Background(), key).Result()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get keys matching %s", key)
	}
	if len(keys) == 0 {
		return nil, nil // no keys found
	}
	vals := make(map[string]string)
	for _, k := range keys {
		val, err := s.Get(k)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get value for key %s", k)
		}
		vals[k] = val
	}
	return vals, nil
}

func (s *RedisStore) Store(conf dynamic.Configuration) error {
	s.removeOldKeys(conf.HTTP.Middlewares, "http_middlewares")
	s.removeOldKeys(conf.HTTP.Routers, "http_routers")
	s.removeOldKeys(conf.HTTP.Services, "http_services")
	s.removeOldKeys(conf.TCP.Middlewares, "tcp_middlewares")
	s.removeOldKeys(conf.TCP.Routers, "tcp_routers")
	s.removeOldKeys(conf.TCP.Services, "tcp_services")
	s.removeOldKeys(conf.UDP.Routers, "udp_routers")
	s.removeOldKeys(conf.UDP.Services, "udp_services")

	kv, err := ConfigToKV(conf)
	if err != nil {
		return err
	}
	for k, v := range kv {
		logrus.Debugf("writing %s = %s with TTL %s", k, v, s.TTL)
		s.client.Set(context.Background(), k, v, s.TTL)
	}

	s.swapKeys(s.sk("http_middlewares"))
	s.swapKeys(s.sk("http_routers"))
	s.swapKeys(s.sk("http_services"))
	s.swapKeys(s.sk("tcp_middlewares"))
	s.swapKeys(s.sk("tcp_routers"))
	s.swapKeys(s.sk("tcp_services"))
	s.swapKeys(s.sk("udp_routers"))
	s.swapKeys(s.sk("udp_services"))

	// Update sentinel key with current timestamp
	s.client.Set(context.Background(), s.sk("kop_last_update"), time.Now().Unix(), s.TTL)

	// Store a copy of the configuration in case redis restarts
	configCopy := conf
	s.lastConfig = &configCopy

	return nil
}

// NeedsUpdate checks if Redis needs a full configuration refresh
// by checking for the sentinel key's existence
func (s *RedisStore) NeedsUpdate() bool {
	// Check if sentinel key exists
	exists, err := s.client.Exists(context.Background(), s.sk("kop_last_update")).Result()
	if err != nil {
		logrus.Warnf("Failed to check Redis status: %s", err)
	}
	return exists == 0
}

// Push the last configuration if needed
func (s *RedisStore) KeepConfAlive() error {
	if s.lastConfig == nil {
		return nil // No config to push yet
	}

	if s.NeedsUpdate() {
		logrus.Warnln("Redis seems to have restarted and needs to be updated. Pushing last known configuration")
		return s.Store(*s.lastConfig)
	}

	return nil
}

func (s *RedisStore) swapKeys(setkey string) error {
	// store router name list by renaming
	err := s.client.Rename(context.Background(), setkey+"_new", setkey).Err()
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			s.client.Unlink(context.Background(), setkey)
			return nil
		}
		return errors.Wrap(err, "rename failed")
	}
	return nil
}

// k returns the actual config key path
// e.g., traefik/http/routers/nginx@docker
func (s RedisStore) k(sk, b string) string {
	k := strings.ReplaceAll(fmt.Sprintf("traefik_%s", sk), "_", "/")
	b = stripDocker(b)
	return fmt.Sprintf("%s/%s", k, b)
}

func (s *RedisStore) removeKeys(setkey string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		logrus.Debugf("removing keys from %s: %s", setkey, strings.Join(keys, ","))
	}
	for _, removeKey := range keys {
		keyPath := s.k(setkey, removeKey) + "/*"
		logrus.Debugf("removing keys matching %s", keyPath)
		res, err := s.client.Keys(context.Background(), keyPath).Result()
		if err != nil {
			return errors.Wrap(err, "fetch failed")
		}
		if err := s.client.Unlink(context.Background(), res...).Err(); err != nil {
			return errors.Wrap(err, "unlink failed")
		}
	}
	return nil
}

func (s *RedisStore) removeOldKeys(m interface{}, setname string) error {
	setkey := s.sk(setname)
	// store new keys in temp set
	newkeys := collectKeys(m)
	if len(newkeys) == 0 {
		res, err := s.client.SMembers(context.Background(), setkey).Result()
		if err != nil {
			return errors.Wrap(err, "fetch failed")
		}
		return s.removeKeys(setname, res)

	} else {
		// make a diff and remove
		err := s.client.SAdd(context.Background(), setkey+"_new", mkslice(newkeys)...).Err()
		if err != nil {
			return errors.Wrap(err, "add failed")
		}

		// diff the existing keys with the new ones
		res, err := s.client.SDiff(context.Background(), setkey, setkey+"_new").Result()
		if err != nil {
			return errors.Wrap(err, "diff failed")
		}
		return s.removeKeys(setname, res)
	}
}

// mkslice converts a string slice to an interface slice
func mkslice(old []string) []interface{} {
	new := make([]interface{}, len(old))
	for i, v := range old {
		new[i] = v
	}
	return new
}
