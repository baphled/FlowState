#!/usr/bin/env python3
import json
import os
import subprocess
import sys


def main():
    request = json.load(sys.stdin)
    policy = request.get("policy", {})
    test_cmd = policy.get("test_cmd") or os.environ.get("TEST_CMD", "make test")

    result = subprocess.run(test_cmd, shell=True, capture_output=True, text=True)
    output = (result.stdout + result.stderr).strip()

    if result.returncode == 0:
        json.dump({"pass": True, "output": output}, sys.stdout)
    else:
        json.dump(
            {"pass": False, "reason": "unit tests failed", "output": output},
            sys.stdout,
        )


if __name__ == "__main__":
    main()
