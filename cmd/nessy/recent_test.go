//go:build nessy

package main

import "testing"

func TestParseRecentSlot(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"1", 1, true},
		{"5", 5, true},
		{"42", 42, true},
		{"0", 0, false},                   // 0 not a valid slot
		{"", 0, false},                    // empty
		{"foo.nes", 0, false},             // has letters
		{"./game.nes", 0, false},          // path
		{"100", 0, false},                 // 3-digit not allowed
		{"-1", 0, false},                  // signed
		{"/home/user/game.nes", 0, false}, // absolute path
	} {
		got, ok := parseRecentSlot(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseRecentSlot(%q) = (%d, %v); want (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseButton(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"A", true},
		{"a", true},
		{"select", true},
		{"Select", true},
		{"S E L E C T", true}, // whitespace stripped
		{"up", true},
		{"start", true},
		{"foo", false},
	} {
		_, ok := parseButton(tc.in)
		if ok != tc.ok {
			t.Errorf("parseButton(%q): ok = %v; want %v", tc.in, ok, tc.ok)
		}
	}
}

func TestNormalize(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"A", "a"},
		{"Arrow Up", "arrowup"},
		{"arrow-up", "arrowup"},
		{"  Z  ", "z"},
	} {
		if got := normalize(tc.in); got != tc.want {
			t.Errorf("normalize(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
