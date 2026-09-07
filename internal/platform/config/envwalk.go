package config

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// EnvKey describes one env-var-addressable leaf field for downstream generators.
type EnvKey struct {
	Key       string
	YAML      string
	Kind      string
	Awareness []string
	Default   any
}

// WalkEnvKeys enumerates every env key derived from the Config struct, prefixed with the given env prefix.
func WalkEnvKeys(prefix string) []EnvKey {
	var out []EnvKey
	walkForEnvKeys(&out, prefix, "", reflect.TypeOf(Config{}), nil)

	defaults := defaultValues()
	for i := range out {
		if v, ok := defaults[strings.ToLower(out[i].YAML)]; ok {
			out[i].Default = v
		}
	}
	return out
}

func defaultValues() map[string]any {
	v := viper.NewWithOptions(viper.KeyDelimiter("."))
	setDefaults(v)
	out := make(map[string]any)
	for _, k := range v.AllKeys() {
		out[k] = v.Get(k)
	}
	return out
}

// FormatDefault renders k.Default as a display string suitable for a generated env-example file.
func (k EnvKey) FormatDefault() string {
	if k.Default == nil {
		return ""
	}
	switch v := k.Default.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case time.Duration:
		return v.String()
	case []string:
		return strings.Join(v, ",")
	case []any:
		parts := make([]string, len(v))
		for i, elt := range v {
			parts[i] = fmt.Sprint(elt)
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(v)
	}
}

func walkForEnvKeys(out *[]EnvKey, envPrefix, yamlPrefix string, t reflect.Type, inherited []string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		seg, ok := segmentFor(f)
		if !ok {
			continue
		}
		yamlKey := seg
		if yamlPrefix != "" {
			yamlKey = yamlPrefix + "." + seg
		}
		awareness := mergeAwareness(inherited, awarenessTag(f))
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Map {
			et := ft.Elem()
			for et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct && !isLeafType(et) {
				continue
			}
		}
		if ft.Kind() == reflect.Struct && !isLeafType(ft) {
			walkForEnvKeys(out, envPrefix, yamlKey, ft, awareness)
			continue
		}
		*out = append(*out, EnvKey{
			Key:       envVarName(envPrefix, yamlKey),
			YAML:      yamlKey,
			Kind:      kindName(ft),
			Awareness: awareness,
		})
	}
}

func awarenessTag(f reflect.StructField) []string {
	raw, ok := f.Tag.Lookup("awareness")
	if !ok || raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeAwareness(inherited, own []string) []string {
	// A field can opt out of inheritance with `awareness:"-"` — same convention as encoding/json.
	// The `-` itself is stripped; anything after it (e.g. `-,secret`) still applies.
	if slices.Contains(own, "-") {
		inherited = nil
		own = slices.DeleteFunc(slices.Clone(own), func(s string) bool { return s == "-" })
	}
	if len(inherited) == 0 && len(own) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(inherited)+len(own))
	out := make([]string, 0, len(inherited)+len(own))
	for _, v := range inherited {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, v := range own {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func kindName(t reflect.Type) string {
	if t == reflect.TypeOf(time.Duration(0)) {
		return "duration"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "uint"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Slice:
		return "[]" + kindName(t.Elem())
	case reflect.Map:
		return "map[" + kindName(t.Key()) + "]" + kindName(t.Elem())
	default:
		return t.Kind().String()
	}
}
