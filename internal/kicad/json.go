package kicad

import "encoding/json"

// jsonNumber is written as-is, so coordinates keep a short exact form.
type jsonNumber = json.Number

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err) // only maps, slices, strings and numbers: can't fail
	}
	return data
}
