// Package concurrency checks that flagd-minimal answers correctly while OFREP, flag evaluation and flag sync clients hit
// it at once and its flags change (NFR-017). The flags of generation g and their expected answers are defined here and,
// identically, in k6/check.js; testdata/fixture.json is the shared fixture.
package concurrency

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Sets are the flag sets of every generation. Keys are prefixed with the set, because the sync payload carries no flag
// set and is a map by key.
var Sets = []string{"a", "b"}

// PerKind is the number of flags of each kind in a set; a set holds 5*PerKind+1 flags, plus a-odd-only in odd generations.
const PerKind = 10

// OddOnly is present only in odd generations.
const OddOnly = "a-odd-only"

// Answer is what a flag evaluates to in one generation. Found is false for a flag the generation does not hold.
type Answer struct {
	Found   bool   `json:"found"`
	Variant string `json:"variant,omitempty"`
	Value   any    `json:"value,omitempty"`
}

// Kinds name the flag types, in the order Keys lists them.
var Kinds = []string{"gen", "str", "bool", "flt", "tgt", "obj"}

// Keys returns the keys of one set that exist in every generation.
func Keys(set string) []string {
	keys := []string{set + "-gen"}
	for _, kind := range Kinds[1:] {
		for i := 0; i < PerKind; i++ {
			keys = append(keys, fmt.Sprintf("%s-%s-%d", set, kind, i))
		}
	}
	return keys
}

// Expected returns the answer of key in generation g for a request with targetingKey.
func Expected(key string, g int, targetingKey string) Answer {
	if key == OddOnly {
		if g%2 == 0 {
			return Answer{}
		}
		return Answer{Found: true, Variant: "on", Value: true}
	}
	parts := strings.Split(key, "-")
	if len(parts) < 2 || (parts[0] != "a" && parts[0] != "b") {
		return Answer{}
	}
	i := 0
	if len(parts) == 3 {
		n, err := strconv.Atoi(parts[2])
		if err != nil || n < 0 || n >= PerKind {
			return Answer{}
		}
		i = n
	}
	switch parts[1] {
	case "gen":
		if len(parts) != 2 {
			return Answer{}
		}
		return Answer{Found: true, Variant: "v", Value: float64(g)}
	case "str":
		return Answer{Found: true, Variant: "v", Value: fmt.Sprintf("g%d-%d", g, i)}
	case "bool":
		if (g+i)%2 == 1 {
			return Answer{Found: true, Variant: "on", Value: true}
		}
		return Answer{Found: true, Variant: "off", Value: false}
	case "flt":
		return Answer{Found: true, Variant: "v", Value: float64(g*1000 + i)}
	case "tgt":
		if strings.HasSuffix(targetingKey, strconv.Itoa(g%10)) {
			return Answer{Found: true, Variant: "hit", Value: "hit"}
		}
		return Answer{Found: true, Variant: "miss", Value: "miss"}
	case "obj":
		return Answer{Found: true, Variant: "v", Value: map[string]any{"gen": float64(g), "i": float64(i)}}
	}
	return Answer{}
}

type flag struct {
	State          string         `json:"state"`
	Variants       map[string]any `json:"variants"`
	DefaultVariant string         `json:"defaultVariant"`
	Targeting      any            `json:"targeting,omitempty"`
	Metadata       map[string]any `json:"metadata"`
}

// Document returns the flag file of generation g.
func Document(g int) []byte {
	flags := map[string]flag{}
	for _, set := range Sets {
		meta := map[string]any{"flagSetId": set}
		for _, key := range Keys(set) {
			a := Expected(key, g, "")
			f := flag{State: "ENABLED", Metadata: meta, DefaultVariant: a.Variant}
			if strings.Contains(key, "-tgt-") {
				f.Variants = map[string]any{"hit": "hit", "miss": "miss"}
				f.DefaultVariant = "miss"
				f.Targeting = map[string]any{"if": []any{
					map[string]any{"ends_with": []any{map[string]any{"var": "targetingKey"}, strconv.Itoa(g % 10)}},
					"hit", "miss"}}
			} else if strings.Contains(key, "-bool-") {
				f.Variants = map[string]any{"on": true, "off": false}
			} else {
				f.Variants = map[string]any{"v": a.Value}
			}
			flags[key] = f
		}
	}
	if g%2 == 1 {
		flags[OddOnly] = flag{State: "ENABLED", Variants: map[string]any{"on": true, "off": false}, DefaultVariant: "on",
			Metadata: map[string]any{"flagSetId": "a"}}
	}
	doc, err := json.MarshalIndent(map[string]any{"flags": flags}, "", "  ")
	if err != nil {
		panic(err)
	}
	return doc
}

// Generation reads the generation out of a sync payload (or a flag file) from the a-gen flag.
func Generation(payload []byte) (int, error) {
	var doc struct {
		Flags map[string]struct {
			Variants map[string]any `json:"variants"`
		} `json:"flags"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return 0, err
	}
	f, ok := doc.Flags["a-gen"]
	if !ok {
		return 0, fmt.Errorf("payload has no a-gen flag")
	}
	v, ok := f.Variants["v"].(float64)
	if !ok {
		return 0, fmt.Errorf("a-gen has no numeric variant v")
	}
	return int(v), nil
}
