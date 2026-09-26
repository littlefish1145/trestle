package p1689

import (
	"strings"
	"testing"
)

func TestDecode(t *testing.T) {
	input := `{"version":1,"revision":0,"rules":[{"primary-output":"src/math.cppm","provides":[{"logical-name":"math"}]},{"primary-output":"src/main.cpp","requires":[{"logical-name":"math"}]}]}`
	document, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Rules) != 2 || document.Rules[0].Provides[0].LogicalName != "math" {
		t.Fatalf("unexpected document: %#v", document)
	}
}

func TestDecodeRejectsUnknownVersion(t *testing.T) {
	if _, err := Decode(strings.NewReader(`{"version":2,"rules":[]}`)); err == nil {
		t.Fatal("expected version error")
	}
}
