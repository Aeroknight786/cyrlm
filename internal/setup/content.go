package setup

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
)

// JSON values are expanded structurally so Windows paths stay valid JSON.
func (e *Executor) content(content string, expand bool) ([]byte, error) {
	if !expand {
		return []byte(content), nil
	}
	if strings.HasPrefix(content, "FROM ") {
		first, rest, _ := strings.Cut(content, "\n")
		model := filepath.ToSlash(e.expand(strings.TrimPrefix(first, "FROM ")))
		return []byte("FROM " + strconv.Quote(model) + "\n" + rest), nil
	}
	var value any
	if json.Unmarshal([]byte(content), &value) != nil {
		return []byte(e.expand(content)), nil
	}
	var walk func(any) any
	walk = func(value any) any {
		switch item := value.(type) {
		case string:
			return e.expand(item)
		case []any:
			for i := range item {
				item[i] = walk(item[i])
			}
		case map[string]any:
			for key := range item {
				item[key] = walk(item[key])
			}
		}
		return value
	}
	return json.MarshalIndent(walk(value), "", "  ")
}
