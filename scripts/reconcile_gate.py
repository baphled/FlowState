"""Run the PLAN-SDLC-001 section 6 decision order for one checkpoint: exit 0 allow, 1 block, 2 usage.

The gate only orchestrates: every check is a call into a merged `reconcile_*` or admission module, and nothing on
its import path reaches the advisor. `decide(world, context)` decides over injected inputs; `main` builds them from
`reconcile.json`, the private admission configuration, the checkout and a local evidence cache.

Decision order, fixed: (1) identity; (2) the live target, resolved twice in the checkpoint's context, a failing try
repeated at most `retry` times; (3) evidence freshness; (4) binding and approval; (5) to (8) scope, dependencies,
duplicate work and release disposition through `reconcile_scope.check_packet`; (9) upstream drift from the baseline
to the live target, classified against the packet's watched patterns; (10) the decision. `current` and
`non_material` allow; `material` and `indeterminate` block; the record is emitted and cached either way.

A cached record is never authority, because a local file is unauthenticated: every check runs again on every call.
A head exempt through a valid activation record has no packet record, so only identity and the resolved target are
checked; the `ci` context and the release checkpoints likewise run no Dropbox-backed check, and neither writes a
record. A refused check raises ReconcileError naming its reason; an admission failure keeps its message, any other
exception is named by its type only, and `main` exits 1 on any of them and 2 on a usage error.
"""
import argparse
import hashlib
import os
import re
import sys
from dataclasses import dataclass
from datetime import datetime, timezone
from scripts.admission_local_dropbox import LocalDropbox
from scripts.admission_metadata import AdmissionError, metadata_reference
from scripts.admission_transport import strict_json
from scripts.reconcile_approval import verify_approval
from scripts.reconcile_changes import GitSource
from scripts.reconcile_classify import classify
from scripts.reconcile_config import ReconcileError, load_config
from scripts.reconcile_evidence import canonical_bytes, load, seal
from scripts.reconcile_records import load_packet
from scripts.reconcile_scope import Live, check_packet
from scripts.reconcile_target import SHA, resolve_twice
from scripts.review_signature import verify_signature

MAX_BYTES = 128 * 1024
# checkpoint: (resolution context, configured branch, Dropbox-backed checks run, duplicate work is checked)
TABLE = {
    "dispatch": ("hook", "integration_branch", True, False), "resume": ("hook", "integration_branch", True, False),
    "pre-push": ("hook", "integration_branch", True, False), "closeout": ("hook", "integration_branch", True, False),
    "preflight": ("merge_script", "integration_branch", True, True), "pre-merge": ("runner", "integration_branch", True, True),
    "release-prepare": ("merge_script", "release_branch", False, False), "release-publish": ("merge_script", "release_branch", False, False),
    "release-verify": ("merge_script", "release_branch", False, False),
}
ALLOWED, VERDICT = ("current", "non_material"), ("blocked", "allowed")
ACTIVATION, ACTIVATION_KEYS = "flowstate-activation-v1", frozenset(("record_type", "exempt", "issued_at", "attestation"))
MOMENT = re.compile(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:Z|\+00:00)")
HEADING = re.compile(r"^### ([A-Z][A-Z0-9]*[0-9]+[a-z]?(?:\.[0-9]+)?) \u2014 ", re.M)
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PRIVATE = os.path.expanduser("~/.config/flowstate/admission.json")
CACHE = os.path.expanduser("~/.cache/flowstate/reconcile")


@dataclass(frozen=True)
class World:
    """One evaluation's inputs: configuration, checkpoint, packet id, candidate head, a `reconcile_changes` source, a Dropbox
    reader of `dropbox:` references, the evidence cache directory (None disables it), the clock and a transport (None is live)."""
    config: object
    checkpoint: str
    packet_id: str
    candidate: str
    source: object
    read: object
    cache: object
    now: object
    transport: object = None


def _cached(world):
    """The bytes cached for this checkpoint and candidate, or None; an unreadable file is as good as absent."""
    if not world.cache:
        return None
    try:
        with open(os.path.join(world.cache, world.checkpoint + "-" + world.candidate + ".json"), "rb") as handle:
            return handle.read(MAX_BYTES + 1)
    except OSError:
        return None


