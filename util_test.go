package main

import (
	"testing"
	"time"
)

func TestRelTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "unknown"},
		{now.Add(-30 * time.Second).UnixMilli(), "just now"},
		{now.Add(-5 * time.Minute).UnixMilli(), "5m ago"},
		{now.Add(-3 * time.Hour).UnixMilli(), "3h ago"},
		{now.Add(-50 * time.Hour).UnixMilli(), "2d ago"},
	}
	for _, c := range cases {
		if got := relTime(c.ms); got != c.want {
			t.Errorf("relTime(%d)=%q want %q", c.ms, got, c.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in, want string
		max      int
	}{
		{"hello", "hello", 10},
		{"hello world", "hell…", 5},
		{"héllo wörld", "héll…", 5}, // rune-safe, not byte-safe
		{"x", "", 0},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.max); got != c.want {
			t.Errorf("truncate(%q,%d)=%q want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestOneLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"single", "single"},
		{"first\nsecond", "first"},
		{"a\nb\nc", "a"},
		{"", ""},
	}
	for _, c := range cases {
		if got := oneLine(c.in); got != c.want {
			t.Errorf("oneLine(%q)=%q want %q", c.in, got, c.want)
		}
	}
}
