package owls

import (
	"os"
	"regexp"
	"testing"
)

func TestSDKLabel(t *testing.T) {
	// The server keeps an sdk label of printable ASCII without spaces, at most 48
	// characters.
	if !regexp.MustCompile(`^owls-insight-go/[\x21-\x7e]+$`).MatchString(SDKLabel) || len(SDKLabel) > 48 {
		t.Fatalf("SDKLabel %q is not a valid sdk label", SDKLabel)
	}
	b, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^## \[([^\]]+)\]`).FindSubmatch(b)
	if m == nil || string(m[1]) != Version {
		t.Fatalf("the newest CHANGELOG.md entry is %q, Version is %q", m, Version)
	}
}
