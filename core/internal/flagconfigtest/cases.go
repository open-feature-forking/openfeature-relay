package flagconfigtest

import (
	"fmt"

	"github.com/open-feature/flagd/core/pkg/sync"
)

const (
	hi = "hi"
	lo = "lo"
)

// later sources win: a source's priority is its index in the list
var bothSources = []string{lo, hi}

// flag renders the members of a valid flag definition; extra is appended as further members.
func flag(extra string) string {
	return `"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}` + extra
}

func meta(m string) string { return `,"metadata":` + m }

func doc(setMeta, flags string) string {
	if setMeta != "" {
		return fmt.Sprintf(`{"metadata":%s,"flags":%s}`, setMeta, flags)
	}
	return fmt.Sprintf(`{"flags":%s}`, flags)
}

func del(source, data string, incremental bool) sync.DataSync {
	return sync.DataSync{Source: source, FlagData: data, IncrementalUpdates: incremental}
}

// r is the row of a valid flag with the default variants and no targeting.
func r(key, source, set string, prio int, metadata string) Row {
	return Row{
		Key: key, Source: source, FlagSetID: set, Priority: prio, State: "ENABLED", DefaultVariant: "on",
		Variants: `{"off":false,"on":true}`, Metadata: metadata,
	}
}

// Cases holds one faulty or notable entry per case.
var Cases = []Case{
	{
		Name:       "map form flags",
		Sources:    []string{hi},
		Deliveries: []sync.DataSync{del(hi, doc("", `{"f1":{`+flag("")+`},"f2":{`+flag("")+`}}`), false)},
		WantRows:   []Row{r("f1", hi, UnnamedSet, 0, "null"), r("f2", hi, UnnamedSet, 0, "null")},
	},
	{
		Name:       "array form flags",
		Sources:    []string{hi},
		Deliveries: []sync.DataSync{del(hi, doc("", `[{"key":"f1",`+flag("")+`},{"key":"f2",`+flag("")+`}]`), false)},
		WantRows:   []Row{r("f1", hi, UnnamedSet, 0, "null"), r("f2", hi, UnnamedSet, 0, "null")},
	},
	{
		Name:    "evaluator reference is transposed",
		Sources: []string{hi},
		Deliveries: []sync.DataSync{del(hi,
			`{"$evaluators":{"e":{"if":[true,"on","off"]}},"flags":{"f1":{`+flag(`,"targeting":{"$ref":"e"}`)+`}}}`, false)},
		WantRows: []Row{{
			Key: "f1", Source: hi, FlagSetID: UnnamedSet, State: "ENABLED", DefaultVariant: "on",
			Variants: `{"off":false,"on":true}`, Targeting: `{"if":[true,"on","off"]}`, Metadata: "null",
		}},
	},
	{
		Name:       "empty evaluator is rejected",
		Sources:    []string{hi},
		Deliveries: []sync.DataSync{del(hi, `{"$evaluators":{"e":{}},"flags":{}}`, false)},
		WantErr:    "transposing evaluators: evaluator object is empty",
	},
	{
		Name:       "evaluators of the wrong type are rejected",
		Sources:    []string{hi},
		Deliveries: []sync.DataSync{del(hi, `{"$evaluators":"x","flags":{}}`, false)},
		WantErr:    "transposing evaluators: unmarshal: json: cannot unmarshal string into Go struct field Evaluators.$evaluators of type map[string]jsontext.Value",
	},
	{
		Name:       "unparsable json is rejected",
		Sources:    []string{hi},
		Deliveries: []sync.DataSync{del(hi, `{`, false)},
		WantErr:    "failed to unmarshal JSON string: unexpected EOF",
	},
	{
		Name:       "unparsable flags map is rejected",
		Sources:    []string{hi},
		Deliveries: []sync.DataSync{del(hi, `{"flags":5}`, false)},
		WantErr:    "unmarshalling flags map: json: cannot unmarshal number into Go value of type map[string]model.Flag",
	},
	{
		Name:       "unparsable flags array is rejected",
		Sources:    []string{hi},
		Deliveries: []sync.DataSync{del(hi, `{"flags":[{"key":5}]}`, false)},
		WantErr:    "unmarshalling flags array: json: cannot unmarshal number into Go struct field .0.key of type string",
	},
	{
		Name:    "missing default variant is rejected",
		Sources: []string{hi},
		Deliveries: []sync.DataSync{del(hi,
			doc("", `{"f1":{"state":"ENABLED","defaultVariant":"nope","variants":{"on":true}}}`), false)},
		WantErr: "default variant: 'nope' isn't a valid variant of flag: 'f1'",
	},
	{
		Name:    "schema violation is accepted",
		Sources: []string{hi},
		Deliveries: []sync.DataSync{del(hi,
			doc("", `{"f1":{"state":"BOGUS","defaultVariant":"on","variants":{"on":true,"off":false}}}`), false)},
		WantRows: []Row{{
			Key: "f1", Source: hi, FlagSetID: UnnamedSet, State: "BOGUS", DefaultVariant: "on",
			Variants: `{"off":false,"on":true}`, Metadata: "null",
		}},
	},
	{
		Name:    "set level flagSetId and metadata merge into flags",
		Sources: []string{hi},
		Deliveries: []sync.DataSync{del(hi,
			doc(`{"flagSetId":"s1","owner":"set"}`,
				`{"f1":{`+flag(meta(`{"owner":"flag","extra":1}`))+`},"f2":{`+flag("")+`}}`), false)},
		WantRows: []Row{
			r("f1", hi, "s1", 0, `{"extra":1,"flagSetId":"s1","owner":"flag"}`),
			r("f2", hi, "s1", 0, `{"flagSetId":"s1","owner":"set"}`),
		},
	},
	{
		Name:    "per flag flagSetId overrides the set",
		Sources: []string{hi},
		Deliveries: []sync.DataSync{del(hi,
			doc(`{"flagSetId":"s1"}`, `{"f1":{`+flag(meta(`{"flagSetId":"s2"}`))+`},"f2":{`+flag("")+`}}`), false)},
		WantRows: []Row{
			r("f2", hi, "s1", 0, `{"flagSetId":"s1"}`),
			r("f1", hi, "s2", 0, `{"flagSetId":"s2"}`),
		},
	},
	{
		Name:    "named and unnamed flags in one source",
		Sources: []string{hi},
		Deliveries: []sync.DataSync{del(hi,
			doc("", `{"f1":{`+flag(meta(`{"flagSetId":"s1"}`))+`},"f2":{`+flag("")+`}}`), false)},
		WantRows: []Row{
			r("f2", hi, UnnamedSet, 0, "null"),
			r("f1", hi, "s1", 0, `{"flagSetId":"s1"}`),
		},
	},
	{
		Name:    "full update replaces the source",
		Sources: []string{hi},
		Deliveries: []sync.DataSync{
			del(hi, doc("", `{"f1":{`+flag("")+`},"f2":{`+flag("")+`}}`), false),
			del(hi, doc("", `{"f2":{`+flag("")+`},"f3":{`+flag("")+`}}`), false),
		},
		WantRows: []Row{r("f2", hi, UnnamedSet, 0, "null"), r("f3", hi, UnnamedSet, 0, "null")},
	},
	{
		Name:    "flag removed from its source",
		Sources: []string{hi},
		Deliveries: []sync.DataSync{
			del(hi, doc("", `{"f1":{`+flag("")+`},"f2":{`+flag("")+`}}`), false),
			del(hi, doc("", `{"f1":{`+flag("")+`}}`), false),
		},
		WantRows: []Row{r("f1", hi, UnnamedSet, 0, "null")},
	},
	{
		Name:    "incremental update replaces only the named flag sets",
		Sources: []string{hi},
		Deliveries: []sync.DataSync{
			del(hi, doc(`{"flagSetId":"s1"}`, `{"f1":{`+flag("")+`}}`), true),
			del(hi, doc(`{"flagSetId":"s2"}`, `{"f2":{`+flag("")+`}}`), true),
			del(hi, doc(`{"flagSetId":"s1"}`, `{"f3":{`+flag("")+`}}`), true),
		},
		WantRows: []Row{
			r("f3", hi, "s1", 0, `{"flagSetId":"s1"}`),
			r("f2", hi, "s2", 0, `{"flagSetId":"s2"}`),
		},
	},
	{
		Name:    "lower priority duplicate is skipped",
		Sources: bothSources,
		Deliveries: []sync.DataSync{
			del(hi, doc("", `{"f1":{`+flag(meta(`{"from":"hi"}`))+`}}`), false),
			del(lo, doc("", `{"f1":{`+flag(meta(`{"from":"lo"}`))+`},"f2":{`+flag("")+`}}`), false),
		},
		WantRows: []Row{r("f1", hi, UnnamedSet, 1, `{"from":"hi"}`), r("f2", lo, UnnamedSet, 0, "null")},
	},
	{
		Name:    "higher priority duplicate overwrites",
		Sources: bothSources,
		Deliveries: []sync.DataSync{
			del(lo, doc("", `{"f1":{`+flag(meta(`{"from":"lo"}`))+`}}`), false),
			del(hi, doc("", `{"f1":{`+flag(meta(`{"from":"hi"}`))+`}}`), false),
		},
		WantRows: []Row{r("f1", hi, UnnamedSet, 1, `{"from":"hi"}`)},
	},
	{
		Name:    "higher priority source removing a shared key removes it for good",
		Sources: bothSources,
		Deliveries: []sync.DataSync{
			del(lo, doc("", `{"f1":{`+flag("")+`}}`), false),
			del(hi, doc("", `{"f1":{`+flag("")+`}}`), false),
			del(hi, doc("", `{}`), false),
		},
	},
	{
		Name:    "rejected update leaves the store unchanged",
		Sources: []string{hi},
		Deliveries: []sync.DataSync{
			del(hi, doc("", `{"f1":{`+flag("")+`}}`), false),
			del(hi, doc("", `{"f2":{"defaultVariant":"nope","variants":{"on":true}}}`), false),
		},
		WantErr:  "default variant: 'nope' isn't a valid variant of flag: 'f2'",
		WantRows: []Row{r("f1", hi, UnnamedSet, 0, "null")},
	},
}
