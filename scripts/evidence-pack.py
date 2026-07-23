#!/usr/bin/env python3

from __future__ import annotations

import argparse
import hashlib
import re
import shutil
import sys
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


DEFAULT_VAULT_ROOT = Path.home() / "vaults" / "baphled"


@dataclass
class Spec:
    label: str | None
    path: Path


def parse_spec(value: str) -> Spec:
    label, sep, raw_path = value.partition("=")
    if sep and label:
        return Spec(label=label, path=Path(raw_path).expanduser())
    return Spec(label=None, path=Path(value).expanduser())


def sanitize_filename(value: str) -> str:
    cleaned = re.sub(r"[^A-Za-z0-9._-]+", "_", value.strip())
    cleaned = cleaned.strip("._-")
    return cleaned or "item"


def unique_name(directory: Path, base_name: str, used_names: set[str]) -> str:
    candidate = base_name
    suffix = 2
    while candidate in used_names or (directory / candidate).exists():
        stem = Path(base_name).stem
        suffix_text = Path(base_name).suffix
        candidate = f"{stem}-{suffix}{suffix_text}"
        suffix += 1
    used_names.add(candidate)
    return candidate


def sha256_of(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def yaml_scalar(value: Any) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (int, float)):
        return str(value)
    text = str(value)
    if text == "":
        return "''"
    if re.fullmatch(r"[A-Za-z0-9_./:@+,-]+", text):
        return text
    return "'" + text.replace("'", "''") + "'"


def dump_yaml(data: Any, indent: int = 0) -> list[str]:
    prefix = "  " * indent
    lines: list[str] = []
    if isinstance(data, dict):
        for key, value in data.items():
            if isinstance(value, dict):
                if value:
                    lines.append(f"{prefix}{key}:")
                    lines.extend(dump_yaml(value, indent + 1))
                else:
                    lines.append(f"{prefix}{key}: {{}}")
            elif isinstance(value, list):
                if value:
                    lines.append(f"{prefix}{key}:")
                    lines.extend(dump_yaml(value, indent + 1))
                else:
                    lines.append(f"{prefix}{key}: []")
            else:
                lines.append(f"{prefix}{key}: {yaml_scalar(value)}")
        return lines
    if isinstance(data, list):
        for item in data:
            if isinstance(item, (dict, list)):
                lines.append(f"{prefix}-")
                lines.extend(dump_yaml(item, indent + 1))
            else:
                lines.append(f"{prefix}- {yaml_scalar(item)}")
        return lines
    lines.append(f"{prefix}{yaml_scalar(data)}")
    return lines


def parse_specs(values: list[str]) -> list[Spec]:
    return [parse_spec(value) for value in values]


def ensure_regular_file(path: Path) -> None:
    if not path.exists():
        raise SystemExit(f"error: source not found: {path}")
    if not path.is_file():
        raise SystemExit(f"error: source is not a regular file: {path}")


def copy_artifacts(kind: str, specs: list[Spec], target_dir: Path, dry_run: bool) -> list[dict[str, Any]]:
    entries: list[dict[str, Any]] = []
    used_names: set[str] = set()
    for spec in specs:
        ensure_regular_file(spec.path)
        base_name = sanitize_filename(spec.label or spec.path.name)
        final_name = unique_name(target_dir, base_name, used_names)
        packed_path = target_dir / final_name
        entry: dict[str, Any] = {
            "kind": kind,
            "original_local_path": str(spec.path),
            "packed_path": str(packed_path.relative_to(target_dir.parent)),
        }
        if dry_run:
            entry["sha256"] = None
            entry["bytes"] = None
        else:
            shutil.copy2(spec.path, packed_path)
            entry["sha256"] = sha256_of(packed_path)
            entry["bytes"] = packed_path.stat().st_size
        entries.append(entry)
    return entries


