package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The combined flag document is the launchpad's allFlags.json: every flag file of the raw flags directory except the
// selector files, deep-merged into one map and encoded with two-space indentation. The merge and the encoder are the
// launchpad's, so the bytes are the ones the launchpad would have written. The map is decoded into interface{}, so a
// number is a float64 and is encoded the way a float64 is (10.0 becomes 10).

// combine reads the flag files of the directory and merges them, in the order of their names.
func combine(dir string) (map[string]any, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read input directory: %v", err)
	}

	combined := make(map[string]any)
	for _, file := range files {
		if filepath.Ext(file.Name()) != ".json" || strings.HasPrefix(file.Name(), "selector-") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %v", file.Name(), err)
		}
		var data map[string]any
		if err := json.Unmarshal(content, &data); err != nil {
			return nil, fmt.Errorf("failed to parse JSON file %s: %v", file.Name(), err)
		}
		combined = deepMerge(combined, data)
	}
	return combined, nil
}

func deepMerge(dst, src map[string]any) map[string]any {
	for key, srcValue := range src {
		if dstValue, exists := dst[key]; exists {
			if srcMap, ok := srcValue.(map[string]any); ok {
				if dstMap, ok := dstValue.(map[string]any); ok {
					dst[key] = deepMerge(dstMap, srcMap)
					continue
				}
			}
		}
		dst[key] = srcValue
	}
	return dst
}

// encode returns the document bytes of the combined flags and their version, the digest of the bytes.
func encode(combined map[string]any) (document []byte, version string, err error) {
	document, err = json.MarshalIndent(combined, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("failed to serialize combined JSON: %v", err)
	}
	sum := sha256.Sum256(document)
	return document, hex.EncodeToString(sum[:]), nil
}

// toggle switches the default variant of the flag between the two variants, as the launchpad does: when it is the
// first one it becomes the second, anything else becomes the first. It returns the new default variant.
func toggle(combined map[string]any, flag string, variants [2]string) (string, error) {
	flags, _ := combined["flags"].(map[string]any)
	entry, exists := flags[flag].(map[string]any)
	if !exists {
		return "", errors.New(flag + " not found in configuration")
	}
	next := variants[0]
	if entry["defaultVariant"] == variants[0] {
		next = variants[1]
	}
	entry["defaultVariant"] = next
	return next, nil
}
