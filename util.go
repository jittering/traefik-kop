package traefikkop

import "encoding/json"

func dumpJson(o interface{}) []byte {
	out, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}
	return out
}
