package bedrock

import (
	"reflect"

	"github.com/adrianliechti/wingman/pkg/jsonutil"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	smithydocument "github.com/aws/smithy-go/document"
)

func newDocument(value any) document.Interface {
	return document.NewLazyDocument(documentValue(value))
}

// Smithy's encoder treats jsonutil.Number as a string. Translate numbers at the
// SDK boundary, including nested schema values, without mutating caller data.
func documentValue(value any) any {
	switch value := value.(type) {
	case jsonutil.Number:
		return smithydocument.Number(value)
	case map[string]any:
		if value == nil {
			return value
		}
		result := make(map[string]any, len(value))
		for key, item := range value {
			result[key] = documentValue(item)
		}
		return result
	case []any:
		if value == nil {
			return value
		}
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = documentValue(item)
		}
		return result
	default:
		// Programmatic callers can supply typed maps/slices in a schema too,
		// for example []jsonutil.Number or []map[string]any inside an enum/anyOf.
		rv := reflect.ValueOf(value)
		if !rv.IsValid() {
			return value
		}
		switch rv.Kind() {
		case reflect.Map:
			if rv.IsNil() || rv.Type().Key().Kind() != reflect.String {
				return value
			}
			result := make(map[string]any, rv.Len())
			iter := rv.MapRange()
			for iter.Next() {
				result[iter.Key().String()] = documentValue(iter.Value().Interface())
			}
			return result
		case reflect.Slice, reflect.Array:
			if rv.Type().Elem().Kind() == reflect.Uint8 || rv.Kind() == reflect.Slice && rv.IsNil() {
				return value
			}
			result := make([]any, rv.Len())
			for i := range result {
				result[i] = documentValue(rv.Index(i).Interface())
			}
			return result
		}
		return value
	}
}
