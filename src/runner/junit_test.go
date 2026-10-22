package runner

import (
	"regexp"
	"testing"
)

func TestClassRegex(t *testing.T) {
	tests := []struct {
		filter, class string
		want          bool
	}{
		{"AppTest", "com.x.AppTest", true},
		{"AppTest", "AppTest", true},
		{"AppTest", "com.x.MyAppTest", false},
		{"*Test", "com.x.FooTest", true},
		{"*IT", "com.x.FooIT", true},
		{"Foo*", "com.x.FooBar", true},
		{"Foo*", "com.x.BarFoo", false},
		{"com.x.*Test", "com.x.FooTest", true},
		{"com.x.*Test", "com.y.FooTest", false},
		{"com.x.Foo", "com.x.Foo", true},
		{"com.x.Foo", "com.xAFoo", false},
	}
	for _, tt := range tests {
		got, err := regexp.MatchString("^(?:"+classRegex(tt.filter)+")$", tt.class)
		if err != nil {
			t.Fatalf("classRegex(%q) is not a valid regex: %v", tt.filter, err)
		}
		if got != tt.want {
			t.Errorf("classRegex(%q) matching %q = %v; want %v", tt.filter, tt.class, got, tt.want)
		}
	}
}