def _remember(world, record):
    """Cache the record under its checkpoint and candidate: in an owner-only directory, replaced atomically; a failure blocks."""
    if not world.cache:
        return
    os.makedirs(world.cache, 0o700, exist_ok=True)
    path = os.path.join(world.cache, record["checkpoint"] + "-" + record["candidate_sha"] + ".json")
    with open(path + ".tmp", "wb") as handle:
        handle.write(canonical_bytes(record))
    os.replace(path + ".tmp", path)


def _target(world, context, branch):
    """Steps 1 and 2: a full-SHA candidate, then the configured branch resolved twice with `retry` more tries."""
    if not SHA.fullmatch(world.candidate):
        raise ReconcileError("candidate must be a full 40-character lowercase SHA")
    for _ in range(world.config.retry):
        try:
            return resolve_twice(world.config, context, world.transport, getattr(world.config, branch))
        except ReconcileError:
            pass
    return resolve_twice(world.config, context, world.transport, getattr(world.config, branch))


def _baseline(world, raw, target, digests):
    """Step 3: the SHA both the candidate diff and the drift are measured from: the cached record's `previous_sha` when
    `reconcile_evidence` loads it and it is bound to this repository, ref, checkpoint, candidate, packet and plan digests,
    and otherwise the live target. It never advances for one head, so re-measuring is cumulative and no refusal can be
    walked past; an older baseline can only widen the measured drift."""
    try:
        record = load(raw)
        wanted = dict(digests, checkpoint=world.checkpoint, candidate_sha=world.candidate, packet_id=world.packet_id,
                      repository=target.repo, ref=target.ref)
        if any(record[key] != wanted[key] for key in wanted):
            return target.sha
    except ReconcileError:
        return target.sha
    return record["previous_sha"]


def packet_ids(plan_raw):
    """The valid packet ids of plan bytes: the front-matter id, then `<plan id>/<heading id>` per packet heading of its packet section."""
    text = plan_raw.decode("utf-8")
    plan = re.search(r"(?m)^id: (PLAN-[A-Z][A-Z0-9-]*-[0-9]{3})$", text)
    section = re.search(r"(?ms)^## 9\. .*(?=^## 10\. )", text)
    if not (plan and section):
        raise ReconcileError("plan bytes carry no front-matter id or no packet section")
    return (plan[1],) + tuple(plan[1] + "/" + name for name in HEADING.findall(section[0]))


def _bound(world, read, key):
    """Step 4: the binding rule for the candidate commit, then its signed approval; returns the packet, receipt, plan, ids, digests."""
    trailer = metadata_reference(world.source.commit_message(world.candidate))
    admission, directory = strict_json(read(trailer)), trailer.rpartition("/")[0]
    packet_raw = read(directory + "/packet.json")
    wanted = {"packet_id": world.packet_id, "packet_ref": directory + "/packet.json",
              "approval_ref": directory + "/plan-approval.json", "packet_sha256": hashlib.sha256(packet_raw).hexdigest()}
    stored = admission if isinstance(admission, dict) else {}
    unbound = next((name for name, value in wanted.items() if stored.get(name) != value), None)
    if unbound:
        raise ReconcileError("the admission record's %s does not bind this packet to the candidate commit: %s" % (unbound, trailer))
    plan_raw = read(strict_json(packet_raw).get("plan_ref"))
    ids = packet_ids(plan_raw)
    receipt = verify_approval(read(wanted["approval_ref"]), packet_raw, plan_raw, key, ids)
    digests = {"packet_sha256": wanted["packet_sha256"], "plan_sha256": hashlib.sha256(plan_raw).hexdigest()}
    packet = load_packet(packet_raw, ids)
    if packet["packet_id"] != world.packet_id:
        raise ReconcileError("the stored packet is %s, not the requested one" % packet["packet_id"])
    return packet, receipt, plan_raw, ids, digests


def _pair(entry):
    """True for one well-formed `exempt` entry; anything else raises, which makes the record invalid."""
    return all((set(entry) == {"pr_number", "head_sha"}, type(entry["pr_number"]) is int, entry["pr_number"] > 0, bool(SHA.fullmatch(entry["head_sha"]))))


def _exempt(world):
    """The activation-record rule: exempt only when the record at `activation_ref` holds exactly its four keys, is signed
    with the plan-approval key, lists a well-formed entry for the candidate and is dated in UTC, not in the future."""
    if not world.config.activation_ref:
        return False
    try:
        record = strict_json(world.read(world.config.activation_ref))
        verify_signature(record, world.config.plan_approval_public_key)
        heads = [entry["head_sha"] for entry in record["exempt"]]
        issued = datetime.fromisoformat(MOMENT.fullmatch(record["issued_at"])[0].replace("Z", "+00:00"))
        return all((set(record) == ACTIVATION_KEYS, record["record_type"] == ACTIVATION, all(map(_pair, record["exempt"])),
                    issued <= world.now(), world.candidate in heads))
    except Exception:
        return False


