package telemetry

import "math"

// ValueType is the type of an attribute value.
type ValueType uint8

const (
	// Invalid is the type of the zero Value.
	Invalid ValueType = iota
	StringType
	Int64Type
	Float64Type
	BoolType
)

// Value is an attribute value. It is a concrete struct rather than an interface, so building an attribute never
// boxes the value.
type Value struct {
	typ ValueType
	// num holds an int64, the bits of a float64, or 1 for true.
	num uint64
	str string
}

// Type returns the type of the value.
func (v Value) Type() ValueType { return v.typ }

// AsString returns the value of a StringType value, "" otherwise.
func (v Value) AsString() string { return v.str }

// AsInt64 returns the value of an Int64Type value, 0 otherwise.
func (v Value) AsInt64() int64 {
	if v.typ != Int64Type {
		return 0
	}
	return int64(v.num)
}

// AsFloat64 returns the value of a Float64Type value, 0 otherwise.
func (v Value) AsFloat64() float64 {
	if v.typ != Float64Type {
		return 0
	}
	return math.Float64frombits(v.num)
}

// AsBool returns the value of a BoolType value, false otherwise.
func (v Value) AsBool() bool { return v.typ == BoolType && v.num == 1 }

// Attribute is a key-value pair attached to a span or a measurement.
type Attribute struct {
	Key   string
	Value Value
}

// String returns a string attribute.
func String(key, value string) Attribute {
	return Attribute{Key: key, Value: Value{typ: StringType, str: value}}
}

// Int64 returns an integer attribute.
func Int64(key string, value int64) Attribute {
	return Attribute{Key: key, Value: Value{typ: Int64Type, num: uint64(value)}}
}

// Int returns an integer attribute.
func Int(key string, value int) Attribute {
	return Int64(key, int64(value))
}

// Float64 returns a floating point attribute.
func Float64(key string, value float64) Attribute {
	return Attribute{Key: key, Value: Value{typ: Float64Type, num: math.Float64bits(value)}}
}

// Bool returns a boolean attribute.
func Bool(key string, value bool) Attribute {
	var num uint64
	if value {
		num = 1
	}
	return Attribute{Key: key, Value: Value{typ: BoolType, num: num}}
}
