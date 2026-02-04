package ir

import (
	"fmt"
	"reflect"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

func (cfg *Config) PopulateFromFile(path string) error {
	v := viper.New()
	v.SetConfigFile(path)

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		DecodeHook: bucketsDecodeHook,
		Result:     cfg,
	})
	if err != nil {
		return fmt.Errorf("failed to create decoder: %w", err)
	}

	if err := decoder.Decode(v.AllSettings()); err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return nil
}

// bucketsDecodeHook handles conversion of buckets from either list or map format to map[string]BucketConfig.
// It defensively handles various map types (map[string]any, map[any]any, etc.) that may result from
// unmarshaling YAML/JSON through different code paths.
func bucketsDecodeHook(from reflect.Type, to reflect.Type, data any) (any, error) {
	if to != reflect.TypeOf((*map[string]BucketConfig)(nil)).Elem() {
		return data, nil
	}

	// Check if it's a slice (list format)
	if sliceType := reflect.TypeOf(data); sliceType != nil && sliceType.Kind() == reflect.Slice {
		return decodeListFormat(data)
	}

	// Check if it's any kind of map (map format)
	if mapType := reflect.TypeOf(data); mapType != nil && mapType.Kind() == reflect.Map {
		return decodeMapFormat(data)
	}

	// Unknown format
	return nil, fmt.Errorf("buckets must be a list or map, got %T", data)
}

// decodeListFormat handles bucket configuration in list format: [{ name: bucket1, routes: [...] }, ...]
func decodeListFormat(data any) (any, error) {
	v := reflect.ValueOf(data)
	result := make(map[string]BucketConfig)
	seen := make(map[string]bool)

	for i := 0; i < v.Len(); i++ {
		item := v.Index(i).Interface()
		m, ok := item.(map[string]any)
		if !ok {
			// Try converting from map[any]any to map[string]any
			if mapAny, isMap := item.(map[any]any); isMap {
				m = convertMapAnyToMapString(mapAny)
			} else {
				return nil, fmt.Errorf("bucket at index %d is not a map, got %T", i, item)
			}
		}

		if name, ok := m["name"].(string); ok {
			var bucket BucketConfig
			bucket.Name = name

			// Decode routes using mapstructure
			decoder, _ := mapstructure.NewDecoder(&mapstructure.DecoderConfig{Result: &bucket})
			if err := decoder.Decode(m); err != nil {
				return nil, fmt.Errorf("failed to decode bucket at index %d: %w", i, err)
			}

			if seen[bucket.Name] {
				return nil, fmt.Errorf("duplicate bucket name %q", bucket.Name)
			}
			seen[bucket.Name] = true
			result[bucket.Name] = bucket
		} else {
			return nil, fmt.Errorf("bucket at index %d missing or invalid name field", i)
		}
	}
	return result, nil
}

// decodeMapFormat handles bucket configuration in map format: { bucket1: { routes: [...] }, ... }
func decodeMapFormat(data any) (any, error) {
	v := reflect.ValueOf(data)
	result := make(map[string]BucketConfig)

	// Iterate through map entries
	for _, keyVal := range v.MapKeys() {
		// Convert key to string
		var name string
		switch keyVal.Kind() {
		case reflect.String:
			name = keyVal.String()
		default:
			name = fmt.Sprint(keyVal.Interface())
		}

		bucketData := v.MapIndex(keyVal).Interface()
		var bucket BucketConfig
		bucket.Name = name

		// Convert bucket data to map[string]any for decoding
		var m map[string]any
		switch bucketDataTyped := bucketData.(type) {
		case map[string]any:
			m = bucketDataTyped
		case map[any]any:
			m = convertMapAnyToMapString(bucketDataTyped)
		default:
			return nil, fmt.Errorf("bucket %q value is not a map, got %T", name, bucketData)
		}

		// Decode routes using mapstructure
		decoder, _ := mapstructure.NewDecoder(&mapstructure.DecoderConfig{Result: &bucket})
		if err := decoder.Decode(m); err != nil {
			return nil, fmt.Errorf("failed to decode bucket %q: %w", name, err)
		}

		result[name] = bucket
	}
	return result, nil
}

// convertMapAnyToMapString converts map[any]any to map[string]any.
// This is necessary because YAML parsers may produce map[any]any when keys are non-string types.
func convertMapAnyToMapString(m map[any]any) map[string]any {
	result := make(map[string]any)
	for k, v := range m {
		// Convert key to string using type switch to avoid redundant assertions
		var keyStr string
		switch k := k.(type) {
		case string:
			keyStr = k
		default:
			keyStr = fmt.Sprint(k)
		}
		result[keyStr] = v
	}
	return result
}