def _decide(world, context):
    table = TABLE[world.checkpoint]
    target = _target(world, context, table[1])
    raw = _cached(world)
    if not (table[2] and context != "ci"):
        return None, "no Dropbox-backed check applies at this checkpoint"
    if _exempt(world):
        return None, "the candidate head is exempt from binding and approval through the activation record"
    key = bytes.fromhex(world.config.plan_approval_public_key)
    packet, receipt, plan_raw, ids, digests = _bound(world, world.read, key)
    baseline = _baseline(world, raw, target, digests)
    live = Live(target.sha, world.source, world.read, plan_raw, key, ids)
    check_packet(packet, receipt, world.source.changed_paths(baseline, world.candidate), world.config.protected_globs,
                 live, world.config.release_sensitive_paths, table[3])
    upstream = [] if baseline == target.sha else [c.path for c in world.source.changed_paths(baseline, target.sha)]
    outcome, reasons = classify(baseline, target.sha, upstream, packet["watched"])
    record = seal(dict(digests, checkpoint=world.checkpoint, repository=target.repo, ref=target.ref, previous_sha=baseline,
                       resolved_sha=target.sha, candidate_sha=world.candidate, packet_id=packet["packet_id"],
                       upstream_paths=upstream, outcome=outcome, reasons=reasons,
                       observed_at=world.now().isoformat(timespec="seconds")))
    _remember(world, record)
    return record, ""


def decide(world, context=None):
    """Run the decision order and return `(record, "")`, or `(None, note)` when no record is written. A refused check
    raises ReconcileError; `context` defaults to the checkpoint's own."""
    try:
        return _decide(world, context or TABLE[world.checkpoint][0])
    except ReconcileError:
        raise
    except AdmissionError as error:
        raise ReconcileError("evidence refused: " + str(error)) from None
    except Exception as error:
        raise ReconcileError("decide failed: " + type(error).__name__) from None


def _sync_root(path):
    """The Dropbox reader of the command line: `LocalDropbox.read_bytes` over the configured root, keyed by `dropbox:` references.
    With no root the gate fails closed naming the setting."""
    try:
        with open(path, "rb") as handle:
            local = LocalDropbox(strict_json(handle.read(MAX_BYTES))["dropbox_local_path"])
    except (OSError, KeyError, AdmissionError) as error:
        raise ReconcileError("evidence reads need the dropbox_local_path setting in " + path + ": " + str(error)) from None
    return lambda reference: local.read_bytes(reference.removeprefix("dropbox:"))


def main(argv=None):
    """The command line: `--checkpoint`, `--packet` and `--candidate` are required and `--config` defaults to the
    `reconcile.json` beside the `scripts` package. Returns 0 allow, 1 block or 2 usage; diagnostics go to stderr."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--checkpoint", required=True, help="one of " + ", ".join(TABLE))
    for name in ("packet", "candidate"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--config", default=os.path.join(ROOT, "reconcile.json"), help="the reconcile configuration")
    args = parser.parse_args(argv)
    if args.checkpoint not in TABLE:
        parser.error("--checkpoint must be one of " + ", ".join(TABLE))
    try:
        world = World(load_config(args.config), args.checkpoint, args.packet, args.candidate, GitSource(os.getcwd()),
                      _sync_root(PRIVATE), CACHE, lambda: datetime.now(timezone.utc))
        record, note = decide(world)
    except Exception as error:  # the gate exits non-zero on any exception
        print("reconcile %s blocked: %s" % (args.checkpoint, error), file=sys.stderr)
        return 1
    if record is None:
        print("reconcile %s allowed: %s" % (args.checkpoint, note), file=sys.stderr)
        return 0
    sys.stdout.buffer.write(canonical_bytes(record))  # exactly the record's bytes, so a redirect stays loadable
    allowed = record["outcome"] in ALLOWED
    print("reconcile %s %s: %s %s" % (args.checkpoint, VERDICT[allowed], record["outcome"], ",".join(record["reasons"])), file=sys.stderr)
    return int(not allowed)


if __name__ == "__main__":
    raise SystemExit(main())
