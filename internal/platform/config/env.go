package config

import (
	"reflect"
	"strings"

	"github.com/spf13/viper"
)

func bindEnv(v *viper.Viper, prefix string, t reflect.Type) {
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
		key := seg
		if prefix != "" {
			key = prefix + "." + seg
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && !isLeafType(ft) {
			bindEnv(v, key, ft)
			continue
		}
		_ = v.BindEnv(key, envVarName("ALT", key))
	}
}

func segmentFor(f reflect.StructField) (string, bool) {
	tag, hasTag := f.Tag.Lookup("mapstructure")
	if hasTag {
		if tag == "-" {
			return "", false
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			return f.Name, true
		}
		return name, true
	}
	return f.Name, true
}

func isLeafType(t reflect.Type) bool {
	pkg := t.PkgPath()
	name := t.Name()
	switch {
	case pkg == "time" && name == "Time":
		return true
	case pkg == "net/url" && name == "URL":
		return true
	}
	return false
}

func envVarName(prefix, key string) string {
	var b strings.Builder
	if prefix != "" {
		b.WriteString(prefix)
		b.WriteByte('_')
	}
	appendUpperSnake(&b, key)
	return b.String()
}

func appendUpperSnake(b *strings.Builder, s string) {
	prev := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '.' || c == '-' || c == '_':
			b.WriteByte('_')
			prev = '_'
			continue
		case c >= 'A' && c <= 'Z':
			if prev != 0 && prev != '_' {
				nextLower := i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z'
				prevLowerOrDigit := (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9')
				prevUpper := prev >= 'A' && prev <= 'Z'
				if prevLowerOrDigit || (prevUpper && nextLower) {
					b.WriteByte('_')
				}
			}
			b.WriteByte(c)
		case c >= 'a' && c <= 'z':
			b.WriteByte(c - 32)
		default:
			b.WriteByte(c)
		}
		prev = c
	}
}
