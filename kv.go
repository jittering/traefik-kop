package traefikkop

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/traefik/traefik/v3/pkg/config/dynamic"
)

type KV struct {
	data           map[string]interface{}
	base           string
	traefikVersion int
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
func ConfigToKV(conf dynamic.Configuration, traefikVersion int) (map[string]interface{}, error) {
	kv := NewKV()
	kv.traefikVersion = traefikVersion
	_, err := walkTypedValue(kv, "traefik", reflect.TypeOf(conf), reflect.ValueOf(conf), walkOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create kv: %w", err)
	}

	return kv.data, nil
}

var (
	reKeyName         = regexp.MustCompile(`^traefik/(http|tcp|udp)/(router|service|middleware)s$`)
	jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

type walkOptions struct {
	allowEmpty bool
}

func walkTypedValue(kv *KV, path string, typ reflect.Type, val reflect.Value, opts walkOptions) (bool, error) {
	if typ == nil || !val.IsValid() {
		return false, nil
	}

	for typ.Kind() == reflect.Interface {
		if val.IsNil() {
			return false, nil
		}
		val = val.Elem()
		typ = val.Type()
	}

	if typ.Kind() == reflect.Pointer {
		if val.IsNil() {
			return false, nil
		}

		added, err := walkTypedValue(kv, path, typ.Elem(), val.Elem(), walkOptions{})
		if err != nil {
			return false, err
		}

		if !added && opts.allowEmpty && typ.Elem().Kind() == reflect.Struct {
			kv.add("true", path)
			return true, nil
		}

		return added, nil
	}

	if isLeafType(typ) {
		s, ok, err := leafString(typ, val)
		if err != nil {
			return false, fmt.Errorf("walk %s: %w", path, err)
		}
		if ok {
			kv.add(s, path)
			return true, nil
		}
		return false, nil
	}

	switch typ.Kind() {
	case reflect.Struct:
		return walkStructValue(kv, path, typ, val)
	case reflect.Map:
		return walkMapValue(kv, path, typ, val)
	case reflect.Slice, reflect.Array:
		return walkSliceValue(kv, path, typ, val)
	default:
		return false, nil
	}
}

func walkStructValue(kv *KV, path string, typ reflect.Type, val reflect.Value) (bool, error) {
	addedAny := false

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}

		fieldName, omitEmpty, skip := jsonFieldName(field)
		if skip {
			continue
		}

		fieldValue := val.Field(i)
		if omitEmpty && isEmptyJSONValue(fieldValue) {
			continue
		}

		if field.Anonymous && fieldName == field.Name {
			added, err := walkTypedValue(kv, path, field.Type, fieldValue, walkOptions{})
			if err != nil {
				return false, err
			}
			addedAny = addedAny || added
			continue
		}

		childPath := joinPath(path, fieldName)
		added, err := walkTypedValue(kv, childPath, field.Type, fieldValue, walkOptions{allowEmpty: field.Tag.Get("label") == "allowEmpty"})
		if err != nil {
			return false, err
		}
		addedAny = addedAny || added
	}

	return addedAny, nil
}

func walkMapValue(kv *KV, path string, typ reflect.Type, val reflect.Value) (bool, error) {
	if val.IsNil() || val.Len() == 0 {
		return false, nil
	}

	if typ.Key().Kind() != reflect.String {
		return false, fmt.Errorf("unsupported map key type %s at %s", typ.Key(), path)
	}

	addedAny := false
	iter := val.MapRange()
	for iter.Next() {
		key := iter.Key().String()
		if reKeyName.MatchString(path) {
			key = stripDocker(key)
		}

		added, err := walkTypedValue(kv, joinPath(path, key), typ.Elem(), iter.Value(), walkOptions{})
		if err != nil {
			return false, err
		}
		addedAny = addedAny || added
	}

	return addedAny, nil
}

func walkSliceValue(kv *KV, path string, typ reflect.Type, val reflect.Value) (bool, error) {
	if typ.Kind() == reflect.Slice && val.IsNil() {
		return false, nil
	}

	if val.Len() == 0 {
		return false, nil
	}

	if kv.traefikVersion >= 3 && typ.Elem().Kind() == reflect.String {
		var parts []string
		for i := 0; i < val.Len(); i++ {
			parts = append(parts, val.Index(i).String())
		}
		kv.add(strings.Join(parts, ","), path)
		return true, nil
	}

	addedAny := false
	for i := 0; i < val.Len(); i++ {
		added, err := walkTypedValue(kv, fmt.Sprintf("%s/%d", path, i), typ.Elem(), val.Index(i), walkOptions{})
		if err != nil {
			return false, err
		}
		addedAny = addedAny || added
	}

	return addedAny, nil
}

func isLeafType(typ reflect.Type) bool {
	if typ.Implements(textMarshalerType) || reflect.PointerTo(typ).Implements(textMarshalerType) ||
		typ.Implements(jsonMarshalerType) || reflect.PointerTo(typ).Implements(jsonMarshalerType) {
		return true
	}
	switch typ.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.String:
		return true
	default:
		return false
	}
}

func leafString(typ reflect.Type, val reflect.Value) (string, bool, error) {
	// Custom text marshaler (e.g., ptypes.Duration) — preferred, gives raw text
	if v, ok := marshalerValue(val, textMarshalerType); ok {
		b, err := v.(encoding.TextMarshaler).MarshalText()
		if err != nil {
			return "", false, err
		}
		s := string(b)
		return s, s != "", nil
	}

	// json.Marshaler fallback (without TextMarshaler); use JSON roundtrip
	if v, ok := marshalerValue(val, jsonMarshalerType); ok {
		b, err := json.Marshal(v)
		if err != nil {
			return "", false, err
		}
		var s string
		if err := json.Unmarshal(b, &s); err == nil {
			return s, s != "", nil
		}
		s = strings.Trim(string(b), `"`)
		return s, s != "", nil
	}

	// Primitives
	switch typ.Kind() {
	case reflect.String:
		s := val.String()
		return s, s != "", nil
	case reflect.Bool:
		return strconv.FormatBool(val.Bool()), true, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(val.Int(), 10), true, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(val.Uint(), 10), true, nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(val.Float(), 'f', -1, 64), true, nil
	}
	return "", false, nil
}

// marshalerValue returns val (or pointer-to-val) as an interface{} if it implements iface.
func marshalerValue(val reflect.Value, iface reflect.Type) (interface{}, bool) {
	if !val.IsValid() {
		return nil, false
	}
	typ := val.Type()
	if typ.Implements(iface) && val.CanInterface() {
		return val.Interface(), true
	}
	if val.CanAddr() && val.Addr().Type().Implements(iface) {
		return val.Addr().Interface(), true
	}
	if reflect.PointerTo(typ).Implements(iface) {
		p := reflect.New(typ)
		p.Elem().Set(val)
		return p.Interface(), true
	}
	return nil, false
}

func joinPath(base, child string) string {
	return base + "/" + child
}

func jsonFieldName(field reflect.StructField) (string, bool, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}

	parts := strings.Split(tag, ",")
	name := field.Name
	if len(parts) > 0 && parts[0] != "" {
		name = parts[0]
	}

	omitEmpty := false
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitEmpty = true
			break
		}
	}

	return name, omitEmpty, false
}

func isEmptyJSONValue(val reflect.Value) bool {
	if !val.IsValid() {
		return true
	}

	switch val.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return val.Len() == 0
	case reflect.Bool:
		return !val.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return val.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return val.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return val.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return val.IsNil()
	default:
		return false
	}
}
