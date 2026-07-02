package library

import "testing"

func TestChapterIndexAt(t *testing.T) {
	chapters := []Chapter{
		{Title: "Ch1", StartTime: 0},
		{Title: "Ch2", StartTime: 100},
		{Title: "Ch3", StartTime: 200},
	}

	cases := []struct {
		name     string
		chapters []Chapter
		position float64
		want     int
	}{
		{"no chapters", nil, 42, -1},
		{"before first chapter", []Chapter{{StartTime: 10}}, 5, -1},
		{"first chapter", chapters, 50, 0},
		{"middle chapter", chapters, 150, 1},
		{"last chapter", chapters, 250, 2},
		{"within tolerance counts as next", chapters, 99.6, 1},
		{"outside tolerance stays previous", chapters, 99.4, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChapterIndexAt(tc.chapters, tc.position); got != tc.want {
				t.Errorf("ChapterIndexAt(_, %v) = %d, want %d", tc.position, got, tc.want)
			}
		})
	}
}
