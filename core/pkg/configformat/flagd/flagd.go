// Package flagd is flagd's configuration format: the JSON flag configuration with its schema,
// `$evaluators` references, flag sets and default variants. It imports no evaluator.
package flagd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	schema "github.com/open-feature/flagd-schemas/json"
	"github.com/open-feature/flagd/core/pkg/configformat"
	"github.com/open-feature/flagd/core/pkg/logger"
	"github.com/open-feature/flagd/core/pkg/model"
	"github.com/open-feature/flagd/core/pkg/store"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.uber.org/zap"
)

// Type is the name of flagd's configuration format.
const Type = configformat.DefaultType

// maxSafeInteger is 2^53-1, the largest integer exactly representable as a float64
const maxSafeInteger = 1<<53 - 1

// Definition is a validated flag configuration.
type Definition struct {
	Flags    []model.Flag
	Metadata map[string]any
}

type evaluators struct {
	Evaluators map[string]json.RawMessage `json:"$evaluators"`
}

// Validator checks flag configurations without touching a store.
type Validator struct {
	logger *logger.Logger
	schema *jsonschema.Schema
}

// NewValidator compiles flagd's JSON schema. Violations of the schema are logged as warnings and do
// not reject a configuration.
func NewValidator(log *logger.Logger) *Validator {
	compiler := jsonschema.NewCompiler()
	for url, data := range map[string]string{
		"https://flagd.dev/schema/v0/flagd.json":     schema.FlagdSchema,
		"https://flagd.dev/schema/v0/flags.json":     schema.FlagSchema,
		"https://flagd.dev/schema/v0/targeting.json": schema.TargetingSchema,
	} {
		if err := addSchemaResource(compiler, url, data); err != nil {
			log.Warn("Failed to add schema resource", zap.Error(err))
		}
	}
	compiled, err := compiler.Compile("https://flagd.dev/schema/v0/flagd.json")
	if err != nil {
		log.Fatal("Failed to compile schema", zap.Error(err))
	}
	return &Validator{logger: log, schema: compiled}
}

func addSchemaResource(compiler *jsonschema.Compiler, url string, schemaData string) error {
	unmarshalJSON, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaData))
	if err != nil {
		return err
	}
	return compiler.AddResource(url, unmarshalJSON)
}

// Validate parses a flag configuration and rejects it for: unparsable JSON, a broken `$evaluators`,
// metadata that is not an object, unparsable `flags`, or a default variant that is not one of the
// flag's variants. Flags are checked in key order for map form and in array order for array form.
// A rejection carries the text of the underlying error as its reason.
func (v *Validator) Validate(in configformat.Input) (Definition, error) {
	var definition Definition

	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(in.Data))
	if err != nil {
		return definition, reject(in.Source, "document", fmt.Errorf("failed to unmarshal JSON string: %v", err))
	}
	if err := v.schema.Validate(inst); err != nil {
		v.logger.Warn(fmt.Sprintf(
			"flag definition does not conform to the schema; validation errors: %s", err),
		)
	}

	type jsonRawDef struct {
		Flags    json.RawMessage `json:"flags"`
		Metadata map[string]any  `json:"metadata"`
	}

	// Transpose evaluators and unmarshal directly into the definition
	transposedConfig, err := transposeEvaluators(in.Data)
	if err != nil {
		return definition, reject(in.Source, "$evaluators", fmt.Errorf("transposing evaluators: %w", err))
	}

	var rawDef jsonRawDef
	if err := json.Unmarshal([]byte(transposedConfig), &rawDef); err != nil {
		return definition, reject(in.Source, "metadata", fmt.Errorf("unmarshalling provided configurations: %w", err))
	}
	definition.Metadata = rawDef.Metadata
	definition.Flags = []model.Flag{}

	// Check if flags is an array or object
	trimmed := strings.TrimSpace(string(rawDef.Flags))
	if strings.HasPrefix(trimmed, "[") {
		var flags []model.Flag
		if err := json.Unmarshal(rawDef.Flags, &flags); err != nil {
			return definition, reject(in.Source, "flags", fmt.Errorf("unmarshalling flags array: %w", err))
		}
		if flags != nil {
			definition.Flags = flags
		}
	} else {
		var flagsMap map[string]model.Flag
		if err := json.Unmarshal(rawDef.Flags, &flagsMap); err != nil {
			return definition, reject(in.Source, "flags", fmt.Errorf("unmarshalling flags map: %w", err))
		}
		for _, k := range sortedKeys(flagsMap) {
			f := flagsMap[k]
			f.Key = k
			definition.Flags = append(definition.Flags, f)
		}
	}
	v.warnUnsafeNumericVariants(definition)
	if err := validateDefaultVariants(definition); err != nil {
		return definition, reject(in.Source, defaultVariantFlag(definition), err)
	}
	return definition, nil
}

func reject(source, entry string, err error) error {
	return configformat.Rejection{Source: source, Entry: entry, Reason: err.Error()}
}

// warnUnsafeNumericVariants warns about numeric variants outside the safe range
func (v *Validator) warnUnsafeNumericVariants(definition Definition) {
	for _, flag := range definition.Flags {
		for name, variant := range flag.Variants {
			if f, ok := variant.(float64); ok && math.Abs(f) > maxSafeInteger {
				// TODO(#2101): update message
				v.logger.Warn(fmt.Sprintf(
					"variant '%s' of flag '%s' is outside [-(2^53-1), 2^53-1]; it may be inaccurate "+
						"and is rejected in strict numeric range mode (the default in future versions)", name, flag.Key))
			}
		}
	}
}

