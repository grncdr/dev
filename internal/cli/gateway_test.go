package cli

import "testing"

func TestListenLooksTLS(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		":443":        true,
		"0.0.0.0:443": true,
		"443":         true,
		":https":      true,
		":8443":       false,
		":80":         false,
		"":            false,
	}
	for input, expected := range cases {
		if got := listenLooksTLS(input); got != expected {
			t.Fatalf("listenLooksTLS(%q)=%v expected %v", input, got, expected)
		}
	}
}
