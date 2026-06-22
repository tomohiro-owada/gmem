package gmem

import "testing"

func TestIsHexCommitPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{in: "e3b183b", want: true},
		{in: "3e7b377f9f2046414f03406bd9eba55456a61182", want: true},
		{in: "git:", want: false},
		{in: "xcrun_db-aHiSkmy1", want: false},
		{in: "abc", want: false},
		{in: "abc123z", want: false},
	}
	for _, tc := range cases {
		if got := isHexCommitPrefix(tc.in); got != tc.want {
			t.Fatalf("isHexCommitPrefix(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
