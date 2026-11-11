package semantic

import "strconv"

type resolvedTypeNamePathPart struct {
	fieldOrKey string
	index      int
}

const (
	resolvedTypeNamePathField  = -1
	resolvedTypeNamePathMapKey = -2
)

func appendResolvedTypeNamePath(dst []byte, path []resolvedTypeNamePathPart) []byte {
	dst = append(dst, '$')
	for _, part := range path {
		switch part.index {
		case resolvedTypeNamePathField:
			dst = append(dst, part.fieldOrKey...)
		case resolvedTypeNamePathMapKey:
			dst = append(strconv.AppendQuote(append(dst, '['), part.fieldOrKey), ']')
		default:
			dst = append(strconv.AppendInt(append(dst, '['), int64(part.index), 10), ']')
		}
	}
	return dst
}
