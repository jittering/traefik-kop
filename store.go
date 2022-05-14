package traefikkop

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
	"gopkg.in/redis.v5"
)

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

type Store struct {
	Hostname string
	Addr     string
	Pass     string
	DB       int

	client *redis.Client
}

func NewStore(hostname string, addr string, pass string, db int) *Store {
	logrus.Infof("creating new redis store at %s for hostname %s", addr, hostname)

	store := &Store{
		Hostname: hostname,
		Addr:     addr,
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

func (s *Store) Ping() error {
	return s.client.Ping().Err()
}

// sk returns the 'set key' for keeping track of our services/routers/middlewares
// e.g., traefik_http_routers@culture.local
func (s Store) sk(b string) string {
	return fmt.Sprintf("traefik_%s@%s", b, s.Hostname)
}

func (s *Store) Store(conf dynamic.Configuration) error {
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
		logrus.Debugf("writing %s = %s", k, v)
		s.client.Set(k, v, 0)
	}

	s.swapKeys(s.sk("http_middlewares"))
	s.swapKeys(s.sk("http_routers"))
	s.swapKeys(s.sk("http_services"))
	s.swapKeys(s.sk("tcp_middlewares"))
	s.swapKeys(s.sk("tcp_routers"))
	s.swapKeys(s.sk("tcp_services"))
	s.swapKeys(s.sk("udp_routers"))
	s.swapKeys(s.sk("udp_services"))

	return nil
}

func (s *Store) swapKeys(setkey string) error {
	// store router name list by renaming
	err := s.client.Rename(setkey+"_new", setkey).Err()
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			s.client.Unlink(setkey)
			return nil
		}
		return errors.Wrap(err, "rename failed")
	}
	return nil
}

// k returns the actual config key path
// e.g., traefik/http/routers/nginx@docker
func (s Store) k(sk, b string) string {
	k := strings.ReplaceAll(fmt.Sprintf("traefik_%s", sk), "_", "/")
	b = strings.TrimSuffix(b, "@docker")
	return fmt.Sprintf("%s/%s", k, b)
}

func (s *Store) removeKeys(setkey string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		logrus.Debugf("removing keys from %s: %s", setkey, strings.Join(keys, ","))
	}
	for _, removeKey := range keys {
		keyPath := s.k(setkey, removeKey) + "/*"
		logrus.Debugf("removing keys matching %s", keyPath)
		res, err := s.client.Keys(keyPath).Result()
		if err != nil {
			return errors.Wrap(err, "fetch failed")
		}
		if err := s.client.Unlink(res...).Err(); err != nil {
			return errors.Wrap(err, "unlink failed")
		}
	}
	return nil
}

func (s *Store) removeOldKeys(m interface{}, setname string) error {
	setkey := s.sk(setname)
	// store new keys in temp set
	newkeys := collectKeys(m)
	if len(newkeys) == 0 {
		res, err := s.client.SMembers(setkey).Result()
		if err != nil {
			return errors.Wrap(err, "fetch failed")
		}
		return s.removeKeys(setname, res)

	} else {
		// make a diff and remove
		err := s.client.SAdd(setkey+"_new", mkslice(newkeys)...).Err()
		if err != nil {
			return errors.Wrap(err, "add failed")
		}

		// diff the existing keys with the new ones
		res, err := s.client.SDiff(setkey, setkey+"_new").Result()
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
