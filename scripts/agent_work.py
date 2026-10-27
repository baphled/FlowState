#!/usr/bin/env python3
"""Offline role/work-plan admission checks; runtime must authenticate and lease atomically."""

import argparse
import json
import sys
from pathlib import Path


def plan_errors(plan, policy):
    errors = []
    if policy['policy_version'] != 'flowstate-agent-roles-v1' or policy['max_active_work_items_per_agent'] != 1:
        return ['unsupported role policy or weakened single-item limit']
    roles = policy['roles']
    assignments = plan['assignments']
    if not assignments:
        return ['an admitted plan needs assignments']
    agents, items, active, owners = {}, set(), {}, {}
    for lease in plan['active_leases']:
        actor, item = lease['agent_id'], lease['work_item_id']
        if not isinstance(actor, str) or not actor or not isinstance(item, str) or not item:
            return ['invalid lease actor/work item identity']
        if item in owners and owners[item] != actor:
            errors.append('work item has conflicting existing owners')
        owners[item] = actor
        active.setdefault(actor, set()).add(item)
    for assignment in assignments:
        actor, item, role = assignment['agent_id'], assignment['work_item_id'], assignment['role']
        if not isinstance(actor, str) or not actor or not isinstance(item, str) or not item:
            return ['invalid assigned actor/work item identity']
        if item in owners and owners[item] != actor:
            errors.append('work item is leased to another actual agent; fence/transfer first')
        owners[item] = actor
        if actor in agents or item in items:
            errors.append('duplicate/empty agent or work item assignment')
        agents[actor] = assignment
        items.add(item)
        active.setdefault(actor, set()).add(item)
        if role not in roles or assignment['work_kind'] not in roles.get(role, {}).get('work_kinds', []):
            errors.append('work complexity does not match role: ' + actor)
        scopes = assignment['scope_ids']
        if not isinstance(scopes, list) or len(scopes) != 1 or not isinstance(scopes[0], str) or not scopes[0]:
            errors.append('one bounded scope per work item: ' + actor)
    if any(len(items) > 1 for items in active.values()):
        errors.append('agent already has another active work item')
    edges, parents = {}, set()
    for edge in plan['delegations']:
        parent, child = edge['parent_agent_id'], edge['child_agent_id']
        if parent not in agents or child not in agents or parent == child or child in parents:
            errors.append('invalid/duplicate delegation edge')
            continue
        parents.add(child)
        parent_role, child_role = agents[parent]['role'], agents[child]['role']
        if child_role not in roles.get(parent_role, {}).get('delegate_to', []):
            errors.append('delegation is not permitted by role')
        if parent_role == 'senior' and agents[parent]['scope_ids'] != agents[child]['scope_ids']:
            errors.append('senior child escapes the parent work scope')
        edges.setdefault(parent, []).append(child)
    def cycle(node, visiting, visited):
        if node in visiting:
            return True
        if node in visited:
            return False
        visiting.add(node)
        if any(cycle(child, visiting, visited) for child in edges.get(node, [])):
            return True
        visiting.remove(node)
        visited.add(node)
        return False
    if any(cycle(node, set(), set()) for node in edges):
        errors.append('delegation cycle')
    budget = plan['budget']
    fields = ('max_tokens', 'spent_tokens', 'reserved_tokens', 'max_minutes', 'spent_minutes', 'max_attempts', 'spent_attempts')
    if any(type(budget[k]) is not int or budget[k] < 0 for k in fields):
        errors.append('budget counters must be nonnegative integers')
    elif (budget['max_tokens'] <= 0 or budget['max_minutes'] <= 0 or budget['max_attempts'] <= 0 or
          budget['spent_tokens'] >= budget['max_tokens'] or
          budget['spent_tokens'] + budget['reserved_tokens'] > budget['max_tokens'] or
          budget['spent_minutes'] >= budget['max_minutes'] or budget['spent_attempts'] >= budget['max_attempts']):
        errors.append('shared cumulative budget is exhausted or over-reserved')
    for review in plan['reviews']:
        author, reviewer = review['author_agent_id'], review['reviewer_agent_id']
        if author == reviewer or author not in agents or reviewer not in agents or agents[reviewer]['role'] != 'verifier':
            errors.append('review must use a separate verifier')
        elif agents[author]['scope_ids'] != agents[reviewer]['scope_ids']:
            errors.append('verifier scope differs from reviewed work')
    return errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--roles', type=Path, required=True, help='read-back current Dropbox role policy')
    parser.add_argument('--plan', type=Path, required=True, help='external read-back Dropbox work plan')
    args = parser.parse_args()
    try:
        errors = plan_errors(json.loads(args.plan.read_text()), json.loads(args.roles.read_text()))
        for error in errors:
            print('FAIL: ' + error, file=sys.stderr)
        if not errors:
            print('POLICY CHECK PASS: offline work plan only; no runtime dispatch or identity authentication')
        return bool(errors)
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as error:
        print('FAIL: malformed policy/plan: ' + str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
