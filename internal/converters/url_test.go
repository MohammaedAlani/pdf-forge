package converters

import (
	"testing"
)

func TestValidateURL_Allowed(t *testing.T) {
	cases := []string{
		"https://example.com",
		"http://example.com/path?q=1",
		"https://www.google.com",
	}
	for _, u := range cases {
		if err := ValidateURL(u); err != nil {
			t.Errorf("ValidateURL(%q) = %v, want nil", u, err)
		}
	}
}

func TestValidateURL_Blocked(t *testing.T) {
	cases := []string{
		"",
		"file:///etc/passwd",
		"ftp://example.com",
		"http://127.0.0.1/",
		"http://localhost/",
		"http://10.0.0.5/",
		"http://192.168.1.1/",
		"http://172.16.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"https://",
	}
	for _, u := range cases {
		if err := ValidateURL(u); err == nil {
			t.Errorf("ValidateURL(%q) = nil, want error", u)
		}
	}
}
