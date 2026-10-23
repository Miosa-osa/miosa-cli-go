package commands

import (
	"reflect"
	"testing"
)

func TestNewLogLines(t *testing.T) {
	cases := []struct {
		prev, cur, want []string
	}{
		{nil, []string{"a"}, []string{"a"}},
		{[]string{"a", "b"}, []string{"a", "b"}, []string{}},
		{[]string{"a", "b"}, []string{"a", "b", "c"}, []string{"c"}},
		{[]string{"a", "b", "c"}, []string{"b", "c", "d"}, []string{"d"}},
		{[]string{"x", "x"}, []string{"x", "x", "x"}, []string{"x"}},
		{[]string{"a"}, []string{"z", "y"}, []string{"z", "y"}},
	}
	for _, c := range cases {
		got := newLogLines(c.prev, c.cur)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("newLogLines(%v, %v) = %v, want %v", c.prev, c.cur, got, c.want)
		}
	}
}
