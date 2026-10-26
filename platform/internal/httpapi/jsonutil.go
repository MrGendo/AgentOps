package httpapi

import "encoding/json"

func jsonUnmarshalString(s string, out any) error { return json.Unmarshal([]byte(s), out) }
