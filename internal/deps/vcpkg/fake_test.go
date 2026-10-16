package vcpkg

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as the entry point of a fake vcpkg executable. When
// TRESTLE_TEST_VCPKG_FAKE is set, the test binary speaks as vcpkg instead of
// running tests, so Search, Install and InstallWithProgress can drive a real
// child process whose arguments, environment, output and exit code are fully
// controlled. The live tests against a real vcpkg checkout stay untouched.
func TestMain(m *testing.M) {
	if behavior := os.Getenv("TRESTLE_TEST_VCPKG_FAKE"); behavior != "" {
		os.Exit(fakeVcpkg(behavior, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// newFakeClient returns a client whose executable is this test binary. The
// root is a fresh temporary directory, so locks and trees stay isolated.
func newFakeClient(t *testing.T, behavior string) Client {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	t.Setenv("TRESTLE_TEST_VCPKG_FAKE", behavior)
	return Client{Executable: executable, Root: t.TempDir()}
}

func fakeVcpkg(behavior string, args []string) int {
	switch behavior {
	case "search":
		return fakeVcpkgSearch(args)
	case "install":
		return fakeVcpkgInstall(args)
	default:
		fmt.Fprintf(os.Stderr, "fake vcpkg: unknown behavior %q\n", behavior)
		return 2
	}
}

func fakeVcpkgSearch(args []string) int {
	query := ""
	if len(args) > 1 {
		query = args[1]
	}
	switch query {
	case "boom":
		fmt.Fprintln(os.Stderr, "fake vcpkg: search backend exploded")
		return 1
	case "truthy":
		fmt.Println("The quick brown fox")
		return 0
	case "big":
		fmt.Println("big 1.0.0 A port with a huge history")
		return 0
	case "mixed":
		fmt.Println("fmt 11.2.0#1 {fmt} is an open-source formatting library")
		fmt.Println("big 1.0.0 A port with a huge history")
		return 0
	default:
		fmt.Println("fmt 11.2.0#1 {fmt} is an open-source formatting library")
		fmt.Println("    fmt[unicode] Unicode support")
		fmt.Println("spdlog 1.17.0 Fast C++ logging library")
		fmt.Println("zlib 1.3.1 A compression library")
		return 0
	}
}

func fakeVcpkgInstall(args []string) int {
	arguments := strings.Join(args, " ")
	switch {
	case strings.Contains(arguments, "fail-silent"):
		return 5
	case strings.Contains(arguments, "fail-loud"):
		fmt.Fprintln(os.Stderr, "error: building fail-loud failed: compile error")
		return 1
	case strings.Contains(arguments, "block"):
		if marker := os.Getenv("TRESTLE_TEST_VCPKG_FAKE_MARKER"); marker != "" {
			_ = os.WriteFile(marker, []byte("started"), 0o644)
		}
		for index := 0; index < 2400; index++ {
			time.Sleep(25 * time.Millisecond)
		}
		return 0
	case strings.Contains(arguments, "quiet"):
		return 0
	default:
		fmt.Fprintln(os.Stdout, "Computing installation plan...")
		fmt.Fprintf(os.Stdout, "args=%s\n", arguments)
		fmt.Fprintf(os.Stdout, "canary=%s\n", os.Getenv("TRESTLE_TEST_VCPKG_FAKE_CANARY"))
		fmt.Fprintln(os.Stderr, "warning: fake vcpkg warning")
		return 0
	}
}
