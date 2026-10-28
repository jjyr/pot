package control

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

func mcpContractSchema(value any) map[string]any { return mcpSchemaType(reflect.TypeOf(value)) }
func mcpSchemaType(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Pointer {
		return map[string]any{"anyOf": []any{mcpSchemaType(t.Elem()), map[string]any{"type": "null"}}}
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		return map[string]any{"type": "object"}
	}
	if t == reflect.TypeFor[time.Time]() {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.Struct:
		properties := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			tag := field.Tag.Get("json")
			name, options, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			properties[name] = mcpSchemaType(field.Type)
			if field.Tag.Get("required") == "true" || (!strings.Contains(options, "omitempty") && field.Type.Kind() != reflect.Pointer) {
				required = append(required, name)
			}
		}
		return mcpSchema(properties, required...)
	case reflect.String:
		return mcpStringSchema()
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Int32, reflect.Uint, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": mcpSchemaType(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object"}
	default:
		return map[string]any{}
	}
}
