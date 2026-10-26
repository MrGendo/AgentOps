package gateway

import "encoding/json"

func unmarshalStrings(b []byte, out *[]string) error { return json.Unmarshal(b, out) }

func unmarshalAny(b []byte, out *any) error { return json.Unmarshal(b, out) }

func marshalAny(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}
