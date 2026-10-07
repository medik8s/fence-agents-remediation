package version

import (
	"os"
	"strings"
	"testing"
)

func TestCommittedVersion(t *testing.T) {
	data, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, found := strings.CutPrefix(line, "DEFAULT_VERSION := "); found {
			if value != Version {
				t.Fatalf("committed binary version %s differs from Makefile %s", Version, value)
			}
			return
		}
	}
	t.Fatal("Makefile must define DEFAULT_VERSION")
}
