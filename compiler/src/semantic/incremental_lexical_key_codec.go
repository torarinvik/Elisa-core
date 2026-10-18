package semantic

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"reflect"
	"unicode/utf8"

	"elisacore/src/incremental"
)

const lexicalKeyPrefix = "L2:"

// Manual encoding must fail closed if a future dependency field is added.
var lexicalKeySchemaValid = func() bool {
	for typ, fields := range map[reflect.Type][]string{
		reflect.TypeOf(lexicalSelectionSpec{}): {"Site:semantic.FunctionSourceAnchor", "Role:string", "Domain:string", "Name:string"},
		reflect.TypeOf(FunctionSourceAnchor{}): {"FunctionKey:string", "Tree:[32]uint8", "Ordinal:uint32", "Kind:string"},
	} {
		if typ.NumField() != len(fields) {
			return false
		}
		for i, expected := range fields {
			field := typ.Field(i)
			if field.Name+":"+field.Type.String() != expected {
				return false
			}
		}
	}
	return true
}()

func validateLexicalSelectionSpec(spec lexicalSelectionSpec) (int, error) {
	if !lexicalKeySchemaValid || spec.Site.FunctionKey == "" || spec.Site.Tree == ([32]byte{}) || spec.Site.Kind == "" || spec.Role == "" || spec.Name == "" {
		return 0, fmt.Errorf("lexical lookup requires anchored owner/site/role")
	}
	switch spec.Domain {
	case "symbol", "local-symbol", "type-param", "const-param", "shape-param", "assoc-type", "param-interface", "permission-param", "error-set-param", "region-param":
	default:
		return 0, fmt.Errorf("unsupported lexical lookup domain")
	}
	size := 0
	for _, text := range []string{spec.Site.FunctionKey, spec.Site.Kind, spec.Role, spec.Domain, spec.Name} {
		if !utf8.ValidString(text) || len(text) > lookupSelectionMaxBytes/6-size {
			return 0, fmt.Errorf("lexical key budget exceeded")
		}
		size += len(text)
	}
	return size, nil
}

// Exact length prefixes preserve every dependency field without hash collisions.
// Base64 keeps keys valid UTF-8 for transport/debug tooling. These keys contain
// no source positions or invocation-local pointers.
func lexicalSelectionKey(spec lexicalSelectionSpec) (incremental.Key, error) {
	size, err := validateLexicalSelectionSpec(spec)
	if err != nil {
		return incremental.Key{}, err
	}
	data := make([]byte, 0, 32+4+5*4+size)
	data = append(data, spec.Site.Tree[:]...)
	data = binary.LittleEndian.AppendUint32(data, spec.Site.Ordinal)
	for _, text := range []string{spec.Site.FunctionKey, spec.Site.Kind, spec.Role, spec.Domain, spec.Name} {
		data = binary.LittleEndian.AppendUint32(data, uint32(len(text)))
		data = append(data, text...)
	}
	encodedSize := len(lexicalKeyPrefix) + base64.RawURLEncoding.EncodedLen(len(data))
	if encodedSize > lookupSelectionMaxBytes {
		return incremental.Key{}, fmt.Errorf("lexical key exceeds encoding budget")
	}
	encoded := make([]byte, encodedSize)
	copy(encoded, lexicalKeyPrefix)
	base64.RawURLEncoding.Encode(encoded[len(lexicalKeyPrefix):], data)
	return incremental.Key{Kind: lexicalSelectionKind, Name: string(encoded)}, nil
}

func decodeLexicalSelectionKey(name string) (lexicalSelectionSpec, error) {
	var spec lexicalSelectionSpec
	if len(name) > lookupSelectionMaxBytes || len(name) < len(lexicalKeyPrefix) || name[:len(lexicalKeyPrefix)] != lexicalKeyPrefix {
		return spec, fmt.Errorf("invalid lexical key version or size")
	}
	encoded := name[len(lexicalKeyPrefix):]
	// DecodeString otherwise ignores CR/LF, even in strict mode. No alternate
	// spelling may identify a second query with the same semantic inputs.
	for i := range encoded {
		c := encoded[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return spec, fmt.Errorf("noncanonical lexical key")
		}
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) < 36 {
		return spec, fmt.Errorf("malformed lexical key")
	}
	copy(spec.Site.Tree[:], data[:32])
	spec.Site.Ordinal = binary.LittleEndian.Uint32(data[32:36])
	text := string(data)
	offset := 36
	for _, field := range []*string{&spec.Site.FunctionKey, &spec.Site.Kind, &spec.Role, &spec.Domain, &spec.Name} {
		if len(data)-offset < 4 {
			return lexicalSelectionSpec{}, fmt.Errorf("truncated lexical key")
		}
		size := uint64(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
		if size > uint64(len(data)-offset) {
			return lexicalSelectionSpec{}, fmt.Errorf("truncated lexical field")
		}
		*field = text[offset : offset+int(size)]
		offset += int(size)
	}
	if offset != len(data) {
		return lexicalSelectionSpec{}, fmt.Errorf("trailing lexical key data")
	}
	if _, err := validateLexicalSelectionSpec(spec); err != nil {
		return lexicalSelectionSpec{}, err
	}
	return spec, nil
}
