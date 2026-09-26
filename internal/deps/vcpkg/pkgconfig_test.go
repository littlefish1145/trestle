package vcpkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePCFieldsAndTransitiveRequirements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.pc")
	data := "prefix=/opt/demo\nlibdir=${prefix}/lib\nName: demo\nRequires: mbedtls >= 3, d2d1\nCflags: -I${prefix}/include -DDEMO=1\nLibs: -L${libdir} -ldemo\nLibs.private: -lbcrypt\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	pc, err := ParsePC(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.Requires) != 2 || pc.Requires[0] != "mbedtls" || pc.Cflags[0] != "-I/opt/demo/include" || pc.Libs[1] != "-ldemo" || pc.LibsPrivate[0] != "-lbcrypt" {
		t.Fatalf("unexpected pc metadata: %#v", pc)
	}
}

func TestParsePCRelocatableQuotedPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "directory with spaces", "lib", "pkgconfig")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "glfw3.pc")
	data := "prefix=${pcfiledir}/../..\nincludedir=${prefix}/include\nlibdir=${prefix}/lib\nName: GLFW\nCflags: \"-I${includedir}\" -D GLFW_DLL\nLibs: \"-L${libdir}\" -lglfw3dll\n"
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	pc, err := ParsePC(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.Cflags) != 2 || pc.Cflags[0] != "-I"+filepath.ToSlash(dir)+"/../../include" || pc.Cflags[1] != "-DGLFW_DLL" || len(pc.Libs) != 2 || pc.Libs[1] != "-lglfw3dll" {
		t.Fatalf("incorrect fields: %#v", pc)
	}
	if strings.Contains(strings.Join(pc.Cflags, " "), "${") {
		t.Fatal("unexpanded variable")
	}
}
