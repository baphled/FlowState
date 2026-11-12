"""CLI orchestration for the neutral metadata guard."""
import argparse
import subprocess
import sys
import pathlib

from scripts.project_metadata.receipts import RECEIPT_SCHEMA, validate_receipt
from scripts.project_metadata.scan import (changed_tree_errors, directory_errors,
                                           external_record_errors, prepare_message,
                                           tree_errors)
from scripts.project_metadata import message_errors

FAIL = 'FAIL: '
LIMITS = ('LIMIT: offline checks cannot authenticate Dropbox reads, '
          'serving identity or review')


def report(errors, success):
    """Print failures, or the success and offline-limit notices."""
    for error in errors:
        print(FAIL + error, file=sys.stderr)
    if not errors:
        print(success)
        print(LIMITS)
    return bool(errors)


def standalone_modes(args, parser):
    """Handle receipt, message-only, prepare-message and tree-root modes."""
    if args.receipt:
        return validate_receipt(RECEIPT_SCHEMA, args.receipt)
    if args.prepare_message:
        if not args.external_record or not args.output:
            parser.error('--prepare-message requires --external-record and --output')
        errors = prepare_message(args.prepare_message, args.external_record, args.output)
        for error in errors:
            print(FAIL + error, file=sys.stderr)
        return bool(errors)
    if args.message_file and not (args.tree_root or args.base or args.staged
                                  or args.external_record):
        # Message-only mode: no Git scan, so `--message-file` can fail
        # loudly on a bad message even while the tree is not yet clean.
        return report(message_errors(args.message_file.read_text()),
                      'PASS: commit message carries only a neutral Dropbox reference')
    if args.tree_root:
        return report(directory_errors(args.tree_root),
                      'PASS: no project run records in the checked tree')
    return None


def build_parser():
    """Define the guard's command-line surface."""
    parser = argparse.ArgumentParser(
        description='Reject project run records in Git; keep only a neutral Dropbox reference.')
    parser.add_argument('--base')
    parser.add_argument('--head', default='HEAD')
    parser.add_argument('--staged', action='store_true')
    parser.add_argument('--changed', action='store_true',
                        help='check touched blobs in every PR commit; requires --base')
    parser.add_argument('--prepare-message', type=pathlib.Path,
                        help='prepare a neutral commit message from a fetched external record')
    parser.add_argument('--output', type=pathlib.Path)
    parser.add_argument('--message-file', type=pathlib.Path)
    parser.add_argument('--external-record', type=pathlib.Path)
    parser.add_argument('--receipt', type=pathlib.Path,
                        help='validate an external attempt receipt against the schema')
    parser.add_argument('--tree-root', type=pathlib.Path,
                        help='check a plain directory tree instead of Git state')
    return parser


def range_errors(args):
    """Check every commit message and blob in the reviewed range."""
    from scripts.project_metadata.scan import git
    errors = changed_tree_errors(args.base, args.head) if args.changed \
        else tree_errors(None if args.staged or not args.base else args.head)
    if args.base:
        for commit in git('rev-list', f'{args.base}..{args.head}').decode().splitlines():
            if not args.changed:
                errors.extend(f'{commit[:12]}: {e}' for e in tree_errors(commit))
            message = git('show', '-s', '--format=%B', commit).decode()
            errors.extend(f'{commit[:12]}: {e}' for e in message_errors(message))
    if args.message_file:
        errors.extend(message_errors(args.message_file.read_text()))
    if args.external_record:
        errors.extend(external_record_errors(args.external_record))
    return errors


def main():
    parser = build_parser()
    args = parser.parse_args()
    if args.changed and not args.base:
        parser.error('--changed requires --base')
    try:
        standalone = standalone_modes(args, parser)
        if standalone is not None:
            return standalone
        return report(range_errors(args),
                      'PASS: checked blobs and commit references satisfy metadata policy')
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(FAIL + str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
