package json2

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/tidwall/gjson"
)

func structFieldsToFieldsSlice(u interface{}) ([]interface{}, error) {
	valInterface := reflect.ValueOf(u)
	if valInterface.Kind() != reflect.Pointer {
		return nil, fmt.Errorf("input argument must be a pointer, got %s", valInterface.Kind())
	}

	val := valInterface.Elem()
	v := make([]interface{}, val.NumField())
	for i := 0; i < val.NumField(); i++ {
		valueField := val.Field(i)
		v[i] = valueField.Addr().Interface()
	}

	return v, nil
}

// StructFields could be used to improve error messages on unmarshal, for now it's unsued
type StructFields []interface{}

func (f *StructFields) UnmarshalJSON(data []byte) error {
	result := gjson.ParseBytes(data)
	if !result.IsArray() {
		return fmt.Errorf("expected array but got %s", result.Type)
	}

	elementResults := result.Array()
	if len(elementResults) != len(*f) {
		return fmt.Errorf("input array has %d elements but we are trying to unserialize in only %d struct fields", len(elementResults), len(*f))
	}

	for i, elementResult := range elementResults {
		reference := (*f)[i]
		err := json.Unmarshal([]byte(elementResult.Raw), reference)
		if err != nil {
			return fmt.Errorf("unable to marshal JSON into field %d of type %T: %w", i, reference, err)
		}
	}

	return nil
}
