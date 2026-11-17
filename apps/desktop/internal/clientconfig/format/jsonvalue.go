package format

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// JSON values are nil, bool, Number, string, []any, and map[string]any, as
// serde_json reads them: Encode writes them the way serde_json does, objects
// by sorted keys, so fingerprints of values match the Tauri shell's.

// Number is a JSON number in the form serde_json writes it.
type Number string

// ParseNumber reads a strict JSON number the way serde_json does; false
// for what serde_json rejects.
func ParseNumber(text string) (Number, bool) {
	if !isStrictJSONNumber(text) {
		return "", false
	}
	if !strings.ContainsAny(text, ".eE") {
		if strings.HasPrefix(text, "-") {
			// serde_json reads -0 as a float.
			if n, err := strconv.ParseInt(text, 10, 64); err == nil && n != 0 {
				return Number(strconv.FormatInt(n, 10)), true
			}
		} else if n, err := strconv.ParseUint(text, 10, 64); err == nil {
			return Number(strconv.FormatUint(n, 10)), true
		}
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(f, 0) {
		return "", false
	}
	return Number(FormatFloat(f)), true
}

func isStrictJSONNumber(text string) bool {
	i := 0
	if i < len(text) && text[i] == '-' {
		i++
	}
	switch {
	case i < len(text) && text[i] == '0':
		i++
	case i < len(text) && text[i] >= '1' && text[i] <= '9':
		for i < len(text) && isASCIIDigit(text[i]) {
			i++
		}
	default:
		return false
	}
	if i < len(text) && text[i] == '.' {
		i++
		if i >= len(text) || !isASCIIDigit(text[i]) {
			return false
		}
		for i < len(text) && isASCIIDigit(text[i]) {
			i++
		}
	}
	if i < len(text) && (text[i] == 'e' || text[i] == 'E') {
		i++
		if i < len(text) && (text[i] == '+' || text[i] == '-') {
			i++
		}
		if i >= len(text) || !isASCIIDigit(text[i]) {
			return false
		}
		for i < len(text) && isASCIIDigit(text[i]) {
			i++
		}
	}
	return i == len(text)
}

// FormatFloat writes f as the ryu crate does, which serde_json uses.
func FormatFloat(f float64) string {
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	// d.ddde±x: the shortest digits that read back as f.
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, exponent, _ := strings.Cut(e, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exp, _ := strconv.Atoi(exponent)
	length := len(digits)
	k := exp - (length - 1)
	kk := length + k
	switch {
	case k >= 0 && kk <= 16:
		return sign + digits + strings.Repeat("0", k) + ".0"
	case kk > 0 && kk <= 16:
		return sign + digits[:kk] + "." + digits[kk:]
	case kk > -5 && kk <= 0:
		return sign + "0." + strings.Repeat("0", -kk) + digits
	case length == 1:
		return sign + digits + "e" + strconv.Itoa(kk-1)
	}
	return sign + digits[:1] + "." + digits[1:] + "e" + strconv.Itoa(kk-1)
}

// DecodeJSON reads strict JSON into a value.
func DecodeJSON(text string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("trailing characters")
	}
	return normalize(value), nil
}

// normalize turns json.Number into Number throughout a decoded value.
func normalize(value any) any {
	switch v := value.(type) {
	case json.Number:
		if n, ok := ParseNumber(string(v)); ok {
			return n
		}
		return Number(v)
	case []any:
		for i := range v {
			v[i] = normalize(v[i])
		}
	case map[string]any:
		for key, item := range v {
			v[key] = normalize(item)
		}
	}
	return value
}

// Object is a JSON object whose members keep their order, as a struct
// serde serializes.
type Object []Member

// Member is one member of an Object.
type Member struct {
	Key   string
	Value any
}

// Encode writes value as compact JSON, as serde_json's to_string does.
func Encode(value any) string {
	var b strings.Builder
	encodeTo(&b, value)
	return b.String()
}

func encodeTo(b *strings.Builder, value any) {
	switch v := value.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case Number:
		b.WriteString(string(v))
	case string:
		encodeString(b, v)
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeTo(b, item)
		}
		b.WriteByte(']')
	case map[string]any:
		b.WriteByte('{')
		for i, key := range SortedKeys(v) {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeString(b, key)
			b.WriteByte(':')
			encodeTo(b, v[key])
		}
		b.WriteByte('}')
	case Object:
		b.WriteByte('{')
		for i, member := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeString(b, member.Key)
			b.WriteByte(':')
			encodeTo(b, member.Value)
		}
		b.WriteByte('}')
	default:
		panic(fmt.Sprintf("format: %T is not a JSON value", value))
	}
}

