package traefikkop

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
)

type KV struct {
	data map[string]interface{}
	base string
}

func NewKV() *KV {
	return &KV{data: make(map[string]interface{})}
}

func (kv *KV) SetBase(b string) {
	kv.base = b
}

func (kv *KV) add(val interface{}, format string, a ...interface{}) {
	if val == nil {
		return // todo: log it? debug?
	}
	str := fmt.Sprintf("%s", val)
	if str == "" {
		return // todo: log it? debug?
	}
	if kv.base != "" {
		format = kv.base + "/" + format
	}

	key := fmt.Sprintf(format, a...)
	if strings.HasPrefix(key, "traefik/tls/") {
		// ignore tls options, only interested in things that can be set per-container
		return
	}

	kv.data[key] = val
}

// ConfigToKV flattens the given configuration into a format suitable for
// putting into a KV store such as redis
func ConfigToKV(conf dynamic.Configuration) (map[string]interface{}, error) {
	b, err := json.Marshal(conf)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create kv")
	}

	hash := make(map[string]interface{})
	err = json.Unmarshal(b, &hash)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create kv")
	}

	kv := NewKV()
	walk(kv, "traefik", hash, "")

	return kv.data, nil
}

var reKeyName = regexp.MustCompile(`^traefik/(http|tcp|udp)/(router|service|middleware)s$`)

func walk(kv *KV, path string, obj interface{}, pos string) {
	if obj == nil {
		return
	}

	val := reflect.ValueOf(obj)

	switch val.Kind() {
	case reflect.Map:
		iter := val.MapRange()
		for iter.Next() {
			key := iter.Key()
			val := iter.Value()
			if !val.CanInterface() {
				continue
			}
			strKey := key.String()
			if reKeyName.MatchString(path) {
				strKey = stripDocker(strKey)
			}
			walk(kv, path+"/"+strKey, val.Interface(), strKey)
		}

	case reflect.Struct:
		num := val.NumField()
		for i := 0; i < num; i++ {
			val := val.Field(i)
			if !val.CanInterface() {
				continue
			}
			walk(kv, path, val.Interface(), pos)
		}

	case reflect.Slice:
		n := val.Len()
		for i := 0; i < n; i++ {
			val := val.Index(i)
			if !val.CanInterface() {
				continue
			}
			walk(kv, fmt.Sprintf("%s/%d", path, i), val.Interface(), pos)
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.String, reflect.Bool:

		// stringify it
		kv.add(stringify(val.Interface()), path)

	case reflect.Float32, reflect.Float64:
		kv.add(stringifyFloat(val.Float()), path)

	default:
		log.Warn().Msgf("unhandled kind %s: %#v\n", val.Kind(), obj)
	}

}

func stringify(val interface{}) string {
	return fmt.Sprintf("%v", val)
}

func stringifyFloat(val float64) string {
	return strconv.FormatFloat(val, 'f', -1, 64)
}