def build_summary(manifest: dict[str, Any]) -> str:
    investigation = manifest["investigation"]
    artifacts = manifest["artifacts"]
    copied = artifacts["copied"]
    sources = artifacts["metadata_only_sources"]
    generated = artifacts["generated"]
    evidence = manifest["evidence"]

    lines = [
        "# Investigation Evidence Pack",
        "",
        f"- Investigation: `{investigation['slug']}`",
        f"- Created: `{investigation['created_at']}`",
        f"- Destination: `{investigation['destination']}`",
        f"- Vault root: `{investigation['vault_root']}`",
        f"- Project root: `{investigation['project_root']}`",
        f"- Evidence ID: `{evidence.get('id') or 'none'}`",
        f"- Supports: {', '.join(f'`{item}`' for item in evidence.get('supports', [])) or 'none'}",
        "",
        "## Redaction policy",
        "",
        "- Safe by default: raw logs are not copied unless explicitly named.",
        "- Redact secrets, tokens, cookies, OAuth material, credentials, and personal data before sharing.",
        "- Keep copied excerpts as small as possible and prefer summaries over transcripts.",
        "",
        "## Metadata-only source paths",
        "",
    ]

    if sources:
        lines.extend(f"- `{item['original_local_path']}`" for item in sources)
    else:
        lines.append("- none")

    lines.extend(["", "## Copied artefacts", ""])

    if copied:
        lines.extend(f"- `{item['packed_path']}` ← `{item['original_local_path']}`" for item in copied)
    else:
        lines.append("- none")

    lines.extend(["", "## Generated artefacts", ""])

    if generated:
        lines.extend(f"- `{item['packed_path']}`" for item in generated)
    else:
        lines.append("- none")

    lines.extend([
        "",
        "## Notes",
        "",
        "- This pack is portable: it records source paths as metadata and stores only explicitly selected artefacts.",
        "- If you need additional evidence, add it explicitly rather than copying a full raw log.",
    ])
    return "\n".join(lines) + "\n"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Create a portable, redacted FlowState investigation evidence pack.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Examples:\n"
            "  scripts/evidence-pack.py --investigation 2026-07-23-session-duration --dry-run\n"
            "  scripts/evidence-pack.py --investigation 2026-07-23-session-duration \\\n"
            "    --source notes=/tmp/session-notes.md --excerpt /tmp/redacted-transcript.txt \\\n"
            "    --command-output make-test=/tmp/make-test.txt\n"
        ),
    )
    parser.add_argument("--investigation", required=True, help="Investigation slug, e.g. 2026-07-23-session-duration")
    parser.add_argument("--vault-root", default=str(DEFAULT_VAULT_ROOT), help="Vault root containing 1. Projects/FlowState")
    parser.add_argument("--project-root", default=None, help="FlowState project root; defaults to <vault-root>/1. Projects/FlowState")
    parser.add_argument("--destination", default=None, help="Explicit destination directory for the evidence pack")
    parser.add_argument("--evidence-id", default=None, help="Optional evidence identifier to record in the manifest")
    parser.add_argument("--supports", action="append", default=[], help="Evidence identifier supported by this pack (repeatable)")
    parser.add_argument("--source", action="append", default=[], help="Metadata-only source path, optionally labelled as label=/path/to/file")
    parser.add_argument("--summary", action="append", default=[], help="Explicit summary file to copy into summaries/, optionally labelled as label=/path/to/file")
    parser.add_argument("--excerpt", action="append", default=[], help="Explicit excerpt file to copy into excerpts/, optionally labelled as label=/path/to/file")
    parser.add_argument("--command-output", action="append", default=[], help="Explicit command output to copy into command-output/, optionally labelled as label=/path/to/file")
    parser.add_argument("--dry-run", action="store_true", help="Show what would be created without writing files")
    parser.add_argument("--force", action="store_true", help="Allow reusing an existing non-empty destination directory")
    return parser