func encodeString(b *strings.Builder, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xF])
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}

// EncodePretty writes value as serde_json's to_string_pretty does.
func EncodePretty(value any) string {
	var b bytes.Buffer
	encodePretty(&b, value, "")
	return b.String()
}

func encodePretty(b *bytes.Buffer, value any, indent string) {
	switch v := value.(type) {
	case []any:
		if len(v) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, item := range v {
			b.WriteString(indent + "  ")
			encodePretty(b, item, indent+"  ")
			if i+1 < len(v) {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "]")
	case map[string]any:
		if len(v) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		keys := SortedKeys(v)
		for i, key := range keys {
			b.WriteString(indent + "  " + Encode(key) + ": ")
			encodePretty(b, v[key], indent+"  ")
			if i+1 < len(keys) {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "}")
	case Object:
		if len(v) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, member := range v {
			b.WriteString(indent + "  " + Encode(member.Key) + ": ")
			encodePretty(b, member.Value, indent+"  ")
			if i+1 < len(v) {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "}")
	default:
		b.WriteString(Encode(value))
	}
}

// SortedKeys lists an object's keys in serde_json's order.
func SortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Rank orders the properties of objects AstrLink writes.
type Rank func(name string) int

// NoRank leaves every property at the same rank, so they go by name.
func NoRank(string) int { return math.MaxInt }

// cstInputOf is value as CST input, each object's properties in rank
// order, then by name.
func cstInputOf(value any, rank Rank) cstInput {
	switch v := value.(type) {
	case nil:
		return cstInput{kind: inputNull}
	case bool:
		return cstInput{kind: inputBool, boolean: v}
	case Number:
		return cstInput{kind: inputNumber, text: string(v)}
	case string:
		return cstInput{kind: inputString, text: v}
	case []any:
		items := make([]cstInput, len(v))
		for i, item := range v {
			items[i] = cstInputOf(item, rank)
		}
		return cstInput{kind: inputArray, items: items}
	case map[string]any:
		keys := SortedKeys(v)
		slices.SortStableFunc(keys, func(a, b string) int {
			ra, rb := rank(a), rank(b)
			switch {
			case ra < rb:
				return -1
			case ra > rb:
				return 1
			}
			return strings.Compare(a, b)
		})
		props := make([]cstInputProp, len(keys))
		for i, key := range keys {
			props[i] = cstInputProp{name: key, value: cstInputOf(v[key], rank)}
		}
		return cstInput{kind: inputObject, props: props}
	}
	panic(fmt.Sprintf("format: %T is not a JSON value", value))
}

// cstValue is the value of a node of a file a client reads; false when it
// does not read as JSON.
func cstValue(node *cstNode) (any, bool) {
	switch node.kind {
	case cstObject:
		object := map[string]any{}
		for _, prop := range node.properties() {
			name, ok := prop.decodedName()
			if !ok {
				return nil, false
			}
			valueNode := prop.propValue()
			if valueNode == nil {
				return nil, false
			}
			value, ok := cstValue(valueNode)
			if !ok {
				return nil, false
			}
			object[name] = value
		}
		return object, true
	case cstArray:
		items := []any{}
		for _, element := range node.elements() {
			value, ok := cstValue(element)
			if !ok {
				return nil, false
			}
			items = append(items, value)
		}
		return items, true
	case cstString:
		return node.decoded()
	case cstBool:
		return node.text == "true", true
	case cstNumber:
		n, ok := ParseNumber(node.text)
		if !ok {
			return nil, false
		}
		return n, true
	case cstNull:
		return nil, true
	}
	return nil, false
}
