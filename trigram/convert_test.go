package trigram

import (
	"testing"
)

func TestToTrigram(t *testing.T) {
	for _, test := range []struct {
		input string
	}{
		{input: "foobar"},
	} {
		grams := ToTrigram(test.input)
		out := FromTrigrams(grams)
		if out != test.input {
			t.Errorf("Got %q, want %q", out, test.input)
		}
	}
}
