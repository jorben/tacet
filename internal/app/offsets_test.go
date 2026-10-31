package app

import (
	"reflect"
	"testing"
)

func TestUTF16OffsetsCountSurrogatePairs(t *testing.T) {
	// runes:   0 1 2(😀) 3 4        utf16: 0 1 2 4 5 6
	text := "ab😀cd"
	for _, tc := range []struct {
		in, want []int
	}{
		{[]int{0, 5}, []int{0, 6}},
		{[]int{3, 2}, []int{4, 2}}, // unsorted input keeps its order
		{[]int{2, 3}, []int{2, 4}},
		{[]int{9}, []int{6}}, // beyond the end clamps
	} {
		if got := utf16Offsets(text, tc.in...); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v = %v, want %v", tc.in, got, tc.want)
		}
	}
}