// validateDefaultVariants returns an error if any of the default variants aren't valid
func validateDefaultVariants(definition Definition) error {
	for _, flag := range definition.Flags {
		// Default Variant is not provided in the config
		if flag.DefaultVariant == "" {
			continue
		}

		if _, ok := flag.Variants[flag.DefaultVariant]; !ok {
			return fmt.Errorf(
				"default variant: '%s' isn't a valid variant of flag: '%s'", flag.DefaultVariant, flag.Key,
			)
		}
	}

	return nil
}

// defaultVariantFlag names the first flag whose default variant is not one of its variants.
func defaultVariantFlag(definition Definition) string {
	for _, flag := range definition.Flags {
		if flag.DefaultVariant == "" {
			continue
		}
		if _, ok := flag.Variants[flag.DefaultVariant]; !ok {
			return flag.Key
		}
	}
	return ""
}

func transposeEvaluators(state string) (string, error) {
	var ev evaluators
	if err := json.Unmarshal([]byte(state), &ev); err != nil {
		return "", fmt.Errorf("unmarshal: %w", err)
	}

	// round-trip to normalize whitespace so we can use plain string matching
	var raw interface{}
	if err := json.Unmarshal([]byte(state), &raw); err != nil {
		return "", fmt.Errorf("normalize: %w", err)
	}
	normalizedBytes, err := json.Marshal(raw)
	if err != nil {
		return "", fmt.Errorf("normalize marshal: %w", err)
	}
	result := string(normalizedBytes)

	for evalName, evalRaw := range ev.Evaluators {
		marshalledEval, err := evalRaw.MarshalJSON()
		if err != nil {
			return "", fmt.Errorf("marshal evaluator: %w", err)
		}

		evalValue := string(marshalledEval)
		if len(evalValue) < 3 {
			return "", errors.New("evaluator object is empty")
		}

		refPattern := `{"$ref":"` + evalName + `"}`
		result = strings.ReplaceAll(result, refPattern, evalValue)
	}

	return result, nil
}

// Format is flagd's configuration format on a store.
type Format struct {
	*Validator
	store *store.Store
}

// NewFormat creates flagd's configuration format on st.
func NewFormat(st *store.Store, log *logger.Logger) *Format {
	return NewValidator(log).On(st)
}

// On creates flagd's configuration format on st, sharing the compiled schema.
func (v *Validator) On(st *store.Store) *Format {
	return &Format{Validator: v, store: st}
}

var _ configformat.ConfigurationFormat = (*Format)(nil)

// Type implements configformat.ConfigurationFormat.
func (*Format) Type() string { return Type }

// Accept validates the delivery and applies its flags and one document per flag set. A document's
// version is a SHA-256 over the sorted fingerprints of its flags.
func (f *Format) Accept(_ context.Context, in configformat.Input) (configformat.Result, error) {
	definition, err := f.Validate(in)
	if err != nil {
		return configformat.Result{}, err
	}

	bySet := map[string][]model.Flag{}
	if id, ok := definition.Metadata["flagSetId"].(string); ok {
		bySet[id] = nil
	}
	for _, flag := range definition.Flags {
		id := store.FlagSetID(flag, definition.Metadata)
		bySet[id] = append(bySet[id], flag)
	}

	previous := map[string]map[string]string{}
	for _, d := range f.store.Documents(Type, in.Source) {
		if fingerprints, ok := d.Value.(map[string]string); ok {
			previous[d.FlagSet] = fingerprints
		}
	}

	var result configformat.Result
	var documents []store.Document
	for _, set := range sortedKeys(bySet) {
		fingerprints := make(map[string]string, len(bySet[set]))
		for _, flag := range bySet[set] {
			fingerprints[flag.Key] = fingerprint(flag)
		}
		report := configformat.DocumentReport{FlagSet: set, FlagKeys: sortedKeys(fingerprints)}
		h := sha256.New()
		for _, key := range report.FlagKeys {
			fmt.Fprintf(h, "%s=%s;", key, fingerprints[key])
			if old, ok := previous[set][key]; !ok {
				report.Added = append(report.Added, key)
			} else if old != fingerprints[key] {
				report.Updated = append(report.Updated, key)
			}
		}
		for _, key := range sortedKeys(previous[set]) {
			if _, ok := fingerprints[key]; !ok {
				report.Removed = append(report.Removed, key)
			}
		}
		report.Version = hex.EncodeToString(h.Sum(nil))

		result.Documents = append(result.Documents, report)
		documents = append(documents, store.Document{
			Format: Type, Source: in.Source, FlagSet: set, Version: report.Version,
			FlagKeys: slices.Clone(report.FlagKeys), Value: fingerprints,
		})
	}

	err = f.store.Apply(store.ApplyRequest{
		Format: Type, Source: in.Source, Flags: definition.Flags, SetMetadata: definition.Metadata,
		Documents: documents, Incremental: in.IncrementalUpdate,
	})
	if err != nil {
		return configformat.Result{}, err
	}
	return result, nil
}

func fingerprint(flag model.Flag) string {
	b, err := json.Marshal(flag)
	if err != nil {
		// a flag that was just unmarshalled from JSON marshals back
		panic(fmt.Sprintf("unable to marshal flag %q: %v", flag.Key, err))
	}
	sum := sha256.Sum256(append([]byte(flag.Key+"\x00"), b...))
	return hex.EncodeToString(sum[:])
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Factory creates flagd's configuration format.
type Factory struct{}

var _ configformat.Factory = Factory{}

// Type implements configformat.Factory.
func (Factory) Type() string { return Type }

// Create implements configformat.Factory.
func (Factory) Create(s configformat.Settings) (configformat.ConfigurationFormat, error) {
	if s.Store == nil || s.Logger == nil {
		return nil, errors.New("flagd's configuration format needs a store and a logger")
	}
	return NewFormat(s.Store, s.Logger), nil
}
