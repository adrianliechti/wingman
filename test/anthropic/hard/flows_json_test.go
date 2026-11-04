package hard_test

import "encoding/json/v2"

func parseJSON(data string, v any) error {
	return json.Unmarshal([]byte(data), v)
}

func marshalJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	return string(data), err
}
