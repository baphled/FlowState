#!/usr/bin/env python3
import json, sys

REQUIRED_ANALYSTS = ["bull", "bear", "market", "financial", "technical"]

def main():
    req = json.load(sys.stdin)
    payload = req.get("payload") or {}
    if isinstance(payload, str):
        try:
            payload = json.loads(payload)
        except Exception:
            json.dump({"pass": False, "reason": "payload is not valid JSON"}, sys.stdout)
            return

    positions = payload.get("positions", {})
    missing = [a for a in REQUIRED_ANALYSTS if a not in positions]
    if missing:
        json.dump({"pass": False, "reason": f"missing positions from: {', '.join(missing)}"}, sys.stdout)
        return

    bull_dec = (positions.get("bull") or {}).get("decision", "").strip().lower()
    bear_dec = (positions.get("bear") or {}).get("decision", "").strip().lower()
    if bull_dec and bear_dec and bull_dec == bear_dec:
        json.dump({
            "pass": False,
            "reason": f"bull and bear both recommend '{bull_dec}' — adversarial review collapsed, re-run"
        }, sys.stdout)
        return

    json.dump({"pass": True}, sys.stdout)

if __name__ == "__main__":
    main()
