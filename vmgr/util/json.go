package util

import (
	"encoding/json"
	"fmt"
	"strings"
)

// deep merges 'overlay' onto 'base':
// - in maps, keys are merged
// - all other values (including slices) are replaced
func DeepMergeJSON(base map[string]any, overlay map[string]any) map[string]any {
	for k, v := range overlay {
		// if overlay is map,
		if vMap, ok := v.(map[string]any); ok {
			// and base exists and is map,
			if baseV, ok := base[k]; ok {
				if baseVMap, ok := baseV.(map[string]any); ok {
					// then recurse
					base[k] = DeepMergeJSON(baseVMap, vMap)
				} else {
					base[k] = v
				}
			} else {
				base[k] = v
			}
		} else {
			base[k] = v
		}
	}
	return base
}

func DeepMergeJSONBytes(base []byte, overlay []byte) ([]byte, error) {
	var baseMap, overlayMap map[string]any

	// empty = {}
	// accept "\n", etc. in case user saves an empty file using code editor
	if strings.TrimSpace(string(base)) != "" {
		if err := json.Unmarshal(base, &baseMap); err != nil {
			return nil, fmt.Errorf("unmarshal base json: %w", err)
		}
	}

	if strings.TrimSpace(string(overlay)) != "" {
		if err := json.Unmarshal(overlay, &overlayMap); err != nil {
			return nil, fmt.Errorf("unmarshal overlay json: %w", err)
		}
	}

	return json.Marshal(DeepMergeJSON(baseMap, overlayMap))
}
