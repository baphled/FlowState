#!/usr/bin/env python3
import json, re, sys

def extract_keywords(text: str) -> set:
    """Extract meaningful keywords from text (words 4+ chars, no stopwords)."""
    stopwords = {
        "that", "this", "with", "from", "have", "will", "been", "were",
        "they", "what", "when", "your", "which", "about", "would", "could",
        "should", "their", "there", "some", "into", "also", "than", "then"
    }
    words = re.findall(r'\b[a-z]{4,}\b', text.lower())
    return {w for w in words if w not in stopwords}

def main():
    req = json.load(sys.stdin)
    payload = req.get("payload") or {}
    if isinstance(payload, str):
        try:
            payload = json.loads(payload)
        except Exception:
            json.dump({"pass": False, "reason": "payload is not valid JSON"}, sys.stdout)
            return

    task_text = payload.get("task_plan", "")
    research_text = payload.get("research", "")

    if not task_text.strip():
        json.dump({"pass": False, "reason": "task_plan is empty — cannot assess relevance"}, sys.stdout)
        return

    if not research_text.strip():
        json.dump({"pass": False, "reason": "research output is empty"}, sys.stdout)
        return

    task_keywords = extract_keywords(task_text)
    research_keywords = extract_keywords(research_text)

    if not task_keywords:
        json.dump({"pass": True, "score": 1.0, "note": "task had no extractable keywords"}, sys.stdout)
        return

    overlap = task_keywords & research_keywords
    score = len(overlap) / len(task_keywords)
    threshold = float((req.get("policy") or {}).get("threshold", 0.4))

    if score >= threshold:
        json.dump({"pass": True, "score": round(score, 2), "overlap": sorted(overlap)}, sys.stdout)
    else:
        missing = sorted(task_keywords - research_keywords)
        json.dump({
            "pass": False,
            "reason": f"research relevance score {score:.2f} below threshold {threshold:.2f}",
            "missing_topics": missing[:10],
            "redirect": f"Research should cover: {', '.join(missing[:5])}"
        }, sys.stdout)

if __name__ == "__main__":
    main()
