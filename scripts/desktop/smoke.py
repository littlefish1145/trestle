#!/usr/bin/env python3
"""Drive a real Trestle Desktop window and check its Go/Ninja side effects."""

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time


def wait_for(proc, operation, timeout=45):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        status = proc.poll()
        if status is not None:
            raise RuntimeError(f"Desktop exited unexpectedly ({status})")
        result = operation()
        if result:
            return result
        time.sleep(0.05)
    raise TimeoutError(f"Desktop wait timed out at {operation.__name__}")


def cmd(args, env, *, check=True):
    return subprocess.run(args, env=env, text=True, capture_output=True, check=check, timeout=15)


def power_shell(expression, env):
    return cmd(["powershell.exe", "-NoProfile", "-NonInteractive", "-Command", expression], env)


def keyboard(proc, env):
    if os.name == "nt":
        def send(key):
            if key not in ("b", "w"):
                raise ValueError(key)
            power_shell(
                f"$shell = New-Object -ComObject WScript.Shell; "
                f"if (-not $shell.AppActivate({proc.pid})) {{ exit 2 }}; "
                f"$shell.SendKeys('^{key}')",
                env,
            )
        return send
    def find_window():
        return cmd(
            ["xdotool", "search", "--all", "--onlyvisible", "--pid", str(proc.pid),
             "--name", "^Trestle Desktop$"],
            env, check=False,
        ).stdout.strip().splitlines()
    window = wait_for(proc, find_window)[0]
    def send(key):
        cmd(["xdotool", "windowfocus", window, "key", f"ctrl+{key}"], env)
    return send


def child_processes(proc, env):
    if os.name == "nt":
        reply = power_shell(
            f"(Get-CimInstance Win32_Process -Filter 'ParentProcessId={proc.pid}').ProcessId",
            env,
        )
    else:
        reply = cmd(["ps", "-o", "pid=", "--ppid", str(proc.pid)], env, check=False)
    return [int(pid) for pid in reply.stdout.split()]


def process_alive(pid, env):
    if os.name == "nt":
        reply = power_shell(f"@(Get-Process -Id {pid} -ErrorAction SilentlyContinue).Count", env)
        return reply.stdout.strip() == "1"
    return Path(f"/proc/{pid}").exists()


def history(state, exclude=()):
    directory = state / "build-history-v1"
    records = sorted(file for file in directory.glob("build-*.json") if file not in exclude)
    if records:
        record = records[-1]
        return record, json.loads(record.read_text(encoding="utf-8"))
    return None


def copy_example(source, destination):
    shutil.copytree(source, destination, ignore=shutil.ignore_patterns(
        "build", "dist", ".trestle", "compile_commands.json", ".git", "*.trestle-lock*",
    ))


def exercise(args, root):
    project = root / "workspace example"
    state = root / "app state"
    copy_example(args.workspace, project)
    env = dict(os.environ, TRESTLE_CORE=str(args.core), TRESTLE_DESKTOP_STATE_DIR=str(state))
    if args.compiler:
        cmd([str(args.core), "configure", "-C", str(project), "-toolchain", args.compiler], env)
    log_path = root / "desktop.log"
    with log_path.open("w", encoding="utf-8") as log:
        proc = subprocess.Popen([args.desktop, project], env=env, stdout=log, stderr=log)
        send = None
        try:
            wait_for(proc, lambda: (state / "recent.json").is_file())
            children = wait_for(proc, lambda: child_processes(proc, env))
            send = keyboard(proc, env)
            send("b")
            record, result = wait_for(proc, lambda: history(state))
            status = result["build"]
            if status["status"] != "completed" or not result["timings"]:
                raise RuntimeError(f"Desktop build failed: {status}")
            if not any(item.get("critical_path") for item in result["ninja_invocations"]):
                raise RuntimeError("Desktop build had no measured dependency chain")
            executable = project / "build/debug/cli/bin/hello-workspace"
            if os.name == "nt":
                executable = executable.with_suffix(".exe")
            assert cmd([str(executable)], env).stdout.strip() == "Hello, workspace!"
            incremental = cmd([str(args.core), "build", "-C", str(project)], env)
            assert incremental.stdout.count("ninja: no work to do.") == 2, incremental.stdout

            # The process writes history before the window's next status poll.
            time.sleep(0.5)
            (project / "cli/src/main.cpp").write_text("#error TrestleDesktopSmokeFailure\n", encoding="utf-8")
            send("b")
            _, failed = wait_for(proc, lambda: history(state, (record,)))
            assert failed["build"]["status"] == "failed", failed["build"]
            assert any(
                "TrestleDesktopSmokeFailure" in Path(file).read_text(encoding="utf-8", errors="replace")
                for file in failed["build"]["log_paths"]
            )
            send("w")
            assert proc.wait(timeout=15) == 0
            assert not any(process_alive(pid, env) for pid in children), children
            print("Native window built and ran the C++ example, reported a compiler failure, "
                  "shared incremental state with the CLI, and reaped Core.")
        finally:
            if proc.poll() is None and send is not None:
                try:
                    send("w")
                    proc.wait(timeout=10)
                except (subprocess.SubprocessError, TimeoutError):
                    pass
            if proc.poll() is None:
                proc.kill()
                proc.wait(timeout=10)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--desktop", type=lambda value: Path(value).resolve(), required=True)
    parser.add_argument("--core", type=lambda value: Path(value).resolve(), required=True)
    parser.add_argument("--workspace", type=Path, default=Path("examples/hello-workspace"))
    parser.add_argument("--compiler")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="trestle-desktop-") as folder:
        root = Path(folder)
        try:
            exercise(args, root)
        except Exception:
            log = root / "desktop.log"
            if log.is_file():
                print(log.read_text(encoding="utf-8", errors="replace"))
            raise


if __name__ == "__main__":
    main()
