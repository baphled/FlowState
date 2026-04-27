#!/usr/bin/env python3
"""
Reference v0 ext-gate: score a swarm member's claim against the operator's
Obsidian vault. Returns pass:true when the best Qdrant similarity is at or
above policy.threshold; otherwise returns pass:false with the sub-threshold
evidence still attached for debug visibility.

Configuration is via env (so the operator does not have to thread args
through their service supervisor):
  QDRANT_URL          default http://localhost:6333
  QDRANT_COLLECTION   default flowstate-vault
  OLLAMA_HOST         default http://localhost:11434
  EMBEDDING_MODEL     default nomic-embed-text
"""
import json
import os
import sys
import urllib.request


def env(name: str, default: str) -> str:
    v = os.environ.get(name)
    return v if v else default


QDRANT_URL = env("QDRANT_URL", "http://localhost:6333")
QDRANT_COLLECTION = env("QDRANT_COLLECTION", "flowstate-vault")
OLLAMA_HOST = env("OLLAMA_HOST", "http://localhost:11434")
EMBEDDING_MODEL = env("EMBEDDING_MODEL", "nomic-embed-text")


def embed(text: str) -> list[float]:
    body = json.dumps({"model": EMBEDDING_MODEL, "prompt": text}).encode()
    req = urllib.request.Request(
        f"{OLLAMA_HOST}/api/embeddings",
        data=body, headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=8) as resp:
        return json.loads(resp.read())["embedding"]


def search(vector: list[float], top_k: int) -> list[dict]:
    body = json.dumps({"vector": vector, "limit": top_k, "with_payload": True}).encode()
    req = urllib.request.Request(
        f"{QDRANT_URL}/collections/{QDRANT_COLLECTION}/points/search",
        data=body, headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=8) as resp:
        return json.loads(resp.read()).get("result", [])


def main() -> None:
    req = json.load(sys.stdin)
    payload = req.get("payload") or ""
    if isinstance(payload, list):
        payload = bytes(payload).decode("utf-8", errors="replace")
    policy = req.get("policy") or {}
    threshold = float(policy.get("threshold", 0.65))
    top_k = int(policy.get("top_k", 3))

    if not payload.strip():
        json.dump({"pass": False, "reason": "empty payload"}, sys.stdout)
        return

    vector = embed(payload)
    hits = search(vector, top_k)
    evidence = [
        {
            "source": h.get("payload", {}).get("source_file", ""),
            "snippet": h.get("payload", {}).get("content", ""),
            "similarity": float(h.get("score", 0.0)),
        }
        for h in hits
    ]
    best = max((e["similarity"] for e in evidence), default=0.0)
    if best >= threshold:
        json.dump({"pass": True, "evidence": evidence}, sys.stdout)
    else:
        reason = f"no supporting evidence above threshold {threshold:.2f} (best {best:.2f})"
        json.dump({"pass": False, "reason": reason, "evidence": evidence}, sys.stdout)


if __name__ == "__main__":
    main()
