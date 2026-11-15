package kicad

import "encoding/json"

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err) // only maps, slices and strings: can't fail
	}
	return data
}
