"""Package committed runner sources and manage a validated Linux user service."""
import argparse
import json
import os
import shutil
import socket
import subprocess
import sys
import zipfile
from pathlib import Path

from scripts.admission_config import private_file
from scripts.admission_metadata import AdmissionError


def command(*args):
    return subprocess.check_output(args, stderr=subprocess.PIPE)


def ready():
    """Tell systemd startup succeeded only after live authority validation."""
    address = os.environ.get('NOTIFY_SOCKET')
    if address:
        with socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM) as notifier:
            notifier.connect('\0' + address[1:] if address.startswith('@') else address)
            notifier.sendall(b'READY=1')


def package(output):
    """Build a zipapp from committed trusted sources, excluding candidate/test code."""
    source = command('git', 'rev-parse', 'HEAD').decode().strip()
    names = command('git', 'ls-tree', '-r', '--name-only', source, 'scripts').decode().splitlines()
    names = [n for n in names if n.startswith(('scripts/admission_', 'scripts/review_')) and n.endswith('.py')]
    names += ['scripts/app_credentials.py', 'scripts/status_issuer.py', 'scripts/requirements-admission.txt']
    output = Path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(output, 'w', zipfile.ZIP_DEFLATED) as archive:
        for name in sorted(names):
            archive.writestr(name, command('git', 'show', source + ':' + name))
        archive.writestr('scripts/__init__.py', '')
        archive.writestr('__main__.py', 'import signal\nfrom scripts.admission_runner import main, terminate\n'
                         'signal.signal(signal.SIGTERM, terminate)\nraise SystemExit(main())\n')
        archive.writestr('SOURCE_SHA', source + '\n')
    print('Packaged admission runner at ' + str(output) + ' from ' + source)


def unit(root, config):
    """Keep credentials outside the package and restart crashed runners visibly."""
    args = [root / 'venv/bin/python', '-B', root / 'runner.pyz', '--config', config]
    start = ' '.join(json.dumps(str(arg), ensure_ascii=False).replace('%', '%%') for arg in args)
    return ('[Unit]\nDescription=FlowState merge admission\nStartLimitIntervalSec=300\nStartLimitBurst=3\n'
            '[Service]\nType=notify\nTimeoutStartSec=120\nExecStart=' + start +
            '\nRestart=on-failure\nRestartSec=10\nTimeoutStopSec=30\n'
            '[Install]\nWantedBy=default.target\n')


def install(artifact, root, config):
    """Validate configuration before installing dependencies or registering a unit."""
    private_file(config)
    root.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(artifact) as archive:
        required = archive.read('scripts/requirements-admission.txt')
        archive.getinfo('__main__.py')
        archive.getinfo('scripts/admission_runner.py')
    command(sys.executable, '-m', 'venv', str(root / 'venv'))
    requirements = root / 'requirements.txt'
    requirements.write_bytes(required)
    command(str(root / 'venv/bin/pip'), 'install', '-r', str(requirements))
    command(str(root / 'venv/bin/python'), '-B', str(Path(artifact).resolve()), '--config', str(config), '--check-config')
    shutil.copyfile(artifact, root / 'runner.pyz')
    service = Path.home() / '.config/systemd/user/flowstate-admission.service'
    service.parent.mkdir(parents=True, exist_ok=True)
    service.write_text(unit(root, config))
    command('systemctl', '--user', 'daemon-reload')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('package', 'install', 'start', 'stop', 'status'))
    parser.add_argument('--artifact', default='build/admission-runner.pyz')
    parser.add_argument('--config', type=Path, default=Path.home() / '.config/flowstate/admission.json')
    args = parser.parse_args(argv)
    root = Path.home() / '.local/share/flowstate-admission'
    try:
        if args.action == 'package':
            package(args.artifact)
        elif args.action == 'install':
            install(args.artifact, root, args.config)
        else:
            if args.action == 'start':
                private_file(args.config)
                command(str(root / 'venv/bin/python'), '-B', str(root / 'runner.pyz'),
                        '--config', str(args.config), '--check-config')
            print(command('systemctl', '--user', args.action, 'flowstate-admission.service').decode())
            if args.action == 'start':
                command('systemctl', '--user', 'is-active', '--quiet', 'flowstate-admission.service')
        return 0
    except (Exception, KeyboardInterrupt) as error:
        message = str(error) if isinstance(error, AdmissionError) else type(error).__name__
        print('FAIL: admission runner ' + args.action + ': ' + message, file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
