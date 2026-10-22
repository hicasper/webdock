package tests

import (
	"regexp"
	"strings"
	"testing"
)

func TestVersionStartsWithZero(t *testing.T) {
	version := strings.TrimSpace(read(t, "../cmd/webdock/VERSION"))
	if !regexp.MustCompile(`^0\.[0-9]+\.[0-9]+$`).MatchString(version) {
		t.Fatalf("version %q must use 0.x.y semver", version)
	}
}
