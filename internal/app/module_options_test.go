package app

import (
	"context"
	"reflect"
	"testing"
)

func TestMapWSLModuleOptionsKeepsRelativeBuildPaths(t *testing.T) {
	options := []string{"-I../include", "-isystem", "../../vendor", "-DVALUE=1"}
	got, err := mapWSLModuleOptions(context.Background(), "Ubuntu", "/mnt/c/work/build/debug", options)
	if err != nil || !reflect.DeepEqual(got, options) {
		t.Fatalf("mapped options = %#v, err=%v", got, err)
	}
	if !moduleScannableSource("src/main.cpp") || !moduleScannableSource("src/math.cppm") || moduleScannableSource("src/kernel.cu") {
		t.Fatal("module source selection is incorrect")
	}
}

func TestMapWSLModuleOptionsRejectsMissingPath(t *testing.T) {
	if _, err := mapWSLModuleOptions(context.Background(), "Ubuntu", "/mnt/c/work/build/debug", []string{"-I"}); err == nil {
		t.Fatal("missing include path was accepted")
	}
}
