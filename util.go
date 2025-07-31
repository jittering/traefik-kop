package traefikkop

import (
	"encoding/json"
	"strings"
)

func dumpJson(o interface{}) []byte {
	out, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}
	return out
}

func splitStringArr(str string) []string {
	trimmed := strings.TrimSpace(str)
	if trimmed != "" {
		trimmedVals := strings.Split(trimmed, ",")
		splitArr := make([]string, len(trimmedVals))
		for i, v := range trimmedVals {
			splitArr[i] = strings.TrimSpace(v)
		}
		return splitArr
	}
	return []string{}
}