def main() -> int:
    args = build_parser().parse_args()

    vault_root = Path(args.vault_root).expanduser()
    project_root = Path(args.project_root).expanduser() if args.project_root else vault_root / "1. Projects" / "FlowState"
    destination = Path(args.destination).expanduser() if args.destination else project_root / "Investigations" / args.investigation / "evidence"

    summary_dir = destination / "summaries"
    excerpt_dir = destination / "excerpts"
    command_output_dir = destination / "command-output"
    manifest_path = destination / "manifest.yaml"
    generated_summary_path = summary_dir / "pack-summary.md"

    source_specs = parse_specs(args.source)
    summary_specs = parse_specs(args.summary)
    excerpt_specs = parse_specs(args.excerpt)
    command_output_specs = parse_specs(args.command_output)

    if not args.dry_run and destination.exists() and any(destination.iterdir()) and not args.force:
        raise SystemExit(f"error: destination already exists and is not empty: {destination} (use --force to reuse it)")

    print("WARNING: review all pack contents for secrets, tokens, cookies, OAuth material, credentials, and personal data before sharing.", file=sys.stderr)
    print("WARNING: raw logs are excluded unless you explicitly add them with --summary, --excerpt, or --command-output.", file=sys.stderr)

    manifest: dict[str, Any] = {
        "schema_version": 1,
        "investigation": {
            "slug": args.investigation,
            "created_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
            "vault_root": str(vault_root),
            "project_root": str(project_root),
            "destination": str(destination),
        },
        "evidence": {
            "id": args.evidence_id,
            "supports": args.supports,
        },
        "redaction_policy": {
            "mode": "safe-by-default",
            "copy_raw_logs_by_default": False,
            "requires_manual_redaction_for_sharing": True,
            "warnings": [
                "Raw logs are not copied unless explicitly named.",
                "Summaries and excerpts should be redacted before distribution.",
            ],
        },
        "artifacts": {
            "metadata_only_sources": [
                {"original_local_path": str(spec.path), "label": spec.label}
                for spec in source_specs
            ],
            "copied": [],
            "generated": [],
        },
    }

    if args.dry_run:
        manifest["artifacts"]["copied"].extend(copy_artifacts("summary", summary_specs, summary_dir, dry_run=True))
        manifest["artifacts"]["copied"].extend(copy_artifacts("excerpt", excerpt_specs, excerpt_dir, dry_run=True))
        manifest["artifacts"]["copied"].extend(copy_artifacts("command_output", command_output_specs, command_output_dir, dry_run=True))
        manifest["artifacts"]["generated"].append(
            {"kind": "generated_summary", "packed_path": str(generated_summary_path.relative_to(destination)), "sha256": None, "bytes": None}
        )
        print(f"Dry run: would create {destination}")
        print(f"Dry run: would write {manifest_path}")
        print(f"Dry run: would write {generated_summary_path}")
        print(f"Dry run: would copy {len(summary_specs)} summary file(s), {len(excerpt_specs)} excerpt file(s), {len(command_output_specs)} command-output file(s)")
        return 0

    destination.mkdir(parents=True, exist_ok=True)
    summary_dir.mkdir(parents=True, exist_ok=True)
    excerpt_dir.mkdir(parents=True, exist_ok=True)
    command_output_dir.mkdir(parents=True, exist_ok=True)

    manifest["artifacts"]["copied"].extend(copy_artifacts("summary", summary_specs, summary_dir, dry_run=False))
    manifest["artifacts"]["copied"].extend(copy_artifacts("excerpt", excerpt_specs, excerpt_dir, dry_run=False))
    manifest["artifacts"]["copied"].extend(copy_artifacts("command_output", command_output_specs, command_output_dir, dry_run=False))

    generated_summary_entry: dict[str, Any] = {
        "kind": "generated_summary",
        "packed_path": str(generated_summary_path.relative_to(destination)),
        "sha256": None,
        "bytes": None,
    }
    manifest["artifacts"]["generated"].append(generated_summary_entry)

    generated_summary_path.write_text(build_summary(manifest), encoding="utf-8")
    generated_summary_entry["sha256"] = sha256_of(generated_summary_path)
    generated_summary_entry["bytes"] = generated_summary_path.stat().st_size

    manifest_path.write_text("\n".join(dump_yaml(manifest)) + "\n", encoding="utf-8")

    print(f"Created evidence pack at: {destination}")
    print(f"Manifest: {manifest_path}")
    print(f"Summary: {generated_summary_path}")
    print(f"Copied: {len(manifest['artifacts']['copied'])} file(s)")
    print(f"Metadata-only sources: {len(manifest['artifacts']['metadata_only_sources'])}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
