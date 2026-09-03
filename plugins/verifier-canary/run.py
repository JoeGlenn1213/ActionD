#!/usr/bin/env python3
"""
verifier-canary — ASSURANCE Phase B 元验证（mutation testing on the verifier）

用两组已知 fixture 校验 verdict 管线本身是否可信：
  must-fail：包含必挂测试的 Go 模块 → `go test` 必须失败；
            若意外通过 ⇒ 验证器会对已知坏代码报 PASS（verdict 管线失效）
  must-pass：包含必过测试的 Go 模块 → `go test` 必须通过；
            若失败 ⇒ 工具链/环境失效（假警报模式）

fail-closed 语义：任何一组与预期不符，本插件输出 error 并以非零码退出
（CI 红色 + 报告 verdict=fail），不给下游发"可信"信号。

触发范围：repoFilter=ActionD.git（验证器自身仓库的 push 才跑，
因为测试/解释器插件的变更只发生在 ActionD 仓库）。
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile

GO_VERSION = "1.21"

# Resolve the go binary explicitly: launchd-managed daemons can have a
# minimal PATH (observed in production: first canary run under a fresh
# launchd instance failed with "go toolchain not found").
_GO_FALLBACKS = [
    "/opt/homebrew/bin/go",
    "/usr/local/go/bin/go",
    os.path.expanduser("~/go/bin/go"),
]


def find_go() -> str:
    found = shutil.which("go")
    if found:
        return found
    for candidate in _GO_FALLBACKS:
        if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
            return candidate
    raise RuntimeError("go toolchain not found (checked PATH and %s)" % _GO_FALLBACKS)


def run_go_test(module_dir: str) -> subprocess.CompletedProcess:
    """Run `go test ./...` in an isolated fixture module."""
    env = dict(os.environ)
    env["GOCACHE"] = os.path.join(tempfile.gettempdir(), "verifier-canary-gocache")
    env["GOFLAGS"] = "-mod=mod"
    try:
        return subprocess.run(
            [find_go(), "test", "./..."],
            cwd=module_dir,
            env=env,
            capture_output=True,
            text=True,
            timeout=180,
        )
    except RuntimeError:
        raise
    except subprocess.TimeoutExpired:
        raise RuntimeError("canary fixture timed out (180s)")


def write_fixture(base: str, failing: bool) -> None:
    """Create a minimal Go module whose only test passes or fails."""
    os.makedirs(base, exist_ok=True)
    with open(os.path.join(base, "go.mod"), "w", encoding="utf-8") as f:
        f.write("module verifier-canary-fixture\n\ngo %s\n" % GO_VERSION)
    if failing:
        test = (
            "package fixture\n\n"
            'import "testing"\n\n'
            "// TestMustFail is EXPECTED to fail; if it passes, the verdict\n"
            "// pipeline is broken (reports pass on known-broken code).\n"
            'func TestMustFail(t *testing.T) {\n'
            '\tt.Fatal("CANARY: expected failure")\n'
            "}\n"
        )
    else:
        test = (
            "package fixture\n\n"
            'import "testing"\n\n'
            "// TestMustPass is EXPECTED to pass; if it fails, the toolchain\n"
            "// or environment is broken (false-alarm mode).\n"
            'func TestMustPass(t *testing.T) {}\n'
        )
    with open(os.path.join(base, "fixture_test.go"), "w", encoding="utf-8") as f:
        f.write(test)


def tail(text: str, n: int = 4) -> str:
    lines = [ln for ln in text.splitlines() if ln.strip()]
    return "\n".join(lines[-n:])


def main() -> int:
    # ExecInput arrives on stdin; the canary ignores the payload but must
    # consume it to keep the protocol clean.
    try:
        json.load(sys.stdin)
    except Exception:
        pass

    tmp = tempfile.mkdtemp(prefix="verifier-canary-")
    try:
        fail_dir = os.path.join(tmp, "must-fail")
        pass_dir = os.path.join(tmp, "must-pass")
        write_fixture(fail_dir, failing=True)
        write_fixture(pass_dir, failing=False)

        must_fail = run_go_test(fail_dir)
        must_pass = run_go_test(pass_dir)

        details = {
            "must_fail_exit": must_fail.returncode,
            "must_pass_exit": must_pass.returncode,
        }

        problems = []
        if must_fail.returncode == 0:
            problems.append(
                "MUST-FAIL FIXTURE PASSED: verdict pipeline is broken "
                "(reports pass on known-broken code)"
            )
        if must_pass.returncode != 0:
            problems.append(
                "MUST-PASS FIXTURE FAILED (exit %d): toolchain/environment "
                "broken\n%s" % (must_pass.returncode, tail(must_pass.stderr))
            )

        if problems:
            print(json.dumps({
                "status": "error",
                "error": "; ".join(problems),
                "details": details,
            }))
            return 1

        print(json.dumps({
            "status": "success",
            "summary": "canary ok: must-fail failed as expected, must-pass passed",
            "details": details,
        }))
        return 0
    except RuntimeError as exc:
        print(json.dumps({"status": "error", "error": str(exc)}))
        return 1
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
