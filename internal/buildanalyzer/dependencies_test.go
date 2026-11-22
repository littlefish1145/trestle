package buildanalyzer

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDepsLogV4UnicodeAndInvalidRecords(t *testing.T) {
	encode := binary.LittleEndian.AppendUint32
	log := encode([]byte("# ninjadeps\n"), 4)
	for id, name := range []string{"obj/main.obj", "C:/项目/公共 头文件.h"} {
		padded := (len(name) + 3) &^ 3
		log = encode(log, uint32(padded+4))
		log = append(log, name...)
		log = append(log, make([]byte, padded-len(name))...)
		log = encode(log, ^uint32(id))
	}
	log = encode(log, 0x80000010)
	log = encode(log, 0)
	log = binary.LittleEndian.AppendUint64(log, 1000)
	log = encode(log, 1)
	path := filepath.Join(t.TempDir(), ".ninja_deps")
	if err := os.WriteFile(path, log, 0o600); err != nil {
		t.Fatal(err)
	}
	deps, err := readDepsLog(path)
	if err != nil || deps["obj/main.obj"].mtime != 1000 ||
		!slices.Equal(deps["obj/main.obj"].paths, []string{"C:/项目/公共 头文件.h"}) {
		t.Fatalf("deps: %+v, %v", deps, err)
	}
	if err := os.WriteFile(path, log[:len(log)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDepsLog(path); err == nil {
		t.Fatal("truncated record accepted")
	}
}

func TestDepfileWindowsEscaping(t *testing.T) {
	edge := Edge{Outputs: []string{`C:\build\out.obj`}}
	input := "C:\\build\\out.obj: C:\\src\\头\\ 文件.h \\\r\n next$$file.h\r\n"
	paths, err := parseDepfile(input, edge)
	if err != nil || !slices.Equal(paths, []string{`C:\src\头 文件.h`, "next$file.h"}) {
		t.Fatalf("depfile: %v, %v", paths, err)
	}
}
