package analytics

import "testing"

// White-box test for classifySource (unexported), so it sits in package
// analytics rather than analytics_test.
func TestClassifySource(t *testing.T) {
	const self = "console.example.com"
	cases := []struct {
		name       string
		referrer   string
		wantSource string
		wantHost   string
	}{
		{"empty is direct", "", "direct", ""},
		{"garbage is direct", "not a url", "direct", ""},
		{"google search", "https://www.google.com/search?q=proxy", "search", "www.google.com"},
		{"baidu search", "https://www.baidu.com/s?wd=x", "search", "www.baidu.com"},
		{"twitter social", "https://twitter.com/u/status/1", "social", "twitter.com"},
		{"x.com social", "https://x.com/u", "social", "x.com"},
		{"github social", "https://github.com/wjsoj/repo", "social", "github.com"},
		{"unknown blog referral", "https://blog.someone.dev/post", "referral", "blog.someone.dev"},
		{"same host is internal", "https://console.example.com/pricing", "internal", "console.example.com"},
		{"subdomain of self is internal", "https://www.console.example.com/x", "internal", "www.console.example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotSource, gotHost := classifySource(c.referrer, self)
			if gotSource != c.wantSource || gotHost != c.wantHost {
				t.Fatalf("classifySource(%q) = (%q, %q), want (%q, %q)",
					c.referrer, gotSource, gotHost, c.wantSource, c.wantHost)
			}
		})
	}
}
