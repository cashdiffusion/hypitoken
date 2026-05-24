package db

import "encoding/json"

// Tiny indirection so test stubs (rare) can swap in a different impl without
// pulling encoding/json into every file. Used by tokens.go for the groups
// JSON-array column.
var (
	jsonMarshal   = json.Marshal
	jsonUnmarshal = json.Unmarshal
)
