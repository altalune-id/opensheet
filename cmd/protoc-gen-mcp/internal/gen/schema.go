package gen

import (
	"bytes"
	"encoding/json"
	"slices"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	int64Pattern  = `^-?[0-9]+$`
	uint64Pattern = `^[0-9]+$`
	// NOTE: https://protobuf.dev/programming-guides/json/ — a Duration is "0, 3, 6 or 9 fractional
	// digits" followed by "s", and any digit count up to nanosecond precision is accepted.
	durationPattern = `^-?[0-9]+(\.[0-9]{1,9})?s$`
)

type jsonObject struct {
	keys []string
	vals []any
}

func newObject() *jsonObject { return &jsonObject{} }

func (o *jsonObject) set(key string, val any) *jsonObject {
	o.keys = append(o.keys, key)
	o.vals = append(o.vals, val)
	return o
}

func (o *jsonObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		encoded, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		b.Write(encoded)
		b.WriteByte(':')
		if encoded, err = json.Marshal(o.vals[i]); err != nil {
			return nil, err
		}
		b.Write(encoded)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func inputSchema(msg *protogen.Message, required []string) (json.RawMessage, error) {
	stack := []protoreflect.FullName{msg.Desc.FullName()}
	schema := newObject().set("type", "object").set("properties", properties(msg, stack))
	if len(required) > 0 {
		schema.set("required", required)
	}
	return json.Marshal(schema.set("additionalProperties", false))
}

func jsonFieldNames(msg *protogen.Message) []string {
	names := make([]string, 0, len(msg.Fields))
	for _, field := range msg.Fields {
		names = append(names, field.Desc.JSONName())
	}
	return names
}

func requiredProperties(msg *protogen.Message, required []string) (resolved, unknown []string) {
	for _, name := range required {
		i := slices.IndexFunc(msg.Fields, func(field *protogen.Field) bool {
			return field.Desc.JSONName() == name || string(field.Desc.Name()) == name
		})
		if i < 0 {
			unknown = append(unknown, name)
			continue
		}
		resolved = append(resolved, msg.Fields[i].Desc.JSONName())
	}
	return resolved, unknown
}

func properties(msg *protogen.Message, stack []protoreflect.FullName) *jsonObject {
	props := newObject()
	for _, field := range msg.Fields {
		props.set(field.Desc.JSONName(), fieldSchema(field, stack))
	}
	return props
}

func messageSchema(msg *protogen.Message, stack []protoreflect.FullName) *jsonObject {
	return newObject().
		set("type", "object").
		set("properties", properties(msg, stack)).
		set("additionalProperties", false)
}

func fieldSchema(field *protogen.Field, stack []protoreflect.FullName) *jsonObject {
	var schema *jsonObject
	switch {
	case field.Desc.IsMap():
		schema = newObject().
			set("type", "object").
			set("additionalProperties", valueSchema(field.Message.Fields[1], stack))
	case field.Desc.IsList():
		schema = newObject().
			set("type", "array").
			set("items", valueSchema(field, stack))
	default:
		schema = valueSchema(field, stack)
	}
	if description := comment(field.Comments.Leading); description != "" {
		schema.set("description", description)
	}
	return schema
}

func valueSchema(field *protogen.Field, stack []protoreflect.FullName) *jsonObject {
	switch field.Desc.Kind() {
	case protoreflect.BoolKind:
		return newObject().set("type", "boolean")
	case protoreflect.StringKind:
		return newObject().set("type", "string")
	case protoreflect.BytesKind:
		return newObject().set("type", "string").set("format", "byte")
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return newObject().set("type", "number")
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return newObject().set("type", "integer")
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return newObject().set("type", "integer").set("minimum", 0)
	// NOTE: protojson serializes 64-bit integers as strings, so the schema must ask for one.
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return newObject().set("type", "string").set("pattern", int64Pattern)
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return newObject().set("type", "string").set("pattern", uint64Pattern)
	case protoreflect.EnumKind:
		return enumSchema(field.Enum)
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return nestedSchema(field.Message, stack)
	default:
		return newObject().set("type", "string")
	}
}

func enumSchema(enum *protogen.Enum) *jsonObject {
	if enum == nil {
		return newObject().set("type", "string")
	}
	values := make([]string, 0, len(enum.Values))
	for _, value := range enum.Values {
		values = append(values, string(value.Desc.Name()))
	}
	return newObject().set("type", "string").set("enum", values)
}

func nestedSchema(msg *protogen.Message, stack []protoreflect.FullName) *jsonObject {
	if msg == nil {
		return newObject().set("type", "object")
	}
	name := msg.Desc.FullName()
	if schema, ok := wellKnownSchema(name); ok {
		return schema
	}
	if slices.Contains(stack, name) {
		return newObject().set("type", "object")
	}
	return messageSchema(msg, append(slices.Clone(stack), name))
}

// NOTE: https://protobuf.dev/programming-guides/json/ — these encode as something other than an
// object over their own fields, so the generic message schema would describe a shape protojson
// rejects.
func wellKnownSchema(name protoreflect.FullName) (*jsonObject, bool) {
	switch name {
	case "google.protobuf.Timestamp":
		return newObject().set("type", "string").set("format", "date-time"), true
	case "google.protobuf.Duration":
		return newObject().set("type", "string").set("pattern", durationPattern), true
	case "google.protobuf.FieldMask":
		return newObject().set("type", "string"), true
	case "google.protobuf.DoubleValue", "google.protobuf.FloatValue":
		return newObject().set("type", "number"), true
	case "google.protobuf.Int32Value":
		return newObject().set("type", "integer"), true
	case "google.protobuf.UInt32Value":
		return newObject().set("type", "integer").set("minimum", 0), true
	case "google.protobuf.Int64Value":
		return newObject().set("type", "string").set("pattern", int64Pattern), true
	case "google.protobuf.UInt64Value":
		return newObject().set("type", "string").set("pattern", uint64Pattern), true
	case "google.protobuf.BoolValue":
		return newObject().set("type", "boolean"), true
	case "google.protobuf.StringValue":
		return newObject().set("type", "string"), true
	case "google.protobuf.BytesValue":
		return newObject().set("type", "string").set("format", "byte"), true
	case "google.protobuf.Struct":
		return newObject().set("type", "object"), true
	case "google.protobuf.ListValue":
		return newObject().set("type", "array"), true
	case "google.protobuf.Value":
		return newObject(), true
	case "google.protobuf.Any":
		return newObject().
			set("type", "object").
			set("properties", newObject().set("@type", newObject().set("type", "string"))).
			set("required", []string{"@type"}), true
	}
	return nil, false
}
