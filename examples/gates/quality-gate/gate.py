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

    coverage_threshold = int(
        policy.get("coverage_threshold")
        or os.environ.get("COVERAGE_THRESHOLD", "80")
    )
    lint_cmd = (
        policy.get("lint_cmd") or os.environ.get("LINT_CMD", "make lint")
    )
    # coverage_cmd should emit a single integer (the coverage %) to stdout
    coverage_cmd = (
        policy.get("coverage_cmd")
        or os.environ.get("COVERAGE_CMD", "make coverage-pct")
    )

    failures = []

    # Check coverage
    cov_code, cov_output = run_cmd(coverage_cmd)
    if cov_code != 0:
        failures.append(
            {
                "check": "coverage",
                "reason": "coverage command failed",
                "output": cov_output,
            }
        )
    else:
        try:
            actual_coverage = int(cov_output.strip().split()[-1].rstrip("%"))
            if actual_coverage < coverage_threshold:
                failures.append(
                    {
                        "check": "coverage",
                        "reason": (
                            f"coverage {actual_coverage}% is below threshold "
                            f"{coverage_threshold}% "
                            f"(shortfall: {coverage_threshold - actual_coverage}%)"
                        ),
                        "actual": actual_coverage,
                        "threshold": coverage_threshold,
                    }
                )
        except (ValueError, IndexError):
            failures.append(
                {
                    "check": "coverage",
                    "reason": f"could not parse coverage output: {cov_output!r}",
                }
            )

    # Check lint
    lint_code, lint_output = run_cmd(lint_cmd)
    if lint_code != 0:
        failures.append(
            {
                "check": "lint",
                "reason": "linter reported issues",
                "output": lint_output,
            }
        )

    if failures:
        json.dump({"pass": False, "failures": failures}, sys.stdout)
    else:
        json.dump({"pass": True}, sys.stdout)


if __name__ == "__main__":
    main()
