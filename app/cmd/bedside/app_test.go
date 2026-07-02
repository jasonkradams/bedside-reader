package main

import "testing"

func TestNextScreenTimeout(t *testing.T) {
	cases := []struct {
		current int
		want    int
	}{
		{0, 1},
		{1, 5},
		{5, 15},
		{15, 60},
		{60, 0},
		{3, 5},   // unknown value resets to a sane default
		{100, 5}, // unknown value resets to a sane default
	}
	for _, tc := range cases {
		if got := nextScreenTimeout(tc.current); got != tc.want {
			t.Errorf("nextScreenTimeout(%d) = %d, want %d", tc.current, got, tc.want)
		}
	}
}
