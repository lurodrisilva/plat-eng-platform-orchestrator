package config

import "testing"

func TestExpandEnv(t *testing.T) {
	t.Setenv("SET_VAR", "real")
	t.Setenv("EMPTY_VAR", "")

	cases := []struct {
		name, in, want string
	}{
		{"plain set", "${SET_VAR}", "real"},
		{"plain unset", "${MISSING_VAR}", ""},
		{"default used when unset", "${MISSING_VAR:-fallback}", "fallback"},
		{"default used when empty", "${EMPTY_VAR:-fallback}", "fallback"},
		{"value wins over default", "${SET_VAR:-fallback}", "real"},
		{"default with colons (url)", "${MISSING:-https://argocd.example.com}", "https://argocd.example.com"},
		{"embedded", "aud=${MISSING_VAR:-api://abc}!", "aud=api://abc!"},
		{"bare dollar var", "$SET_VAR", "real"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := expandEnv(c.in); got != c.want {
				t.Errorf("expandEnv(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
