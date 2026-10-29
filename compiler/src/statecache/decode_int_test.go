package statecache

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"
)

func TestDecodeSignedIntegers(t *testing.T) {
	type namedInt int
	for _, n := range []int{math.MinInt, -1, 0, 1, math.MaxInt} {
		encoded := uint64(int64(n)<<1) ^ uint64(int64(n)>>63)
		d := decoder{data: binary.AppendUvarint(nil, encoded)}
		var got namedInt
		if err := d.value(reflect.ValueOf(&got).Elem()); err != nil || int(got) != n {
			t.Fatalf("decode %d: got %d, error %v", n, got, err)
		}
	}
	// Smaller signed types must still reject out-of-range encoded values.
	d := decoder{data: binary.AppendUvarint(nil, 256)} // 128 in zigzag encoding
	var small int8
	if err := d.value(reflect.ValueOf(&small).Elem()); err == nil {
		t.Fatal("accepted out-of-range int8")
	}
}
