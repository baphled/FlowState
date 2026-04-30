#!/usr/bin/env python3
import json
import os
import subprocess
import sys


def run_cmd(cmd: str) -> tuple[int, str]:
    result = subprocess.run(cmd, shell=True, capture_output=True, text=True)
    return result.returncode, (result.stdout + result.stderr).strip()


def main():
    request = json.load(sys.stdin)
    policy = request.get("policy", {})

    integration_cmd = (
        policy.get("integration_cmd")
        or os.environ.get("INTEGRATION_CMD", "make test-integration")
    )
    breaking_cmd = policy.get("breaking_cmd") or os.environ.get("BREAKING_CMD", "")

    # Run integration tests
    code, output = run_cmd(integration_cmd)
    if code != 0:
        json.dump(
            {
                "pass": False,
                "reason": "integration tests failed",
                "output": output,
            },
            sys.stdout,
        )
        return

    # Optionally check for breaking API changes
    if breaking_cmd:
        b_code, b_output = run_cmd(breaking_cmd)
        if b_code != 0:
            json.dump(
                {
                    "pass": False,
                    "reason": "breaking API changes detected",
                    "output": b_output,
                },
                sys.stdout,
            )
            return

    json.dump({"pass": True, "output": output}, sys.stdout)


if __name__ == "__main__":
    main()
